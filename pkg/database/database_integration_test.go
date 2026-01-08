package database

import (
	"context"
	"embed"
	"log/slog"
	"os"
	"testing"
	"time"
)

//go:embed testdata/migrations
var testMigrations embed.FS

func getTestConfig() *Config {
	cfg := DefaultConfig()
	if host := os.Getenv("POSTGRES_HOST"); host != "" {
		cfg.Host = host
	}
	cfg.Database = "delos_test"
	return cfg
}

func setupTestDB(t *testing.T) *DB {
	t.Helper()

	cfg := getTestConfig()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	db, err := Connect(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
	})

	return db
}

func TestConnect_Integration(t *testing.T) {
	db := setupTestDB(t)

	// Verify we can execute a simple query
	var result int
	err := db.QueryRow("SELECT 1").Scan(&result)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if result != 1 {
		t.Errorf("result = %d, want 1", result)
	}
}

func TestConnect_ConnectionPool_Integration(t *testing.T) {
	cfg := getTestConfig()
	cfg.MaxOpenConns = 5
	cfg.MaxIdleConns = 2
	cfg.ConnMaxLifetime = 1 * time.Minute
	cfg.ConnMaxIdleTime = 30 * time.Second

	ctx := context.Background()
	db, err := Connect(ctx, cfg)
	if err != nil {
		t.Skipf("PostgreSQL not available: %v", err)
	}
	defer db.Close()

	// Verify pool settings
	stats := db.Stats()
	if stats.MaxOpenConnections != 5 {
		t.Errorf("MaxOpenConnections = %d, want 5", stats.MaxOpenConnections)
	}
}

func TestDB_Close_Integration(t *testing.T) {
	db := setupTestDB(t)

	err := db.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Subsequent queries should fail
	var result int
	err = db.QueryRow("SELECT 1").Scan(&result)
	if err == nil {
		t.Error("expected error after Close()")
	}
}

