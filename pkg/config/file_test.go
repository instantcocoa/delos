package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "delos.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

// clearDelosEnv blanks every DELOS_* variable for the test so that file-layer
// assertions are hermetic: env beats file by design, and CI exports DELOS_DB_*
// for the integration database.
func clearDelosEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if key, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(key, "DELOS_") {
			t.Setenv(key, "")
		}
	}
}

func TestLoadFileValid(t *testing.T) {
	clearDelosEnv(t)
	path := writeFile(t, `
env: production
log_level: debug
port: 9091
storage: postgres
database:
  host: db.internal
  port: 6432
  name: delos_prod
  sslmode: require
gateway:
  port: 9090
  url: https://gw.internal
  request_timeout: 90s
  cache:
    enabled: false
    ttl: 30s
  routes:
    gpt-4:
      - openai/gpt-4o
      - anthropic/claude-sonnet-4-5
telemetry:
  otlp_endpoint: http://otel:4318
  otlp_protocol: http
  trace_content: true
  sampling: 0.25
`)
	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if problems := f.Validate(); len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}

	cfg, err := loadWith("test", path, f)
	if err != nil {
		t.Fatalf("loadWith: %v", err)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q", cfg.Environment)
	}
	if cfg.Port != 9091 {
		t.Errorf("Port = %d, want 9091", cfg.Port)
	}
	if !cfg.UsePostgresStorage() {
		t.Errorf("storage = %v, want postgres", cfg.StorageBackend)
	}
	if cfg.DBHost != "db.internal" || cfg.DBPort != 6432 || cfg.DBSSLMode != "require" {
		t.Errorf("database = %s:%d %s", cfg.DBHost, cfg.DBPort, cfg.DBSSLMode)
	}
	if cfg.GatewayPort != 9090 || cfg.GatewayURL != "https://gw.internal" {
		t.Errorf("gateway = %d %s", cfg.GatewayPort, cfg.GatewayURL)
	}
	if cfg.RequestTimeout != 90*time.Second {
		t.Errorf("RequestTimeout = %v", cfg.RequestTimeout)
	}
	if cfg.CacheEnabled {
		t.Errorf("CacheEnabled = true, want false")
	}
	if cfg.CacheTTL != 30*time.Second {
		t.Errorf("CacheTTL = %v", cfg.CacheTTL)
	}
	if got := cfg.Routes["gpt-4"]; len(got) != 2 || got[0] != "openai/gpt-4o" {
		t.Errorf("Routes = %v", cfg.Routes)
	}
	if cfg.OTLPEndpoint != "http://otel:4318" || !cfg.TraceContent || cfg.TracingSampling != 0.25 {
		t.Errorf("telemetry = %q %v %v", cfg.OTLPEndpoint, cfg.TraceContent, cfg.TracingSampling)
	}
	if cfg.ConfigPath != path {
		t.Errorf("ConfigPath = %q", cfg.ConfigPath)
	}
}

func TestLoadFileUnknownKey(t *testing.T) {
	path := writeFile(t, "prot: 8080\n")
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("expected an error for an unknown key")
	}
	if !strings.Contains(err.Error(), "unknown option") {
		t.Errorf("error should name the unknown option, got: %v", err)
	}
}

