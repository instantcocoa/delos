package runtime

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/instantcocoa/delos/pkg/database"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testKeyServiceContract runs the same table of key-lifecycle assertions
// against any KeyServiceStore, so the memory and Bifrost backends are held to
// one contract (mirrors keys_postgres_test.go for the legacy store).
func testKeyServiceContract(t *testing.T, store KeyServiceStore) {
	t.Helper()
	ctx := context.Background()

	t.Run("mint_and_get", func(t *testing.T) {
		secret, key, err := store.MintKey(ctx, MintSpec{
			SessionID: "sess-1",
			OrgID:     "org-1",
			Subject:   "agent-1",
			Labels:    map[string]string{"team": "core"},
			Name:      "session-key",
			Models:    []string{"anthropic/*"},
			ExpiresAt: time.Now().Add(time.Hour),
			Budgets:   []BudgetSpec{{MaxLimitUSD: 5, ResetDuration: "1d"}, {MaxLimitUSD: 25, ResetDuration: ""}},
		})
		if err != nil {
			t.Fatalf("MintKey: %v", err)
		}
		if !strings.HasPrefix(secret, "dk_") {
			t.Errorf("secret = %q, want dk_ prefix", secret)
		}
		if !key.Active {
			t.Error("minted key must be active")
		}
		if key.SessionID != "sess-1" || key.OrgID != "org-1" || key.Subject != "agent-1" {
			t.Errorf("attribution = %q/%q/%q", key.SessionID, key.OrgID, key.Subject)
		}
		if key.Labels["team"] != "core" {
			t.Errorf("labels = %v", key.Labels)
		}
		if !key.AllowsModel("anthropic/claude-sonnet-4-5") {
			t.Error("scoped model should be allowed")
		}
		if key.AllowsModel("openai/gpt-4o") {
			t.Error("out-of-scope model allowed")
		}
		if key.ExpiredAt(time.Now()) {
			t.Error("key should not be expired before expires_at")
		}
		if !key.ExpiredAt(time.Now().Add(2 * time.Hour)) {
			t.Error("key should be expired after expires_at")
		}
		if len(key.Budgets) != 2 {
			t.Fatalf("budgets = %d, want 2", len(key.Budgets))
		}
		// Empty reset duration ("total") is stored as "1Y".
		if key.Budgets[1].ResetDuration != "1Y" {
			t.Errorf("total budget reset = %q, want 1Y", key.Budgets[1].ResetDuration)
		}

		got, err := store.GetKey(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if got.SessionID != "sess-1" || got.OrgID != "org-1" || got.Subject != "agent-1" {
			t.Errorf("roundtrip attribution = %q/%q/%q", got.SessionID, got.OrgID, got.Subject)
		}
	})

	t.Run("revoke", func(t *testing.T) {
		_, key, err := store.MintKey(ctx, MintSpec{SessionID: "sess-2"})
		if err != nil {
			t.Fatalf("MintKey: %v", err)
		}
		revoked, err := store.RevokeKey(ctx, key.ID)
		if err != nil {
			t.Fatalf("RevokeKey: %v", err)
		}
		if revoked.Active {
			t.Error("revoked key must be inactive")
		}
		got, err := store.GetKey(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetKey: %v", err)
		}
		if got.Active {
			t.Error("revoked key still active on read")
		}
	})

	t.Run("revoke_missing", func(t *testing.T) {
		if _, err := store.RevokeKey(ctx, "does-not-exist"); !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("RevokeKey missing = %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("usage", func(t *testing.T) {
		_, key, err := store.MintKey(ctx, MintSpec{
			Budgets:   []BudgetSpec{{MaxLimitUSD: 10, ResetDuration: "1d"}},
			RateLimit: &RateLimitSpec{TokenMaxLimit: 1000, TokenResetDuration: "1M"},
		})
		if err != nil {
			t.Fatalf("MintKey: %v", err)
		}
		usage, err := store.GetKeyUsage(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetKeyUsage: %v", err)
		}
		if len(usage.Budgets) != 1 || usage.Budgets[0].MaxLimitUSD != 10 {
			t.Errorf("usage budgets = %+v", usage.Budgets)
		}
		if usage.RateLimit == nil || usage.RateLimit.TokenMaxLimit != 1000 {
			t.Errorf("usage rate limit = %+v", usage.RateLimit)
		}
	})

	t.Run("unscoped_allows_all", func(t *testing.T) {
		_, key, err := store.MintKey(ctx, MintSpec{SessionID: "sess-3"})
		if err != nil {
			t.Fatalf("MintKey: %v", err)
		}
		if !key.AllowsModel("anything") {
			t.Error("unscoped key must allow all models")
		}
	})

	t.Run("invalid_budget", func(t *testing.T) {
		_, _, err := store.MintKey(ctx, MintSpec{Budgets: []BudgetSpec{{MaxLimitUSD: 1, ResetDuration: "nonsense"}}})
		if err == nil {
			t.Error("invalid reset duration must be refused")
		}
	})
}

func TestMemoryKeyServiceStoreContract(t *testing.T) {
	testKeyServiceContract(t, NewMemoryKeyServiceStore())
}

func TestKeyRecordHelpers(t *testing.T) {
	if !(&KeyRecord{}).AllowsModel("any") {
		t.Error("empty scope allows all")
	}
	scoped := &KeyRecord{Models: []string{"gpt-4o", "anthropic/*"}}
	if !scoped.AllowsModel("gpt-4o") || !scoped.AllowsModel("anthropic/claude-x") {
		t.Error("scoped models allowed")
	}
	if scoped.AllowsModel("gemini") {
		t.Error("out-of-scope allowed")
	}
	if (&KeyRecord{}).ExpiredAt(time.Now()) {
		t.Error("zero expiry is never expired")
	}
	exp := time.Now().Add(-time.Second)
	if !(&KeyRecord{ExpiresAt: exp}).ExpiredAt(time.Now()) {
		t.Error("past expiry must be expired")
	}
}

func TestEffectiveResetDuration(t *testing.T) {
	if got, err := effectiveResetDuration(""); err != nil || got != "1Y" {
		t.Errorf("empty = %q, %v; want 1Y", got, err)
	}
	if got, err := effectiveResetDuration("1d"); err != nil || got != "1d" {
		t.Errorf("1d = %q, %v", got, err)
	}
	if _, err := effectiveResetDuration("bogus"); err == nil {
		t.Error("bogus duration accepted")
	}
}

// ---- Bifrost (Postgres) store ----

// bifrostTestDSN returns the DSN used for both the database/sql handle (Delos
// migrations and the legacy virtual_keys table) and, parsed by parseDSN, the
// Bifrost config store connection.
func bifrostTestDSN() string {
	if dsn := os.Getenv("DELOS_TEST_DSN"); dsn != "" {
		return dsn
	}
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		host = "localhost"
	}
	port := os.Getenv("POSTGRES_PORT")
	if port == "" {
		port = "5432"
	}
	return "host=" + host + " port=" + port + " user=delos password=delos dbname=delos_test sslmode=disable"
}

// parseDSN splits a libpq keyword/value DSN into a map.
func parseDSN(dsn string) map[string]string {
	out := make(map[string]string)
	for _, part := range strings.Fields(dsn) {
		if k, v, ok := strings.Cut(part, "="); ok {
			out[k] = v
		}
	}
	return out
}

// openTestBifrostStore opens the Bifrost config store against the test
// database. It skips when Postgres is unavailable, following the repo
// convention (see keys_postgres_test.go).
func openTestBifrostStore(t *testing.T) (*BifrostKeyStore, *sql.DB, func()) {
	t.Helper()
	dsn := bifrostTestDSN()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		t.Skipf("PostgreSQL not available: %v", err)
	}

	kv := parseDSN(dsn)
	port := kv["port"]
	if port == "" {
		port = "5432"
	}
	// Bifrost's config store runs a large GORM migration set on first open, so
	// give it a generous budget separate from the ping above.
	openCtx, openCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer openCancel()
	store, err := OpenBifrostKeyStore(openCtx, BifrostDBConfig{
		Host:     kv["host"],
		Port:     port,
		User:     kv["user"],
		Password: kv["password"],
		DBName:   kv["dbname"],
		SSLMode:  kv["sslmode"],
	}, discardLogger())
	if err != nil {
		db.Close()
		t.Skipf("could not open Bifrost config store: %v", err)
	}

	cleanup := func() {
		_ = store.Close(context.Background())
		// Remove the keys this test minted so re-runs do not collide on the
		// unique governance_virtual_keys.name index. Order: virtual keys first
		// (cascades to their budgets), then the rate limits they referenced.
		_, _ = db.Exec(`DELETE FROM governance_virtual_keys`)
		_, _ = db.Exec(`DELETE FROM governance_rate_limits`)
		_, _ = db.Exec(`DELETE FROM key_migration`)
		db.Close()
	}
	return store, db, cleanup
}

