// Package config provides configuration loading from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
	// ConfigPath is the delos.yaml that contributed to this configuration,
	// or "" when none was found.
	ConfigPath string

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

	// Service addresses (for inter-service communication)
	RuntimeAddr  string
	PromptAddr   string
	DatasetsAddr string
	EvalAddr     string
	DeployAddr   string

	// Control plane
	Port int // `delos serve` listen port (DELOS_PORT)

	// Security. All three are environment-only: delos.yaml is meant to be
	// commit-safe, so it has no schema for secrets (see file.go).
	//
	// AuthToken is the control plane's shared secret (DELOS_AUTH_TOKEN). When
	// empty the control plane refuses to serve anything but loopback.
	AuthToken string
	// BindAddr is an explicit listen host (DELOS_BIND), e.g. 0.0.0.0 or
	// 127.0.0.1. Empty means "decide from the security posture".
	BindAddr string
	// AllowUnauthenticated acknowledges an open gateway
	// (DELOS_ALLOW_UNAUTHENTICATED=true) on a reachable address.
	AllowUnauthenticated bool
	// ObserveMemoryMaxSpans caps the in-memory span store
	// (DELOS_OBSERVE_MEMORY_MAX_SPANS).
	ObserveMemoryMaxSpans int

	// Gateway (data plane)
	GatewayPort    int           // DELOS_GATEWAY_PORT
	GatewayURL     string        // DELOS_GATEWAY_URL - how clients reach the gateway
	RequestTimeout time.Duration // DELOS_REQUEST_TIMEOUT, end-to-end budget
	CacheEnabled   bool          // DELOS_CACHE=off disables
	CacheTTL       time.Duration // DELOS_CACHE_TTL
	CacheRedisURL  string        // DELOS_REDIS_URL; empty means in-process LRU
	// Routes maps a model alias to an ordered fallback chain of
	// provider/model targets. Set from the config file; DELOS_ROUTES (JSON)
	// overrides it and is exposed as RoutesJSON.
	Routes     map[string][]string
	RoutesJSON string

	// Telemetry
	OTLPEndpoint string // DELOS_OTLP_ENDPOINT
	OTLPProtocol string // DELOS_OTLP_PROTOCOL: http | grpc
	TraceStdout  bool   // DELOS_TRACE_STDOUT
	TraceContent bool   // DELOS_TRACE_CONTENT - prompt/completion capture
	TraceRedact  string // DELOS_TRACE_REDACT - comma-separated regexes
}

// Load loads configuration with precedence: environment variable > delos.yaml
// > built-in default. The file is optional; it is located via DELOS_CONFIG or
// ./delos.yaml. A malformed file is an error - it is never silently ignored.
func Load(serviceName string) (*Base, error) {
	path := DiscoverFilePath()
	var f *File
	if path != "" {
		loaded, err := LoadFile(path)
		if err != nil {
			return nil, err
		}
		if problems := loaded.Validate(); len(problems) > 0 {
			msgs := make([]string, 0, len(problems))
			for _, p := range problems {
				msgs = append(msgs, p.String())
			}
			return nil, fmt.Errorf("%s is invalid: %s (run `delos config validate` for details)",
				path, strings.Join(msgs, "; "))
		}
		f = loaded
	}
	return loadWith(serviceName, path, f)
}

