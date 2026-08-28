package runtime

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

// Virtual keys gate every request through the gateway. Keys are random,
// shown once at creation, and stored only as argon2id hashes. A key may be
// scoped to specific models and carry monthly token/dollar budgets.

// VirtualKey is a stored key record. The plaintext secret never appears here.
type VirtualKey struct {
	ID          string
	Name        string
	Prefix      string // first bytes of the secret, for lookup and display
	Hash        string // argon2id encoded hash of the full secret
	Models      []string
	TokenBudget int64   // monthly, 0 = unlimited
	USDBudget   float64 // monthly, 0 = unlimited
	Revoked     bool
	CreatedAt   time.Time
}

// KeyUsage is a key's consumption in one calendar month.
type KeyUsage struct {
	Tokens int64
	USD    float64
}

// AllowsModel reports whether the key may use the given model.
func (k *VirtualKey) AllowsModel(model string) bool {
	if len(k.Models) == 0 {
		return true
	}
	for _, m := range k.Models {
		if m == model {
			return true
		}
		// allow "provider/*" style scoping
		if strings.HasSuffix(m, "/*") && strings.HasPrefix(model, strings.TrimSuffix(m, "*")) {
			return true
		}
	}
	return false
}

// KeyStore persists virtual keys and their usage.
type KeyStore interface {
	CreateKey(ctx context.Context, key *VirtualKey) error
	// GetKeyByPrefix returns the non-revoked key with the given prefix.
	GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error)
	ListKeys(ctx context.Context) ([]*VirtualKey, error)
	RevokeKey(ctx context.Context, id string) error
	// AddUsage records consumption for a key in the month of now.
	AddUsage(ctx context.Context, keyID string, month string, tokens int64, usd float64) error
	// GetUsage returns consumption for a key in the given month ("2026-08").
	GetUsage(ctx context.Context, keyID string, month string) (KeyUsage, error)
}

// ErrKeyNotFound is returned when no key matches.
var ErrKeyNotFound = errors.New("key not found")

// ErrKeyStoreUnavailable reports that the key store could not answer, as
// distinct from answering "no such key". The two must not be collapsed: a
// database blip that reads as "key not found" turns every valid key into a 401
// telling the caller their key is invalid, which sends operators hunting for a
// key problem that does not exist. Surfaces map this to 503.
var ErrKeyStoreUnavailable = errors.New("key store unavailable")

// ErrKeyPrefixConflict reports that a key with the same lookup prefix already
// exists. Callers regenerate rather than storing a colliding key.
var ErrKeyPrefixConflict = errors.New("key prefix already in use")

// ---- key material ----

// keyPrefixLen is the length of the lookup prefix, in characters of the
// secret. "dk_" plus 16 base64url characters is 96 bits of prefix, which makes
// a collision between two generated keys unreachable in practice (a 50%
// chance needs ~3e14 keys).
//
// It used to be 8 — "dk_" plus five characters, about 30 bits, where a 50%
// chance of a collision arrives at roughly 38,000 keys. A collision was not
// merely a failed lookup: on the verification cache's hit path a request
// authenticated with one key could be attributed to, and charged against, the
// other key's budget and model scope. The stored prefix is public (it is shown
// by `delos key list`), and revealing 96 of the secret's 256 bits still leaves
// 160 bits unguessable.
const keyPrefixLen = 19

// legacyKeyPrefixLen is the prefix length of keys created before the widening.
// Lookups fall back to it so existing keys keep working; the fallback costs one
// extra store round trip and only on the legacy path.
const legacyKeyPrefixLen = 8

// keySecretBytes is the entropy in a generated secret.
const keySecretBytes = 32

