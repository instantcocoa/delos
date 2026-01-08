// Package config provides configuration for the CLI.
package config

import (
	"os"
	"strconv"
)

// Config holds CLI configuration.
type Config struct {
	// Service endpoints
	ObserveAddr  string
	RuntimeAddr  string
	PromptAddr   string
	DatasetsAddr string
	EvalAddr     string
	DeployAddr   string

	// Output format
	Format string // json, table, yaml

	// Verbosity
	Verbose bool
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		ObserveAddr:  getEnv("DELOS_OBSERVE_ADDR", "localhost:9000"),
		RuntimeAddr:  getEnv("DELOS_RUNTIME_ADDR", "localhost:9001"),
		PromptAddr:   getEnv("DELOS_PROMPT_ADDR", "localhost:9002"),
		DatasetsAddr: getEnv("DELOS_DATASETS_ADDR", "localhost:9003"),
		EvalAddr:     getEnv("DELOS_EVAL_ADDR", "localhost:9004"),
		DeployAddr:   getEnv("DELOS_DEPLOY_ADDR", "localhost:9005"),
		Format:       getEnv("DELOS_FORMAT", "table"),
		Verbose:      getEnvBool("DELOS_VERBOSE", false),
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
