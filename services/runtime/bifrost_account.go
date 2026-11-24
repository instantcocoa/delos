package runtime

import (
	"context"
	"os"
	"strings"

	"github.com/maximhq/bifrost/core/schemas"
)

// bifrostAccount implements schemas.Account with provider credentials read
// exclusively from the environment, mirroring delos-gateway's own provider
// registration. Secrets stay environment-only: nothing here reads a config file
// and nothing is ever serialised with a key value in it.
//
// Base-URL overrides exist so the conformance/agent suites can point the
// gateway at tests/compat/mockupstream; each mirrors a provider's real default
// when unset.
type bifrostAccount struct{}

func (bifrostAccount) GetConfiguredProviders() ([]schemas.ModelProvider, error) {
	var providers []schemas.ModelProvider
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		providers = append(providers, schemas.Anthropic)
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		providers = append(providers, schemas.OpenAI)
	}
	if env("GEMINI_API_KEY", "GOOGLE_API_KEY") != "" {
		providers = append(providers, schemas.Gemini)
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_PROFILE") != "" {
		providers = append(providers, schemas.Bedrock)
	}
	if os.Getenv("DELOS_RUNTIME_OLLAMA_URL") != "" || strings.EqualFold(os.Getenv("DELOS_RUNTIME_OLLAMA_ENABLED"), "true") {
		providers = append(providers, schemas.Ollama)
	}
	return providers, nil
}

// env returns the first set environment variable among names. Standard provider
// variable names (GEMINI_API_KEY) take priority; GOOGLE_API_KEY is accepted as a
// fallback.
func env(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

func (bifrostAccount) GetKeysForProvider(_ context.Context, providerKey schemas.ModelProvider) ([]schemas.Key, error) {
	var value string
	var k schemas.Key
	switch providerKey {
	case schemas.Anthropic:
		value = os.Getenv("ANTHROPIC_API_KEY")
	case schemas.OpenAI:
		value = os.Getenv("OPENAI_API_KEY")
	case schemas.Gemini:
		value = env("GEMINI_API_KEY", "GOOGLE_API_KEY")
	case schemas.Bedrock:
		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
		}
		k.BedrockKeyConfig = &schemas.BedrockKeyConfig{
			AccessKey:    *schemas.NewSecretVar(os.Getenv("AWS_ACCESS_KEY_ID")),
			SecretKey:    *schemas.NewSecretVar(os.Getenv("AWS_SECRET_ACCESS_KEY")),
			SessionToken: schemas.NewSecretVar(os.Getenv("AWS_SESSION_TOKEN")),
			Region:       schemas.NewSecretVar(region),
		}
	case schemas.Ollama:
		url := os.Getenv("DELOS_RUNTIME_OLLAMA_URL")
		if url == "" {
			url = "http://localhost:11434"
		}
		k.OllamaKeyConfig = &schemas.OllamaKeyConfig{URL: *schemas.NewSecretVar(strings.TrimSuffix(url, "/"))}
	}
	if value == "" && providerKey != schemas.Bedrock && providerKey != schemas.Ollama {
		return nil, nil
	}
	if value != "" {
		k.Value = *schemas.NewSecretVar(value)
	}
	if k.ID == "" {
		k.ID = string(providerKey) + "-env"
	}
	if k.Name == "" {
		k.Name = string(providerKey) + " env key"
	}
	k.Models = schemas.WhiteList{"*"}
	if k.Weight == 0 {
		k.Weight = 1
	}
	return []schemas.Key{k}, nil
}

// baseURLFor returns the provider's effective base URL, honouring the mock-
// upstream overrides the D0 spike established (kept for DL-T's suites).
func baseURLFor(providerKey schemas.ModelProvider) string {
	switch providerKey {
	case schemas.Anthropic:
		if u := os.Getenv("DELOS_BIFROST_ANTHROPIC_BASE_URL"); u != "" {
			return strings.TrimSuffix(u, "/")
		}
		return "https://api.anthropic.com"
	case schemas.OpenAI:
		if u := os.Getenv("DELOS_BIFROST_OPENAI_BASE_URL"); u != "" {
			return strings.TrimSuffix(u, "/")
		}
		return "https://api.openai.com"
	case schemas.Gemini:
		if u := os.Getenv("DELOS_BIFROST_GEMINI_BASE_URL"); u != "" {
			return strings.TrimSuffix(u, "/")
		}
		return "https://generativelanguage.googleapis.com"
	case schemas.Ollama:
		url := os.Getenv("DELOS_RUNTIME_OLLAMA_URL")
		if url == "" {
			url = "http://localhost:11434"
		}
		return strings.TrimSuffix(url, "/")
	}
	return ""
}

func (bifrostAccount) GetConfigForProvider(providerKey schemas.ModelProvider) (*schemas.ProviderConfig, error) {
	nc := schemas.NetworkConfig{BaseURL: baseURLFor(providerKey)}
	if providerKey == schemas.Anthropic {
		// User-scoped keys (sk-ant-usr-…) are not bound to a workspace, so
		// api.anthropic.com requires the anthropic-workspace-id header on every
		// call. Workspace-scoped keys don't need it; the header is harmless and
		// only added when the operator sets it.
		if ws := os.Getenv("DELOS_BIFROST_ANTHROPIC_WORKSPACE_ID"); ws != "" {
			nc.ExtraHeaders = map[string]schemas.SecretVar{
				"anthropic-workspace-id": *schemas.NewSecretVar(ws),
			}
		}
	}
	cfg := &schemas.ProviderConfig{NetworkConfig: nc}
	cfg.CheckAndSetDefaults()
	return cfg, nil
}

// compile-time assertion that bifrostAccount satisfies the Bifrost Account
// interface this gateway is built against.
var _ schemas.Account = bifrostAccount{}
