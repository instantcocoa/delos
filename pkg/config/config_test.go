package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Clean up environment after test
	envVars := []string{
		"DELOS_ENV", "DELOS_VERSION", "DELOS_GRPC_PORT", "DELOS_HTTP_PORT",
		"DELOS_DB_HOST", "DELOS_DB_PORT", "DELOS_DB_USER", "DELOS_DB_PASSWORD",
		"DELOS_DB_NAME", "DELOS_DB_SSLMODE", "DELOS_REDIS_URL",
		"DELOS_OBSERVE_ENDPOINT", "DELOS_LOG_LEVEL", "DELOS_LOG_FORMAT",
		"DELOS_TRACING_ENABLED", "DELOS_TRACING_SAMPLING",
	}
	originalValues := make(map[string]string)
	for _, key := range envVars {
		originalValues[key] = os.Getenv(key)
	}
	defer func() {
		for key, val := range originalValues {
			if val == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, val)
			}
		}
	}()

	// Clear all env vars for default test
	for _, key := range envVars {
		os.Unsetenv(key)
	}

	t.Run("defaults", func(t *testing.T) {
		cfg, err := Load("test-service")
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.ServiceName != "test-service" {
			t.Errorf("ServiceName = %v, want %v", cfg.ServiceName, "test-service")
		}
		if cfg.Environment != "development" {
			t.Errorf("Environment = %v, want %v", cfg.Environment, "development")
		}
		if cfg.Version != "dev" {
			t.Errorf("Version = %v, want %v", cfg.Version, "dev")
		}
		if cfg.GRPCPort != 9000 {
			t.Errorf("GRPCPort = %v, want %v", cfg.GRPCPort, 9000)
		}
		if cfg.HTTPPort != 8080 {
			t.Errorf("HTTPPort = %v, want %v", cfg.HTTPPort, 8080)
		}
		if cfg.DBHost != "localhost" {
			t.Errorf("DBHost = %v, want %v", cfg.DBHost, "localhost")
		}
		if cfg.DBPort != 5432 {
			t.Errorf("DBPort = %v, want %v", cfg.DBPort, 5432)
		}
		if cfg.DBUser != "delos" {
			t.Errorf("DBUser = %v, want %v", cfg.DBUser, "delos")
		}
		if cfg.DBName != "delos" {
			t.Errorf("DBName = %v, want %v", cfg.DBName, "delos")
		}
		if cfg.DBSSLMode != "disable" {
			t.Errorf("DBSSLMode = %v, want %v", cfg.DBSSLMode, "disable")
		}
		if cfg.RedisURL != "redis://localhost:6379" {
			t.Errorf("RedisURL = %v, want %v", cfg.RedisURL, "redis://localhost:6379")
		}
		if cfg.LogLevel != "info" {
			t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, "info")
		}
		if cfg.LogFormat != "json" {
			t.Errorf("LogFormat = %v, want %v", cfg.LogFormat, "json")
		}
		if !cfg.TracingEnabled {
			t.Errorf("TracingEnabled = %v, want %v", cfg.TracingEnabled, true)
		}
		if cfg.TracingSampling != 1.0 {
			t.Errorf("TracingSampling = %v, want %v", cfg.TracingSampling, 1.0)
		}
	})

	t.Run("from environment", func(t *testing.T) {
		os.Setenv("DELOS_ENV", "production")
		os.Setenv("DELOS_VERSION", "1.2.3")
		os.Setenv("DELOS_GRPC_PORT", "9099")
		os.Setenv("DELOS_HTTP_PORT", "8888")
		os.Setenv("DELOS_DB_HOST", "db.example.com")
		os.Setenv("DELOS_DB_PORT", "5433")
		os.Setenv("DELOS_DB_USER", "admin")
		os.Setenv("DELOS_DB_PASSWORD", "secret123")
		os.Setenv("DELOS_DB_NAME", "mydb")
		os.Setenv("DELOS_DB_SSLMODE", "require")
		os.Setenv("DELOS_REDIS_URL", "redis://redis.example.com:6380")
		os.Setenv("DELOS_OBSERVE_ENDPOINT", "observe.example.com:9000")
		os.Setenv("DELOS_LOG_LEVEL", "debug")
		os.Setenv("DELOS_LOG_FORMAT", "text")
		os.Setenv("DELOS_TRACING_ENABLED", "false")
		os.Setenv("DELOS_TRACING_SAMPLING", "0.5")

		cfg, err := Load("prod-service")
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.Environment != "production" {
			t.Errorf("Environment = %v, want %v", cfg.Environment, "production")
		}
		if cfg.Version != "1.2.3" {
			t.Errorf("Version = %v, want %v", cfg.Version, "1.2.3")
		}
		if cfg.GRPCPort != 9099 {
			t.Errorf("GRPCPort = %v, want %v", cfg.GRPCPort, 9099)
		}
		if cfg.HTTPPort != 8888 {
			t.Errorf("HTTPPort = %v, want %v", cfg.HTTPPort, 8888)
		}
		if cfg.DBHost != "db.example.com" {
			t.Errorf("DBHost = %v, want %v", cfg.DBHost, "db.example.com")
		}
		if cfg.DBPort != 5433 {
			t.Errorf("DBPort = %v, want %v", cfg.DBPort, 5433)
		}
		if cfg.DBUser != "admin" {
			t.Errorf("DBUser = %v, want %v", cfg.DBUser, "admin")
		}
		if cfg.DBPassword != "secret123" {
			t.Errorf("DBPassword = %v, want %v", cfg.DBPassword, "secret123")
		}
		if cfg.DBName != "mydb" {
			t.Errorf("DBName = %v, want %v", cfg.DBName, "mydb")
		}
		if cfg.DBSSLMode != "require" {
			t.Errorf("DBSSLMode = %v, want %v", cfg.DBSSLMode, "require")
		}
		if cfg.RedisURL != "redis://redis.example.com:6380" {
			t.Errorf("RedisURL = %v, want %v", cfg.RedisURL, "redis://redis.example.com:6380")
		}
		if cfg.ObserveEndpoint != "observe.example.com:9000" {
			t.Errorf("ObserveEndpoint = %v, want %v", cfg.ObserveEndpoint, "observe.example.com:9000")
		}
		if cfg.LogLevel != "debug" {
			t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, "debug")
		}
		if cfg.LogFormat != "text" {
			t.Errorf("LogFormat = %v, want %v", cfg.LogFormat, "text")
		}
		if cfg.TracingEnabled {
			t.Errorf("TracingEnabled = %v, want %v", cfg.TracingEnabled, false)
		}
		if cfg.TracingSampling != 0.5 {
			t.Errorf("TracingSampling = %v, want %v", cfg.TracingSampling, 0.5)
		}
	})

	t.Run("invalid values use defaults", func(t *testing.T) {
		os.Setenv("DELOS_GRPC_PORT", "not-a-number")
		os.Setenv("DELOS_DB_PORT", "invalid")
		os.Setenv("DELOS_TRACING_ENABLED", "invalid-bool")
		os.Setenv("DELOS_TRACING_SAMPLING", "not-a-float")

		cfg, err := Load("test-service")
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}

		if cfg.GRPCPort != 9000 {
			t.Errorf("GRPCPort with invalid input = %v, want default %v", cfg.GRPCPort, 9000)
		}
		if cfg.DBPort != 5432 {
			t.Errorf("DBPort with invalid input = %v, want default %v", cfg.DBPort, 5432)
		}
		if !cfg.TracingEnabled {
			t.Errorf("TracingEnabled with invalid input = %v, want default %v", cfg.TracingEnabled, true)
		}
		if cfg.TracingSampling != 1.0 {
			t.Errorf("TracingSampling with invalid input = %v, want default %v", cfg.TracingSampling, 1.0)
		}
	})
}