// GenerateKey creates a new secret of the form "dk_<random>". The caller
// shows it once and stores only the record returned by HashKey.
func GenerateKey() (string, error) {
	raw := make([]byte, keySecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "dk_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// KeyPrefix returns the lookup prefix of a presented secret.
func KeyPrefix(secret string) string {
	if len(secret) <= keyPrefixLen {
		return secret
	}
	return secret[:keyPrefixLen]
}

// legacyKeyPrefix returns the pre-widening lookup prefix, or "" when the
// secret is too short to have one distinct from KeyPrefix.
func legacyKeyPrefix(secret string) string {
	if len(secret) <= legacyKeyPrefixLen {
		return ""
	}
	short := secret[:legacyKeyPrefixLen]
	if short == KeyPrefix(secret) {
		return ""
	}
	return short
}

// argon2id parameters: modest, since verification results are cached and the
// threat model is offline hash cracking of a 256-bit random secret.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

// maxConcurrentVerifications bounds how many argon2id verifications run at
// once. Each costs 64MB and four threads, and the prefix of every live key is
// public (`delos key list` prints it), so an attacker can aim unlimited
// garbage at a known-good prefix and force a verification per request. Without
// a bound that is a few hundred requests per second to exhaust memory and CPU.
// Four in flight caps the cost at ~256MB and 16 busy threads; the rest queue.
const maxConcurrentVerifications = 4

var verifySem = make(chan struct{}, maxConcurrentVerifications)

// verifyKeyBounded runs VerifyKey under the concurrency bound, giving up if
// the caller's context is cancelled while queued.
func verifyKeyBounded(ctx context.Context, secret, encoded string) (bool, error) {
	select {
	case verifySem <- struct{}{}:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	defer func() { <-verifySem }()
	return VerifyKey(secret, encoded), nil
}

// HashKey computes an encoded argon2id hash of the secret.
func HashKey(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(secret), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

// VerifyKey checks a presented secret against an encoded argon2id hash.
func VerifyKey(secret, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// ---- key creation ----

// KeySpec describes a key to create. It is everything about a key that is not
// generated.
type KeySpec struct {
	ID          string
	Name        string
	Models      []string
	TokenBudget int64
	USDBudget   float64
	CreatedAt   time.Time
}

// createKeyAttempts bounds prefix-collision retries. With a 96-bit prefix one
// attempt always suffices; the loop exists so that a collision is a retry
// rather than either a duplicate-key error to the operator or, worse, two live
// keys sharing a lookup prefix.
const createKeyAttempts = 5

// CreateVirtualKey generates a secret, stores its record, and returns the
// plaintext secret (shown once) alongside the stored key. It regenerates on a
// prefix collision instead of storing a second key under an existing prefix.
func CreateVirtualKey(ctx context.Context, store KeyStore, spec KeySpec) (string, *VirtualKey, error) {
	for attempt := 0; attempt < createKeyAttempts; attempt++ {
		secret, err := GenerateKey()
		if err != nil {
			return "", nil, fmt.Errorf("generating key: %w", err)
		}
		hash, err := HashKey(secret)
		if err != nil {
			return "", nil, fmt.Errorf("hashing key: %w", err)
		}
		created := spec.CreatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		key := &VirtualKey{
			ID:          spec.ID,
			Name:        spec.Name,
			Prefix:      KeyPrefix(secret),
			Hash:        hash,
			Models:      spec.Models,
			TokenBudget: spec.TokenBudget,
			USDBudget:   spec.USDBudget,
			CreatedAt:   created,
		}
		err = store.CreateKey(ctx, key)
		if errors.Is(err, ErrKeyPrefixConflict) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return secret, key, nil
	}
	return "", nil, fmt.Errorf("could not generate a key with an unused prefix after %d attempts", createKeyAttempts)
}

// ---- authenticator with verification cache ----

// KeyAuthenticator verifies presented keys against a KeyStore, caching both
// successful and failed verifications (argon2id is deliberately slow; the
// cache keeps it off the hot path).
type KeyAuthenticator struct {
	store KeyStore

	mu    sync.RWMutex
	cache map[string]cachedAuth // sha256(secret) -> result
}

// cachedAuth is one cached verification. key == nil records a verification
// that failed.
type cachedAuth struct {
	key     *VirtualKey
	expires time.Time
}

const (
	// authCacheTTL bounds how long a successful verification is reused. The
	// record is still re-read from the store on every request inside the
	// window, so revocation and budget changes take effect immediately; what
	// the cache saves is the argon2id verification, not the lookup.
	authCacheTTL = time.Minute

	// authNegativeCacheTTL bounds how long a failed verification is
	// remembered. Without it, an attacker who knows a live key's public prefix
	// forces a fresh 64MB argon2id hash on every request; with it, a flood of
	// one bad secret costs one hash per TTL. It is deliberately short so a key
	// that is rotated in is not locked out for long by an earlier typo.
	authNegativeCacheTTL = 30 * time.Second
)

// NewKeyAuthenticator wraps a KeyStore.
func NewKeyAuthenticator(store KeyStore) *KeyAuthenticator {
	return &KeyAuthenticator{store: store, cache: make(map[string]cachedAuth)}
}

// Authenticate resolves a presented secret to its key record.
//
// It returns ErrKeyNotFound for a secret that does not name a live key, and an
// error wrapping ErrKeyStoreUnavailable when the store could not be consulted.
// Callers must distinguish the two: the first is the caller's fault (401), the
// second is ours (503).
func (a *KeyAuthenticator) Authenticate(ctx context.Context, secret string) (*VirtualKey, error) {
	if secret == "" {
		return nil, ErrKeyNotFound
	}
	digest := sha256.Sum256([]byte(secret))
	cacheKey := hex.EncodeToString(digest[:])

	a.mu.RLock()
	entry, ok := a.cache[cacheKey]
	a.mu.RUnlock()
	if ok && time.Now().Before(entry.expires) {
		if entry.key == nil {
			// Negative hit: this exact secret already failed verification.
			return nil, ErrKeyNotFound
		}
		fresh, err := a.lookup(ctx, secret)
		switch {
		case err == nil && !fresh.Revoked && fresh.ID == entry.key.ID && fresh.Hash == entry.key.Hash:
			// The freshly loaded record is the very record this secret was
			// verified against — same ID, same hash — so reusing the
			// verification is sound without re-running argon2id. Re-reading
			// also means revocation and budget edits apply within the TTL.
			//
			// The old code returned whatever record shared the prefix and
			// verified nothing: with two keys on one prefix, a request
			// authenticated with key A was served as key B, inheriting B's
			// model scope and charging B's budget.
			return fresh, nil
		case errors.Is(err, ErrKeyStoreUnavailable):
			// Availability tradeoff, deliberate: a store outage must not
			// invalidate keys we have already verified. The entry stays in the
			// cache and keeps serving until its TTL runs out, so a brief
			// Postgres blip degrades to "revocations lag by up to a minute"
			// rather than "every key on the fleet is rejected". Evicting here
			// — what the old code did — destroyed exactly the state that could
			// have ridden out the outage.
			return entry.key, nil
		default:
			// Not found, revoked, or a different record on the same prefix:
			// drop the entry and fall through to a full verification.
			a.evict(cacheKey)
		}
	}

	key, err := a.lookup(ctx, secret)
	if err != nil {
		// A store failure is never reported as "invalid key".
		return nil, err
	}
	if key.Revoked {
		return nil, ErrKeyNotFound
	}
	verified, err := verifyKeyBounded(ctx, secret, key.Hash)
	if err != nil {
		return nil, fmt.Errorf("verifying key: %w", err)
	}
	if !verified {
		a.remember(cacheKey, cachedAuth{expires: time.Now().Add(authNegativeCacheTTL)})
		return nil, ErrKeyNotFound
	}

	a.remember(cacheKey, cachedAuth{key: key, expires: time.Now().Add(authCacheTTL)})
	return key, nil
}

// lookup finds the live key record for a secret, trying the current prefix
// length and then the legacy one. Store failures are wrapped in
// ErrKeyStoreUnavailable; a genuine miss is ErrKeyNotFound.
func (a *KeyAuthenticator) lookup(ctx context.Context, secret string) (*VirtualKey, error) {
	key, err := a.store.GetKeyByPrefix(ctx, KeyPrefix(secret))
	if err == nil {
		return key, nil
	}
	if !errors.Is(err, ErrKeyNotFound) {
		return nil, fmt.Errorf("%w: %w", ErrKeyStoreUnavailable, err)
	}
	if legacy := legacyKeyPrefix(secret); legacy != "" {
		key, err = a.store.GetKeyByPrefix(ctx, legacy)
		if err == nil {
			return key, nil
		}
		if !errors.Is(err, ErrKeyNotFound) {
			return nil, fmt.Errorf("%w: %w", ErrKeyStoreUnavailable, err)
		}
	}
	return nil, ErrKeyNotFound
}

func (a *KeyAuthenticator) remember(cacheKey string, entry cachedAuth) {
	a.mu.Lock()
	a.cache[cacheKey] = entry
	a.mu.Unlock()
}

func (a *KeyAuthenticator) evict(cacheKey string) {
	a.mu.Lock()
	delete(a.cache, cacheKey)
	a.mu.Unlock()
}

// CurrentMonth formats t as a usage month key.
func CurrentMonth(t time.Time) string { return t.UTC().Format("2006-01") }

// ---- in-memory store (dev mode and tests) ----

// MemoryKeyStore is an in-memory KeyStore for dev mode and tests.
type MemoryKeyStore struct {
	mu    sync.RWMutex
	keys  map[string]*VirtualKey // by ID
	usage map[string]KeyUsage    // keyID + "|" + month
}

// NewMemoryKeyStore creates an empty in-memory key store.
func NewMemoryKeyStore() *MemoryKeyStore {
	return &MemoryKeyStore{keys: make(map[string]*VirtualKey), usage: make(map[string]KeyUsage)}
}

func (s *MemoryKeyStore) CreateKey(ctx context.Context, key *VirtualKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Mirrors the UNIQUE index on virtual_keys.prefix: two live keys must
	// never share a lookup prefix, revoked ones included, so that a prefix
	// lookup can only ever return the key the secret belongs to.
	for _, existing := range s.keys {
		if existing.ID != key.ID && existing.Prefix == key.Prefix {
			return ErrKeyPrefixConflict
		}
	}
	s.keys[key.ID] = key
	return nil
}

func (s *MemoryKeyStore) GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// CreateKey enforces prefix uniqueness, so at most one key can match; the
	// lowest ID wins for any record predating that rule, so the answer never
	// depends on map iteration order.
	var found *VirtualKey
	for _, k := range s.keys {
		if k.Prefix != prefix || k.Revoked {
			continue
		}
		if found == nil || k.ID < found.ID {
			found = k
		}
	}
	if found == nil {
		return nil, ErrKeyNotFound
	}
	copied := *found
	return &copied, nil
}

func (s *MemoryKeyStore) ListKeys(ctx context.Context) ([]*VirtualKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*VirtualKey, 0, len(s.keys))
	for _, k := range s.keys {
		copied := *k
		out = append(out, &copied)
	}
	return out, nil
}

func (s *MemoryKeyStore) RevokeKey(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.keys[id]
	if !ok {
		return ErrKeyNotFound
	}
	k.Revoked = true
	return nil
}

func (s *MemoryKeyStore) AddUsage(ctx context.Context, keyID, month string, tokens int64, usd float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.usage[keyID+"|"+month]
	u.Tokens += tokens
	u.USD += usd
	s.usage[keyID+"|"+month] = u
	return nil
}

func (s *MemoryKeyStore) GetUsage(ctx context.Context, keyID, month string) (KeyUsage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usage[keyID+"|"+month], nil
}
