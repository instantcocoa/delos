package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	// Clear environment for testing
	envVars := []string{
		"DELOS_CONTROL_PLANE_ADDR", "DELOS_GATEWAY_URL",
		"DELOS_FORMAT", "DELOS_VERBOSE",
	}
	originalValues := make(map[string]string)
	for _, key := range envVars {
		originalValues[key] = os.Getenv(key)
		os.Unsetenv(key)
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

	t.Run("default values", func(t *testing.T) {
		cfg := DefaultConfig()

		if cfg.ControlPlaneAddr != "localhost:8081" {
			t.Errorf("ControlPlaneAddr = %v, want localhost:8081", cfg.ControlPlaneAddr)
		}
		if cfg.GatewayURL != "http://localhost:8080" {
			t.Errorf("GatewayURL = %v, want http://localhost:8080", cfg.GatewayURL)
		}
		if cfg.Format != "table" {
			t.Errorf("Format = %v, want table", cfg.Format)
		}
		if cfg.Verbose {
			t.Error("Verbose = true, want false")
		}
	})

	t.Run("from environment", func(t *testing.T) {
		os.Setenv("DELOS_CONTROL_PLANE_ADDR", "control.example.com:8081")
		os.Setenv("DELOS_GATEWAY_URL", "https://gateway.example.com")
		os.Setenv("DELOS_FORMAT", "json")
		os.Setenv("DELOS_VERBOSE", "true")

		cfg := DefaultConfig()

		if cfg.ControlPlaneAddr != "control.example.com:8081" {
			t.Errorf("ControlPlaneAddr = %v, want control.example.com:8081", cfg.ControlPlaneAddr)
		}
		if cfg.GatewayURL != "https://gateway.example.com" {
			t.Errorf("GatewayURL = %v, want https://gateway.example.com", cfg.GatewayURL)
		}
		if cfg.Format != "json" {
			t.Errorf("Format = %v, want json", cfg.Format)
		}
		if !cfg.Verbose {
			t.Error("Verbose = false, want true")
		}
	})
}

func TestGetEnv(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV")

	t.Run("unset returns default", func(t *testing.T) {
		result := getEnv("TEST_GET_ENV", "default-value")
		if result != "default-value" {
			t.Errorf("getEnv() = %v, want default-value", result)
		}
	})

	t.Run("set returns value", func(t *testing.T) {
		os.Setenv("TEST_GET_ENV", "custom-value")
		defer os.Unsetenv("TEST_GET_ENV")

		result := getEnv("TEST_GET_ENV", "default-value")
		if result != "custom-value" {
			t.Errorf("getEnv() = %v, want custom-value", result)
		}
	})
}

func TestGetEnvBool(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_BOOL")

	t.Run("unset returns default", func(t *testing.T) {
		result := getEnvBool("TEST_GET_ENV_BOOL", true)
		if !result {
			t.Error("getEnvBool() = false, want true")
		}

		result = getEnvBool("TEST_GET_ENV_BOOL", false)
		if result {
			t.Error("getEnvBool() = true, want false")
		}
	})

	t.Run("valid bool values", func(t *testing.T) {
		tests := []struct {
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

		for _, tt := range tests {
			os.Setenv("TEST_GET_ENV_BOOL", tt.value)
			result := getEnvBool("TEST_GET_ENV_BOOL", !tt.want)
			if result != tt.want {
				t.Errorf("getEnvBool(%q) = %v, want %v", tt.value, result, tt.want)
			}
		}
		os.Unsetenv("TEST_GET_ENV_BOOL")
	})

	t.Run("invalid bool returns default", func(t *testing.T) {
		os.Setenv("TEST_GET_ENV_BOOL", "not-a-bool")
		defer os.Unsetenv("TEST_GET_ENV_BOOL")

		result := getEnvBool("TEST_GET_ENV_BOOL", true)
		if !result {
			t.Error("getEnvBool() with invalid value = false, want true (default)")
		}
	})
}

func TestConfig_Fields(t *testing.T) {
	cfg := &Config{
		ControlPlaneAddr: "addr1",
		GatewayURL:       "http://addr2",
		Format:           "yaml",
		Verbose:          true,
	}

	if cfg.ControlPlaneAddr != "addr1" {
		t.Errorf("ControlPlaneAddr = %v, want addr1", cfg.ControlPlaneAddr)
	}
	if cfg.GatewayURL != "http://addr2" {
		t.Errorf("GatewayURL = %v, want http://addr2", cfg.GatewayURL)
	}
	if cfg.Format != "yaml" {
		t.Errorf("Format = %v, want yaml", cfg.Format)
	}
	if !cfg.Verbose {
		t.Error("Verbose = false, want true")
	}
}