func TestBase_DatabaseDSN(t *testing.T) {
	cfg := &Base{
		DBHost:     "localhost",
		DBPort:     5432,
		DBUser:     "testuser",
		DBPassword: "testpass",
		DBName:     "testdb",
		DBSSLMode:  "disable",
	}

	dsn := cfg.DatabaseDSN()
	expected := "host=localhost port=5432 user=testuser password=testpass dbname=testdb sslmode=disable"
	if dsn != expected {
		t.Errorf("DatabaseDSN() = %v, want %v", dsn, expected)
	}
}

func TestBase_IsDevelopment(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"development", true},
		{"staging", false},
		{"production", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			cfg := &Base{Environment: tt.env}
			if got := cfg.IsDevelopment(); got != tt.want {
				t.Errorf("IsDevelopment() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBase_IsProduction(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"production", true},
		{"development", false},
		{"staging", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			cfg := &Base{Environment: tt.env}
			if got := cfg.IsProduction(); got != tt.want {
				t.Errorf("IsProduction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetEnv(t *testing.T) {
	os.Unsetenv("TEST_ENV_VAR")

	// Test default value
	if got := getEnv("TEST_ENV_VAR", "default"); got != "default" {
		t.Errorf("getEnv() with unset var = %v, want %v", got, "default")
	}

	// Test set value
	os.Setenv("TEST_ENV_VAR", "custom")
	defer os.Unsetenv("TEST_ENV_VAR")

	if got := getEnv("TEST_ENV_VAR", "default"); got != "custom" {
		t.Errorf("getEnv() with set var = %v, want %v", got, "custom")
	}
}

func TestGetEnvInt(t *testing.T) {
	os.Unsetenv("TEST_INT_VAR")

	// Test default value
	if got := getEnvInt("TEST_INT_VAR", 42); got != 42 {
		t.Errorf("getEnvInt() with unset var = %v, want %v", got, 42)
	}

	// Test valid int
	os.Setenv("TEST_INT_VAR", "123")
	defer os.Unsetenv("TEST_INT_VAR")

	if got := getEnvInt("TEST_INT_VAR", 42); got != 123 {
		t.Errorf("getEnvInt() with valid int = %v, want %v", got, 123)
	}

	// Test invalid int
	os.Setenv("TEST_INT_VAR", "not-a-number")
	if got := getEnvInt("TEST_INT_VAR", 42); got != 42 {
		t.Errorf("getEnvInt() with invalid int = %v, want default %v", got, 42)
	}
}

func TestGetEnvBool(t *testing.T) {
	os.Unsetenv("TEST_BOOL_VAR")

	// Test default value
	if got := getEnvBool("TEST_BOOL_VAR", true); got != true {
		t.Errorf("getEnvBool() with unset var = %v, want %v", got, true)
	}

	// Test valid bool values
	testCases := []struct {
		value string
		want  bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
		{"TRUE", true},
		{"FALSE", false},
	}

	for _, tc := range testCases {
		os.Setenv("TEST_BOOL_VAR", tc.value)
		if got := getEnvBool("TEST_BOOL_VAR", !tc.want); got != tc.want {
			t.Errorf("getEnvBool(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}

	// Test invalid bool
	os.Setenv("TEST_BOOL_VAR", "not-a-bool")
	if got := getEnvBool("TEST_BOOL_VAR", true); got != true {
		t.Errorf("getEnvBool() with invalid bool = %v, want default %v", got, true)
	}

	os.Unsetenv("TEST_BOOL_VAR")
}

func TestGetEnvFloat(t *testing.T) {
	os.Unsetenv("TEST_FLOAT_VAR")

	// Test default value
	if got := getEnvFloat("TEST_FLOAT_VAR", 3.14); got != 3.14 {
		t.Errorf("getEnvFloat() with unset var = %v, want %v", got, 3.14)
	}

	// Test valid float
	os.Setenv("TEST_FLOAT_VAR", "2.718")
	defer os.Unsetenv("TEST_FLOAT_VAR")

	if got := getEnvFloat("TEST_FLOAT_VAR", 3.14); got != 2.718 {
		t.Errorf("getEnvFloat() with valid float = %v, want %v", got, 2.718)
	}

	// Test invalid float
	os.Setenv("TEST_FLOAT_VAR", "not-a-float")
	if got := getEnvFloat("TEST_FLOAT_VAR", 3.14); got != 3.14 {
		t.Errorf("getEnvFloat() with invalid float = %v, want default %v", got, 3.14)
	}
}

func TestGetEnvDuration(t *testing.T) {
	os.Unsetenv("TEST_DURATION_VAR")

	defaultDur := 5 * 1000000000 // 5 seconds in nanoseconds

	// Test default value
	if got := getEnvDuration("TEST_DURATION_VAR", 5000000000); got != 5000000000 {
		t.Errorf("getEnvDuration() with unset var = %v, want %v", got, defaultDur)
	}

	// Test valid duration
	os.Setenv("TEST_DURATION_VAR", "10s")
	defer os.Unsetenv("TEST_DURATION_VAR")

	if got := getEnvDuration("TEST_DURATION_VAR", 5000000000); got != 10000000000 {
		t.Errorf("getEnvDuration() with valid duration = %v, want %v", got, 10000000000)
	}

	// Test invalid duration
	os.Setenv("TEST_DURATION_VAR", "not-a-duration")
	if got := getEnvDuration("TEST_DURATION_VAR", 5000000000); got != 5000000000 {
		t.Errorf("getEnvDuration() with invalid duration = %v, want default %v", got, defaultDur)
	}
}
