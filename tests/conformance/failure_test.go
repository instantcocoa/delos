package conformance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/instantcocoa/delos/services/runtime"
)

// Failure injection: the gateway must degrade honestly. A broken upstream
// either fails over to another target and delivers a complete stream, or it
// surfaces the error - it must never hand a client a truncated response that
// looks complete.

const failModel = "fail-test-model"

// upstreamFunc serves an OpenAI-compatible upstream. Model discovery is
// answered here so routing never consumes scenario responses.
func compatUpstream(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	return newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]string{{"id": failModel, "object": "model"}},
			})
			return
		}
		h(w, r)
	}))
}

// sseWriter writes SSE frames with a flush after each one.
func sseWriter(w http.ResponseWriter) func(string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	return func(frame string) {
		_, _ = io.WriteString(w, frame+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func chunkFrame(content string) string {
	return fmt.Sprintf(`data: {"id":"chatcmpl-fail","object":"chat.completion.chunk","created":1730000000,"model":%q,"choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`,
		failModel, content)
}

func finalFrame() string {
	return fmt.Sprintf(`data: {"id":"chatcmpl-fail","object":"chat.completion.chunk","created":1730000000,"model":%q,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		failModel)
}

// routedGateway wires providers into a single fallback chain under the alias
// failModel, in the order given.
func routedGateway(t *testing.T, opts []runtime.HTTPServerOption, providers ...runtime.Provider) string {
	t.Helper()
	svc := newService(providers...)
	targets := make([]runtime.RouteTarget, 0, len(providers))
	for _, p := range providers {
		targets = append(targets, runtime.RouteTarget{Provider: p.Name(), Model: failModel})
	}
	svc.SetRoutes(map[string][]runtime.RouteTarget{failModel: targets})
	url, stop := serveService(svc, opts...)
	t.Cleanup(stop)
	return url
}

func chatRequest(stream bool) []byte {
	body := map[string]any{
		"model":    failModel,
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	if stream {
		body["stream"] = true
	}
	out, _ := json.Marshal(body)
	return out
}

// TestStreamFailures covers broken streams: whatever happens, a partial
// stream must never terminate with [DONE].
func TestStreamFailures(t *testing.T) {
	tests := []struct {
		name string
		// upstream serves the single (primary) provider.
		upstream http.HandlerFunc
		// wantDeltas is the text the client must have received.
		wantDeltas string
		// wantErrorFrame is a substring of the SSE error frame.
		wantErrorFrame string
	}{
		{
			name: "malformed SSE mid-stream",
			upstream: func(w http.ResponseWriter, r *http.Request) {
				write := sseWriter(w)
				write(chunkFrame("Hello"))
				write(`data: {"id":"chatcmpl-fail","choices":[{"delta":{"content":`) // truncated JSON
			},
			wantDeltas:     "Hello",
			wantErrorFrame: "provider_stream_error",
		},
		{
			name: "connection closed mid-stream",
			upstream: func(w http.ResponseWriter, r *http.Request) {
				write := sseWriter(w)
				write(chunkFrame("Hello"))
				// Abort the response without terminating the stream: the
				// client's read fails, exactly like a dropped provider.
				panic(http.ErrAbortHandler)
			},
			wantDeltas:     "Hello",
			wantErrorFrame: "provider_stream_error",
		},
		{
			name: "provider error event mid-stream",
			upstream: func(w http.ResponseWriter, r *http.Request) {
				write := sseWriter(w)
				write(chunkFrame("Hello"))
				write(`data: {"error":{"message":"overloaded","type":"server_error"}}`)
				write(`data: not-json-at-all`)
			},
			wantDeltas:     "Hello",
			wantErrorFrame: "provider_stream_error",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstreamURL := compatUpstream(t, tc.upstream)
			primary := runtime.NewOpenAICompatProvider("primary", upstreamURL+"/v1", "k")
			gw := routedGateway(t, nil, primary)

			resp, err := callGateway(gw, "/v1/chat/completions", chatRequest(true), "openai")
			if err != nil {
				t.Fatalf("gateway call failed: %v", err)
			}
			if resp.Status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (the stream had already started)\nbody: %s", resp.Status, resp.Body)
			}
			view := ReadStream("openai", resp.Body)
			if view.Text() != tc.wantDeltas {
				t.Errorf("client saw deltas %q, want %q", view.Text(), tc.wantDeltas)
			}
			if view.Done {
				t.Error("a partial stream was terminated with [DONE]: the client cannot tell it was truncated")
			}
			if !strings.Contains(resp.Body, tc.wantErrorFrame) {
				t.Errorf("stream is missing an error frame containing %q\nbody: %s", tc.wantErrorFrame, resp.Body)
			}
		})
	}
}

// TestUpstreamTimeout asserts the end-to-end request budget is enforced and
// the caller gets an honest error rather than a hang.
func TestUpstreamTimeout(t *testing.T) {
	// The upstream never answers: the gateway must give up on its own budget.
	// release is closed before the server is torn down (cleanups run LIFO),
	// so the blocked handler never delays the suite.
	release := make(chan struct{})
	upstreamURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	})
	t.Cleanup(func() { close(release) })
	primary := runtime.NewOpenAICompatProvider("primary", upstreamURL+"/v1", "k")
	gw := routedGateway(t, []runtime.HTTPServerOption{runtime.WithRequestTimeout(150 * time.Millisecond)}, primary)

	start := time.Now()
	resp, err := callGateway(gw, "/v1/chat/completions", chatRequest(false), "openai")
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	elapsed := time.Since(start)

	if resp.Status != http.StatusBadGateway {
		t.Errorf("status = %d, want 502\nbody: %s", resp.Status, resp.Body)
	}
	if elapsed > 5*time.Second {
		t.Errorf("request took %s: the timeout budget was not enforced", elapsed)
	}
	if !strings.Contains(resp.Body, "error") {
		t.Errorf("timeout response carries no error envelope: %s", resp.Body)
	}
}

// TestFailoverOnUpstream500 asserts a failing primary is replaced by the
// fallback, transparently to the client.
func TestFailoverOnUpstream500(t *testing.T) {
	var primaryCalls int
	primaryURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		primaryCalls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"upstream exploded","type":"server_error"}}`)
	})
	fallbackURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"chatcmpl-ok","object":"chat.completion","created":1730000000,"model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"served by the fallback"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4,"total_tokens":9}}`, failModel)
	})

	gw := routedGateway(t, nil,
		runtime.NewOpenAICompatProvider("primary", primaryURL+"/v1", "k"),
		runtime.NewOpenAICompatProvider("fallback", fallbackURL+"/v1", "k"),
	)

	resp, err := callGateway(gw, "/v1/chat/completions", chatRequest(false), "openai")
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody: %s", resp.Status, resp.Body)
	}
	doc, err := resp.JSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath(doc, "choices.0.message.content")
	if err != nil {
		t.Fatalf("choices.0.message.content: %v\nbody: %s", err, resp.Body)
	}
	if got != "served by the fallback" {
		t.Errorf("content = %v, want the fallback's response", got)
	}
	if primaryCalls == 0 {
		t.Error("the primary was never tried")
	}
}

// TestMidStreamKillDoesNotSpliceCompletions covers the hard half of the
// Phase 1 mid-stream failover criterion. Failing over after bytes have
// reached the client would splice two different completions into one
// response, so once content is committed the break is surfaced as an error
// instead. The recoverable case - a break before any content - is covered by
// TestStreamStartFailureFailsOver and by the unit-level chain tests.
func TestMidStreamKillDoesNotSpliceCompletions(t *testing.T) {
	primaryURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		write := sseWriter(w)
		write(chunkFrame("Par"))
		panic(http.ErrAbortHandler)
	})
	var fallbackCalled bool
	fallbackURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		fallbackCalled = true
		write := sseWriter(w)
		write(chunkFrame("Hello"))
		write(chunkFrame(" world"))
		write(finalFrame())
		write("data: [DONE]")
	})

	gw := routedGateway(t, nil,
		runtime.NewOpenAICompatProvider("primary", primaryURL+"/v1", "k"),
		runtime.NewOpenAICompatProvider("fallback", fallbackURL+"/v1", "k"),
	)

	resp, err := callGateway(gw, "/v1/chat/completions", chatRequest(true), "openai")
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200 (headers were already sent)\nbody: %s", resp.Status, resp.Body)
	}

	view := ReadStream("openai", resp.Body)
	if strings.Contains(view.Text(), "Hello world") {
		t.Errorf("the fallback's completion was spliced onto the primary's partial output: %q", view.Text())
	}
	if fallbackCalled {
		t.Error("the fallback must not be started once the client holds partial content")
	}
	if view.Done {
		t.Errorf("a broken stream must not be terminated with [DONE]\nbody: %s", resp.Body)
	}
	if !strings.Contains(resp.Body, "provider_stream_error") {
		t.Errorf("the client must be told the stream broke\nbody: %s", resp.Body)
	}
}

