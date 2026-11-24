package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/postgresconn"

	"github.com/instantcocoa/delos/pkg/database"
)

// This file is DL-K's Bifrost-backed key store (gap 4). It is the persistence
// layer for the control-plane KeyService and, through the same tables, for the
// gateway's Bifrost governance enforcement (DL-G). The gateway's legacy
// argon2id key path (keys.go, keys_postgres.go) stays intact on this branch;
// DL-G removes it once the Bifrost path holds.

// Metadata keys carried on every minted virtual key. DL-G reads these to stamp
// spans (see docs/design/notes/0002-keys-on-bifrost-governance.md).
const (
	MetadataSessionID = "delos.session_id"
	MetadataOrgID     = "delos.org_id"
	MetadataSubject   = "delos.subject"
	MetadataLabels    = "delos.labels"
	MetadataModels    = "delos.models"
)

// BudgetSpec is one spending limit with a reset window. ResetDuration is a
// Bifrost duration string ("30s", "1h", "1d", "1w", "1M", "1Q", "1Y"); empty
// means total (never resets within the key's life), stored as "1Y" because
// Bifrost has no "never reset" sentinel.
type BudgetSpec struct {
	MaxLimitUSD   float64
	ResetDuration string
}

// RateLimitSpec sets token and/or request limits. A zero max is unlimited.
type RateLimitSpec struct {
	TokenMaxLimit        int64
	TokenResetDuration   string
	RequestMaxLimit      int64
	RequestResetDuration string
}

// MintSpec is everything about a key that is not generated.
type MintSpec struct {
	SessionID string
	OrgID     string
	Subject   string
	Labels    map[string]string
	Name      string
	Models    []string // empty = all models
	ExpiresAt time.Time
	Budgets   []BudgetSpec
	RateLimit *RateLimitSpec
}

// KeyRecord is the stored key, read back without its secret.
type KeyRecord struct {
	ID        string
	Name      string
	SessionID string
	OrgID     string
	Subject   string
	Labels    map[string]string
	Models    []string
	Budgets   []BudgetSpec
	RateLimit *RateLimitSpec
	ExpiresAt time.Time
	Active    bool
	CreatedAt time.Time
}

// BudgetUsageRecord is a budget's current consumption.
type BudgetUsageRecord struct {
	ID               string
	MaxLimitUSD      float64
	ResetDuration    string
	CurrentUsageUSD  float64
	LastReset        time.Time
}

// RateLimitUsageRecord is a rate limit's current consumption.
type RateLimitUsageRecord struct {
	ID                   string
	TokenMaxLimit        int64
	TokenCurrentUsage    int64
	RequestMaxLimit      int64
	RequestCurrentUsage  int64
}

// UsageReport is a key's consumption across its budgets and rate limit.
type UsageReport struct {
	KeyID     string
	Budgets   []BudgetUsageRecord
	RateLimit *RateLimitUsageRecord
}

// AllowsModel mirrors the legacy VirtualKey.AllowsModel rule so DL-G enforces
// model scope uniformly from the key metadata. Empty scope allows everything;
// "provider/*" matches every model under the provider.
func (k *KeyRecord) AllowsModel(model string) bool {
	if len(k.Models) == 0 {
		return true
	}
	for _, m := range k.Models {
		if m == model {
			return true
		}
		if strings.HasSuffix(m, "/*") && strings.HasPrefix(model, strings.TrimSuffix(m, "*")) {
			return true
		}
	}
	return false
}

// ExpiredAt reports whether the key has passed its expiry; zero ExpiresAt means
// never expires. now == expires_at is treated as expired.
func (k *KeyRecord) ExpiredAt(now time.Time) bool {
	if k.ExpiresAt.IsZero() {
		return false
	}
	return !now.UTC().Before(k.ExpiresAt.UTC())
}

// KeyServiceStore persists minted keys, their revocation state and their usage.
// Both the Bifrost (Postgres) and the in-memory implementations must satisfy
// this contract; the table-driven tests in keys_bifrost_test.go run the same
// assertions against both.
type KeyServiceStore interface {
	MintKey(ctx context.Context, spec MintSpec) (secret string, key *KeyRecord, err error)
	RevokeKey(ctx context.Context, id string) (*KeyRecord, error)
	GetKey(ctx context.Context, id string) (*KeyRecord, error)
	ListKeys(ctx context.Context) ([]*KeyRecord, error)
	GetKeyUsage(ctx context.Context, id string) (*UsageReport, error)
	Close(ctx context.Context) error
}