func TestMigrator_Up_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up any existing test tables
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_mig_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_users")

	migrator := NewMigrator(db, "test_mig")
	migrator.migrations = []Migration{
		{
			Version: 1,
			Name:    "create_users",
			Up:      "CREATE TABLE test_users (id SERIAL PRIMARY KEY, name TEXT NOT NULL)",
			Down:    "DROP TABLE test_users",
		},
	}

	err := migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Verify table was created
	var tableExists bool
	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_name = 'test_users'
		)
	`).Scan(&tableExists)
	if err != nil {
		t.Fatalf("check table exists error = %v", err)
	}
	if !tableExists {
		t.Error("test_users table should exist after migration")
	}

	// Verify migration was recorded
	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 1 {
		t.Errorf("Version() = %d, want 1", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_mig_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_users")
}

func TestMigrator_Down_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up any existing test tables
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_down_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_down_users")

	migrator := NewMigrator(db, "test_down")
	migrator.migrations = []Migration{
		{
			Version: 1,
			Name:    "create_users",
			Up:      "CREATE TABLE test_down_users (id SERIAL PRIMARY KEY)",
			Down:    "DROP TABLE test_down_users",
		},
	}

	// Apply migration
	err := migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Roll back
	err = migrator.Down(ctx)
	if err != nil {
		t.Fatalf("Down() error = %v", err)
	}

	// Verify table was dropped
	var tableExists bool
	err = db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_name = 'test_down_users'
		)
	`).Scan(&tableExists)
	if err != nil {
		t.Fatalf("check table exists error = %v", err)
	}
	if tableExists {
		t.Error("test_down_users table should not exist after rollback")
	}

	// Verify version is 0
	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 0 {
		t.Errorf("Version() = %d, want 0", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_down_schema_migrations")
}

func TestMigrator_MultipleMigrations_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS multi_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS multi_users")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS multi_posts")

	migrator := NewMigrator(db, "multi")
	migrator.migrations = []Migration{
		{
			Version: 1,
			Name:    "create_users",
			Up:      "CREATE TABLE multi_users (id SERIAL PRIMARY KEY, name TEXT)",
			Down:    "DROP TABLE multi_users",
		},
		{
			Version: 2,
			Name:    "create_posts",
			Up:      "CREATE TABLE multi_posts (id SERIAL PRIMARY KEY, user_id INT REFERENCES multi_users(id))",
			Down:    "DROP TABLE multi_posts",
		},
	}

	// Apply all migrations
	err := migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	version, _ := migrator.Version(ctx)
	if version != 2 {
		t.Errorf("Version() = %d, want 2", version)
	}

	// Roll back one migration
	err = migrator.Down(ctx)
	if err != nil {
		t.Fatalf("Down() error = %v", err)
	}

	version, _ = migrator.Version(ctx)
	if version != 1 {
		t.Errorf("Version() = %d, want 1", version)
	}

	// Roll back another
	err = migrator.Down(ctx)
	if err != nil {
		t.Fatalf("Down() second error = %v", err)
	}

	version, _ = migrator.Version(ctx)
	if version != 0 {
		t.Errorf("Version() = %d, want 0", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS multi_schema_migrations")
}

func TestMigrator_Down_NoMigrations_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS empty_schema_migrations")

	migrator := NewMigrator(db, "empty")
	migrator.migrations = []Migration{}

	// Down with no migrations should not error
	err := migrator.Down(ctx)
	if err != nil {
		t.Errorf("Down() with no migrations error = %v", err)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS empty_schema_migrations")
}

func TestMigrator_Up_Idempotent_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS idem_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS idem_users")

	migrator := NewMigrator(db, "idem")
	migrator.migrations = []Migration{
		{
			Version: 1,
			Name:    "create_users",
			Up:      "CREATE TABLE idem_users (id SERIAL PRIMARY KEY)",
			Down:    "DROP TABLE idem_users",
		},
	}

	// Apply twice - second should be no-op
	err := migrator.Up(ctx)
	if err != nil {
		t.Fatalf("first Up() error = %v", err)
	}

	err = migrator.Up(ctx)
	if err != nil {
		t.Fatalf("second Up() error = %v", err)
	}

	version, _ := migrator.Version(ctx)
	if version != 1 {
		t.Errorf("Version() = %d, want 1", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS idem_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS idem_users")
}

func TestMigrator_Up_FailedMigration_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS fail_schema_migrations")

	migrator := NewMigrator(db, "fail")
	migrator.migrations = []Migration{
		{
			Version: 1,
			Name:    "bad_migration",
			Up:      "CREATE TABLE this is invalid SQL",
			Down:    "",
		},
	}

	err := migrator.Up(ctx)
	if err == nil {
		t.Error("expected error for invalid SQL")
	}

	// Version should still be 0
	version, _ := migrator.Version(ctx)
	if version != 0 {
		t.Errorf("Version() = %d, want 0 after failed migration", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS fail_schema_migrations")
}

func TestMigrator_ManualMigrations_Integration(t *testing.T) {
	db := &DB{}
	migrator := NewMigrator(db, "test")

	// Manually set migrations (simulating LoadMigrations behavior)
	migrator.migrations = []Migration{
		{Version: 1, Name: "create_users", Up: "CREATE TABLE test_load_users (id INT)", Down: "DROP TABLE test_load_users"},
		{Version: 2, Name: "add_email", Up: "ALTER TABLE test_load_users ADD email TEXT", Down: "ALTER TABLE test_load_users DROP email"},
	}

	if len(migrator.migrations) != 2 {
		t.Errorf("expected 2 migrations, got %d", len(migrator.migrations))
	}
	if migrator.migrations[0].Version != 1 {
		t.Errorf("first migration version = %d, want 1", migrator.migrations[0].Version)
	}
	if migrator.migrations[1].Version != 2 {
		t.Errorf("second migration version = %d, want 2", migrator.migrations[1].Version)
	}
}

func TestMigrator_Version_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS ver_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS ver_users")

	migrator := NewMigrator(db, "ver")
	migrator.migrations = []Migration{
		{Version: 1, Name: "m1", Up: "CREATE TABLE ver_users (id INT)", Down: "DROP TABLE ver_users"},
		{Version: 2, Name: "m2", Up: "SELECT 1", Down: "SELECT 1"},
	}

	// Initially 0
	version, err := migrator.Version(ctx)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 0 {
		t.Errorf("initial Version() = %d, want 0", version)
	}

	// After Up
	migrator.Up(ctx)
	version, _ = migrator.Version(ctx)
	if version != 2 {
		t.Errorf("Version() after Up = %d, want 2", version)
	}

	// After one Down
	migrator.Down(ctx)
	version, _ = migrator.Version(ctx)
	if version != 1 {
		t.Errorf("Version() after Down = %d, want 1", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS ver_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS ver_users")
}

func TestMigrator_LoadMigrations_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS load_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_table")

	migrator := NewMigrator(db, "load")

	err := migrator.LoadMigrations(testMigrations, "testdata/migrations")
	if err != nil {
		t.Fatalf("LoadMigrations() error = %v", err)
	}

	if len(migrator.migrations) != 2 {
		t.Fatalf("LoadMigrations() loaded %d migrations, want 2", len(migrator.migrations))
	}

	// Verify first migration
	if migrator.migrations[0].Version != 1 {
		t.Errorf("first migration version = %d, want 1", migrator.migrations[0].Version)
	}
	if migrator.migrations[0].Name != "create_table" {
		t.Errorf("first migration name = %q, want %q", migrator.migrations[0].Name, "create_table")
	}
	if migrator.migrations[0].Up == "" {
		t.Error("first migration Up should not be empty")
	}
	if migrator.migrations[0].Down == "" {
		t.Error("first migration Down should not be empty")
	}

	// Verify second migration
	if migrator.migrations[1].Version != 2 {
		t.Errorf("second migration version = %d, want 2", migrator.migrations[1].Version)
	}

	// Apply migrations
	err = migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() after LoadMigrations error = %v", err)
	}

	version, _ := migrator.Version(ctx)
	if version != 2 {
		t.Errorf("Version() = %d, want 2", version)
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS load_schema_migrations")
	db.ExecContext(ctx, "DROP TABLE IF EXISTS test_table")
}

func TestMigrator_LoadMigrations_InvalidDir_Integration(t *testing.T) {
	db := &DB{}
	migrator := NewMigrator(db, "test")

	err := migrator.LoadMigrations(testMigrations, "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestMigrator_Down_MigrationNotFound_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS notfound_schema_migrations")

	migrator := NewMigrator(db, "notfound")

	// Manually insert a migration record for version that doesn't exist in migrations slice
	migrator.migrations = []Migration{}
	db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS notfound_schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())")
	db.ExecContext(ctx, "INSERT INTO notfound_schema_migrations (version, name) VALUES (99, 'missing')")

	err := migrator.Down(ctx)
	if err == nil {
		t.Error("expected error when migration not found")
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS notfound_schema_migrations")
}

func TestMigrator_Down_FailedRollback_Integration(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS rollback_fail_schema_migrations")

	migrator := NewMigrator(db, "rollback_fail")
	migrator.migrations = []Migration{
		{
			Version: 1,
			Name:    "create_something",
			Up:      "SELECT 1", // Simple up
			Down:    "DROP TABLE nonexistent_table_xyz", // Will fail
		},
	}

	// Apply
	err := migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}

	// Down should fail
	err = migrator.Down(ctx)
	if err == nil {
		t.Error("expected error for failed rollback")
	}

	// Clean up
	db.ExecContext(ctx, "DROP TABLE IF EXISTS rollback_fail_schema_migrations")
}

func TestDB_WithLogger_Integration(t *testing.T) {
	db := setupTestDB(t)

	logger := slog.Default()
	result := db.WithLogger(logger)

	if result != db {
		t.Error("WithLogger should return same DB instance")
	}
}

func TestMigrator_WithLogger_Integration(t *testing.T) {
	db := &DB{}
	migrator := NewMigrator(db, "test")

	logger := slog.Default()
	result := migrator.WithLogger(logger)

	if result != migrator {
		t.Error("WithLogger should return same Migrator instance")
	}
}
