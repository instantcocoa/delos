package runtime

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAnthropicStreamIsMeteredWhenTheProviderReportsNoUsage(t *testing.T) {
	p := &unmeteredStreamProvider{mockProvider{name: "anthropic", models: []string{"claude-sonnet-4-5"}}}
	srv, store, secret, key := meteringServer(t, p)

	req := jsonReq(http.MethodPost, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("X-Api-Key", secret)
	rec := doReq(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	usage, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tokens <= 0 {
		t.Errorf("recorded %d tokens for a completed stream; an unmetered stream is a budget bypass", usage.Tokens)
	}

	// The real API always carries usage on message_delta and SDKs read it
	// there, so the gateway always sends it - from the provider when it
	// reported usage, from the estimate otherwise.
	body := rec.Body.String()
	delta, ok := sseEventData(body, "message_delta")
	if !ok {
		t.Fatalf("no message_delta event in:\n%s", body)
	}
	var ev struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(delta), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Usage == nil {
		t.Fatalf("message_delta carries no usage: %s", delta)
	}
	if ev.Usage.InputTokens <= 0 || ev.Usage.OutputTokens <= 0 {
		t.Errorf("message_delta usage = %+v, want non-zero counts", *ev.Usage)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Errorf("stream did not terminate cleanly:\n%s", body)
	}
}

func TestAnthropicBrokenStreamIsStillMetered(t *testing.T) {
	p := &brokenStreamProvider{mockProvider{name: "anthropic", models: []string{"claude-sonnet-4-5"}}}
	srv, store, secret, key := meteringServer(t, p)

	req := jsonReq(http.MethodPost, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("X-Api-Key", secret)
	doReq(srv, req)

	usage, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tokens <= 0 {
		t.Errorf("a stream that delivered content before breaking recorded %d tokens", usage.Tokens)
	}
}

// sseEventData returns the data payload of the first SSE frame with the given
// event name.
func sseEventData(body, event string) (string, bool) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "event: "+event {
			continue
		}
		for _, next := range lines[i+1:] {
			if data, ok := strings.CutPrefix(next, "data: "); ok {
				return data, true
			}
		}
	}
	return "", false
}