// ErrKeyStoreUnavailable is reused here with the same meaning as in keys.go: a
// database blip must not read as "no such key" to a caller.

// ---- Bifrost (Postgres) implementation ----

// BifrostKeyStore is a KeyServiceStore over Bifrost's governance config store.
type BifrostKeyStore struct {
	cs configstore.ConfigStore
}

// NewBifrostKeyStore wraps an already-open Bifrost config store.
func NewBifrostKeyStore(cs configstore.ConfigStore) *BifrostKeyStore {
	return &BifrostKeyStore{cs: cs}
}

// ConfigStore exposes the underlying store for DL-G / callers that need the
// same handle the governance plugin reads.
func (s *BifrostKeyStore) ConfigStore() configstore.ConfigStore { return s.cs }

// BifrostDBConfig is the Postgres target for the Bifrost config store. The
// control plane reuses the same DELOS_DB_* values it already connects with,
// but the Bifrost config store lives in its own database so Bifrost's own
// migrations (prompt repos and the like) cannot collide with Delos's tables.
type BifrostDBConfig struct {
	Host          string
	Port          string
	User          string
	Password      string
	DBName        string
	SSLMode       string
	BifrostDBName string // defaults to DBName + "_bifrost" when empty
}

// OpenBifrostKeyStore opens Bifrost's Postgres config store (running Bifrost's
// own migrations) and returns a KeyServiceStore over it. The Delos key
// migrations (003) are applied by the caller once Bifrost's tables exist; see
// OpenKeyServiceStore.
func OpenBifrostKeyStore(ctx context.Context, cfg BifrostDBConfig, logger *slog.Logger) (*BifrostKeyStore, error) {
	bifrostDB := cfg.BifrostDBName
	if bifrostDB == "" {
		bifrostDB = cfg.DBName + "_bifrost"
	}
	pc := &postgresconn.Config{
		Host:     schemas.NewSecretVar(cfg.Host),
		Port:     schemas.NewSecretVar(cfg.Port),
		User:     schemas.NewSecretVar(cfg.User),
		Password: schemas.NewSecretVar(cfg.Password),
		DBName:   schemas.NewSecretVar(bifrostDB),
		SSLMode:  schemas.NewSecretVar(cfg.SSLMode),
	}
	csCfg := &configstore.Config{
		Enabled: true,
		Type:    configstore.ConfigStoreTypePostgres,
		Config:  pc,
	}
	cs, err := configstore.NewConfigStore(ctx, csCfg, &slogBifrostLogger{l: logger})
	if err != nil {
		return nil, fmt.Errorf("failed to open Bifrost config store: %w", err)
	}
	return NewBifrostKeyStore(cs), nil
}

// OpenKeyServiceStore opens the production KeyServiceStore: Bifrost's config
// store, then Delos's key migrations (which create the key_migration ledger),
// then the non-destructive copy of legacy dk_ keys. db is the control plane's
// existing Postgres handle (for Delos migrations and the ledger); the Bifrost
// config store opens its own pool against the same database.
func OpenKeyServiceStore(ctx context.Context, db *sql.DB, cfg BifrostDBConfig, logger *slog.Logger) (KeyServiceStore, error) {
	store, err := OpenBifrostKeyStore(ctx, cfg, logger)
	if err != nil {
		return nil, err
	}

	migrator := database.NewMigrator(&database.DB{DB: db}, "keys").WithLogger(logger)
	if err := migrator.LoadMigrations(KeysMigrations, "migrations/keys"); err != nil {
		_ = store.Close(ctx)
		return nil, fmt.Errorf("failed to load key migrations: %w", err)
	}
	if err := migrator.Up(ctx); err != nil {
		_ = store.Close(ctx)
		return nil, fmt.Errorf("failed to apply key migrations: %w", err)
	}

	if n, err := MigrateLegacyKeys(ctx, db, store.ConfigStore()); err != nil {
		_ = store.Close(ctx)
		return nil, fmt.Errorf("failed to migrate legacy dk_ keys: %w", err)
	} else if n > 0 {
		logger.Info("migrated legacy dk_ keys into Bifrost governance", "count", n)
	}
	return store, nil
}

// slogBifrostLogger adapts *slog.Logger to schemas.Logger for the config store.
type slogBifrostLogger struct{ l *slog.Logger }

