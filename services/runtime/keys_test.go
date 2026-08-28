package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
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

// ---- key prefix collisions ----

// A lookup prefix must be wide enough that two generated keys never share it,
// and long enough not to leak the secret it is taken from.
func TestKeyPrefixWidth(t *testing.T) {
	secret, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	prefix := KeyPrefix(secret)
	if len(prefix) != keyPrefixLen {
		t.Fatalf("prefix %q has length %d, want %d", prefix, len(prefix), keyPrefixLen)
	}
	// "dk_" plus 16 base64url characters is 96 bits, versus the 30 bits that
	// gave a 50% collision chance at ~38k keys.
	if bits := (len(prefix) - len("dk_")) * 6; bits < 90 {
		t.Errorf("prefix carries %d bits, want at least 90", bits)
	}
	// The prefix is public; the remainder of the secret must not be.
	if len(secret)-len(prefix) < 20 {
		t.Errorf("only %d characters of the secret stay private", len(secret)-len(prefix))
	}
}

// The store must refuse a second key on an existing prefix rather than store
// it, and CreateVirtualKey must regenerate instead of failing.
func TestKeyPrefixCollisionIsRefused(t *testing.T) {
	store := NewMemoryKeyStore()
	ctx := context.Background()

	first := &VirtualKey{ID: "k1", Name: "first", Prefix: "dk_sameprefix000000", Hash: "h1"}
	if err := store.CreateKey(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := &VirtualKey{ID: "k2", Name: "second", Prefix: "dk_sameprefix000000", Hash: "h2"}
	if err := store.CreateKey(ctx, second); !errors.Is(err, ErrKeyPrefixConflict) {
		t.Fatalf("second key on the same prefix: err = %v, want ErrKeyPrefixConflict", err)
	}

	keys, err := store.ListKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("store holds %d keys, want 1", len(keys))
	}

	// Even a revoked key keeps its prefix reserved, so revoke-and-recreate
	// cannot resurrect an ambiguity.
	if err := store.RevokeKey(ctx, "k1"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateKey(ctx, second); !errors.Is(err, ErrKeyPrefixConflict) {
		t.Errorf("prefix of a revoked key was reused: err = %v", err)
	}
}

// CreateVirtualKey retries past a collision instead of surfacing one.
func TestCreateVirtualKeyRetriesPastCollision(t *testing.T) {
	store := &collidingStore{MemoryKeyStore: NewMemoryKeyStore(), failures: 2}
	secret, key, err := CreateVirtualKey(context.Background(), store, KeySpec{ID: "k1", Name: "team"})
	if err != nil {
		t.Fatalf("CreateVirtualKey: %v", err)
	}
	if store.attempts != 3 {
		t.Errorf("attempts = %d, want 3 (two collisions then success)", store.attempts)
	}
	if key.Prefix != KeyPrefix(secret) {
		t.Errorf("stored prefix %q does not match the secret", key.Prefix)
	}
	if !VerifyKey(secret, key.Hash) {
		t.Error("stored hash does not verify the returned secret")
	}
	if key.CreatedAt.IsZero() {
		t.Error("CreatedAt was not set")
	}
}

// collidingStore reports a prefix conflict for its first N creates.
type collidingStore struct {
	*MemoryKeyStore
	failures int
	attempts int
}

func (s *collidingStore) CreateKey(ctx context.Context, key *VirtualKey) error {
	s.attempts++
	if s.attempts <= s.failures {
		return ErrKeyPrefixConflict
	}
	return s.MemoryKeyStore.CreateKey(ctx, key)
}

// ---- cache-hit re-verification ----

// prefixCollisionStore returns one fixed record for every prefix lookup,
// standing in for two keys that share a prefix (or for a record swapped out
// from under the cache).
type prefixCollisionStore struct {
	*MemoryKeyStore
	serve *VirtualKey
}

func (s *prefixCollisionStore) GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error) {
	copied := *s.serve
	return &copied, nil
}

