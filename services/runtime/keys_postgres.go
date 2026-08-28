package runtime

import (
	"context"
	"database/sql"
	"embed"
	"errors"

	"github.com/lib/pq"
)

// pgUniqueViolation is Postgres SQLSTATE 23505.
const pgUniqueViolation = "23505"

// Migrations contains the gateway's schema migrations, applied at startup
// when Postgres is configured.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// PostgresKeyStore is the production KeyStore.
type PostgresKeyStore struct {
	db *sql.DB
}

// NewPostgresKeyStore wraps an open database handle.
func NewPostgresKeyStore(db *sql.DB) *PostgresKeyStore {
	return &PostgresKeyStore{db: db}
}

func (s *PostgresKeyStore) CreateKey(ctx context.Context, key *VirtualKey) error {
	// pq.Array of a nil slice is SQL NULL, and models is NOT NULL: an
	// unscoped key — the common case, a key with no --model restriction —
	// would be rejected by the column constraint. An unscoped key is an empty
	// array, not an absent one.
	models := key.Models
	if models == nil {
		models = []string{}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO virtual_keys (id, name, prefix, hash, models, token_budget, usd_budget, revoked, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		key.ID, key.Name, key.Prefix, key.Hash, pq.Array(models),
		key.TokenBudget, key.USDBudget, key.Revoked, key.CreatedAt,
	)
	// A prefix collision is reported as such so the caller regenerates rather
	// than surfacing a raw constraint violation. (Migration 002 added the
	// unique index; see keys.go for why the prefix must be unique.)
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation &&
		pgErr.Constraint == "idx_virtual_keys_prefix_unique" {
		return ErrKeyPrefixConflict
	}
	return err
}

func (s *PostgresKeyStore) GetKeyByPrefix(ctx context.Context, prefix string) (*VirtualKey, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, prefix, hash, models, token_budget, usd_budget, revoked, created_at
		FROM virtual_keys WHERE prefix = $1 AND NOT revoked
		ORDER BY created_at, id
		LIMIT 1`, prefix)
	return scanKey(row)
}

func (s *PostgresKeyStore) ListKeys(ctx context.Context) ([]*VirtualKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, prefix, hash, models, token_budget, usd_budget, revoked, created_at
		FROM virtual_keys ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []*VirtualKey
	for rows.Next() {
		k, err := scanKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func (s *PostgresKeyStore) RevokeKey(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE virtual_keys SET revoked = TRUE WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrKeyNotFound
	}
	return nil
}

func (s *PostgresKeyStore) AddUsage(ctx context.Context, keyID, month string, tokens int64, usd float64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO key_usage (key_id, month, tokens, usd) VALUES ($1, $2, $3, $4)
		ON CONFLICT (key_id, month) DO UPDATE
		SET tokens = key_usage.tokens + EXCLUDED.tokens,
		    usd    = key_usage.usd + EXCLUDED.usd`,
		keyID, month, tokens, usd,
	)
	return err
}

func (s *PostgresKeyStore) GetUsage(ctx context.Context, keyID, month string) (KeyUsage, error) {
	var u KeyUsage
	err := s.db.QueryRowContext(ctx, `
		SELECT tokens, usd FROM key_usage WHERE key_id = $1 AND month = $2`,
		keyID, month,
	).Scan(&u.Tokens, &u.USD)
	if errors.Is(err, sql.ErrNoRows) {
		return KeyUsage{}, nil
	}
	return u, err
}

type rowScanner interface{ Scan(dest ...any) error }

func scanKey(row rowScanner) (*VirtualKey, error) {
	var k VirtualKey
	var models pq.StringArray
	err := row.Scan(&k.ID, &k.Name, &k.Prefix, &k.Hash, &models,
		&k.TokenBudget, &k.USDBudget, &k.Revoked, &k.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrKeyNotFound
	}
	if err != nil {
		return nil, err
	}
	k.Models = models
	return &k, nil
}
