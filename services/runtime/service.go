package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// RuntimeService is the gateway's core: it resolves models to providers and
// executes requests. Virtual keys, caching, and failover chains hook in here
// rather than in the HTTP surfaces or the providers.
type RuntimeService struct {
	registry *Registry
	logger   *slog.Logger

	routes     map[string][]RouteTarget
	breakersMu sync.Mutex
	breakers   map[string]*breaker

	cache    ResponseCache
	cacheTTL time.Duration

	tracer *GatewayTracer // nil = no span emission; see telemetry.go
}

// SetCache enables exact-match response caching with the given TTL.
func (s *RuntimeService) SetCache(c ResponseCache, ttl time.Duration) {
	s.cache = c
	s.cacheTTL = ttl
}

// NewRuntimeService creates a new runtime service.
func NewRuntimeService(registry *Registry, logger *slog.Logger) *RuntimeService {
	return &RuntimeService{
		registry: registry,
		logger:   logger.With("component", "service"),
	}
}

// Registry exposes the provider registry for surfaces and wiring.
func (s *RuntimeService) Registry() *Registry { return s.registry }

// Complete performs a completion request against a resolved provider.
func (s *RuntimeService) Complete(ctx context.Context, p Provider, params CompletionParams) (*CompletionResult, error) {
	s.logger.InfoContext(ctx, "completing request",
		"provider", p.Name(),
		"model", params.Model,
		"messages", len(params.Messages),
		"tools", len(params.Tools),
	)

	result, err := p.Complete(ctx, params)
	if err != nil {
		s.logger.ErrorContext(ctx, "completion failed",
			"provider", p.Name(),
			"model", params.Model,
			"error", err,
		)
		return nil, err
	}

	s.logger.InfoContext(ctx, "completion succeeded",
		"provider", result.Provider,
		"model", result.Model,
		"finish", result.FinishReason,
		"tokens", result.Usage.TotalTokens,
		"cost_usd", result.Usage.CostUSD,
	)
	return result, nil
}

// CompleteStream performs a streaming completion request.
func (s *RuntimeService) CompleteStream(ctx context.Context, p Provider, params CompletionParams) (<-chan StreamChunk, error) {
	s.logger.InfoContext(ctx, "starting stream",
		"provider", p.Name(),
		"model", params.Model,
	)
	return p.CompleteStream(ctx, params)
}

// Embed generates embeddings against a resolved provider.
func (s *RuntimeService) Embed(ctx context.Context, p Provider, params EmbedParams) (*EmbedResult, error) {
	s.logger.InfoContext(ctx, "generating embeddings",
		"provider", p.Name(),
		"texts", len(params.Texts),
	)
	return p.Embed(ctx, params)
}

// ResolveProvider finds the provider serving the requested model and returns
// the provider plus the model name with any provider prefix stripped. An
// explicit "provider/model" prefix wins, then an exact model listing, then
// well-known model name prefixes.
func (s *RuntimeService) ResolveProvider(ctx context.Context, model string) (Provider, string, error) {
	if name, rest, ok := cutProviderPrefix(model); ok {
		if p, found := s.registry.Get(name); found {
			return p, rest, nil
		}
	}
	for _, p := range s.registry.List() {
		for _, m := range p.Models(ctx) {
			if m == model {
				return p, model, nil
			}
		}
	}
	if name := wellKnownProvider(model); name != "" {
		if p, found := s.registry.Get(name); found {
			return p, model, nil
		}
	}
	return nil, "", fmt.Errorf("model %q is not served by any configured provider", model)
}

// ListProviders returns all registered providers with their models.
func (s *RuntimeService) ListProviders(ctx context.Context) []ProviderInfo {
	providers := s.registry.List()
	result := make([]ProviderInfo, 0, len(providers))
	for _, p := range providers {
		result = append(result, ProviderInfo{
			Name:   p.Name(),
			Models: p.Models(ctx),
		})
	}
	return result
}

// cutProviderPrefix splits "provider/model" if the prefix names a provider.
func cutProviderPrefix(model string) (provider, rest string, ok bool) {
	for i := 0; i < len(model); i++ {
		if model[i] == '/' {
			return model[:i], model[i+1:], true
		}
	}
	return "", "", false
}

// wellKnownProvider maps well-known model name prefixes to provider names.
func wellKnownProvider(model string) string {
	switch {
	case hasAnyPrefix(model, "gpt-", "o1", "o3", "o4", "text-embedding-", "chatgpt-"):
		return "openai"
	case hasAnyPrefix(model, "claude-"):
		return "anthropic"
	case hasAnyPrefix(model, "gemini-", "text-embedding-004"):
		return "gemini"
	case hasAnyPrefix(model, "anthropic.", "amazon.", "meta.", "us.", "eu."):
		return "bedrock"
	}
	return ""
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}