func TestLoadFileWrongType(t *testing.T) {
	path := writeFile(t, "port: not-a-number\n")
	if _, err := LoadFile(path); err == nil {
		t.Fatal("expected an error for a non-numeric port")
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestLoadFileEmpty(t *testing.T) {
	f, err := LoadFile(writeFile(t, "\n#  only comments\n"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if len(f.Validate()) != 0 {
		t.Errorf("empty file should be valid")
	}
}

func TestValidateProblems(t *testing.T) {
	path := writeFile(t, `
env: prod
log_level: chatty
port: 99999
storage: mysql
database:
  sslmode: maybe
gateway:
  url: gw.internal
  request_timeout: soon
  cache:
    ttl: 5 minutes
    redis_url: localhost:6379
  routes:
    gpt-4: [gpt-4o]
telemetry:
  otlp_protocol: thrift
  sampling: 4
`)
	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	problems := f.Validate()
	want := []string{
		"env", "log_level", "port", "storage", "database.sslmode",
		"gateway.url", "gateway.request_timeout", "gateway.cache.ttl",
		"gateway.cache.redis_url", "gateway.routes.gpt-4",
		"telemetry.otlp_protocol", "telemetry.sampling",
	}
	got := map[string]bool{}
	for _, p := range problems {
		got[p.Field] = true
		if p.Message == "" {
			t.Errorf("problem %q has no message", p.Field)
		}
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected a problem for %q, got %v", w, problems)
		}
	}
}

func TestEnvOverridesFile(t *testing.T) {
	clearDelosEnv(t)
	path := writeFile(t, "port: 9091\ngateway:\n  port: 9090\n  url: https://gw.internal\n")
	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	t.Setenv("DELOS_PORT", "7000")
	t.Setenv("DELOS_GATEWAY_URL", "http://localhost:8080")

	cfg, err := loadWith("test", path, f)
	if err != nil {
		t.Fatalf("loadWith: %v", err)
	}
	if cfg.Port != 7000 {
		t.Errorf("env should win for port: got %d", cfg.Port)
	}
	if cfg.GatewayURL != "http://localhost:8080" {
		t.Errorf("env should win for gateway.url: got %q", cfg.GatewayURL)
	}
	if cfg.GatewayPort != 9090 {
		t.Errorf("file should win over the default for gateway.port: got %d", cfg.GatewayPort)
	}
}

func TestDefaultsWithNoFile(t *testing.T) {
	clearDelosEnv(t)
	cfg, err := loadWith("test", "", nil)
	if err != nil {
		t.Fatalf("loadWith: %v", err)
	}
	if cfg.Port != 8081 || cfg.GatewayPort != 8080 {
		t.Errorf("ports = %d %d, want 8081 8080", cfg.Port, cfg.GatewayPort)
	}
	if !cfg.CacheEnabled || cfg.CacheTTL != 5*time.Minute {
		t.Errorf("cache defaults = %v %v", cfg.CacheEnabled, cfg.CacheTTL)
	}
	if cfg.OTLPProtocol != "http" {
		t.Errorf("OTLPProtocol = %q", cfg.OTLPProtocol)
	}
	if cfg.CacheRedisURL != "" {
		t.Errorf("CacheRedisURL should default to empty (in-process LRU), got %q", cfg.CacheRedisURL)
	}
}

func TestCacheOffEnv(t *testing.T) {
	clearDelosEnv(t)
	t.Setenv("DELOS_CACHE", "off")
	cfg, err := loadWith("test", "", nil)
	if err != nil {
		t.Fatalf("loadWith: %v", err)
	}
	if cfg.CacheEnabled {
		t.Error("DELOS_CACHE=off should disable the cache")
	}
}

func TestDiscoverFilePath(t *testing.T) {
	t.Setenv("DELOS_CONFIG", "/tmp/somewhere/delos.yaml")
	if got := DiscoverFilePath(); got != "/tmp/somewhere/delos.yaml" {
		t.Errorf("DiscoverFilePath() = %q", got)
	}
	t.Setenv("DELOS_CONFIG", "")
	dir := t.TempDir()
	t.Chdir(dir)
	if got := DiscoverFilePath(); got != "" {
		t.Errorf("DiscoverFilePath() with no file = %q, want empty", got)
	}
	if err := os.WriteFile(filepath.Join(dir, DefaultFileName), []byte("port: 8081\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverFilePath(); !strings.HasSuffix(got, DefaultFileName) {
		t.Errorf("DiscoverFilePath() = %q, want the local delos.yaml", got)
	}
}

func TestExampleFileIsValid(t *testing.T) {
	path := "../../delos.yaml.example"
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s not present", path)
	}
	f, err := LoadFile(path)
	if err != nil {
		t.Fatalf("delos.yaml.example does not parse: %v", err)
	}
	if problems := f.Validate(); len(problems) != 0 {
		t.Fatalf("delos.yaml.example is invalid: %v", problems)
	}
}
