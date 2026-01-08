package runtime

import (
	"context"
	"fmt"
	"log/slog"
)

// RuntimeService handles LLM completion requests.
type RuntimeService struct {
	registry *Registry
	logger   *slog.Logger
}

// NewRuntimeService creates a new runtime service.
func NewRuntimeService(registry *Registry, logger *slog.Logger) *RuntimeService {
	return &RuntimeService{
		registry: registry,
		logger:   logger.With("component", "service"),
	}
}

// Complete performs a completion request.
func (s *RuntimeService) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	p, err := s.selectProvider(ctx, params)
	if err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "completing request",
		"provider", p.Name(),
		"model", params.Model,
		"messages", len(params.Messages),
	)

	result, err := p.Complete(ctx, params)
	if err != nil {
		s.logger.ErrorContext(ctx, "completion failed",
			"provider", p.Name(),
			"error", err,
		)
		return nil, fmt.Errorf("completion failed: %w", err)
	}

	s.logger.InfoContext(ctx, "completion succeeded",
		"provider", result.Provider,
		"model", result.Model,
		"tokens", result.Usage.TotalTokens,
		"cost_usd", result.Usage.CostUSD,
	)

	return result, nil
}

// CompleteStream performs a streaming completion request.
func (s *RuntimeService) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	p, err := s.selectProvider(ctx, params)
	if err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "starting stream",
		"provider", p.Name(),
		"model", params.Model,
	)

	return p.CompleteStream(ctx, params)
}

// Embed generates embeddings.
func (s *RuntimeService) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	var p Provider

	if params.Provider != "" {
		p, _ = s.registry.Get(params.Provider)
		if p == nil {
			return nil, fmt.Errorf("provider not found: %s", params.Provider)
		}
	} else {
		// Default to OpenAI for embeddings
		p, _ = s.registry.Get("openai")
		if p == nil {
			return nil, fmt.Errorf("no embedding provider available")
		}
	}

	s.logger.InfoContext(ctx, "generating embeddings",
		"provider", p.Name(),
		"texts", len(params.Texts),
	)

	return p.Embed(ctx, params)
}

// ListProviders returns all available providers.
func (s *RuntimeService) ListProviders(ctx context.Context) []ProviderInfo {
	providers := s.registry.List()
	result := make([]ProviderInfo, 0, len(providers))

	for _, p := range providers {
		result = append(result, ProviderInfo{
			Name:            p.Name(),
			Models:          p.Models(),
			Available:       p.Available(ctx),
			CostPer1KTokens: p.CostPer1KTokens(),
		})
	}

	return result
}

func (s *RuntimeService) selectProvider(ctx context.Context, params CompletionParams) (Provider, error) {
	// If specific provider requested
	if params.Routing == RoutingSpecificProvider || params.Provider != "" {
		providerName := params.Provider
		if providerName == "" {
			return nil, fmt.Errorf("provider name required for specific provider routing")
		}
		p, ok := s.registry.Get(providerName)
		if !ok {
			return nil, fmt.Errorf("provider not found: %s", providerName)
		}
		if !p.Available(ctx) {
			return nil, fmt.Errorf("provider not available: %s", providerName)
		}
		return p, nil
	}

	// Get available providers
	available := s.registry.Available(ctx)
	if len(available) == 0 {
		return nil, fmt.Errorf("no providers available")
	}

	// Select based on routing strategy
	switch params.Routing {
	case RoutingCostOptimized:
		return s.selectCheapest(available, params.Model), nil
	case RoutingLatencyOptimized:
		// For now, just return first available
		// TODO: Implement latency tracking
		return available[0], nil
	case RoutingQualityOptimized:
		// Prefer Anthropic Claude or GPT-4
		return s.selectHighestQuality(available), nil
	default:
		// Default: cost optimized
		return s.selectCheapest(available, params.Model), nil
	}
}

func (s *RuntimeService) selectCheapest(providers []Provider, model string) Provider {
	var cheapest Provider
	var lowestCost float64 = -1

	for _, p := range providers {
		costs := p.CostPer1KTokens()

		// If model specified, check if provider supports it
		if model != "" {
			if cost, ok := costs[model]; ok {
				if lowestCost < 0 || cost < lowestCost {
					lowestCost = cost
					cheapest = p
				}
			}
		} else {
			// Find cheapest model from this provider
			for _, cost := range costs {
				if lowestCost < 0 || cost < lowestCost {
					lowestCost = cost
					cheapest = p
				}
			}
		}
	}

	if cheapest == nil && len(providers) > 0 {
		return providers[0]
	}
	return cheapest
}

func (s *RuntimeService) selectHighestQuality(providers []Provider) Provider {
	// Prefer Anthropic, then OpenAI
	for _, p := range providers {
		if p.Name() == "anthropic" {
			return p
		}
	}
	for _, p := range providers {
		if p.Name() == "openai" {
			return p
		}
	}
	if len(providers) > 0 {
		return providers[0]
	}
	return nil
}
