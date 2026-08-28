package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// anthropicCapture points a provider at a stub server and returns the provider
// alongside a pointer to the last request body it received.
func anthropicCapture(t *testing.T, status int, body string) (*AnthropicProvider, *string) {
	t.Helper()
	var captured string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		captured = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewAnthropicProvider("k", WithAnthropicBaseURL(srv.URL)), &captured
}

// Prompt caching was silently disabled: cache_control never reached the API,
// so a user who asked for caching paid full input price on every turn — around
// a 10x regression against what they were expecting.
func TestAnthropicCacheControlIsForwarded(t *testing.T) {
	p, captured := anthropicCapture(t, http.StatusOK,
		`{"id":"msg-1","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)

	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			{Role: "system", Content: []ContentPart{{Type: "text", Text: "long preamble", CacheControl: "ephemeral"}}},
			{Role: "user", Content: []ContentPart{{Type: "text", Text: "big document", CacheControl: "ephemeral"}}},
		},
		Tools: []Tool{{Name: "search", Parameters: json.RawMessage(`{"type":"object"}`), CacheControl: "ephemeral"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(*captured), &sent); err != nil {
		t.Fatalf("request body is not JSON (%q): %v", *captured, err)
	}
	if strings.Count(*captured, `"cache_control"`) != 3 {
		t.Errorf("expected three cache breakpoints (system, message, tool), got: %s", *captured)
	}

	// A cached system prompt has to go out as blocks, not a bare string.
	system, ok := sent["system"].([]any)
	if !ok {
		t.Fatalf("system = %#v, want a block array when it carries cache_control", sent["system"])
	}
	block, _ := system[0].(map[string]any)
	if block["cache_control"] == nil {
		t.Errorf("system block lost its cache_control: %#v", block)
	}
}

// Without cache_control the system prompt keeps the simpler string form.
func TestAnthropicSystemStaysAStringWithoutCaching(t *testing.T) {
	p, captured := anthropicCapture(t, http.StatusOK,
		`{"id":"m","model":"claude-sonnet-4-5","content":[],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`)

	if _, err := p.Complete(context.Background(), CompletionParams{
		Model: "claude-sonnet-4-5",
		Messages: []Message{
			TextMessage("system", "first"),
			TextMessage("system", "second"),
			TextMessage("user", "hi"),
		},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(*captured), &sent); err != nil {
		t.Fatal(err)
	}
	if got, ok := sent["system"].(string); !ok || got != "first\n\nsecond" {
		t.Errorf("system = %#v, want the joined string", sent["system"])
	}
}

// cache_creation_input_tokens and cache_read_input_tokens were never parsed,
// so cached tokens were billed at $0 and there was no way to tell from usage
// whether caching was working at all.
func TestAnthropicCacheTokensAreBilled(t *testing.T) {
	p, _ := anthropicCapture(t, http.StatusOK, `{
		"id":"msg-2","model":"claude-sonnet-4-5-20250929",
		"content":[{"type":"text","text":"ok"}],
		"stop_reason":"end_turn",
		"usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":20000,"cache_read_input_tokens":30000}
	}`)

	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "claude-sonnet-4-5",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	u := result.Usage
	if u.CacheCreationTokens != 20000 || u.CacheReadTokens != 30000 {
		t.Errorf("cache tokens = %+v", u)
	}
	// Anthropic excludes cache tokens from input_tokens, so they belong in
	// the total the token budget is charged against.
	if u.TotalTokens != 100+50+20000+30000 {
		t.Errorf("TotalTokens = %d, want %d", u.TotalTokens, 100+50+20000+30000)
	}
	want := (100*3.00 + 50*15.00 + 20000*3.75 + 30000*0.30) / 1e6
	if !closeEnough(u.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", u.CostUSD, want)
	}
}

// The same accounting on the streaming path: message_start carries input and
// cache counters, message_delta carries output.
func TestAnthropicStreamCacheTokens(t *testing.T) {
	events := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg-3","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_read_input_tokens":2000}}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
		``,
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(events))
	}))
	defer srv.Close()

	p := NewAnthropicProvider("k", WithAnthropicBaseURL(srv.URL))
	ch, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "claude-sonnet-4-5",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	var final StreamChunk
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.Done {
			final = chunk
		}
	}
	if final.Usage == nil {
		t.Fatal("no usage on the final chunk")
	}
	if final.Usage.CacheCreationTokens != 1000 || final.Usage.CacheReadTokens != 2000 {
		t.Errorf("cache tokens = %+v", final.Usage)
	}
	if final.Usage.TotalTokens != 10+5+1000+2000 {
		t.Errorf("TotalTokens = %d, want %d", final.Usage.TotalTokens, 10+5+1000+2000)
	}
	want := (10*3.00 + 5*15.00 + 1000*3.75 + 2000*0.30) / 1e6
	if !closeEnough(final.Usage.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", final.Usage.CostUSD, want)
	}
}

// Dated model IDs must price as their family, not fall through to $0.
func TestAnthropicPricingCoversListedModels(t *testing.T) {
	p := NewAnthropicProvider("k")
	for _, model := range p.Models(context.Background()) {
		if _, ok := p.pricing.Rate(model); !ok {
			t.Errorf("model %q has no pricing entry", model)
		}
		if _, ok := p.pricing.Rate(model + "-20991231"); !ok {
			t.Errorf("dated variant of %q has no pricing entry", model)
		}
	}
	rate, ok := p.pricing.Rate("claude-sonnet-4-5-20250929")
	if !ok {
		t.Fatal("dated sonnet is unpriced")
	}
	if rate.Output <= rate.Input {
		t.Errorf("rate = %+v; output must cost more than input", rate)
	}
}