// On a cache hit the authenticator re-read the record by prefix and returned
// it without verifying the secret against it. With two keys on one prefix, a
// request authenticated as key A was served as key B — inheriting B's model
// scope and charging B's budget. The cached verification may only be reused
// when the freshly loaded record is the one that was actually verified.
func TestAuthCacheHitDoesNotServeAnotherKey(t *testing.T) {
	ctx := context.Background()
	backing := NewMemoryKeyStore()
	secretA, keyA := newStoredKey(t, backing, "alpha", 0, 0)

	store := &prefixCollisionStore{MemoryKeyStore: backing, serve: keyA}
	auth := NewKeyAuthenticator(store)

	got, err := auth.Authenticate(ctx, secretA)
	if err != nil {
		t.Fatalf("first authenticate: %v", err)
	}
	if got.ID != keyA.ID {
		t.Fatalf("got key %s, want %s", got.ID, keyA.ID)
	}

	// Now the same prefix resolves to a different key. The warm cache entry
	// must not be honoured for it.
	other := &VirtualKey{
		ID: "key-beta", Name: "beta", Prefix: keyA.Prefix,
		Hash: "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2Fs$bm90LXRoZS1yaWdodC1kaWdlc3QtYXQtYWxs",
	}
	store.serve = other

	got, err = auth.Authenticate(ctx, secretA)
	if err == nil && got.ID != keyA.ID {
		t.Fatalf("secret for %s authenticated as %s", keyA.ID, got.ID)
	}
	if err == nil {
		t.Fatalf("a record the secret does not verify against was accepted as %s", got.ID)
	}
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("err = %v, want ErrKeyNotFound", err)
	}
}

// Revocation still takes effect within the cache TTL.
func TestAuthCacheHonoursRevocation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryKeyStore()
	secret, key := newStoredKey(t, store, "revokable", 0, 0)
	auth := NewKeyAuthenticator(store)

	if _, err := auth.Authenticate(ctx, secret); err != nil {
		t.Fatalf("first authenticate: %v", err)
	}
	if err := store.RevokeKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Authenticate(ctx, secret); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("revoked key on a warm cache: err = %v, want ErrKeyNotFound", err)
	}
}

// ---- store outages ----

// flakyStore fails every operation with a transport-shaped error while down.
type flakyStore struct {
	*MemoryKeyStore
	down bool
	err  error
}

func (s *flakyStore) GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error) {
	if s.down {
		return nil, s.err
	}
	return s.MemoryKeyStore.GetKeyByPrefix(ctx, prefix)
}

// A store failure is not "your key is invalid". Collapsing the two turned a
// database blip into a fleet-wide 401 telling every caller to check a
// credential that was fine.
func TestAuthStoreFailureIsNotUnauthorized(t *testing.T) {
	ctx := context.Background()
	backing := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, backing, "team", 0, 0)
	store := &flakyStore{MemoryKeyStore: backing, err: errors.New("dial tcp: connection refused")}
	auth := NewKeyAuthenticator(store)

	store.down = true
	_, err := auth.Authenticate(ctx, secret)
	if err == nil {
		t.Fatal("a request during a store outage was authenticated from a cold cache")
	}
	if !errors.Is(err, ErrKeyStoreUnavailable) {
		t.Fatalf("err = %v, want ErrKeyStoreUnavailable", err)
	}
	if errors.Is(err, ErrKeyNotFound) {
		t.Error("store failure reported as an invalid key")
	}
}

// A warm cache is what lets the gateway ride out a brief outage, so a store
// error must not evict it.
func TestAuthCacheSurvivesStoreOutage(t *testing.T) {
	ctx := context.Background()
	backing := NewMemoryKeyStore()
	secret, key := newStoredKey(t, backing, "resilient", 0, 0)
	store := &flakyStore{MemoryKeyStore: backing, err: errors.New("dial tcp: connection refused")}
	auth := NewKeyAuthenticator(store)

	if _, err := auth.Authenticate(ctx, secret); err != nil {
		t.Fatalf("warming the cache: %v", err)
	}

	store.down = true
	got, err := auth.Authenticate(ctx, secret)
	if err != nil {
		t.Fatalf("cached key rejected during a store outage: %v", err)
	}
	if got.ID != key.ID {
		t.Errorf("got key %s, want %s", got.ID, key.ID)
	}

	// And the entry is still there when the store comes back.
	store.down = false
	if _, err := auth.Authenticate(ctx, secret); err != nil {
		t.Errorf("after recovery: %v", err)
	}
}

