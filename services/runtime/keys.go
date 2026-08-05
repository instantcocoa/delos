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

// ---- key material ----

const keyPrefixLen = 8

// GenerateKey creates a new secret of the form "dk_<random>". The caller
// shows it once and stores only the record returned by HashKey.
func GenerateKey() (string, error) {
	raw := make([]byte, 24)
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

// argon2id parameters: modest, since verification results are cached and the
// threat model is offline hash cracking of a 192-bit random secret.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

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

// ---- authenticator with verification cache ----

// KeyAuthenticator verifies presented keys against a KeyStore, caching
// successful verifications (argon2id is deliberately slow; the cache keeps it
// off the hot path).
type KeyAuthenticator struct {
	store KeyStore

	mu    sync.RWMutex
	cache map[string]cachedAuth // sha256(secret) -> result
}

type cachedAuth struct {
	key     *VirtualKey
	expires time.Time
}

const authCacheTTL = time.Minute

// NewKeyAuthenticator wraps a KeyStore.
func NewKeyAuthenticator(store KeyStore) *KeyAuthenticator {
	return &KeyAuthenticator{store: store, cache: make(map[string]cachedAuth)}
}

// Authenticate resolves a presented secret to its key record.
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
		// Revocation must take effect within the TTL: re-check the record.
		fresh, err := a.store.GetKeyByPrefix(ctx, entry.key.Prefix)
		if err != nil || fresh.Revoked {
			a.evict(cacheKey)
			return nil, ErrKeyNotFound
		}
		return fresh, nil
	}

	key, err := a.store.GetKeyByPrefix(ctx, KeyPrefix(secret))
	if err != nil {
		return nil, ErrKeyNotFound
	}
	if key.Revoked || !VerifyKey(secret, key.Hash) {
		return nil, ErrKeyNotFound
	}

	a.mu.Lock()
	a.cache[cacheKey] = cachedAuth{key: key, expires: time.Now().Add(authCacheTTL)}
	a.mu.Unlock()
	return key, nil
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
	s.keys[key.ID] = key
	return nil
}

func (s *MemoryKeyStore) GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.keys {
		if k.Prefix == prefix && !k.Revoked {
			copied := *k
			return &copied, nil
		}
	}
	return nil, ErrKeyNotFound
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
