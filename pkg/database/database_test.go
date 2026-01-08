package database

import (
	"context"
	"embed"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Host != "localhost" {
		t.Errorf("Host = %v, want %v", cfg.Host, "localhost")
	}
	if cfg.Port != 5432 {
		t.Errorf("Port = %v, want %v", cfg.Port, 5432)
	}
	if cfg.User != "delos" {
		t.Errorf("User = %v, want %v", cfg.User, "delos")
	}
	if cfg.Password != "delos" {
		t.Errorf("Password = %v, want %v", cfg.Password, "delos")
	}
	if cfg.Database != "delos" {
		t.Errorf("Database = %v, want %v", cfg.Database, "delos")
	}
	if cfg.SSLMode != "disable" {
		t.Errorf("SSLMode = %v, want %v", cfg.SSLMode, "disable")
	}
	if cfg.MaxOpenConns != 25 {
		t.Errorf("MaxOpenConns = %v, want %v", cfg.MaxOpenConns, 25)
	}
	if cfg.MaxIdleConns != 5 {
		t.Errorf("MaxIdleConns = %v, want %v", cfg.MaxIdleConns, 5)
	}
	if cfg.ConnMaxLifetime != 5*time.Minute {
		t.Errorf("ConnMaxLifetime = %v, want %v", cfg.ConnMaxLifetime, 5*time.Minute)
	}
	if cfg.ConnMaxIdleTime != 1*time.Minute {
		t.Errorf("ConnMaxIdleTime = %v, want %v", cfg.ConnMaxIdleTime, 1*time.Minute)
	}
}

