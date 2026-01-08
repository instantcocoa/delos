package config

import (
	"os"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	// Clear environment for testing
	envVars := []string{
		"DELOS_OBSERVE_ADDR", "DELOS_RUNTIME_ADDR", "DELOS_PROMPT_ADDR",
		"DELOS_DATASETS_ADDR", "DELOS_EVAL_ADDR", "DELOS_DEPLOY_ADDR",
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

		if cfg.ObserveAddr != "localhost:9000" {
			t.Errorf("ObserveAddr = %v, want localhost:9000", cfg.ObserveAddr)
		}
		if cfg.RuntimeAddr != "localhost:9001" {
			t.Errorf("RuntimeAddr = %v, want localhost:9001", cfg.RuntimeAddr)
		}
		if cfg.PromptAddr != "localhost:9002" {
			t.Errorf("PromptAddr = %v, want localhost:9002", cfg.PromptAddr)
		}
		if cfg.DatasetsAddr != "localhost:9003" {
			t.Errorf("DatasetsAddr = %v, want localhost:9003", cfg.DatasetsAddr)
		}
		if cfg.EvalAddr != "localhost:9004" {
			t.Errorf("EvalAddr = %v, want localhost:9004", cfg.EvalAddr)
		}
		if cfg.DeployAddr != "localhost:9005" {
			t.Errorf("DeployAddr = %v, want localhost:9005", cfg.DeployAddr)
		}
		if cfg.Format != "table" {
			t.Errorf("Format = %v, want table", cfg.Format)
		}
		if cfg.Verbose {
			t.Error("Verbose = true, want false")
		}
	})

	t.Run("from environment", func(t *testing.T) {
		os.Setenv("DELOS_OBSERVE_ADDR", "observe.example.com:9000")
		os.Setenv("DELOS_RUNTIME_ADDR", "runtime.example.com:9001")
		os.Setenv("DELOS_PROMPT_ADDR", "prompt.example.com:9002")
		os.Setenv("DELOS_DATASETS_ADDR", "datasets.example.com:9003")
		os.Setenv("DELOS_EVAL_ADDR", "eval.example.com:9004")
		os.Setenv("DELOS_DEPLOY_ADDR", "deploy.example.com:9005")
		os.Setenv("DELOS_FORMAT", "json")
		os.Setenv("DELOS_VERBOSE", "true")

		cfg := DefaultConfig()

		if cfg.ObserveAddr != "observe.example.com:9000" {
			t.Errorf("ObserveAddr = %v, want observe.example.com:9000", cfg.ObserveAddr)
		}
		if cfg.RuntimeAddr != "runtime.example.com:9001" {
			t.Errorf("RuntimeAddr = %v, want runtime.example.com:9001", cfg.RuntimeAddr)
		}
		if cfg.PromptAddr != "prompt.example.com:9002" {
			t.Errorf("PromptAddr = %v, want prompt.example.com:9002", cfg.PromptAddr)
		}
		if cfg.DatasetsAddr != "datasets.example.com:9003" {
			t.Errorf("DatasetsAddr = %v, want datasets.example.com:9003", cfg.DatasetsAddr)
		}
		if cfg.EvalAddr != "eval.example.com:9004" {
			t.Errorf("EvalAddr = %v, want eval.example.com:9004", cfg.EvalAddr)
		}
		if cfg.DeployAddr != "deploy.example.com:9005" {
			t.Errorf("DeployAddr = %v, want deploy.example.com:9005", cfg.DeployAddr)
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
		ObserveAddr:  "addr1",
		RuntimeAddr:  "addr2",
		PromptAddr:   "addr3",
		DatasetsAddr: "addr4",
		EvalAddr:     "addr5",
		DeployAddr:   "addr6",
		Format:       "yaml",
		Verbose:      true,
	}

	if cfg.ObserveAddr != "addr1" {
		t.Errorf("ObserveAddr = %v, want addr1", cfg.ObserveAddr)
	}
	if cfg.RuntimeAddr != "addr2" {
		t.Errorf("RuntimeAddr = %v, want addr2", cfg.RuntimeAddr)
	}
	if cfg.PromptAddr != "addr3" {
		t.Errorf("PromptAddr = %v, want addr3", cfg.PromptAddr)
	}
	if cfg.DatasetsAddr != "addr4" {
		t.Errorf("DatasetsAddr = %v, want addr4", cfg.DatasetsAddr)
	}
	if cfg.EvalAddr != "addr5" {
		t.Errorf("EvalAddr = %v, want addr5", cfg.EvalAddr)
	}
	if cfg.DeployAddr != "addr6" {
		t.Errorf("DeployAddr = %v, want addr6", cfg.DeployAddr)
	}
	if cfg.Format != "yaml" {
		t.Errorf("Format = %v, want yaml", cfg.Format)
	}
	if !cfg.Verbose {
		t.Error("Verbose = false, want true")
	}
}