// loadWith applies the precedence rules against an already-parsed file (which
// may be nil).
func loadWith(serviceName, path string, f *File) (*Base, error) {
	if f == nil {
		f = &File{}
	}
	db := f.Database
	if db == nil {
		db = &DatabaseFile{}
	}
	gw := f.Gateway
	if gw == nil {
		gw = &GatewayFile{}
	}
	cache := gw.Cache
	if cache == nil {
		cache = &CacheFile{}
	}
	tel := f.Telemetry
	if tel == nil {
		tel = &TelemetryFile{}
	}

	cfg := &Base{
		ConfigPath:  path,
		ServiceName: serviceName,
		Environment: fileStr("DELOS_ENV", f.Env, "development"),
		Version:     fileStr("DELOS_VERSION", f.Version, "dev"),

		GRPCPort: getEnvInt("DELOS_GRPC_PORT", 9000),
		HTTPPort: getEnvInt("DELOS_HTTP_PORT", 8080),

		StorageBackend: parseStorageBackend(fileStr("DELOS_STORAGE_BACKEND", f.Storage, "memory")),

		DBHost: fileStr("DELOS_DB_HOST", db.Host, "localhost"),
		DBPort: fileInt("DELOS_DB_PORT", db.Port, 5432),
		DBUser: fileStr("DELOS_DB_USER", db.User, "delos"),
		// Passwords are env-only on purpose; they do not belong in delos.yaml.
		DBPassword: getEnv("DELOS_DB_PASSWORD", ""),
		DBName:     fileStr("DELOS_DB_NAME", db.Name, "delos"),
		DBSSLMode:  fileStr("DELOS_DB_SSLMODE", db.SSLMode, "disable"),

		RedisURL: getEnv("DELOS_REDIS_URL", "redis://localhost:6379"),

		ObserveEndpoint: getEnv("DELOS_OBSERVE_ENDPOINT", "localhost:9000"),
		LogLevel:        fileStr("DELOS_LOG_LEVEL", f.LogLevel, "info"),
		LogFormat:       fileStr("DELOS_LOG_FORMAT", f.LogFormat, "json"),

		TracingEnabled:  getEnvBool("DELOS_TRACING_ENABLED", true),
		TracingSampling: fileFloat("DELOS_TRACING_SAMPLING", tel.Sampling, 1.0),

		RuntimeAddr:  getEnv("DELOS_RUNTIME_ENDPOINT", "localhost:9001"),
		PromptAddr:   getEnv("DELOS_PROMPT_ENDPOINT", "localhost:9002"),
		DatasetsAddr: getEnv("DELOS_DATASETS_ENDPOINT", "localhost:9003"),
		EvalAddr:     getEnv("DELOS_EVAL_ENDPOINT", "localhost:9004"),
		DeployAddr:   getEnv("DELOS_DEPLOY_ENDPOINT", "localhost:9005"),

		Port: fileInt("DELOS_PORT", f.Port, 8081),

		AuthToken:             getEnv("DELOS_AUTH_TOKEN", ""),
		BindAddr:              getEnv("DELOS_BIND", ""),
		AllowUnauthenticated:  getEnvBool("DELOS_ALLOW_UNAUTHENTICATED", false),
		ObserveMemoryMaxSpans: getEnvInt("DELOS_OBSERVE_MEMORY_MAX_SPANS", 50000),

		GatewayPort:    fileInt("DELOS_GATEWAY_PORT", gw.Port, 8080),
		GatewayURL:     fileStr("DELOS_GATEWAY_URL", gw.URL, "http://localhost:8080"),
		RequestTimeout: fileDuration("DELOS_REQUEST_TIMEOUT", gw.RequestTimeout, 5*time.Minute),
		CacheEnabled:   cacheEnabled(cache.Enabled),
		CacheTTL:       fileDuration("DELOS_CACHE_TTL", cache.TTL, 5*time.Minute),
		CacheRedisURL:  fileStr("DELOS_REDIS_URL", cache.RedisURL, ""),
		Routes:         gw.Routes,
		RoutesJSON:     os.Getenv("DELOS_ROUTES"),

		OTLPEndpoint: fileStr("DELOS_OTLP_ENDPOINT", tel.OTLPEndpoint, ""),
		OTLPProtocol: fileStr("DELOS_OTLP_PROTOCOL", tel.OTLPProtocol, "http"),
		TraceStdout:  fileBool("DELOS_TRACE_STDOUT", tel.Stdout, false),
		TraceContent: fileBool("DELOS_TRACE_CONTENT", tel.TraceContent, false),
		TraceRedact:  fileStr("DELOS_TRACE_REDACT", tel.TraceRedact, ""),
	}

	return cfg, nil
}

// cacheEnabled honours the historical DELOS_CACHE=off switch on top of the
// file's gateway.cache.enabled.
func cacheEnabled(fileVal *bool) bool {
	if v := os.Getenv("DELOS_CACHE"); v != "" {
		return !strings.EqualFold(v, "off") && !strings.EqualFold(v, "false")
	}
	if fileVal != nil {
		return *fileVal
	}
	return true
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