func TestConfig_DSN(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "default config",
			cfg:  DefaultConfig(),
			want: "host=localhost port=5432 user=delos password=delos dbname=delos sslmode=disable",
		},
		{
			name: "custom config",
			cfg: &Config{
				Host:     "db.example.com",
				Port:     5433,
				User:     "admin",
				Password: "secret123",
				Database: "mydb",
				SSLMode:  "require",
			},
			want: "host=db.example.com port=5433 user=admin password=secret123 dbname=mydb sslmode=require",
		},
		{
			name: "empty password",
			cfg: &Config{
				Host:     "localhost",
				Port:     5432,
				User:     "postgres",
				Password: "",
				Database: "test",
				SSLMode:  "disable",
			},
			want: "host=localhost port=5432 user=postgres password= dbname=test sslmode=disable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.DSN()
			if got != tt.want {
				t.Errorf("DSN() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfig_Fields(t *testing.T) {
	cfg := &Config{
		Host:            "db.example.com",
		Port:            5433,
		User:            "admin",
		Password:        "secret",
		Database:        "mydb",
		SSLMode:         "require",
		MaxOpenConns:    50,
		MaxIdleConns:    10,
		ConnMaxLifetime: 10 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}

	if cfg.Host != "db.example.com" {
		t.Errorf("Host = %v, want %v", cfg.Host, "db.example.com")
	}
	if cfg.Port != 5433 {
		t.Errorf("Port = %v, want %v", cfg.Port, 5433)
	}
	if cfg.User != "admin" {
		t.Errorf("User = %v, want %v", cfg.User, "admin")
	}
	if cfg.Password != "secret" {
		t.Errorf("Password = %v, want %v", cfg.Password, "secret")
	}
	if cfg.Database != "mydb" {
		t.Errorf("Database = %v, want %v", cfg.Database, "mydb")
	}
	if cfg.SSLMode != "require" {
		t.Errorf("SSLMode = %v, want %v", cfg.SSLMode, "require")
	}
	if cfg.MaxOpenConns != 50 {
		t.Errorf("MaxOpenConns = %v, want %v", cfg.MaxOpenConns, 50)
	}
	if cfg.MaxIdleConns != 10 {
		t.Errorf("MaxIdleConns = %v, want %v", cfg.MaxIdleConns, 10)
	}
	if cfg.ConnMaxLifetime != 10*time.Minute {
		t.Errorf("ConnMaxLifetime = %v, want %v", cfg.ConnMaxLifetime, 10*time.Minute)
	}
	if cfg.ConnMaxIdleTime != 2*time.Minute {
		t.Errorf("ConnMaxIdleTime = %v, want %v", cfg.ConnMaxIdleTime, 2*time.Minute)
	}
}

func TestConnect_InvalidDSN(t *testing.T) {
	cfg := &Config{
		Host:            "invalid-host-that-does-not-exist",
		Port:            5432,
		User:            "user",
		Password:        "pass",
		Database:        "db",
		SSLMode:         "disable",
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Second,
		ConnMaxIdleTime: time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := Connect(ctx, cfg)
	if err == nil {
		t.Error("expected error when connecting to invalid host")
	}
}

func TestMigration_Fields(t *testing.T) {
	mig := Migration{
		Version: 1,
		Name:    "create_users",
		Up:      "CREATE TABLE users (id SERIAL PRIMARY KEY);",
		Down:    "DROP TABLE users;",
	}

	if mig.Version != 1 {
		t.Errorf("Version = %v, want %v", mig.Version, 1)
	}
	if mig.Name != "create_users" {
		t.Errorf("Name = %v, want %v", mig.Name, "create_users")
	}
	if mig.Up != "CREATE TABLE users (id SERIAL PRIMARY KEY);" {
		t.Errorf("Up = %v, want %v", mig.Up, "CREATE TABLE users (id SERIAL PRIMARY KEY);")
	}
	if mig.Down != "DROP TABLE users;" {
		t.Errorf("Down = %v, want %v", mig.Down, "DROP TABLE users;")
	}
}

func TestNewMigrator(t *testing.T) {
	db := &DB{}
	migrator := NewMigrator(db, "test")

	if migrator == nil {
		t.Fatal("NewMigrator() returned nil")
	}
	if migrator.db != db {
		t.Error("migrator.db not set correctly")
	}
	if migrator.schema != "test" {
		t.Errorf("migrator.schema = %v, want %v", migrator.schema, "test")
	}
	if migrator.logger == nil {
		t.Error("migrator.logger should not be nil")
	}
}

func TestMigrator_WithLogger(t *testing.T) {
	db := &DB{}
	migrator := NewMigrator(db, "test")

	result := migrator.WithLogger(nil)
	if result != migrator {
		t.Error("WithLogger should return the same migrator for chaining")
	}
}

func TestMigrationFileParsing(t *testing.T) {
	tests := []struct {
		filename  string
		version   int
		name      string
		direction string
		valid     bool
	}{
		{"001_create_users.up.sql", 1, "create_users", "up", true},
		{"001_create_users.down.sql", 1, "create_users", "down", true},
		{"010_add_email.up.sql", 10, "add_email", "up", true},
		{"100_big_migration.down.sql", 100, "big_migration", "down", true},
		{"invalid.sql", 0, "", "", false},
		{"001_no_direction.sql", 0, "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			name := tt.filename
			var version int
			var migName, direction string
			valid := false

			if len(name) > 4 && name[len(name)-4:] == ".sql" {
				parts := splitN(name, "_", 2)
				if len(parts) == 2 {
					version = parseVersion(parts[0])
					rest := parts[1]
					if hasSuffix(rest, ".up.sql") {
						migName = trimSuffix(rest, ".up.sql")
						direction = "up"
						valid = true
					} else if hasSuffix(rest, ".down.sql") {
						migName = trimSuffix(rest, ".down.sql")
						direction = "down"
						valid = true
					}
				}
			}

			if valid != tt.valid {
				t.Errorf("valid = %v, want %v", valid, tt.valid)
			}
			if tt.valid {
				if version != tt.version {
					t.Errorf("version = %v, want %v", version, tt.version)
				}
				if migName != tt.name {
					t.Errorf("name = %v, want %v", migName, tt.name)
				}
				if direction != tt.direction {
					t.Errorf("direction = %v, want %v", direction, tt.direction)
				}
			}
		})
	}
}

func splitN(s, sep string, n int) []string {
	result := make([]string, 0, n)
	for i := 0; i < n-1; i++ {
		idx := -1
		for j := 0; j < len(s); j++ {
			if j+len(sep) <= len(s) && s[j:j+len(sep)] == sep {
				idx = j
				break
			}
		}
		if idx < 0 {
			break
		}
		result = append(result, s[:idx])
		s = s[idx+len(sep):]
	}
	result = append(result, s)
	return result
}

func parseVersion(s string) int {
	var v int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			v = v*10 + int(c-'0')
		}
	}
	return v
}

func hasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}

func trimSuffix(s, suffix string) string {
	if hasSuffix(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

func TestLoadMigrations_InvalidFS(t *testing.T) {
	db := &DB{}
	migrator := NewMigrator(db, "test")

	var emptyFS embed.FS

	err := migrator.LoadMigrations(emptyFS, "nonexistent")
	if err == nil {
		t.Error("expected error when loading from nonexistent directory")
	}
}

func TestDB_WithLogger(t *testing.T) {
	db := &DB{}
	result := db.WithLogger(nil)

	if result != db {
		t.Error("WithLogger should return the same DB for chaining")
	}
}
