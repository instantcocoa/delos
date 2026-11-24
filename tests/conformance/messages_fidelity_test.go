package conformance

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestAnthropicMessagesFidelityBifrost pins the D1 contract for
// POST /v1/messages (note 0001 §"Wire behavior changes", gap 1). It replaces
// the D0 baseline test of the same name, which recorded the OLD behavior
// (thinking/cache_control/metadata/document/beta silently dropped or
// rejected) on team/dl-t/d0-baseline. The post-D1 behavior on the Bifrost
// path is byte-faithful same-provider passthrough:
//
//   - same-provider (Anthropic→Anthropic) routes go through Bifrost
//     PassthroughStream: the request body and anthropic-* headers are
//     forwarded as sent, so thinking, cache_control, metadata and document
//     blocks all pass through, and an anthropic-beta header reaches the
//     provider unchanged.
//
// The test is hermetic: a fake Anthropic Messages upstream records exactly
// what the gateway forwards, so pass-through versus drop is observed on the
// wire rather than inferred from the request struct. It drives the Bifrost
// path directly via NewBifrostHTTPServer and points Bifrost's env account at
// the fake upstream (DELOS_BIFROST_ANTHROPIC_BASE_URL + ANTHROPIC_API_KEY).
func TestAnthropicMessagesFidelityBifrost(t *testing.T) {
	const model = "claude-sonnet-4-5"

	cases := []struct {
		name        string
		body        map[string]any
		header      http.Header
		wantStatus  int
		wantCall    bool     // whether the gateway should reach the provider
		wantUpBody  []string // substrings the forwarded request must contain
		wantNotBody []string // substrings the forwarded request must NOT contain
		wantHeader  []string // header names the forwarded request must carry
	}{
		{
			name: "thinking block passes through",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
				"thinking":   map[string]any{"type": "enabled", "budget_tokens": 1024},
			},
			wantStatus: http.StatusOK,
			wantCall:   true,
			wantUpBody: []string{`"thinking"`, `budget_tokens`},
		},
		{
			name: "cache_control on an input block passes through",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"system": []map[string]any{{
					"type":          "text",
					"text":          "You are terse.",
					"cache_control": map[string]any{"type": "ephemeral"},
				}},
				"messages": []map[string]any{{
					"role": "user",
					"content": []map[string]any{{
						"type":          "text",
						"text":          "hi",
						"cache_control": map[string]any{"type": "ephemeral"},
					}},
				}},
			},
			wantStatus: http.StatusOK,
			wantCall:   true,
			wantUpBody: []string{`cache_control`, `ephemeral`},
		},
		{
			name: "top-level metadata passes through",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
				"metadata":   map[string]any{"user_id": "12345"},
			},
			wantStatus: http.StatusOK,
			wantCall:   true,
			wantUpBody: []string{`"metadata"`, `user_id`},
		},
		{
			name: "document content block passes through",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages": []map[string]any{{
					"role": "user",
					"content": []map[string]any{{
						"type":   "document",
						"source": map[string]any{"type": "text", "media_type": "text/plain", "data": "..."},
					}},
				}},
			},
			wantStatus: http.StatusOK,
			wantCall:   true,
			wantUpBody: []string{`"document"`, `media_type`},
		},
		{
			name: "anthropic-beta header passes through",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
			},
			header:     http.Header{"Anthropic-Beta": []string{"prompt-caching-2024-07-31"}},
			wantStatus: http.StatusOK,
			wantCall:   true,
			wantHeader: []string{"Anthropic-Version", "Anthropic-Beta"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newAnthropicFidelityUpstream(t)
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-conformance")
			t.Setenv("DELOS_BIFROST_ANTHROPIC_BASE_URL", up.baseURL())

			gwURL, stop := serveBifrostService(newService())
			t.Cleanup(stop)

			resp := postAnthropicFidelity(t, gwURL, tc.body, tc.header)
			if resp.Status != tc.wantStatus {
				t.Fatalf("status = %d, want %d\nbody: %s", resp.Status, tc.wantStatus, truncate(resp.Body, 2000))
			}

			calls := up.calls()
			if !tc.wantCall {
				if len(calls) != 0 {
					t.Errorf("gateway made %d provider call(s) for a request it should have rejected locally", len(calls))
				}
				return
			}
			if len(calls) == 0 {
				t.Fatal("gateway never called the provider")
			}
			call := calls[0]
			for _, want := range tc.wantUpBody {
				if !strings.Contains(call.body, want) {
					t.Errorf("forwarded request body does not contain %q\nbody: %s", want, truncate(call.body, 2000))
				}
			}
			for _, unwanted := range tc.wantNotBody {
				if strings.Contains(call.body, unwanted) {
					t.Errorf("forwarded request body contains %q, which must not be forwarded\nbody: %s",
						unwanted, truncate(call.body, 2000))
				}
			}
			for _, want := range tc.wantHeader {
				if call.header.Get(want) == "" {
					t.Errorf("forwarded request is missing the %s header (headers: %v)", want, call.header)
				}
			}
		})
	}
}

// recordedFidelityCall is one request the fake Anthropic upstream received.
type recordedFidelityCall struct {
	path   string
	header http.Header
	body   string
}

// anthropicFidelityUpstream is a minimal Anthropic Messages API that records
// what the gateway forwards and answers with a fixed, valid message.
type anthropicFidelityUpstream struct {
	url string

	mu       sync.Mutex
	recorded []recordedFidelityCall
}

func newAnthropicFidelityUpstream(t *testing.T) *anthropicFidelityUpstream {
	t.Helper()
	u := &anthropicFidelityUpstream{}
	u.url = newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"claude-sonnet-4-5","object":"model"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.recorded = append(u.recorded, recordedFidelityCall{path: r.URL.Path, header: r.Header.Clone(), body: string(body)})
		u.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"msg_fidelity","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`)
	}))
	return u
}

func (u *anthropicFidelityUpstream) baseURL() string { return u.url }

func (u *anthropicFidelityUpstream) calls() []recordedFidelityCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]recordedFidelityCall(nil), u.recorded...)
}

// postAnthropicFidelity posts a raw request at the gateway's /v1/messages,
// carrying the headers a real Anthropic client sends plus any extras.
func postAnthropicFidelity(t *testing.T, gwURL string, body map[string]any, extra http.Header) GatewayResponse {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, gwURL+"/v1/messages", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "delos-dev")
	req.Header.Set("anthropic-version", "2023-06-01")
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("gateway call failed: %v", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading gateway response: %v", err)
	}
	return GatewayResponse{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: string(out)}
}
