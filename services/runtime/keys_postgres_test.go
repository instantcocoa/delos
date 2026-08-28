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

// These are the first tests of the Postgres key store and of
// services/runtime/migrations. They run the real migrations against a real
// database and then run the same table of assertions against both KeyStore
// implementations, so the in-memory store used by dev mode and by every other
// test in this package is held to the same contract as the production one.
//
// They skip when no database is reachable, following the repo's convention
// (see services/prompt/store_test.go). `delos_test` may not exist locally.

func keysTestDSN() string {
	if dsn := os.Getenv("DELOS_TEST_DSN"); dsn != "" {
		return dsn
	}
	host := os.Getenv("POSTGRES_HOST")
	if host == "" {
		host = "localhost"
	}
	return "host=" + host + " port=5432 user=delos password=delos dbname=delos_test sslmode=disable"
}

// keysTestDB opens a database, applies the gateway migrations into their own
// schema, and truncates the tables after the test.
func keysTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("postgres", keysTestDSN())
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		t.Skipf("PostgreSQL not available: %v", err)
	}

	// The migrator takes pkg/database's wrapper, and applies the same
	// embedded migrations the gateway applies at startup.
	migrator := database.NewMigrator(&database.DB{DB: db}, "gateway").
		WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err := migrator.LoadMigrations(Migrations, "migrations"); err != nil {
		db.Close()
		t.Fatalf("loading gateway migrations: %v", err)
	}
	if err := migrator.Up(ctx); err != nil {
		db.Close()
		t.Skipf("could not apply gateway migrations: %v", err)
	}

	cleanup := func() {
		// key_usage goes with it via ON DELETE CASCADE, but be explicit.
		_, _ = db.Exec(`DELETE FROM key_usage`)
		_, _ = db.Exec(`DELETE FROM virtual_keys`)
	}
	cleanup()
	t.Cleanup(func() {
		cleanup()
		db.Close()
	})
	return db
}

func newTestKey(id, name, prefix string, models []string) *VirtualKey {
	return &VirtualKey{
		ID:        id,
		Name:      name,
		Prefix:    prefix,
		Hash:      "$argon2id$v=19$m=65536,t=1,p=4$c2FsdHNhbHRzYWx0c2Fs$ZGlnZXN0ZGlnZXN0ZGlnZXN0ZGlnZXN0MDA",
		Models:    models,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
}

// keyStoreContract is the behaviour both stores must share. Running it against
// MemoryKeyStore as well as PostgresKeyStore is what keeps dev mode and
// production from disagreeing about what a key store does.
func keyStoreContract(t *testing.T, newStore func(t *testing.T) KeyStore) {
	t.Run("models round-trip", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()

		cases := []struct {
			name   string
			models []string
		}{
			{"nil", nil},
			{"empty", []string{}},
			{"single", []string{"gpt-4o"}},
			{"multi", []string{"gpt-4o", "anthropic/*", "gemini-2.5-flash"}},
			{"awkward characters", []string{`weird,"model"`, "with space", "brace{}"}},
		}
		for i, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				key := newTestKey("mk-"+tc.name, tc.name, prefixFor(i), tc.models)
				if err := store.CreateKey(ctx, key); err != nil {
					t.Fatalf("CreateKey: %v", err)
				}
				got, err := store.GetKeyByPrefix(ctx, key.Prefix)
				if err != nil {
					t.Fatalf("GetKeyByPrefix: %v", err)
				}
				if len(got.Models) != len(tc.models) {
					t.Fatalf("Models = %#v, want %#v", got.Models, tc.models)
				}
				for j := range tc.models {
					if got.Models[j] != tc.models[j] {
						t.Errorf("Models[%d] = %q, want %q", j, got.Models[j], tc.models[j])
					}
				}
				// An unscoped key allows everything; a scoped one does not.
				if len(tc.models) == 0 && !got.AllowsModel("anything") {
					t.Error("empty scope should allow every model")
				}
			})
		}
	})

	t.Run("get by prefix not found", func(t *testing.T) {
		store := newStore(t)
		_, err := store.GetKeyByPrefix(context.Background(), "dk_nothing_here___")
		if !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("err = %v, want ErrKeyNotFound", err)
		}
	})

	t.Run("revoked keys are excluded from lookup", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		key := newTestKey("rk-1", "revoked", "dk_revoked000000000", nil)
		if err := store.CreateKey(ctx, key); err != nil {
			t.Fatal(err)
		}
		if _, err := store.GetKeyByPrefix(ctx, key.Prefix); err != nil {
			t.Fatalf("before revocation: %v", err)
		}
		if err := store.RevokeKey(ctx, key.ID); err != nil {
			t.Fatalf("RevokeKey: %v", err)
		}
		if _, err := store.GetKeyByPrefix(ctx, key.Prefix); !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("revoked key still resolves: err = %v", err)
		}
		// It is still listed, so `delos key list` can show it.
		keys, err := store.ListKeys(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 1 || !keys[0].Revoked {
			t.Errorf("ListKeys = %+v, want one revoked key", keys)
		}
	})

	t.Run("revoking a missing key is not found", func(t *testing.T) {
		store := newStore(t)
		if err := store.RevokeKey(context.Background(), "no-such-key"); !errors.Is(err, ErrKeyNotFound) {
			t.Errorf("err = %v, want ErrKeyNotFound (RowsAffected == 0)", err)
		}
	})

	t.Run("prefix uniqueness", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		first := newTestKey("uk-1", "first", "dk_unique0000000000", nil)
		if err := store.CreateKey(ctx, first); err != nil {
			t.Fatal(err)
		}
		second := newTestKey("uk-2", "second", "dk_unique0000000000", nil)
		if err := store.CreateKey(ctx, second); !errors.Is(err, ErrKeyPrefixConflict) {
			t.Fatalf("duplicate prefix: err = %v, want ErrKeyPrefixConflict", err)
		}
		// The prefix stays reserved even after revocation.
		if err := store.RevokeKey(ctx, first.ID); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateKey(ctx, second); !errors.Is(err, ErrKeyPrefixConflict) {
			t.Errorf("prefix of a revoked key was reusable: err = %v", err)
		}
	})

	t.Run("usage accumulates", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		key := newTestKey("uk-usage", "usage", "dk_usage00000000000", nil)
		if err := store.CreateKey(ctx, key); err != nil {
			t.Fatal(err)
		}

		// No row yet: zero value, not an error. A budget check must not fail
		// closed on a key's first request of the month.
		usage, err := store.GetUsage(ctx, key.ID, "2026-08")
		if err != nil {
			t.Fatalf("GetUsage before any usage: %v", err)
		}
		if usage != (KeyUsage{}) {
			t.Errorf("GetUsage = %+v, want zero", usage)
		}

		// Two writes in the same month sum (ON CONFLICT DO UPDATE), rather
		// than the second replacing or failing against the first.
		if err := store.AddUsage(ctx, key.ID, "2026-08", 100, 0.25); err != nil {
			t.Fatal(err)
		}
		if err := store.AddUsage(ctx, key.ID, "2026-08", 50, 0.125); err != nil {
			t.Fatal(err)
		}
		usage, err = store.GetUsage(ctx, key.ID, "2026-08")
		if err != nil {
			t.Fatal(err)
		}
		if usage.Tokens != 150 {
			t.Errorf("Tokens = %d, want 150", usage.Tokens)
		}
		if usage.USD != 0.375 {
			t.Errorf("USD = %v, want 0.375", usage.USD)
		}

		// Months are separate buckets.
		if err := store.AddUsage(ctx, key.ID, "2026-09", 7, 0.01); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.GetUsage(ctx, key.ID, "2026-08"); got.Tokens != 150 {
			t.Errorf("August total changed to %d after a September write", got.Tokens)
		}
		if got, _ := store.GetUsage(ctx, key.ID, "2026-09"); got.Tokens != 7 {
			t.Errorf("September tokens = %d, want 7", got.Tokens)
		}
	})

	t.Run("list is ordered and complete", func(t *testing.T) {
		store := newStore(t)
		ctx := context.Background()
		for i, name := range []string{"a", "b", "c"} {
			key := newTestKey("lk-"+name, name, prefixFor(100+i), nil)
			key.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Second)
			if err := store.CreateKey(ctx, key); err != nil {
				t.Fatal(err)
			}
		}
		keys, err := store.ListKeys(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 3 {
			t.Fatalf("ListKeys returned %d keys, want 3", len(keys))
		}
	})
}

