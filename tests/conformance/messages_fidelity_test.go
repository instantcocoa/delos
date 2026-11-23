package conformance

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestAnthropicMessagesUnsupportedParams pins what POST /v1/messages does
// TODAY with the fields gap 1 (docs/plans/delos-harness.md) says the narrow
// request struct cannot carry. The D0 task for DL-T asks for this to be
// recorded, because docs/providers.md promises:
//
//	"Unsupported parameters are rejected with a structured 400 - never
//	 silently dropped."
//
// The recorded reality is the baseline the Bifrost swap is measured against:
// in D1 these fields must start passing through to the provider (and the
// passthrough-fidelity suite replaces these assertions). Until then this test
// documents divergence, not endorsement: each case records whether the gateway
// passes the field through, drops it silently, or rejects it with a
// structured 400.
//
// The suite is hermetic: a fake Anthropic Messages upstream records exactly
// what the gateway forwards, so pass-through versus drop is observed on the
// wire rather than inferred from the request struct.
func TestAnthropicMessagesUnsupportedParams(t *testing.T) {
	const model = "claude-sonnet-4-5"

	cases := []struct {
		name        string
		body        map[string]any
		header      http.Header
		wantStatus  int
		wantBody    []string // substrings the gateway response must contain
		wantCall    bool     // whether the gateway should reach the provider
		wantUpBody  []string // substrings the forwarded request must contain
		wantNotBody []string // substrings the forwarded request must NOT contain (dropped)
		wantHeader  []string // header names the forwarded request must carry
		wantNotHdr  []string // header names the forwarded request must NOT carry (dropped)
	}{
		{
			name: "thinking block is silently dropped",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
				"thinking":   map[string]any{"type": "enabled", "budget_tokens": 1024},
			},
			wantStatus:  http.StatusOK,
			wantCall:    true,
			wantNotBody: []string{`"thinking"`, `budget_tokens`},
		},
		{
			name: "cache_control on an input block is silently dropped",
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
			wantStatus:  http.StatusOK,
			wantCall:    true,
			wantUpBody:  []string{"You are terse.", "hi"},
			wantNotBody: []string{`cache_control`, `ephemeral`},
		},
		{
			name: "top-level metadata is silently dropped",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
				"metadata":   map[string]any{"user_id": "12345"},
			},
			wantStatus:  http.StatusOK,
			wantCall:    true,
			wantNotBody: []string{`"metadata"`, `user_id`},
		},
		{
			name: "document content block is rejected with a structured 400",
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
			wantStatus: http.StatusBadRequest,
			wantCall:   false, // rejected locally, before any provider call
			wantBody:   []string{`invalid_request_error`, `document`},
		},
		{
			name: "anthropic-beta header is silently dropped",
			body: map[string]any{
				"model":      model,
				"max_tokens": 64,
				"messages":   []map[string]any{{"role": "user", "content": "hi"}},
			},
			header:     http.Header{"Anthropic-Beta": []string{"prompt-caching-2024-07-31"}},
			wantStatus: http.StatusOK,
			wantCall:   true,
			wantHeader: []string{"Anthropic-Version"}, // the header we do forward
			wantNotHdr: []string{"Anthropic-Beta"},    // the beta header never reaches the provider
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			up := newAnthropicMessagesUpstream(t)
			spec := providerSpecs["anthropic"]
			provider := spec.build(up.baseURL(), spec.replayKey)
			gwURL, stop := serveGateway(provider)
			t.Cleanup(stop)

			resp := postAnthropicMessages(t, gwURL, tc.body, tc.header)
			if resp.Status != tc.wantStatus {
				t.Fatalf("status = %d, want %d\nbody: %s", resp.Status, tc.wantStatus, truncate(resp.Body, 2000))
			}
			for _, want := range tc.wantBody {
				if !strings.Contains(resp.Body, want) {
					t.Errorf("gateway response does not contain %q\nbody: %s", want, truncate(resp.Body, 2000))
				}
			}

			calls := up.calls()
			if !tc.wantCall {
				if len(calls) != 0 {
					t.Errorf("gateway made %d provider call(s) for a request it should have rejected locally", len(calls))
				}
				t.Logf("behavior: structured 400 (invalid_request_error), no provider call\nbody: %s", truncate(resp.Body, 500))
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
					t.Errorf("forwarded request body contains %q, which the client sent and the gateway silently dropped\nbody: %s",
						unwanted, truncate(call.body, 2000))
				}
			}
			for _, want := range tc.wantHeader {
				if call.header.Get(want) == "" {
					t.Errorf("forwarded request is missing the %s header (headers: %v)", want, call.header)
				}
			}
			for _, unwanted := range tc.wantNotHdr {
				if call.header.Get(unwanted) != "" {
					t.Errorf("forwarded request carries %s, which the client sent and the gateway silently dropped", unwanted)
				}
			}
			t.Logf("behavior: 200 OK, field silently dropped (not forwarded to the provider)")
		})
	}
}

// recordedAnthropicCall is one request the fake Anthropic upstream received.
type recordedAnthropicCall struct {
	path   string
	header http.Header
	body   string
}

// anthropicMessagesUpstream is a minimal Anthropic Messages API that records
// what the gateway forwards and answers with a fixed, valid message.
type anthropicMessagesUpstream struct {
	url string

	mu       sync.Mutex
	recorded []recordedAnthropicCall
}

func newAnthropicMessagesUpstream(t *testing.T) *anthropicMessagesUpstream {
	t.Helper()
	u := &anthropicMessagesUpstream{}
	u.url = newLoopbackServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"claude-sonnet-4-5","object":"model"}]}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		u.recorded = append(u.recorded, recordedAnthropicCall{path: r.URL.Path, header: r.Header.Clone(), body: string(body)})
		u.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"msg_fidelity","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":1}}`)
	}))
	return u
}

func (u *anthropicMessagesUpstream) baseURL() string { return u.url }

func (u *anthropicMessagesUpstream) calls() []recordedAnthropicCall {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]recordedAnthropicCall(nil), u.recorded...)
}

// postAnthropicMessages posts a raw request at the gateway's /v1/messages,
// carrying the headers a real Anthropic client sends plus any extras.
func postAnthropicMessages(t *testing.T, gwURL string, body map[string]any, extra http.Header) GatewayResponse {
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