func TestBifrostKeyStoreContract(t *testing.T) {
	store, _, cleanup := openTestBifrostStore(t)
	defer cleanup()
	testKeyServiceContract(t, store)
}

func TestMigrateLegacyKeys(t *testing.T) {
	store, db, cleanup := openTestBifrostStore(t)
	defer cleanup()
	ctx := context.Background()

	// Apply the Delos gateway migrations (001/002) to get virtual_keys.
	migrator := database.NewMigrator(&database.DB{DB: db}, "gateway").WithLogger(discardLogger())
	if err := migrator.LoadMigrations(Migrations, "migrations"); err != nil {
		t.Fatalf("loading gateway migrations: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		t.Fatalf("applying gateway migrations: %v", err)
	}
	// And the key migration set (003) so key_migration exists.
	keyMigrator := database.NewMigrator(&database.DB{DB: db}, "keys").WithLogger(discardLogger())
	if err := keyMigrator.LoadMigrations(KeysMigrations, "migrations/keys"); err != nil {
		t.Fatalf("loading key migrations: %v", err)
	}
	if err := keyMigrator.Up(ctx); err != nil {
		t.Fatalf("applying key migrations: %v", err)
	}

	// Start from a clean virtual_keys so a re-run of the test is idempotent.
	if _, err := db.Exec(`DELETE FROM key_usage`); err != nil {
		t.Fatalf("clearing key_usage: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM virtual_keys`); err != nil {
		t.Fatalf("clearing virtual_keys: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO virtual_keys (id, name, prefix, hash, models, token_budget, usd_budget, revoked, created_at)
		VALUES ('legacy-1', 'legacy one', 'dk_abcdefgh', '$argon2id$v=19$m=65536,t=1,p=4$c2FsdA$c2FsdA', ARRAY['anthropic/*'], 1000, 2.50, FALSE, now())`); err != nil {
		t.Fatalf("inserting legacy key: %v", err)
	}

	n, err := MigrateLegacyKeys(ctx, db, store.ConfigStore())
	if err != nil {
		t.Fatalf("MigrateLegacyKeys: %v", err)
	}
	if n != 1 {
		t.Fatalf("migrated = %d, want 1", n)
	}

	// Non-destructive: the source row is still there.
	var one int
	if err := db.QueryRow(`SELECT 1 FROM virtual_keys WHERE id = 'legacy-1'`).Scan(&one); err != nil {
		t.Fatalf("source virtual_keys row missing: %v", err)
	}

	// Ledger records the mapping.
	var oldID, newID, note string
	if err := db.QueryRow(`SELECT old_id, new_id, note FROM key_migration WHERE old_id = 'legacy-1'`).Scan(&oldID, &newID, &note); err != nil {
		t.Fatalf("ledger row missing: %v", err)
	}
	if newID != "legacy-1" {
		t.Errorf("new_id = %q, want legacy-1", newID)
	}

	// The migrated governance key exists, is inactive, and carries the models
	// metadata and a monthly USD budget.
	rec, err := store.GetKey(ctx, "legacy-1")
	if err != nil {
		t.Fatalf("GetKey migrated: %v", err)
	}
	if rec.Active {
		t.Error("migrated key must be inactive")
	}
	if len(rec.Models) != 1 || rec.Models[0] != "anthropic/*" {
		t.Errorf("migrated models = %v", rec.Models)
	}
	if len(rec.Budgets) != 1 || rec.Budgets[0].MaxLimitUSD != 2.50 {
		t.Errorf("migrated budgets = %+v", rec.Budgets)
	}
	if rec.RateLimit == nil || rec.RateLimit.TokenMaxLimit != 1000 {
		t.Errorf("migrated rate limit = %+v", rec.RateLimit)
	}

	// Idempotent: a second run migrates nothing new.
	n, err = MigrateLegacyKeys(ctx, db, store.ConfigStore())
	if err != nil {
		t.Fatalf("MigrateLegacyKeys (second): %v", err)
	}
	if n != 0 {
		t.Errorf("second run migrated = %d, want 0", n)
	}
}