func (s *slogBifrostLogger) Debug(msg string, args ...any) { s.l.Debug(fmt.Sprintf(msg, args...)) }
func (s *slogBifrostLogger) Info(msg string, args ...any)  { s.l.Info(fmt.Sprintf(msg, args...)) }
func (s *slogBifrostLogger) Warn(msg string, args ...any)  { s.l.Warn(fmt.Sprintf(msg, args...)) }
func (s *slogBifrostLogger) Error(msg string, args ...any) { s.l.Error(fmt.Sprintf(msg, args...)) }
func (s *slogBifrostLogger) Fatal(msg string, args ...any) { s.l.Error(fmt.Sprintf(msg, args...)) }
func (s *slogBifrostLogger) SetLevel(level schemas.LogLevel) {}
func (s *slogBifrostLogger) SetOutputType(outputType schemas.LoggerOutputType) {
}
func (s *slogBifrostLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func boolPtr(b bool) *bool        { return &b }
func strPtr(s string) *string     { return &s }
func int64Ptr(n int64) *int64     { return &n }

// totalResetDuration is how an empty ResetDuration ("total budget") is stored:
// Bifrost requires a positive reset window and has no "never reset" sentinel.
// A year is total for a session key, which lives minutes to hours.
const totalResetDuration = "1Y"

func effectiveResetDuration(d string) (string, error) {
	if d == "" {
		return totalResetDuration, nil
	}
	if _, err := tables.ParseDuration(d); err != nil {
		return "", fmt.Errorf("invalid reset duration %q: %w", d, err)
	}
	return d, nil
}

// metadataFor builds the key metadata carrying session attribution, labels and
// model scope. All values pass Bifrost's ValidateVirtualKeyMetadata.
func metadataFor(spec MintSpec) (map[string]string, error) {
	md := make(map[string]string, 4)
	if spec.SessionID != "" {
		md[MetadataSessionID] = spec.SessionID
	}
	if spec.OrgID != "" {
		md[MetadataOrgID] = spec.OrgID
	}
	if spec.Subject != "" {
		md[MetadataSubject] = spec.Subject
	}
	if len(spec.Labels) > 0 {
		raw, err := json.Marshal(spec.Labels)
		if err != nil {
			return nil, fmt.Errorf("encoding labels: %w", err)
		}
		md[MetadataLabels] = string(raw)
	}
	if len(spec.Models) > 0 {
		raw, err := json.Marshal(spec.Models)
		if err != nil {
			return nil, fmt.Errorf("encoding models: %w", err)
		}
		md[MetadataModels] = string(raw)
	}
	return md, nil
}

func parseModels(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseLabels(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// tableToRecord converts a Bifrost virtual key back to a KeyRecord, without
// ever exposing the secret.
func tableToRecord(vk *tables.TableVirtualKey) *KeyRecord {
	if vk == nil {
		return nil
	}
	rec := &KeyRecord{
		ID:        vk.ID,
		Name:      vk.Name,
		SessionID: vk.Metadata[MetadataSessionID],
		OrgID:     vk.Metadata[MetadataOrgID],
		Subject:   vk.Metadata[MetadataSubject],
		Labels:    parseLabels(vk.Metadata[MetadataLabels]),
		Models:    parseModels(vk.Metadata[MetadataModels]),
		Active:    vk.IsActiveValue(),
		CreatedAt: vk.CreatedAt,
	}
	if vk.ExpiresAt != nil {
		rec.ExpiresAt = *vk.ExpiresAt
	}
	for _, b := range vk.Budgets {
		rec.Budgets = append(rec.Budgets, BudgetSpec{MaxLimitUSD: b.MaxLimit, ResetDuration: b.ResetDuration})
	}
	if vk.RateLimit != nil {
		rl := vk.RateLimit
		rec.RateLimit = &RateLimitSpec{}
		if rl.TokenMaxLimit != nil {
			rec.RateLimit.TokenMaxLimit = *rl.TokenMaxLimit
		}
		if rl.TokenResetDuration != nil {
			rec.RateLimit.TokenResetDuration = *rl.TokenResetDuration
		}
		if rl.RequestMaxLimit != nil {
			rec.RateLimit.RequestMaxLimit = *rl.RequestMaxLimit
		}
		if rl.RequestResetDuration != nil {
			rec.RateLimit.RequestResetDuration = *rl.RequestResetDuration
		}
	}
	return rec
}

func (s *BifrostKeyStore) MintKey(ctx context.Context, spec MintSpec) (string, *KeyRecord, error) {
	secret, err := GenerateKey()
	if err != nil {
		return "", nil, fmt.Errorf("generating key: %w", err)
	}
	keyID := uuid.NewString()
	now := time.Now().UTC()
	name := spec.Name
	if name == "" {
		name = "key-" + keyID[:8]
	}

	md, err := metadataFor(spec)
	if err != nil {
		return "", nil, err
	}

	// Rate limit is created first because the key references it. The rate limit
	// row carries no FK back, so ordering against the key is one-way.
	var rateLimitID *string
	if spec.RateLimit != nil {
		rl, err := s.buildRateLimit(keyID, spec.RateLimit, now)
		if err != nil {
			return "", nil, err
		}
		if err := s.cs.CreateRateLimit(ctx, rl); err != nil {
			return "", nil, fmt.Errorf("creating rate limit: %w", err)
		}
		rateLimitID = &rl.ID
	}

	// Two-phase mint: the key is created inactive, its budgets are created, and
	// only then is it activated. A failure while creating budgets therefore
	// leaves an inactive key (never a live key with no budgets).
	active := false
	vk := &tables.TableVirtualKey{
		ID:                keyID,
		Name:              name,
		Value:             *schemas.NewSecretVar(secret),
		IsActive:          &active,
		AllowAllProviders: len(spec.Models) == 0,
		RateLimitID:       rateLimitID,
		Metadata:          md,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if !spec.ExpiresAt.IsZero() {
		exp := spec.ExpiresAt.UTC()
		vk.ExpiresAt = &exp
	}
	if err := s.cs.CreateVirtualKey(ctx, vk); err != nil {
		return "", nil, fmt.Errorf("creating virtual key: %w", err)
	}

	for _, b := range spec.Budgets {
		budget, err := s.buildBudget(keyID, b, now)
		if err != nil {
			return "", nil, err
		}
		if err := s.cs.CreateBudget(ctx, budget); err != nil {
			return "", nil, fmt.Errorf("creating budget: %w", err)
		}
	}

	// Activate now that every budget exists.
	vk.IsActive = boolPtr(true)
	if err := s.cs.UpdateVirtualKey(ctx, vk); err != nil {
		return "", nil, fmt.Errorf("activating virtual key: %w", err)
	}

	// Re-read the persisted row so the returned record carries the budgets and
	// rate limit (preloaded by GetVirtualKey), not just the in-memory struct.
	persisted, err := s.cs.GetVirtualKey(ctx, keyID)
	if err != nil {
		return "", nil, fmt.Errorf("reading minted key: %w", err)
	}
	return secret, tableToRecord(persisted), nil
}

func (s *BifrostKeyStore) buildBudget(keyID string, spec BudgetSpec, now time.Time) (*tables.TableBudget, error) {
	reset, err := effectiveResetDuration(spec.ResetDuration)
	if err != nil {
		return nil, err
	}
	if spec.MaxLimitUSD <= 0 {
		return nil, errors.New("budget max_limit_usd must be positive")
	}
	return &tables.TableBudget{
		ID:            uuid.NewString(),
		MaxLimit:      spec.MaxLimitUSD,
		ResetDuration: reset,
		LastReset:     now,
		CurrentUsage:  0,
		VirtualKeyID:  &keyID,
		CreatedAt:     now,
		UpdatedAt:     now,
	}, nil
}

func (s *BifrostKeyStore) buildRateLimit(keyID string, spec *RateLimitSpec, now time.Time) (*tables.TableRateLimit, error) {
	rl := &tables.TableRateLimit{
		ID:               uuid.NewString(),
		TokenLastReset:   now,
		RequestLastReset: now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if spec.TokenMaxLimit > 0 {
		if _, err := tables.ParseDuration(spec.TokenResetDuration); err != nil {
			return nil, fmt.Errorf("invalid token reset duration %q: %w", spec.TokenResetDuration, err)
		}
		rl.TokenMaxLimit = int64Ptr(spec.TokenMaxLimit)
		rl.TokenResetDuration = strPtr(spec.TokenResetDuration)
	}
	if spec.RequestMaxLimit > 0 {
		if _, err := tables.ParseDuration(spec.RequestResetDuration); err != nil {
			return nil, fmt.Errorf("invalid request reset duration %q: %w", spec.RequestResetDuration, err)
		}
		rl.RequestMaxLimit = int64Ptr(spec.RequestMaxLimit)
		rl.RequestResetDuration = strPtr(spec.RequestResetDuration)
	}
	return rl, nil
}

func (s *BifrostKeyStore) RevokeKey(ctx context.Context, id string) (*KeyRecord, error) {
	vk, err := s.cs.GetVirtualKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}
	inactive := false
	vk.IsActive = &inactive
	if err := s.cs.UpdateVirtualKey(ctx, vk); err != nil {
		return nil, fmt.Errorf("revoking virtual key: %w", err)
	}
	return tableToRecord(vk), nil
}

func (s *BifrostKeyStore) GetKey(ctx context.Context, id string) (*KeyRecord, error) {
	vk, err := s.cs.GetVirtualKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}
	return tableToRecord(vk), nil
}

func (s *BifrostKeyStore) ListKeys(ctx context.Context) ([]*KeyRecord, error) {
	vks, err := s.cs.GetVirtualKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*KeyRecord, 0, len(vks))
	for i := range vks {
		if rec := tableToRecord(&vks[i]); rec != nil {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *BifrostKeyStore) GetKeyUsage(ctx context.Context, id string) (*UsageReport, error) {
	vk, err := s.cs.GetVirtualKey(ctx, id)
	if err != nil {
		if errors.Is(err, configstore.ErrNotFound) {
			return nil, ErrKeyNotFound
		}
		return nil, err
	}
	report := &UsageReport{KeyID: id}
	for _, b := range vk.Budgets {
		report.Budgets = append(report.Budgets, BudgetUsageRecord{
			ID:              b.ID,
			MaxLimitUSD:     b.MaxLimit,
			ResetDuration:   b.ResetDuration,
			CurrentUsageUSD: b.CurrentUsage,
			LastReset:       b.LastReset,
		})
	}
	if vk.RateLimit != nil {
		rl := vk.RateLimit
		u := &RateLimitUsageRecord{
			ID:                  rl.ID,
			TokenCurrentUsage:   rl.TokenCurrentUsage,
			RequestCurrentUsage: rl.RequestCurrentUsage,
		}
		if rl.TokenMaxLimit != nil {
			u.TokenMaxLimit = *rl.TokenMaxLimit
		}
		if rl.RequestMaxLimit != nil {
			u.RequestMaxLimit = *rl.RequestMaxLimit
		}
		report.RateLimit = u
	}
	return report, nil
}

func (s *BifrostKeyStore) Close(ctx context.Context) error {
	return s.cs.Close(ctx)
}

// ---- in-memory implementation (dev mode and tests) ----

// MemoryKeyServiceStore is a KeyServiceStore for dev mode and tests. It holds
// records in maps and must satisfy the same contract as BifrostKeyStore.
type MemoryKeyServiceStore struct {
	keys  map[string]*KeyRecord
	usage map[string]*UsageReport
}

// NewMemoryKeyServiceStore creates an empty in-memory key service store.
func NewMemoryKeyServiceStore() *MemoryKeyServiceStore {
	return &MemoryKeyServiceStore{keys: make(map[string]*KeyRecord), usage: make(map[string]*UsageReport)}
}

func (m *MemoryKeyServiceStore) MintKey(ctx context.Context, spec MintSpec) (string, *KeyRecord, error) {
	secret, err := GenerateKey()
	if err != nil {
		return "", nil, fmt.Errorf("generating key: %w", err)
	}
	keyID := uuid.NewString()
	name := spec.Name
	if name == "" {
		name = "key-" + keyID[:8]
	}
	rec := &KeyRecord{
		ID:        keyID,
		Name:      name,
		SessionID: spec.SessionID,
		OrgID:     spec.OrgID,
		Subject:   spec.Subject,
		Labels:    cloneLabels(spec.Labels),
		Models:    append([]string(nil), spec.Models...),
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}
	if !spec.ExpiresAt.IsZero() {
		rec.ExpiresAt = spec.ExpiresAt.UTC()
	}
	for _, b := range spec.Budgets {
		reset, err := effectiveResetDuration(b.ResetDuration)
		if err != nil {
			return "", nil, err
		}
		if b.MaxLimitUSD <= 0 {
			return "", nil, errors.New("budget max_limit_usd must be positive")
		}
		rec.Budgets = append(rec.Budgets, BudgetSpec{MaxLimitUSD: b.MaxLimitUSD, ResetDuration: reset})
	}
	if spec.RateLimit != nil {
		if spec.RateLimit.TokenMaxLimit > 0 {
			if _, err := tables.ParseDuration(spec.RateLimit.TokenResetDuration); err != nil {
				return "", nil, fmt.Errorf("invalid token reset duration %q: %w", spec.RateLimit.TokenResetDuration, err)
			}
		}
		if spec.RateLimit.RequestMaxLimit > 0 {
			if _, err := tables.ParseDuration(spec.RateLimit.RequestResetDuration); err != nil {
				return "", nil, fmt.Errorf("invalid request reset duration %q: %w", spec.RateLimit.RequestResetDuration, err)
			}
		}
		rec.RateLimit = &RateLimitSpec{
			TokenMaxLimit:        spec.RateLimit.TokenMaxLimit,
			TokenResetDuration:   spec.RateLimit.TokenResetDuration,
			RequestMaxLimit:      spec.RateLimit.RequestMaxLimit,
			RequestResetDuration: spec.RateLimit.RequestResetDuration,
		}
	}

	m.keys[keyID] = rec
	m.usage[keyID] = &UsageReport{KeyID: keyID}
	for _, b := range rec.Budgets {
		m.usage[keyID].Budgets = append(m.usage[keyID].Budgets, BudgetUsageRecord{
			ID:              uuid.NewString(),
			MaxLimitUSD:     b.MaxLimitUSD,
			ResetDuration:   b.ResetDuration,
			CurrentUsageUSD: 0,
			LastReset:       time.Now().UTC(),
		})
	}
	if rec.RateLimit != nil {
		m.usage[keyID].RateLimit = &RateLimitUsageRecord{ID: uuid.NewString(),
			TokenMaxLimit: rec.RateLimit.TokenMaxLimit, RequestMaxLimit: rec.RateLimit.RequestMaxLimit}
	}
	return secret, cloneRecord(rec), nil
}

func (m *MemoryKeyServiceStore) RevokeKey(ctx context.Context, id string) (*KeyRecord, error) {
	rec, ok := m.keys[id]
	if !ok {
		return nil, ErrKeyNotFound
	}
	rec.Active = false
	return cloneRecord(rec), nil
}

func (m *MemoryKeyServiceStore) GetKey(ctx context.Context, id string) (*KeyRecord, error) {
	rec, ok := m.keys[id]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return cloneRecord(rec), nil
}

func (m *MemoryKeyServiceStore) ListKeys(ctx context.Context) ([]*KeyRecord, error) {
	out := make([]*KeyRecord, 0, len(m.keys))
	for _, rec := range m.keys {
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (m *MemoryKeyServiceStore) GetKeyUsage(ctx context.Context, id string) (*UsageReport, error) {
	u, ok := m.usage[id]
	if !ok {
		return nil, ErrKeyNotFound
	}
	return cloneUsage(u), nil
}

func (m *MemoryKeyServiceStore) Close(ctx context.Context) error { return nil }

// SetBudgetUsage exists so tests can simulate consumption against the memory
// store without a gateway. Production callers use the gateway's charge path.
func (m *MemoryKeyServiceStore) SetBudgetUsage(ctx context.Context, keyID string, budgetIndex int, usageUSD float64) error {
	u, ok := m.usage[keyID]
	if !ok {
		return ErrKeyNotFound
	}
	if budgetIndex < 0 || budgetIndex >= len(u.Budgets) {
		return errors.New("budget index out of range")
	}
	u.Budgets[budgetIndex].CurrentUsageUSD = usageUSD
	return nil
}

func cloneLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneRecord(in *KeyRecord) *KeyRecord {
	if in == nil {
		return nil
	}
	out := *in
	out.Labels = cloneLabels(in.Labels)
	out.Models = append([]string(nil), in.Models...)
	out.Budgets = append([]BudgetSpec(nil), in.Budgets...)
	if in.RateLimit != nil {
		rl := *in.RateLimit
		out.RateLimit = &rl
	}
	return &out
}

func cloneUsage(in *UsageReport) *UsageReport {
	if in == nil {
		return nil
	}
	out := *in
	out.Budgets = append([]BudgetUsageRecord(nil), in.Budgets...)
	if in.RateLimit != nil {
		rl := *in.RateLimit
		out.RateLimit = &rl
	}
	return &out
}
