package conformance

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/instantcocoa/delos/services/runtime"
)

// ---- fake upstream ----

// RecordedRequest is one call the gateway made to the backend.
type RecordedRequest struct {
	Method    string
	Path      string
	RawQuery  string
	Header    http.Header
	Body      string
	Discovery bool // GET .../models, issued by model resolution
}

// fakeUpstream replays a cassette's upstream_responses in order. Model
// discovery (GET .../models) is served out of band so it never consumes a
// recorded response.
type fakeUpstream struct {
	mu        sync.Mutex
	responses []UpstreamResponse
	next      int
	requests  []RecordedRequest
	models    []string
	overflow  int
}

func newFakeUpstream(responses []UpstreamResponse, models []string) *fakeUpstream {
	return &fakeUpstream{responses: responses, models: models}
}

func (u *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	discovery := r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models")

	u.mu.Lock()
	u.requests = append(u.requests, RecordedRequest{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
		Header: r.Header.Clone(), Body: string(body), Discovery: discovery,
	})
	var resp UpstreamResponse
	if !discovery {
		if u.next < len(u.responses) {
			resp = u.responses[u.next]
			u.next++
		} else {
			u.overflow++
		}
	}
	u.mu.Unlock()

	if discovery {
		items := make([]map[string]string, 0, len(u.models))
		for _, m := range u.models {
			items = append(items, map[string]string{"id": m, "object": "model"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": items})
		return
	}

	if resp.Status == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"conformance harness: no recorded upstream response left"}}`)
		return
	}

	ct := resp.ContentType
	if ct == "" {
		ct = "application/json"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.Status)
	_, _ = io.WriteString(w, resp.Body)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// stats reports how the upstream was exercised.
func (u *fakeUpstream) stats() (consumed, queued, overflow int, reqs []RecordedRequest) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.next, len(u.responses), u.overflow, append([]RecordedRequest(nil), u.requests...)
}

// firstCall returns the first non-discovery request the gateway made.
func (u *fakeUpstream) firstCall() (RecordedRequest, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, r := range u.requests {
		if !r.Discovery {
			return r, true
		}
	}
	return RecordedRequest{}, false
}

// ---- provider wiring ----

// providerSpec describes how to build one backend provider against a base URL
// and how it authenticates.
type providerSpec struct {
	// build constructs the provider pointed at baseURL (the scheme://host of
	// the fake upstream or the recording proxy).
	build func(baseURL, apiKey string) runtime.Provider
	// authHeader is the header carrying the credential, asserted present
	// whenever a key is configured.
	authHeader string
	// keyEnv lists the environment variables holding a live key, used by
	// record mode only.
	keyEnv []string
	// liveBaseURL is the real endpoint record mode proxies to.
	liveBaseURL string
	// replayKey is the placeholder credential used during replay.
	replayKey string
}

// providerSpecs is the curated provider set the conformance suite covers.
// Bedrock is excluded: it is signed by the AWS SDK and is unit-tested
// separately in services/runtime/provider_bedrock_test.go.
var providerSpecs = map[string]providerSpec{
	"openai": {
		build: func(baseURL, apiKey string) runtime.Provider {
			return runtime.NewOpenAICompatProvider("openai", baseURL+"/v1", apiKey)
		},
		authHeader:  "Authorization",
		keyEnv:      []string{"OPENAI_API_KEY", "DELOS_RUNTIME_OPENAI_KEY"},
		liveBaseURL: "https://api.openai.com",
		replayKey:   "sk-conformance-openai",
	},
	"anthropic": {
		build: func(baseURL, apiKey string) runtime.Provider {
			return runtime.NewAnthropicProvider(apiKey, runtime.WithAnthropicBaseURL(baseURL+"/v1"))
		},
		authHeader:  "X-Api-Key",
		keyEnv:      []string{"ANTHROPIC_API_KEY", "DELOS_RUNTIME_ANTHROPIC_KEY"},
		liveBaseURL: "https://api.anthropic.com",
		replayKey:   "sk-ant-conformance",
	},
	"gemini": {
		build: func(baseURL, apiKey string) runtime.Provider {
			return runtime.NewGeminiProvider(apiKey, runtime.WithGeminiBaseURL(baseURL+"/v1beta"))
		},
		authHeader:  "X-Goog-Api-Key",
		keyEnv:      []string{"GEMINI_API_KEY", "GOOGLE_API_KEY", "DELOS_RUNTIME_GEMINI_KEY"},
		liveBaseURL: "https://generativelanguage.googleapis.com",
		replayKey:   "conformance-gemini",
	},
	// Ollama stands in for every OpenAI-compatible endpoint (vLLM, SGLang,
	// Together, OpenRouter). It is unauthenticated by default.
	"ollama": {
		build: func(baseURL, apiKey string) runtime.Provider {
			return runtime.NewOpenAICompatProvider("ollama", baseURL+"/v1", apiKey)
		},
		keyEnv:      []string{"DELOS_RUNTIME_OLLAMA_URL"},
		liveBaseURL: "http://localhost:11434",
		replayKey:   "",
	},
}

// discardLogger keeps test output about the gateway, not from it.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newService builds the gateway's core over the given providers.
func newService(providers ...runtime.Provider) *runtime.RuntimeService {
	registry := runtime.NewRegistry()
	for _, p := range providers {
		registry.Register(p)
	}
	return runtime.NewRuntimeService(registry, discardLogger())
}

// ---- gateway response ----

// GatewayResponse is what the client observed.
type GatewayResponse struct {
	Status int
	Header http.Header
	Body   string
}

// JSON decodes the body as JSON.
func (r GatewayResponse) JSON() (any, error) {
	var doc any
	if err := json.Unmarshal([]byte(r.Body), &doc); err != nil {
		return nil, fmt.Errorf("response body is not JSON: %w", err)
	}
	return doc, nil
}

// callGateway posts the cassette request at a running gateway.
func callGateway(baseURL, endpoint string, body []byte, surface string) (GatewayResponse, error) {
	req, err := http.NewRequest(http.MethodPost, baseURL+endpoint, strings.NewReader(string(body)))
	if err != nil {
		return GatewayResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if surface == "anthropic" {
		req.Header.Set("x-api-key", "delos-dev")
		req.Header.Set("anthropic-version", "2023-06-01")
	} else {
		req.Header.Set("Authorization", "Bearer delos-dev")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return GatewayResponse{}, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return GatewayResponse{}, err
	}
	return GatewayResponse{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: string(out)}, nil
}

// ---- SSE reading (client side) ----

// StreamView is the client's view of a streamed gateway response.
type StreamView struct {
	Deltas     []string // text deltas in order
	Done       bool     // stream terminated properly
	Events     []string // SSE event names (anthropic surface)
	DataFrames []string // raw data payloads
}

// Text returns the concatenated deltas.
func (s StreamView) Text() string { return strings.Join(s.Deltas, "") }

// ReadStream parses a gateway SSE body for the given surface.
func ReadStream(surface, body string) StreamView {
	var view StreamView
	event := ""
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
			view.Events = append(view.Events, event)
			continue
		case !strings.HasPrefix(line, "data: "):
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		view.DataFrames = append(view.DataFrames, data)

		if surface == "anthropic" {
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if json.Unmarshal([]byte(data), &ev) != nil {
				continue
			}
			if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				view.Deltas = append(view.Deltas, ev.Delta.Text)
			}
			if ev.Type == "message_stop" {
				view.Done = true
			}
			continue
		}

		if data == "[DONE]" {
			view.Done = true
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content *string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != nil && *ch.Delta.Content != "" {
				view.Deltas = append(view.Deltas, *ch.Delta.Content)
			}
		}
	}
	return view
}

// serveService starts the production gateway HTTP surface over a configured
// service and returns its base URL plus a shutdown func.
func serveService(svc *runtime.RuntimeService, opts ...runtime.HTTPServerOption) (string, func()) {
	ts := httptest.NewServer(runtime.NewHTTPServer(svc, discardLogger(), opts...))
	return ts.URL, ts.Close
}

// serveGateway starts a gateway over the given providers and returns its base
// URL plus a shutdown func.
func serveGateway(providers ...runtime.Provider) (string, func()) {
	return serveService(newService(providers...))
}
