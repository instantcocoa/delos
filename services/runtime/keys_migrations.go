package runtime

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// KeysMigrations is the D2 key migration set. It lives in a subdirectory of
// services/runtime/migrations so the gateway's legacy startup path
// (//go:embed migrations/*.sql, top-level only) does not apply it before
// Bifrost's tables exist. Apply it after the Bifrost config store is open.
//
//go:embed migrations/keys/*.sql
var KeysMigrations embed.FS

// legacyKey is one row of the source virtual_keys table.
type legacyKey struct {
	id          string
	name        string
	prefix      string
	models      []string
	tokenBudget int64
	usdBudget   float64
	revoked     bool
	createdAt   time.Time
}

// MigrateLegacyKeys copies legacy dk_ virtual keys from virtual_keys into
// Bifrost governance tables, recording the old->new mapping in the
// key_migration ledger created by 003_migrate_dk_keys.up.sql.
//
// It is non-destructive and idempotent:
//   - virtual_keys and key_usage are read, never modified or dropped;
//   - a source key already present in the ledger is skipped, so re-running
//     after a partial failure continues where it left off;
//   - every migrated key is created inactive with a placeholder value, because
//     the plaintext secret cannot be recovered from Delos's argon2id hash.
//
// It returns the number of keys migrated (including keys skipped as already
// migrated is NOT counted; only keys actually copied are).
func MigrateLegacyKeys(ctx context.Context, db *sql.DB, cs configstore.ConfigStore) (int, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, name, prefix, models, token_budget, usd_budget, revoked, created_at
		FROM virtual_keys ORDER BY created_at, id`)
	if err != nil {
		return 0, fmt.Errorf("reading legacy virtual_keys: %w", err)
	}
	defer rows.Close()

	var keys []legacyKey
	for rows.Next() {
		var k legacyKey
		var models pq.StringArray
		if err := rows.Scan(&k.id, &k.name, &k.prefix, &models,
			&k.tokenBudget, &k.usdBudget, &k.revoked, &k.createdAt); err != nil {
			return 0, fmt.Errorf("scanning legacy key: %w", err)
		}
		k.models = models
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("reading legacy virtual_keys: %w", err)
	}
	if len(keys) == 0 {
		return 0, nil
	}

	// Names of every Bifrost virtual key, so a legacy name that collides is
	// deterministically suffixed rather than failing the migration.
	takenNames := make(map[string]bool)
	existing, err := cs.GetVirtualKeys(ctx)
	if err != nil {
		return 0, fmt.Errorf("reading existing Bifrost virtual keys: %w", err)
	}
	for _, vk := range existing {
		takenNames[vk.Name] = true
	}

	migrated := 0
	for _, k := range keys {
		done, err := migrationDone(ctx, db, k.id)
		if err != nil {
			return migrated, err
		}
		if done {
			continue
		}

		name := k.name
		if takenNames[name] {
			name = fmt.Sprintf("%s (migrated %s)", k.name, k.id)
		}
		takenNames[name] = true

		if err := migrateOneKey(ctx, db, cs, k, name); err != nil {
			return migrated, fmt.Errorf("migrating legacy key %s: %w", k.id, err)
		}
		migrated++
	}
	return migrated, nil
}

func migrationDone(ctx context.Context, db *sql.DB, oldID string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM key_migration WHERE old_id = $1`, oldID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("checking key_migration ledger: %w", err)
	}
	return true, nil
}

func migrateOneKey(ctx context.Context, db *sql.DB, cs configstore.ConfigStore, k legacyKey, name string) error {
	now := time.Now().UTC()

	md := map[string]string{}
	if len(k.models) > 0 {
		raw, err := json.Marshal(k.models)
		if err != nil {
			return fmt.Errorf("encoding models: %w", err)
		}
		md[MetadataModels] = string(raw)
	}

	var rateLimitID *string
	if k.tokenBudget > 0 {
		rl := &tables.TableRateLimit{
			ID:                  k.id + "-rl",
			TokenMaxLimit:       int64Ptr(k.tokenBudget),
			TokenResetDuration:  strPtr("1M"),
			TokenLastReset:      now,
			RequestLastReset:    now,
			CreatedAt:           now,
			UpdatedAt:           now,
		}
		if err := cs.CreateRateLimit(ctx, rl); err != nil {
			return fmt.Errorf("creating migrated rate limit: %w", err)
		}
		rateLimitID = strPtr(rl.ID)
	}

	inactive := false
	createdAt := k.createdAt
	if createdAt.IsZero() {
		createdAt = now
	}
	vk := &tables.TableVirtualKey{
		ID:               k.id,
		Name:             name,
		Value:            *schemas.NewSecretVar("dk-migrated-" + k.id),
		IsActive:         &inactive,
		AllowAllProviders: len(k.models) == 0,
		RateLimitID:      rateLimitID,
		Metadata:         md,
		CreatedAt:        createdAt,
		UpdatedAt:        now,
	}
	if err := cs.CreateVirtualKey(ctx, vk); err != nil {
		return fmt.Errorf("creating migrated virtual key: %w", err)
	}

	if k.usdBudget > 0 {
		budget := &tables.TableBudget{
			ID:            k.id + "-budget",
			MaxLimit:      k.usdBudget,
			ResetDuration: "1M",
			LastReset:     createdAt,
			CurrentUsage:  0,
			VirtualKeyID:  strPtr(k.id),
			CreatedAt:     now,
			UpdatedAt:     now,
		}
		if err := cs.CreateBudget(ctx, budget); err != nil {
			return fmt.Errorf("creating migrated budget: %w", err)
		}
	}

	_, err := db.ExecContext(ctx, `
		INSERT INTO key_migration (old_id, old_prefix, new_id, note)
		VALUES ($1, $2, $3, $4)`,
		k.id, k.prefix, k.id,
		"migrated dk_ key; plaintext secret unrecoverable (argon2id), created inactive",
	)
	if err != nil {
		return fmt.Errorf("recording key_migration row: %w", err)
	}
	return nil
}