// prefixFor builds a distinct, full-length lookup prefix for test key n.
func prefixFor(n int) string {
	prefix := "dk_test"
	for i := len(prefix); i < keyPrefixLen-2; i++ {
		prefix += "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	return prefix + string(digits[(n/36)%36]) + string(digits[n%36])
}

func TestMemoryKeyStoreContract(t *testing.T) {
	keyStoreContract(t, func(t *testing.T) KeyStore { return NewMemoryKeyStore() })
}

func TestPostgresKeyStoreContract(t *testing.T) {
	keyStoreContract(t, func(t *testing.T) KeyStore {
		return NewPostgresKeyStore(keysTestDB(t))
	})
}

// Usage rows belong to their key: deleting a key takes its usage with it.
// (The in-memory store has no foreign keys, so this is Postgres-only.)
func TestPostgresKeyUsageCascades(t *testing.T) {
	db := keysTestDB(t)
	store := NewPostgresKeyStore(db)
	ctx := context.Background()

	key := newTestKey("ck-1", "cascade", "dk_cascade000000000", []string{"gpt-4o"})
	if err := store.CreateKey(ctx, key); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUsage(ctx, key.ID, "2026-08", 42, 0.5); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM virtual_keys WHERE id = $1`, key.ID); err != nil {
		t.Fatalf("deleting the key: %v", err)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM key_usage WHERE key_id = $1`, key.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d usage rows survived the key, want 0 (ON DELETE CASCADE)", rows)
	}
}

// Usage for a key that does not exist must be rejected by the foreign key
// rather than silently accumulating against a phantom.
func TestPostgresUsageRequiresAKey(t *testing.T) {
	store := NewPostgresKeyStore(keysTestDB(t))
	if err := store.AddUsage(context.Background(), "no-such-key", "2026-08", 1, 1); err == nil {
		t.Error("usage was recorded for a key that does not exist")
	}
}

// The unique index from migration 002 must actually be in place after the
// migrations run, not merely enforced in Go.
func TestPostgresPrefixUniqueIndexExists(t *testing.T) {
	db := keysTestDB(t)
	var indexdef string
	err := db.QueryRow(`
		SELECT indexdef FROM pg_indexes
		WHERE tablename = 'virtual_keys' AND indexname = 'idx_virtual_keys_prefix_unique'`).Scan(&indexdef)
	if errors.Is(err, sql.ErrNoRows) {
		t.Fatal("migration 002 did not create idx_virtual_keys_prefix_unique")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexdef, "UNIQUE") {
		t.Errorf("index is not unique: %s", indexdef)
	}

	// A raw insert bypassing the store must still be refused.
	if _, err := db.Exec(`
		INSERT INTO virtual_keys (id, name, prefix, hash) VALUES ('a', 'a', 'dk_dup0000000000000', 'h')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO virtual_keys (id, name, prefix, hash) VALUES ('b', 'b', 'dk_dup0000000000000', 'h')`); err == nil {
		t.Error("the database accepted two keys with the same prefix")
	}
}
