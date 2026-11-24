package runtime

import (
	"net/http"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
)

// Tests for the Bifrost-backed gateway path (the default build). These run
// without any provider credentials or network access: the Bifrost core is
// initialised against an account that reports no configured providers.

func TestProviderForModel(t *testing.T) {
	cases := []struct {
		model, path string
		want        schemas.ModelProvider
	}{
		{"claude-sonnet-4-5", "/v1/messages", schemas.Anthropic},
		{"gpt-4o", "/v1/messages", ""}, // cross-provider
		{"gpt-4o", "/v1/chat/completions", schemas.OpenAI},
		{"claude-sonnet-4-5", "/v1/chat/completions", ""},
		{"text-embedding-3-small", "/v1/embeddings", schemas.OpenAI},
		{"gpt-5", "/v1/responses", schemas.OpenAI},
	}
	for _, c := range cases {
		if got := providerForModel(c.model, c.path); got != c.want {
			t.Errorf("providerForModel(%q, %q) = %q, want %q", c.model, c.path, got, c.want)
		}
	}
}

func TestResolveProviderForModel(t *testing.T) {
	p, m := resolveProviderForModel("openai/gpt-4o")
	if p != schemas.OpenAI || m != "gpt-4o" {
		t.Errorf("resolveProviderForModel(openai/gpt-4o) = (%q, %q)", p, m)
	}
	p, m = resolveProviderForModel("claude-sonnet-4-5")
	if p != schemas.Anthropic || m != "claude-sonnet-4-5" {
		t.Errorf("resolveProviderForModel(claude-sonnet-4-5) = (%q, %q)", p, m)
	}
}

func TestBifrostAccountKeysFromEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("DELOS_RUNTIME_OLLAMA_URL", "")
	t.Setenv("DELOS_RUNTIME_OLLAMA_ENABLED", "")

	acct := bifrostAccount{}
	providers, err := acct.GetConfiguredProviders()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range providers {
		if p == schemas.Anthropic {
			found = true
		}
	}
	if !found {
		t.Fatalf("Anthropic not in configured providers: %v", providers)
	}

	keys, err := acct.GetKeysForProvider(t.Context(), schemas.Anthropic)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Value.GetValue() != "sk-ant-test" {
		t.Fatalf("unexpected keys: %+v", keys)
	}
	if keys[0].Value.IsRedacted() {
		t.Error("a key read straight from env must not be redacted")
	}
}

func TestBifrostHTTPServerHealthzAndModels(t *testing.T) {
	// No provider keys configured: the Bifrost core comes up with an empty
	// account and /v1/models returns an empty list without any provider call.
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("AWS_ACCESS_KEY_ID", "")
	t.Setenv("AWS_PROFILE", "")
	t.Setenv("DELOS_RUNTIME_OLLAMA_URL", "")
	t.Setenv("DELOS_RUNTIME_OLLAMA_ENABLED", "")
	t.Setenv("DELOS_OTLP_ENDPOINT", "")

	srv := NewBifrostHTTPServer(newTestService(), newTestLogger())

	rec, _ := doJSON(t, srv, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d", rec.Code)
	}

	rec, out := doJSON(t, srv, http.MethodGet, "/v1/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("models status = %d: %s", rec.Code, rec.Body.String())
	}
	if out["object"] != "list" {
		t.Errorf("models object = %v", out["object"])
	}
}
