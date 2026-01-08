// Package config provides configuration loading from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// StorageBackend represents the storage implementation type.
type StorageBackend string

const (
	// StorageMemory uses in-memory storage (for development/testing).
	StorageMemory StorageBackend = "memory"
	// StoragePostgres uses PostgreSQL storage (for production).
	StoragePostgres StorageBackend = "postgres"
)

// Base contains common configuration shared by all services.
type Base struct {
	// Service identification
	ServiceName string
	Environment string // development, staging, production
	Version     string

	// Server
	GRPCPort int
	HTTPPort int

	// Storage backend
	StorageBackend StorageBackend

	// Database (used when StorageBackend is "postgres")
	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// Redis
	RedisURL string

	// Observability
	ObserveEndpoint string
	LogLevel        string
	LogFormat       string // json, text

	// Tracing
	TracingEnabled  bool
	TracingSampling float64
}

// Load loads base configuration from environment variables.
func Load(serviceName string) (*Base, error) {
	cfg := &Base{
		ServiceName: serviceName,
		Environment: getEnv("DELOS_ENV", "development"),
		Version:     getEnv("DELOS_VERSION", "dev"),

		GRPCPort: getEnvInt("DELOS_GRPC_PORT", 9000),
		HTTPPort: getEnvInt("DELOS_HTTP_PORT", 8080),

		StorageBackend: parseStorageBackend(getEnv("DELOS_STORAGE_BACKEND", "memory")),

		DBHost:     getEnv("DELOS_DB_HOST", "localhost"),
		DBPort:     getEnvInt("DELOS_DB_PORT", 5432),
		DBUser:     getEnv("DELOS_DB_USER", "delos"),
		DBPassword: getEnv("DELOS_DB_PASSWORD", ""),
		DBName:     getEnv("DELOS_DB_NAME", "delos"),
		DBSSLMode:  getEnv("DELOS_DB_SSLMODE", "disable"),

		RedisURL: getEnv("DELOS_REDIS_URL", "redis://localhost:6379"),

		ObserveEndpoint: getEnv("DELOS_OBSERVE_ENDPOINT", "localhost:9000"),
		LogLevel:        getEnv("DELOS_LOG_LEVEL", "info"),
		LogFormat:       getEnv("DELOS_LOG_FORMAT", "json"),

		TracingEnabled:  getEnvBool("DELOS_TRACING_ENABLED", true),
		TracingSampling: getEnvFloat("DELOS_TRACING_SAMPLING", 1.0),
	}

	return cfg, nil
}

// DatabaseDSN returns the PostgreSQL connection string.
func (c *Base) DatabaseDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// IsDevelopment returns true if running in development mode.
func (c *Base) IsDevelopment() bool {
	return c.Environment == "development"
}

// IsProduction returns true if running in production mode.
func (c *Base) IsProduction() bool {
	return c.Environment == "production"
}

// UseMemoryStorage returns true if using in-memory storage.
func (c *Base) UseMemoryStorage() bool {
	return c.StorageBackend == StorageMemory
}

// UsePostgresStorage returns true if using PostgreSQL storage.
func (c *Base) UsePostgresStorage() bool {
	return c.StorageBackend == StoragePostgres
}

// Helper functions

func parseStorageBackend(s string) StorageBackend {
	switch s {
	case "postgres", "postgresql", "pg":
		return StoragePostgres
	default:
		return StorageMemory
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if b, err := strconv.ParseBool(value); err == nil {
			return b
		}
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
