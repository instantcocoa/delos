// Package config provides configuration for the CLI.
package config

import (
	"os"
	"strconv"
)

// Config holds CLI configuration.
type Config struct {
	// ControlPlaneAddr is the gRPC address of the delos control plane, which
	// serves observe, prompt, datasets, eval and deploy from one port.
	ControlPlaneAddr string

	// GatewayURL is the base HTTP URL of the delos-gateway data plane.
	GatewayURL string

	// AuthToken is the control plane's shared secret (DELOS_AUTH_TOKEN). It is
	// environment-only - it must never be written to a config file - and is
	// sent as `authorization: Bearer <token>` on every control-plane RPC and
	// on HTTP calls to /v1/gates/{gate}/verdict. Empty means the control plane
	// is unauthenticated (loopback-only development).
	AuthToken string

	// Output format
	Format string // json, table, yaml

	// Verbosity
	Verbose bool
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		ControlPlaneAddr: getEnv("DELOS_CONTROL_PLANE_ADDR", "localhost:8081"),
		GatewayURL:       getEnv("DELOS_GATEWAY_URL", "http://localhost:8080"),
		AuthToken:        getEnv("DELOS_AUTH_TOKEN", ""),
		Format:           getEnv("DELOS_FORMAT", "table"),
		Verbose:          getEnvBool("DELOS_VERBOSE", false),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		b, err := strconv.ParseBool(value)
		if err == nil {
			return b
		}
	}
	return defaultValue
}