// The gateway surface maps a store outage to 503, not 401.
func TestHTTPStoreOutageIsServiceUnavailable(t *testing.T) {
	backing := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, backing, "team", 0, 0)
	store := &flakyStore{MemoryKeyStore: backing, err: errors.New("dial tcp: connection refused"), down: true}
	srv := authTestServer(t, store)

	req := jsonReq(http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := doReq(srv, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "invalid or missing API key") {
		t.Error("a store outage was reported to the caller as an invalid key")
	}

	// Same on the Anthropic surface.
	req = jsonReq(http.MethodPost, "/v1/messages",
		`{"model":"gpt-4o","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	req.Header.Set("X-Api-Key", secret)
	if rec := doReq(srv, req); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("anthropic surface status = %d, want 503", rec.Code)
	}
}

// ---- negative caching and the argon2 bound ----

// countingStore counts prefix lookups, which stand in one-for-one for argon2
// verifications on the miss path.
type countingStore struct {
	*MemoryKeyStore
	mu      sync.Mutex
	lookups int
}

func (s *countingStore) GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error) {
	s.mu.Lock()
	s.lookups++
	s.mu.Unlock()
	return s.MemoryKeyStore.GetKeyByPrefix(ctx, prefix)
}

// Failed verifications are cached. Every live key's prefix is public, so
// without negative caching an attacker aiming garbage at a known prefix forces
// a fresh 64MB argon2id hash per request.
func TestAuthNegativeCaching(t *testing.T) {
	ctx := context.Background()
	backing := NewMemoryKeyStore()
	_, key := newStoredKey(t, backing, "target", 0, 0)
	store := &countingStore{MemoryKeyStore: backing}
	auth := NewKeyAuthenticator(store)

	// A secret with the right prefix but the wrong body: the expensive case.
	forged := key.Prefix + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	for i := range 20 {
		if _, err := auth.Authenticate(ctx, forged); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("attempt %d: err = %v, want ErrKeyNotFound", i, err)
		}
	}

	store.mu.Lock()
	lookups := store.lookups
	store.mu.Unlock()
	if lookups != 1 {
		t.Errorf("%d store lookups (and argon2 verifications) for 20 identical bad keys, want 1", lookups)
	}
}

// A rejected secret that later becomes valid must not stay rejected past the
// negative TTL, and a valid secret must never land in the negative cache.
func TestAuthNegativeCacheDoesNotShadowValidKeys(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryKeyStore()
	secret, _ := newStoredKey(t, store, "good", 0, 0)
	auth := NewKeyAuthenticator(store)

	if _, err := auth.Authenticate(ctx, secret+"tampered"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("tampered secret: err = %v", err)
	}
	if _, err := auth.Authenticate(ctx, secret); err != nil {
		t.Fatalf("valid secret rejected after a nearby failure: %v", err)
	}
	if authNegativeCacheTTL >= authCacheTTL {
		t.Errorf("negative TTL %v should be shorter than the positive TTL %v", authNegativeCacheTTL, authCacheTTL)
	}
}

// Concurrent verifications are bounded: argon2id costs 64MB and four threads
// each, so an unbounded flood is a memory and CPU exhaustion primitive.
func TestVerifyConcurrencyIsBounded(t *testing.T) {
	if cap(verifySem) != maxConcurrentVerifications {
		t.Fatalf("semaphore capacity = %d, want %d", cap(verifySem), maxConcurrentVerifications)
	}
	if maxConcurrentVerifications <= 0 || maxConcurrentVerifications > 16 {
		t.Errorf("maxConcurrentVerifications = %d is not a useful bound", maxConcurrentVerifications)
	}

	secret, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := HashKey(secret)
	if err != nil {
		t.Fatal(err)
	}

	// A cancelled caller gives up its place in the queue rather than blocking.
	ctx, cancel := context.WithCancel(context.Background())
	for range maxConcurrentVerifications {
		verifySem <- struct{}{}
	}
	cancel()
	if _, err := verifyKeyBounded(ctx, secret, hash); err == nil {
		t.Error("a cancelled verification should not have proceeded")
	}
	for range maxConcurrentVerifications {
		<-verifySem
	}

	ok, err := verifyKeyBounded(context.Background(), secret, hash)
	if err != nil || !ok {
		t.Errorf("verifyKeyBounded = (%v, %v), want (true, nil)", ok, err)
	}
}

// ---- budgets ----

// A key with a budget but no key store must be refused, not panic. recordUsage
// already guarded the nil store; the budget lookup did not.
func TestBudgetCheckWithoutKeyStore(t *testing.T) {
	srv := newTestHTTPServer(chatProvider()) // no WithKeyStore, so keyStore is nil
	key := &VirtualKey{ID: "k", Name: "budgeted", TokenBudget: 100}

	be := srv.checkKeyLimits(context.Background(), key, "gpt-4o")
	if be == nil {
		t.Fatal("a budgeted key with no store was allowed through")
	}
	if got := budgetStatus(be); got != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", got)
	}
}

// Concurrent requests must see each other's in-flight reservations, so a key
// one request away from its budget cannot admit an unbounded burst. Before
// reservations, every concurrent request read the same pre-burst usage and all
// of them passed: a key at 999/1000 tokens could run any number of 1M-token
// requests at once.
func TestBudgetReservationsBoundConcurrentBursts(t *testing.T) {
	store := NewMemoryKeyStore()
	ctx := context.Background()

	// Ten reservations' worth of token budget, nothing spent yet. Token
	// budgets are integers, so the arithmetic here is exact.
	const slots = 10
	key := &VirtualKey{ID: "k-burst", Name: "burst", TokenBudget: slots * reservedTokensPerRequest}
	if err := store.CreateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	srv := authTestServer(t, store)

	admitted := 0
	for range slots * 3 {
		if be := srv.checkKeyLimits(ctx, key, "gpt-4o"); be == nil {
			admitted++
		}
	}
	if admitted == slots*3 {
		t.Fatal("every concurrent request was admitted; reservations are not visible to each other")
	}
	// The request whose reservation would reach the budget exactly is the one
	// refused, so slots-1 get through.
	if admitted != slots-1 {
		t.Errorf("admitted %d concurrent requests, want %d", admitted, slots-1)
	}

	// Releasing the in-flight reservations frees the budget again.
	for range admitted {
		keyBudgets.release(key.ID)
	}
	if be := srv.checkKeyLimits(ctx, key, "gpt-4o"); be != nil {
		t.Errorf("request refused after reservations were released: %s", be.msg)
	}
	keyBudgets.release(key.ID)
}

// A refused request must not keep its reservation.
func TestBudgetReservationReleasedOnRefusal(t *testing.T) {
	store := NewMemoryKeyStore()
	ctx := context.Background()
	key := &VirtualKey{ID: "k-refused", Name: "refused", TokenBudget: 10}
	if err := store.CreateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUsage(ctx, key.ID, CurrentMonth(nowFunc()), 100, 0); err != nil {
		t.Fatal(err)
	}
	srv := authTestServer(t, store)

	if be := srv.checkKeyLimits(ctx, key, "gpt-4o"); be == nil {
		t.Fatal("over-budget request admitted")
	}
	keyBudgets.mu.Lock()
	held := len(keyBudgets.inflight[key.ID])
	keyBudgets.mu.Unlock()
	if held != 0 {
		t.Errorf("%d reservations still held after a refusal", held)
	}
}

// Leaked reservations — a request that errors before recording usage — expire
// rather than costing the key budget for the rest of the month.
func TestBudgetReservationsExpire(t *testing.T) {
	ledger := &budgetLedger{inflight: map[string][]reservationEntry{
		"k": {{tokens: 1_000_000, usd: 100, expires: nowFunc().Add(-time.Hour)}},
	}}
	tokens, usd := ledger.reserve("k")
	if tokens != reservedTokensPerRequest || usd != reservedUSDPerRequest {
		t.Errorf("expired reservation still counted: tokens=%d usd=%v", tokens, usd)
	}
}
