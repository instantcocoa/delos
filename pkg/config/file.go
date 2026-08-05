package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// delos.yaml is the one config file. It is optional: every value has a
// default, and every value can be overridden by an environment variable.
// Precedence is env var > file > default.
//
// Secrets never belong in this file. Provider credentials (OPENAI_API_KEY,
// ANTHROPIC_API_KEY, AWS credentials, ...) and the database password are read
// from the environment only.

// DefaultFileName is the file looked for in the working directory when
// DELOS_CONFIG is not set.
const DefaultFileName = "delos.yaml"

// File is the on-disk delos.yaml schema. Every field is a pointer (or a map)
// so that "absent" is distinguishable from "set to the zero value" - absent
// fields fall through to the environment and then to the built-in default.
type File struct {
	Env       *string        `yaml:"env"`
	Version   *string        `yaml:"version"`
	LogLevel  *string        `yaml:"log_level"`
	LogFormat *string        `yaml:"log_format"`
	Port      *int           `yaml:"port"`
	Storage   *string        `yaml:"storage"`
	Database  *DatabaseFile  `yaml:"database"`
	Gateway   *GatewayFile   `yaml:"gateway"`
	Telemetry *TelemetryFile `yaml:"telemetry"`
}

// DatabaseFile configures the control plane's PostgreSQL connection.
type DatabaseFile struct {
	Host    *string `yaml:"host"`
	Port    *int    `yaml:"port"`
	User    *string `yaml:"user"`
	Name    *string `yaml:"name"`
	SSLMode *string `yaml:"sslmode"`
}

// GatewayFile configures the data plane, plus the URL the control plane and
// CLI use to reach it.
type GatewayFile struct {
	Port           *int                `yaml:"port"`
	URL            *string             `yaml:"url"`
	RequestTimeout *string             `yaml:"request_timeout"`
	Cache          *CacheFile          `yaml:"cache"`
	Routes         map[string][]string `yaml:"routes"`
}

// CacheFile configures the gateway's exact-match response cache.
type CacheFile struct {
	Enabled  *bool   `yaml:"enabled"`
	TTL      *string `yaml:"ttl"`
	RedisURL *string `yaml:"redis_url"`
}

// TelemetryFile configures gen_ai span emission and export.
type TelemetryFile struct {
	OTLPEndpoint *string  `yaml:"otlp_endpoint"`
	OTLPProtocol *string  `yaml:"otlp_protocol"`
	Stdout       *bool    `yaml:"stdout"`
	Sampling     *float64 `yaml:"sampling"`
	TraceContent *bool    `yaml:"trace_content"`
	TraceRedact  *string  `yaml:"trace_redact"`
}

// Problem is a single human-readable configuration complaint.
type Problem struct {
	Field   string // dotted path into the file, e.g. "gateway.cache.ttl"
	Message string // plain-language explanation, including the fix
}

func (p Problem) String() string {
	if p.Field == "" {
		return p.Message
	}
	return p.Field + ": " + p.Message
}

// DiscoverFilePath returns the config file to load, or "" if there is none.
// DELOS_CONFIG wins; otherwise ./delos.yaml is used when it exists.
func DiscoverFilePath() string {
	if p := os.Getenv("DELOS_CONFIG"); p != "" {
		return p
	}
	if _, err := os.Stat(DefaultFileName); err == nil {
		abs, err := filepath.Abs(DefaultFileName)
		if err != nil {
			return DefaultFileName
		}
		return abs
	}
	return ""
}

// LoadFile parses a delos.yaml. Unknown keys and wrong types are errors, not
// silently ignored: a typo in a config file should be loud.
func LoadFile(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config file %s does not exist", path)
		}
		return nil, fmt.Errorf("could not read %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return &File{}, nil
	}
	var f File
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil {
		if errors.Is(err, io.EOF) {
			// Comments only - an empty document is a valid (all-default) config.
			return &File{}, nil
		}
		return nil, fmt.Errorf("%s is not valid: %s", path, humanizeYAMLError(err))
	}
	return &f, nil
}

// humanizeYAMLError rewrites yaml.v3's internal wording into something a
// person reading a config file can act on.
func humanizeYAMLError(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "yaml: unmarshal errors:\n")
	lines := strings.Split(msg, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "line 7: field prot not found in type config.File"
		if i := strings.Index(line, " not found in type "); i >= 0 {
			field := strings.TrimPrefix(line[:i], "line ")
			if j := strings.Index(field, ": field "); j >= 0 {
				lineNo := field[:j]
				name := field[j+len(": field "):]
				line = fmt.Sprintf("line %s: unknown option %q (check the spelling against delos.yaml.example)", lineNo, name)
			}
		}
		line = strings.ReplaceAll(line, "cannot unmarshal", "wrong type:")
		out = append(out, line)
	}
	return strings.Join(out, "; ")
}

