package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- stop ----

func TestStopNullIsUnset(t *testing.T) {
	// Client SDKs that serialize unset optionals as explicit null used to
	// get stop: [""], which every provider rejects with a 400 the caller
	// cannot explain.
	var req chatCompletionRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-4o","stop":null}`), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Stop) != 0 {
		t.Errorf("stop = %#v, want empty for an explicit null", req.Stop)
	}

	if err := json.Unmarshal([]byte(`{"model":"gpt-4o","stop":"END"}`), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "END" {
		t.Errorf("stop = %#v, want [END]", req.Stop)
	}

	if err := json.Unmarshal([]byte(`{"model":"gpt-4o","stop":["A","B"]}`), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Stop) != 2 {
		t.Errorf("stop = %#v, want two sequences", req.Stop)
	}

	// An empty string is a real (if useless) stop sequence, and stays one.
	if err := json.Unmarshal([]byte(`{"model":"gpt-4o","stop":""}`), &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Stop) != 1 || req.Stop[0] != "" {
		t.Errorf("stop = %#v, want the caller's explicit empty string", req.Stop)
	}
}

func TestStopNullReachesTheProvider(t *testing.T) {
	p := chatProvider()
	srv := newTestHTTPServer(p)
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","stop":null,"messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if len(p.lastParams.Stop) != 0 {
		t.Errorf("provider received stop = %#v, want none", p.lastParams.Stop)
	}
}

// ---- data: URIs ----

func TestParseImageURL(t *testing.T) {
	t.Run("base64 passthrough", func(t *testing.T) {
		part, err := parseImageURL("data:image/png;base64,AAAA")
		if err != nil {
			t.Fatal(err)
		}
		if part.MediaType != "image/png" || part.ImageData != "AAAA" || part.ImageURL != "" {
			t.Errorf("part = %+v", part)
		}
	})

	t.Run("percent-encoded data is re-encoded, not relabelled", func(t *testing.T) {
		// ";base64" used to be trimmed unconditionally and re-added
		// downstream, so raw bytes were shipped upstream labelled as
		// base64 - a corrupt image the provider could only reject.
		part, err := parseImageURL("data:image/svg+xml,%3Csvg%3E%3C/svg%3E")
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.StdEncoding.DecodeString(part.ImageData)
		if err != nil {
			t.Fatalf("ImageData is not valid base64: %v", err)
		}
		if string(decoded) != "<svg></svg>" {
			t.Errorf("decoded = %q, want the original bytes", decoded)
		}
		if part.MediaType != "image/svg+xml" {
			t.Errorf("media type = %q", part.MediaType)
		}
	})

	t.Run("media-type parameters are dropped", func(t *testing.T) {
		part, err := parseImageURL("data:image/png;charset=utf-8;base64,AAAA")
		if err != nil {
			t.Fatal(err)
		}
		if part.MediaType != "image/png" {
			t.Errorf("media type = %q, want the bare type", part.MediaType)
		}
	})

	t.Run("remote URLs pass through", func(t *testing.T) {
		part, err := parseImageURL("https://example.com/cat.png")
		if err != nil {
			t.Fatal(err)
		}
		if part.ImageURL != "https://example.com/cat.png" || part.ImageData != "" {
			t.Errorf("part = %+v", part)
		}
	})

	t.Run("malformed URIs are rejected with a reason", func(t *testing.T) {
		for _, raw := range []string{"data:image/png;base64", "data:,AAAA", "data:;base64,AAAA"} {
			if _, err := parseImageURL(raw); err == nil {
				t.Errorf("%q was accepted", raw)
			}
		}
	})
}

// ---- /v1/models ----