// TestStreamStartFailureFailsOver covers the cheaper case: the primary
// refuses the stream outright (429) before any bytes are sent.
func TestStreamStartFailureFailsOver(t *testing.T) {
	primaryURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"slow down","type":"rate_limit_error"}}`)
	})
	fallbackURL := compatUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		write := sseWriter(w)
		write(chunkFrame("Hello"))
		write(finalFrame())
		write("data: [DONE]")
	})

	gw := routedGateway(t, nil,
		runtime.NewOpenAICompatProvider("primary", primaryURL+"/v1", "k"),
		runtime.NewOpenAICompatProvider("fallback", fallbackURL+"/v1", "k"),
	)

	resp, err := callGateway(gw, "/v1/chat/completions", chatRequest(true), "openai")
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	view := ReadStream("openai", resp.Body)
	if !view.Done || view.Text() != "Hello" {
		t.Errorf("client did not receive the fallback's complete stream: text=%q done=%v\nbody: %s",
			view.Text(), view.Done, resp.Body)
	}
}

// TestAllTargetsFailStreaming asserts the last-resort path: when no target can
// produce a stream, the client gets an error and never a clean termination.
func TestAllTargetsFailStreaming(t *testing.T) {
	dead := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"message":"provider down","type":"server_error"}}`)
	}
	gw := routedGateway(t, nil,
		runtime.NewOpenAICompatProvider("primary", compatUpstream(t, dead)+"/v1", "k"),
		runtime.NewOpenAICompatProvider("fallback", compatUpstream(t, dead)+"/v1", "k"),
	)

	resp, err := callGateway(gw, "/v1/chat/completions", chatRequest(true), "openai")
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	if resp.Status == http.StatusOK {
		view := ReadStream("openai", resp.Body)
		if view.Done {
			t.Errorf("a failed request was terminated with [DONE]\nbody: %s", resp.Body)
		}
		if !strings.Contains(resp.Body, "provider down") {
			t.Errorf("the provider's error was not passed through verbatim\nbody: %s", resp.Body)
		}
		return
	}
	if !strings.Contains(resp.Body, "provider down") {
		t.Errorf("the provider's error was not passed through verbatim: %s", resp.Body)
	}
}