// Validate reports every problem with a parsed file, in plain language. An
// empty slice means the file is valid.
func (f *File) Validate() []Problem {
	var problems []Problem
	if f == nil {
		return nil
	}
	add := func(field, format string, args ...any) {
		problems = append(problems, Problem{Field: field, Message: fmt.Sprintf(format, args...)})
	}

	checkEnum := func(field string, v *string, allowed ...string) {
		if v == nil {
			return
		}
		for _, a := range allowed {
			if *v == a {
				return
			}
		}
		add(field, "%q is not one of %s", *v, strings.Join(allowed, ", "))
	}
	checkPort := func(field string, v *int) {
		if v == nil {
			return
		}
		if *v < 1 || *v > 65535 {
			add(field, "%d is not a valid TCP port (1-65535)", *v)
		}
	}
	checkDuration := func(field string, v *string) {
		if v == nil {
			return
		}
		if _, err := time.ParseDuration(*v); err != nil {
			add(field, "%q is not a duration; use values like \"30s\", \"5m\" or \"1h\"", *v)
		}
	}

	checkEnum("env", f.Env, "development", "staging", "production")
	checkEnum("log_level", f.LogLevel, "debug", "info", "warn", "error")
	checkEnum("log_format", f.LogFormat, "json", "text")
	checkPort("port", f.Port)
	checkEnum("storage", f.Storage, "memory", "postgres")

	if db := f.Database; db != nil {
		checkPort("database.port", db.Port)
		checkEnum("database.sslmode", db.SSLMode,
			"disable", "allow", "prefer", "require", "verify-ca", "verify-full")
		if db.Host != nil && strings.TrimSpace(*db.Host) == "" {
			add("database.host", "must not be empty; remove the key to use the default (localhost)")
		}
	}

	if gw := f.Gateway; gw != nil {
		checkPort("gateway.port", gw.Port)
		checkDuration("gateway.request_timeout", gw.RequestTimeout)
		if gw.URL != nil {
			u := *gw.URL
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				add("gateway.url", "%q must be a full URL starting with http:// or https://", u)
			}
		}
		if c := gw.Cache; c != nil {
			checkDuration("gateway.cache.ttl", c.TTL)
			if c.RedisURL != nil && *c.RedisURL != "" &&
				!strings.HasPrefix(*c.RedisURL, "redis://") && !strings.HasPrefix(*c.RedisURL, "rediss://") {
				add("gateway.cache.redis_url", "%q must start with redis:// or rediss://", *c.RedisURL)
			}
		}
		for alias, targets := range gw.Routes {
			field := "gateway.routes." + alias
			if len(targets) == 0 {
				add(field, "needs at least one target, e.g. [openai/gpt-4o]")
				continue
			}
			for _, t := range targets {
				if !strings.Contains(t, "/") {
					add(field, "target %q must be written as provider/model, e.g. openai/gpt-4o", t)
				}
			}
		}
	}

	if tel := f.Telemetry; tel != nil {
		checkEnum("telemetry.otlp_protocol", tel.OTLPProtocol, "http", "grpc")
		if tel.Sampling != nil && (*tel.Sampling < 0 || *tel.Sampling > 1) {
			add("telemetry.sampling", "%v is out of range; use a fraction between 0 and 1", *tel.Sampling)
		}
		if tel.TraceRedact != nil && *tel.TraceRedact != "" {
			if _, err := ParseRedactionSpec(*tel.TraceRedact); err != nil {
				add("telemetry.trace_redact", "%v", err)
			}
		}
		if tel.OTLPEndpoint != nil && strings.HasPrefix(*tel.OTLPEndpoint, "http") && tel.OTLPProtocol != nil && *tel.OTLPProtocol == "grpc" {
			// Not fatal, but almost always a mistake worth flagging.
			add("telemetry.otlp_endpoint", "grpc protocol expects a host:port endpoint, not a URL (%q)", *tel.OTLPEndpoint)
		}
	}

	return problems
}

// ParseRedactionSpec checks the shape of a DELOS_TRACE_REDACT value without
// importing the gateway package: a comma-separated list of regexes.
func ParseRedactionSpec(spec string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no rules found; expected a comma-separated list of regular expressions")
	}
	return out, nil
}

// ---- precedence helpers: env var > file > default ----

func fileStr(key string, fileVal *string, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if fileVal != nil {
		return *fileVal
	}
	return def
}

func fileInt(key string, fileVal *int, def int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	if fileVal != nil {
		return *fileVal
	}
	return def
}

func fileBool(key string, fileVal *bool, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	if fileVal != nil {
		return *fileVal
	}
	return def
}

func fileFloat(key string, fileVal *float64, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if fl, err := strconv.ParseFloat(v, 64); err == nil {
			return fl
		}
	}
	if fileVal != nil {
		return *fileVal
	}
	return def
}

func fileDuration(key string, fileVal *string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	if fileVal != nil {
		if d, err := time.ParseDuration(*fileVal); err == nil {
			return d
		}
	}
	return def
}