func TestListModelsRequiresAuth(t *testing.T) {
	// Enumerating every configured provider tells an anonymous caller which
	// paid vendors the operator uses, and for OpenAI-compatible backends it
	// triggers a live upstream call.
	store := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, store, "team", 0, 0)
	srv := authTestServer(t, store)

	rec, out := doJSON(t, srv, http.MethodGet, "/v1/models", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/models without a key = %d, want 401", rec.Code)
	}
	if out["error"].(map[string]any)["code"] != "invalid_api_key" {
		t.Errorf("error = %v", out)
	}

	rec, _ = doJSON(t, srv, http.MethodGet, "/v1/models/gpt-4o", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /v1/models/{model} without a key = %d, want 401", rec.Code)
	}

	req := jsonReq(http.MethodGet, "/v1/models", "")
	req.Header.Set("Authorization", "Bearer "+secret)
	if rec := doReq(srv, req); rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/models with a valid key = %d, want 200", rec.Code)
	}
}

func TestListModelsIsScopedToTheKey(t *testing.T) {
	store := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, store, "scoped", 0, 0, "gpt-4o")
	srv := authTestServer(t, store)

	req := jsonReq(http.MethodGet, "/v1/models", "")
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var out struct {
		Data []modelObject `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Data {
		if m.ID != "gpt-4o" {
			t.Errorf("key scoped to gpt-4o can see %q", m.ID)
		}
	}
	if len(out.Data) != 1 {
		t.Errorf("models = %+v, want only the one in scope", out.Data)
	}

	// The same scoping applies to a direct lookup.
	req = jsonReq(http.MethodGet, "/v1/models/text-embedding-3-small", "")
	req.Header.Set("Authorization", "Bearer "+secret)
	if rec := doReq(srv, req); rec.Code != http.StatusNotFound {
		t.Errorf("out-of-scope model lookup = %d, want 404", rec.Code)
	}
}

// ---- stream metering ----

// unmeteredStreamProvider streams content and never reports usage, the way
// several OpenAI-compatible servers behave.
type unmeteredStreamProvider struct {
	mockProvider
}

func (p *unmeteredStreamProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	p.lastParams = &params
	ch := make(chan StreamChunk, 4)
	ch <- StreamChunk{Delta: "Hello ", Provider: "openai"}
	ch <- StreamChunk{Delta: "world, this is a reasonably long answer.", Provider: "openai"}
	ch <- StreamChunk{Done: true, FinishReason: FinishStop, Provider: "openai"} // no Usage
	close(ch)
	return ch, nil
}

func meteringServer(t *testing.T, p Provider) (*HTTPServer, KeyStore, string, *VirtualKey) {
	t.Helper()
	store := NewMemoryKeyStore()
	secret, key := newStoredKey(t, store, "metered", 0, 0)
	srv := NewHTTPServer(newTestService(p), newTestLogger(), WithKeyStore(store))
	return srv, store, secret, key
}

func TestStreamIsMeteredWhenTheProviderReportsNoUsage(t *testing.T) {
	// Metering only what the provider volunteers makes every request to a
	// backend that omits usage free, which is a budget bypass rather than a
	// rounding error.
	p := &unmeteredStreamProvider{mockProvider{name: "openai", models: []string{"gpt-4o"}}}
	srv, store, secret, key := meteringServer(t, p)

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	usage, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tokens <= 0 {
		t.Fatalf("recorded %d tokens for a completed stream; an unmetered stream is a budget bypass", usage.Tokens)
	}

	// include_usage promises a final usage chunk. The promise is to the
	// client, not to the provider: it is kept even when the numbers had to
	// be estimated.
	body := rec.Body.String()
	if !strings.Contains(body, `"usage"`) {
		t.Errorf("stream_options.include_usage was set but no usage chunk was emitted:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("stream did not terminate cleanly:\n%s", body)
	}
}

func TestStreamUsageChunkOmittedWithoutIncludeUsage(t *testing.T) {
	p := &unmeteredStreamProvider{mockProvider{name: "openai", models: []string{"gpt-4o"}}}
	srv, _, secret, _ := meteringServer(t, p)

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)
	if strings.Contains(rec.Body.String(), `"usage"`) {
		t.Errorf("usage chunk emitted without stream_options.include_usage:\n%s", rec.Body.String())
	}
}

// brokenStreamProvider delivers content and then dies.
type brokenStreamProvider struct {
	mockProvider
}

func (p *brokenStreamProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	p.lastParams = &params
	ch := make(chan StreamChunk, 3)
	ch <- StreamChunk{Delta: "a long partial answer that was really generated", Provider: "openai"}
	ch <- StreamChunk{Err: &ProviderError{Provider: "openai", StatusCode: 502, Message: "connection reset"}}
	close(ch)
	return ch, nil
}

func TestBrokenStreamIsStillMetered(t *testing.T) {
	// Tokens delivered before a stream broke were generated upstream and
	// billed by the provider; writing them off would make "disconnect late"
	// a free tier.
	p := &brokenStreamProvider{mockProvider{name: "openai", models: []string{"gpt-4o"}}}
	srv, store, secret, key := meteringServer(t, p)

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	doReq(srv, req)

	usage, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tokens <= 0 {
		t.Errorf("a stream that delivered content before breaking recorded %d tokens", usage.Tokens)
	}
}

func TestEstimateStreamUsage(t *testing.T) {
	params := CompletionParams{Messages: []Message{TextMessage("user", "hello there")}}
	u := estimateStreamUsage(params, "a delivered answer")
	if u.PromptTokens <= 0 || u.CompletionTokens <= 0 {
		t.Errorf("usage = %+v, want a non-zero estimate on both sides", u)
	}
	if u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		t.Errorf("total = %d, want the sum of %d and %d", u.TotalTokens, u.PromptTokens, u.CompletionTokens)
	}
	// Cost cannot be estimated without the provider's pricing, and inventing
	// one would be worse than reporting none.
	if u.CostUSD != 0 {
		t.Errorf("estimated cost = %v, want 0", u.CostUSD)
	}
	if estimateTokens("") != 0 {
		t.Error("the empty string is zero tokens")
	}
	if estimateTokens("a") != 1 {
		t.Error("a non-empty string is never zero tokens")
	}
}

// ---- cache-hit billing over HTTP ----

func TestCacheHitIsNotBilledOverHTTP(t *testing.T) {
	p := chatProvider()
	store := NewMemoryKeyStore()
	secret, key := newStoredKey(t, store, "cached", 0, 0)
	svc := newTestService(p)
	svc.SetCache(NewLRUCache(10), 0)
	srv := NewHTTPServer(svc, newTestLogger(), WithKeyStore(store))

	body := `{"model":"gpt-4o","temperature":0,"messages":[{"role":"user","content":"hi"}]}`
	send := func() *httptest.ResponseRecorder {
		req := jsonReq(http.MethodPost, "/v1/chat/completions", body)
		req.Header.Set("Authorization", "Bearer "+secret)
		return doReq(srv, req)
	}

	if rec := send(); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	afterMiss, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		t.Fatal(err)
	}
	if afterMiss.USD == 0 {
		t.Fatal("a real provider call must be billed")
	}

	rec := send()
	if rec.Header().Get("X-Delos-Cache") != "hit" {
		t.Fatalf("second identical deterministic request was not a cache hit (body %s)", rec.Body.String())
	}
	afterHit, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(nowFunc()))
	if err != nil {
		t.Fatal(err)
	}
	if afterHit.USD != afterMiss.USD {
		t.Errorf("cache hit changed billed cost from %v to %v; it cost nothing upstream", afterMiss.USD, afterHit.USD)
	}
	if afterHit.Tokens != afterMiss.Tokens {
		t.Errorf("cache hit changed billed tokens from %d to %d", afterMiss.Tokens, afterHit.Tokens)
	}

	var out chatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Usage == nil || out.Usage.CostUSD != 0 {
		t.Errorf("cached response reports cost %v, want 0 alongside X-Delos-Cache: hit", out.Usage)
	}
	if out.Usage.TotalTokens == 0 {
		t.Error("token counts describe the payload the client received and should survive a cache hit")
	}
}
