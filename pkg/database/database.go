// Package database provides PostgreSQL connection and migration utilities.
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

// Config holds database connection configuration.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string

	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultConfig returns sensible defaults for database configuration.
func DefaultConfig() *Config {
	return &Config{
		Host:            "localhost",
		Port:            5432,
		User:            "delos",
		Password:        "delos",
		Database:        "delos",
		SSLMode:         "disable",
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 1 * time.Minute,
	}
}

// DSN returns the PostgreSQL connection string.
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Database, c.SSLMode,
	)
}

// DB wraps sql.DB with additional functionality.
type DB struct {
	*sql.DB
	logger *slog.Logger
}

// Connect creates a new database connection.
func Connect(ctx context.Context, cfg *Config) (*DB, error) {
	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Verify connection
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{
		DB:     db,
		logger: slog.Default(),
	}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.DB.Close()
}

// WithLogger sets the logger for the database.
func (db *DB) WithLogger(logger *slog.Logger) *DB {
	db.logger = logger
	return db
}

// Migration represents a database migration.
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// Migrator handles database migrations.
type Migrator struct {
	db         *DB
	schema     string
	migrations []Migration
	logger     *slog.Logger
}

// NewMigrator creates a new migrator.
func NewMigrator(db *DB, schema string) *Migrator {
	return &Migrator{
		db:     db,
		schema: schema,
		logger: slog.Default(),
	}
}

// WithLogger sets the logger for the migrator.
func (m *Migrator) WithLogger(logger *slog.Logger) *Migrator {
	m.logger = logger
	return m
}

// LoadMigrations loads migrations from an embedded filesystem.
// Expects files named like: 001_create_users.up.sql, 001_create_users.down.sql
func (m *Migrator) LoadMigrations(fsys embed.FS, dir string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Group by version
	migrationMap := make(map[int]*Migration)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}

		// Parse filename: 001_create_users.up.sql
		var version int
		var migName, direction string
		parts := strings.SplitN(name, "_", 2)
		if len(parts) != 2 {
			continue
		}
		fmt.Sscanf(parts[0], "%d", &version)

		rest := parts[1]
		if strings.HasSuffix(rest, ".up.sql") {
			migName = strings.TrimSuffix(rest, ".up.sql")
			direction = "up"
		} else if strings.HasSuffix(rest, ".down.sql") {
			migName = strings.TrimSuffix(rest, ".down.sql")
			direction = "down"
		} else {
			continue
		}

		content, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", name, err)
		}

		if _, ok := migrationMap[version]; !ok {
			migrationMap[version] = &Migration{
				Version: version,
				Name:    migName,
			}
		}

		if direction == "up" {
			migrationMap[version].Up = string(content)
		} else {
			migrationMap[version].Down = string(content)
		}
	}

	// Sort by version
	versions := make([]int, 0, len(migrationMap))
	for v := range migrationMap {
		versions = append(versions, v)
	}
	sort.Ints(versions)

	m.migrations = make([]Migration, 0, len(versions))
	for _, v := range versions {
		m.migrations = append(m.migrations, *migrationMap[v])
	}

	return nil
}

// ensureMigrationsTable creates the migrations tracking table if needed.
func (m *Migrator) ensureMigrationsTable(ctx context.Context) error {
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s_schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`, m.schema)

	_, err := m.db.ExecContext(ctx, query)
	return err
}

// appliedVersions returns the set of already applied migration versions.
func (m *Migrator) appliedVersions(ctx context.Context) (map[int]bool, error) {
	query := fmt.Sprintf("SELECT version FROM %s_schema_migrations", m.schema)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[int]bool)
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		applied[version] = true
	}

	return applied, rows.Err()
}

// Up runs all pending migrations.
func (m *Migrator) Up(ctx context.Context) error {
	if err := m.ensureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("failed to ensure migrations table: %w", err)
	}

	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get applied versions: %w", err)
	}

	for _, mig := range m.migrations {
		if applied[mig.Version] {
			continue
		}

		m.logger.Info("applying migration", "version", mig.Version, "name", mig.Name)

		tx, err := m.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.ExecContext(ctx, mig.Up); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to apply migration %d (%s): %w", mig.Version, mig.Name, err)
		}

		insertQuery := fmt.Sprintf(
			"INSERT INTO %s_schema_migrations (version, name) VALUES ($1, $2)",
			m.schema,
		)
		if _, err := tx.ExecContext(ctx, insertQuery, mig.Version, mig.Name); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration: %w", err)
		}

		m.logger.Info("applied migration", "version", mig.Version, "name", mig.Name)
	}

	return nil
}

// Down rolls back the last applied migration.
func (m *Migrator) Down(ctx context.Context) error {
	if err := m.ensureMigrationsTable(ctx); err != nil {
		return fmt.Errorf("failed to ensure migrations table: %w", err)
	}

	applied, err := m.appliedVersions(ctx)
	if err != nil {
		return fmt.Errorf("failed to get applied versions: %w", err)
	}

	// Find the highest applied version
	var maxVersion int
	for v := range applied {
		if v > maxVersion {
			maxVersion = v
		}
	}

	if maxVersion == 0 {
		m.logger.Info("no migrations to rollback")
		return nil
	}

	// Find the migration
	var mig *Migration
	for i := range m.migrations {
		if m.migrations[i].Version == maxVersion {
			mig = &m.migrations[i]
			break
		}
	}

	if mig == nil {
		return fmt.Errorf("migration %d not found", maxVersion)
	}

	m.logger.Info("rolling back migration", "version", mig.Version, "name", mig.Name)

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if _, err := tx.ExecContext(ctx, mig.Down); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to rollback migration %d (%s): %w", mig.Version, mig.Name, err)
	}

	deleteQuery := fmt.Sprintf(
		"DELETE FROM %s_schema_migrations WHERE version = $1",
		m.schema,
	)
	if _, err := tx.ExecContext(ctx, deleteQuery, mig.Version); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to remove migration record: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit rollback: %w", err)
	}

	m.logger.Info("rolled back migration", "version", mig.Version, "name", mig.Name)
	return nil
}

// Version returns the current migration version.
func (m *Migrator) Version(ctx context.Context) (int, error) {
	if err := m.ensureMigrationsTable(ctx); err != nil {
		return 0, err
	}

	query := fmt.Sprintf("SELECT COALESCE(MAX(version), 0) FROM %s_schema_migrations", m.schema)
	var version int
	err := m.db.QueryRowContext(ctx, query).Scan(&version)
	return version, err
}
