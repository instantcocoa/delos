package runtime

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestKeyHashRoundTrip(t *testing.T) {
	secret, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "dk_") {
		t.Errorf("secret = %q", secret)
	}
	hash, err := HashKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, secret) {
		t.Fatal("hash must not contain the secret")
	}
	if !VerifyKey(secret, hash) {
		t.Error("valid secret rejected")
	}
	if VerifyKey(secret+"x", hash) {
		t.Error("wrong secret accepted")
	}
	if VerifyKey(secret, "garbage") {
		t.Error("garbage hash accepted")
	}
}

func TestKeyAllowsModel(t *testing.T) {
	unrestricted := &VirtualKey{}
	if !unrestricted.AllowsModel("anything") {
		t.Error("empty scope must allow all models")
	}
	scoped := &VirtualKey{Models: []string{"gpt-4o", "anthropic/*"}}
	if !scoped.AllowsModel("gpt-4o") || !scoped.AllowsModel("anthropic/claude-x") {
		t.Error("scoped models should be allowed")
	}
	if scoped.AllowsModel("gemini-2.5-flash") {
		t.Error("out-of-scope model allowed")
	}
}

func newStoredKey(t *testing.T, store KeyStore, name string, budgetTokens int64, budgetUSD float64, models ...string) (string, *VirtualKey) {
	t.Helper()
	secret, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	key := &VirtualKey{
		ID:          "key-" + name,
		Name:        name,
		Prefix:      KeyPrefix(secret),
		Hash:        hash,
		Models:      models,
		TokenBudget: budgetTokens,
		USDBudget:   budgetUSD,
		CreatedAt:   time.Now(),
	}
	if err := store.CreateKey(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	return secret, key
}

func TestAuthenticator(t *testing.T) {
	store := NewMemoryKeyStore()
	secret, key := newStoredKey(t, store, "test", 0, 0)
	auth := NewKeyAuthenticator(store)
	ctx := context.Background()

	got, err := auth.Authenticate(ctx, secret)
	if err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	if got.ID != key.ID {
		t.Errorf("got key %s", got.ID)
	}

	// cached path
	if _, err := auth.Authenticate(ctx, secret); err != nil {
		t.Fatalf("cached auth failed: %v", err)
	}

	if _, err := auth.Authenticate(ctx, "dk_wrong"); err == nil {
		t.Error("invalid key accepted")
	}
	if _, err := auth.Authenticate(ctx, ""); err == nil {
		t.Error("empty key accepted")
	}

	// revocation takes effect even with a warm cache
	if err := store.RevokeKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, secret); err == nil {
		t.Error("revoked key accepted")
	}
}

// authTestServer builds an HTTP server with key enforcement and one provider.
func authTestServer(t *testing.T, store KeyStore) *HTTPServer {
	t.Helper()
	return NewHTTPServer(newTestService(chatProvider()), newTestLogger(), WithKeyStore(store))
}

func TestHTTPAuthRequired(t *testing.T) {
	store := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, store, "team", 0, 0)
	srv := authTestServer(t, store)

	// no key -> 401
	rec, out := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if out["error"].(map[string]any)["code"] != "invalid_api_key" {
		t.Errorf("error = %v", out)
	}

	// valid key -> 200
	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec = doReq(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status with valid key = %d: %s", rec.Code, rec.Body.String())
	}

	// x-api-key also works (Anthropic-style clients)
	req = jsonReq(http.MethodPost, "/v1/messages",
		`{"model":"gpt-4o","max_tokens":50,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("X-Api-Key", secret)
	rec = doReq(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("x-api-key status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPModelScope(t *testing.T) {
	store := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, store, "scoped", 0, 0, "some-other-model")
	srv := authTestServer(t, store)

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPBudgetExceeded(t *testing.T) {
	store := NewMemoryKeyStore()
	secret, key := newStoredKey(t, store, "capped", 100, 0)
	srv := authTestServer(t, store)

	// consume the budget
	if err := store.AddUsage(context.Background(), key.ID, CurrentMonth(time.Now()), 100, 0); err != nil {
		t.Fatal(err)
	}

	provider := chatProvider()
	// rebuild server with a provider we can observe
	srv = NewHTTPServer(newTestService(provider), newTestLogger(), WithKeyStore(store))

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "budget_exceeded") {
		t.Errorf("expected budget_exceeded code: %s", rec.Body.String())
	}
	// the provider must never have been contacted
	if provider.lastParams != nil {
		t.Error("over-budget request reached the provider")
	}
}

func TestHTTPUsageRecorded(t *testing.T) {
	store := NewMemoryKeyStore()
	secret, key := newStoredKey(t, store, "metered", 0, 0)
	srv := authTestServer(t, store)

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}

	usage, err := store.GetUsage(context.Background(), key.ID, CurrentMonth(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if usage.Tokens != 15 { // chatProvider's mock usage
		t.Errorf("recorded tokens = %d, want 15", usage.Tokens)
	}
	if usage.USD == 0 {
		t.Error("recorded cost should be non-zero")
	}
}

func TestDevModeNoAuth(t *testing.T) {
	srv := newTestHTTPServer(chatProvider()) // no WithKeyStore
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dev mode must not require a key, got %d", rec.Code)
	}
}

// The gateway must not read an unbounded request body: an oversized payload is
// rejected rather than buffered into memory.
func TestRequestBodyLimit(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())

	huge := strings.Repeat("x", maxRequestBody+1024)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"` + huge + `"}]}`
	rec := doReq(srv, jsonReq(http.MethodPost, "/v1/chat/completions", body))

	if rec.Code == http.StatusOK {
		t.Fatalf("oversized body was accepted (status %d)", rec.Code)
	}
}

// Authentication happens before the body is parsed, so an unauthenticated
// client cannot make the gateway spend memory decoding a large payload.
func TestAuthPrecedesBodyParsing(t *testing.T) {
	store := NewMemoryKeyStore()
	newStoredKey(t, store, "gate", 0, 0)
	srv := authTestServer(t, store)

	// Malformed JSON with no key: the 401 must win over the parse error.
	rec := doReq(srv, jsonReq(http.MethodPost, "/v1/chat/completions", `{"model":`))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 before any body parsing", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "could not parse") {
		t.Error("body was parsed before authentication")
	}

	// Same on the Anthropic surface.
	rec = doReq(srv, jsonReq(http.MethodPost, "/v1/messages", `{"model":`))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anthropic surface status = %d, want 401", rec.Code)
	}
}
