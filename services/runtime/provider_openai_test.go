package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func openaiStub(t *testing.T, body string) *OpenAIProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	p := NewOpenAIProvider("k")
	p.baseURL = srv.URL
	return p
}

// Several OpenAI-compatible backends report finish_reason "stop" alongside a
// populated tool_calls array. Agent loops branch on the finish reason, so an
// uncorrected "stop" means the tool never runs and the loop just ends. Gemini
// already made this correction; OpenAI was the inconsistent one.
func TestOpenAIFinishReasonCorrectedForToolCalls(t *testing.T) {
	p := openaiStub(t, `{
		"id":"chatcmpl-1","model":"gpt-4o",
		"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"SF\"}"}}]}}],
		"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
	}`)

	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "weather?")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(result.Message.ToolCalls) != 1 {
		t.Fatalf("tool calls = %+v", result.Message.ToolCalls)
	}
	if result.FinishReason != FinishToolCalls {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, FinishToolCalls)
	}
}

// A genuine "length" stop must not be rewritten: the correction only rescues
// "stop".
func TestOpenAIFinishReasonNotOverwritten(t *testing.T) {
	p := openaiStub(t, `{
		"id":"chatcmpl-2","model":"gpt-4o",
		"choices":[{"finish_reason":"length","message":{"role":"assistant","content":null,
			"tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":"{"}}]}}]
	}`)
	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.FinishReason != FinishLength {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, FinishLength)
	}
}

// Text-only responses keep "stop".
func TestOpenAIFinishReasonPlainStop(t *testing.T) {
	p := openaiStub(t, `{"id":"c","model":"gpt-4o","choices":[{"finish_reason":"stop",
		"message":{"role":"assistant","content":"hello"}}]}`)
	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.FinishReason != FinishStop {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, FinishStop)
	}
}

// The same correction on the streaming path.
func TestOpenAIStreamFinishReasonCorrectedForToolCalls(t *testing.T) {
	events := "data: " + `{"id":"c1","model":"gpt-4o","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"f","arguments":"{}"}}]},"finish_reason":null}]}` + "\n\n" +
		"data: " + `{"id":"c1","model":"gpt-4o","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		"data: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(events))
	}))
	defer srv.Close()

	p := NewOpenAIProvider("k")
	p.baseURL = srv.URL
	ch, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "gpt-4o",
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
	if final.FinishReason != FinishToolCalls {
		t.Errorf("FinishReason = %q, want %q", final.FinishReason, FinishToolCalls)
	}
}

// ---- model discovery ----

// Discovery used to hold a package-level mutex across the whole 5s network
// call, so a stopped Ollama serialized every concurrent GET /v1/models behind
// it: ten callers waited fifty seconds between them. Now at most one discovery
// is in flight and everyone else gets the last known list immediately.
func TestModelDiscoveryDoesNotSerializeCallers(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // stand in for a backend that never answers
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gemma3:4b"}]}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatProvider("ollama", srv.URL, "")

	// One caller blocks on the slow backend.
	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		p.Models(context.Background())
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the first caller never reached the backend")
	}

	// Everyone else must return promptly rather than queueing behind it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for range 10 {
			wg.Go(func() { p.Models(context.Background()) })
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("concurrent Models() calls queued behind the in-flight discovery")
	}

	close(release)
	<-blocked

	if got := calls.Load(); got != 1 {
		t.Errorf("%d discovery requests for 11 concurrent callers, want 1", got)
	}
}

// A failed discovery is remembered, so a dead backend is not re-dialled on
// every single request.
func TestModelDiscoveryNegativeCaching(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := NewOpenAICompatProvider("vllm", srv.URL, "")
	for range 5 {
		if models := p.Models(context.Background()); len(models) != 0 {
			t.Errorf("failed discovery returned %v", models)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("%d discovery attempts against a failing backend, want 1 (negatively cached)", got)
	}
	if modelDiscoveryFailTTL >= modelDiscoveryTTL {
		t.Errorf("failure TTL %v should be shorter than the success TTL %v", modelDiscoveryFailTTL, modelDiscoveryTTL)
	}
}

// A successful discovery is cached and its result is served from then on.
func TestModelDiscoverySuccessIsCached(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"gemma3:4b"},{"id":"qwen3:8b"}]}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatProvider("ollama", srv.URL, "")
	for range 4 {
		models := p.Models(context.Background())
		if len(models) != 2 || models[0] != "gemma3:4b" {
			t.Fatalf("Models() = %v", models)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("%d discovery requests, want 1", got)
	}
}

// A backend that recovers after failing must be picked up once the failure TTL
// lapses, not stay negatively cached forever.
func TestModelDiscoveryRecoversAfterFailure(t *testing.T) {
	var healthy atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"back"}]}`))
	}))
	defer srv.Close()

	p := NewOpenAICompatProvider("ollama", srv.URL, "")
	if models := p.Models(context.Background()); len(models) != 0 {
		t.Fatalf("Models() while down = %v", models)
	}

	healthy.Store(true)
	// Expire the negative cache the way the clock would.
	p.modelsMu.Lock()
	p.lastAttempt = time.Now().Add(-2 * modelDiscoveryFailTTL)
	p.modelsMu.Unlock()

	if models := p.Models(context.Background()); len(models) != 1 || models[0] != "back" {
		t.Errorf("Models() after recovery = %v", models)
	}
}

// A provider with a static model list never dials anything.
func TestStaticModelsNeedNoDiscovery(t *testing.T) {
	p := NewOpenAIProvider("k")
	p.baseURL = "http://127.0.0.1:1" // would fail instantly if dialled
	models := p.Models(context.Background())
	if len(models) == 0 {
		t.Fatal("static model list is empty")
	}
	for _, m := range models {
		if _, ok := p.pricing.Rate(m); !ok {
			t.Errorf("listed model %q has no pricing entry", m)
		}
	}
}
