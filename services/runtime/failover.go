package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

// Routing: a model alias maps to an ordered fallback chain of
// provider/model targets. Requests walk the chain: retryable failures
// (429/5xx/network) retry with jittered backoff, then fall through to the
// next target. A circuit breaker per provider skips targets that are
// currently failing hard.

// RouteTarget is one candidate in a fallback chain.
type RouteTarget struct {
	Provider string
	Model    string
}

func (t RouteTarget) String() string { return t.Provider + "/" + t.Model }

// ParseRoutes parses the DELOS_ROUTES JSON:
//
//	{"gpt-4": ["openai/gpt-4o", "anthropic/claude-sonnet-4-5"]}
func ParseRoutes(raw string) (map[string][]RouteTarget, error) {
	if raw == "" {
		return nil, nil
	}
	var routes map[string][]string
	if err := json.Unmarshal([]byte(raw), &routes); err != nil {
		return nil, fmt.Errorf("invalid routes JSON: %w", err)
	}
	return RoutesFromMap(routes)
}

// RoutesFromMap builds the alias table from an already-decoded mapping, as it
// appears under gateway.routes in delos.yaml.
func RoutesFromMap(routes map[string][]string) (map[string][]RouteTarget, error) {
	if len(routes) == 0 {
		return nil, nil
	}
	out := make(map[string][]RouteTarget, len(routes))
	for alias, targets := range routes {
		if len(targets) == 0 {
			return nil, fmt.Errorf("route %q has no targets", alias)
		}
		for _, target := range targets {
			provider, model, ok := cutProviderPrefix(target)
			if !ok {
				return nil, fmt.Errorf("route %q: target %q must be provider/model", alias, target)
			}
			out[alias] = append(out[alias], RouteTarget{Provider: provider, Model: model})
		}
	}
	return out, nil
}

// SetRoutes installs the alias table.
func (s *RuntimeService) SetRoutes(routes map[string][]RouteTarget) {
	s.routes = routes
}

// ResolveChain resolves a requested model to its fallback chain. An alias
// expands to its configured targets; anything else resolves to a single
// target via ResolveProvider.
func (s *RuntimeService) ResolveChain(ctx context.Context, model string) ([]RouteTarget, error) {
	if targets, ok := s.routes[model]; ok {
		return targets, nil
	}
	p, resolved, err := s.ResolveProvider(ctx, model)
	if err != nil {
		return nil, err
	}
	return []RouteTarget{{Provider: p.Name(), Model: resolved}}, nil
}

// ---- circuit breaker ----

const (
	breakerThreshold = 5                // consecutive failures to open
	breakerCooldown  = 30 * time.Second // open duration before a probe
)

type breaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	probing   bool
}

// allow reports whether a request may proceed. In the open state one probe
// request is let through after the cooldown (half-open).
func (b *breaker) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < breakerThreshold {
		return true
	}
	if now.Before(b.openUntil) {
		return false
	}
	if b.probing {
		return false
	}
	b.probing = true
	return true
}

func (b *breaker) success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.probing = false
}

func (b *breaker) failure(now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.probing = false
	if b.failures >= breakerThreshold {
		b.openUntil = now.Add(breakerCooldown)
	}
}

func (s *RuntimeService) breakerFor(provider string) *breaker {
	s.breakersMu.Lock()
	defer s.breakersMu.Unlock()
	if s.breakers == nil {
		s.breakers = make(map[string]*breaker)
	}
	b, ok := s.breakers[provider]
	if !ok {
		b = &breaker{}
		s.breakers[provider] = b
	}
	return b
}

// BreakerState reports a provider's breaker state for observability
// ("closed", "open", "half-open").
func (s *RuntimeService) BreakerState(provider string) string {
	b := s.breakerFor(provider)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failures < breakerThreshold {
		return "closed"
	}
	if time.Now().Before(b.openUntil) {
		return "open"
	}
	return "half-open"
}

// ---- retry policy ----

const (
	attemptsPerTarget = 2
	baseBackoff       = 200 * time.Millisecond
	maxBackoff        = 2 * time.Second
)

// retryable reports whether an error is worth retrying or failing over.
func retryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// Connection-level failures come through url.Error and friends; treat
	// any non-ProviderError transport failure as retryable, but a cancelled
	// or timed-out caller context is final.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return !errors.Is(err, errNotRetryable)
}

var errNotRetryable = errors.New("not retryable")

// backoffDelay computes jittered exponential backoff for attempt n (0-based).
func backoffDelay(attempt int) time.Duration {
	d := baseBackoff << attempt
	if d > maxBackoff {
		d = maxBackoff
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ---- failover execution ----

// cacheable reports whether a request may be served from / stored in the
// response cache: only deterministic requests qualify.
func cacheable(params CompletionParams) bool {
	return params.Temperature == nil || *params.Temperature == 0
}

// CompleteChain runs a completion against a fallback chain.
func (s *RuntimeService) CompleteChain(ctx context.Context, chain []RouteTarget, params CompletionParams) (result *CompletionResult, err error) {
	tried := 0
	ctx, rspan := s.startSpan(ctx, "chat", params)
	defer func() { rspan.SetAttempts(tried); rspan.End(result, err) }()

	var cacheKey string
	if s.cache != nil && cacheable(params) {
		cacheKey = CacheKey(params)
		if cached, ok := s.cache.Get(ctx, cacheKey); ok && cacheKey != "" {
			hit := *cached
			hit.Cached = true
			s.logger.InfoContext(ctx, "cache hit", "model", params.Model)
			return &hit, nil
		}
	}

	var lastErr error
	for _, target := range chain {
		p, ok := s.registry.Get(target.Provider)
		if !ok {
			lastErr = fmt.Errorf("provider %q not configured", target.Provider)
			continue
		}
		b := s.breakerFor(target.Provider)
		if !b.allow(time.Now()) {
			s.logger.WarnContext(ctx, "circuit open, skipping target", "target", target.String())
			lastErr = fmt.Errorf("provider %s: circuit breaker open", target.Provider)
			continue
		}
		tried++

		targetParams := params
		targetParams.Provider = target.Provider
		targetParams.Model = target.Model

		for attempt := 0; attempt < attemptsPerTarget; attempt++ {
			result, err := s.Complete(ctx, p, targetParams)
			if err == nil {
				b.success()
				if s.cache != nil && cacheKey != "" {
					s.cache.Set(ctx, cacheKey, result, s.cacheTTL)
				}
				return result, nil
			}
			lastErr = err
			if !retryable(err) {
				b.success() // a 4xx is not the provider's unhealthiness
				return nil, err
			}
			b.failure(time.Now())
			if attempt+1 < attemptsPerTarget {
				if !b.allow(time.Now()) {
					break
				}
				if err := sleepCtx(ctx, backoffDelay(attempt)); err != nil {
					return nil, lastErr
				}
			}
		}
		s.logger.WarnContext(ctx, "target exhausted, failing over",
			"target", target.String(), "error", lastErr)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no targets available")
	}
	if tried == 0 {
		return nil, fmt.Errorf("all providers unavailable: %w", lastErr)
	}
	return nil, lastErr
}

// EmbedChain runs an embedding request against a fallback chain.
func (s *RuntimeService) EmbedChain(ctx context.Context, chain []RouteTarget, params EmbedParams) (result *EmbedResult, err error) {
	ctx, rspan := s.startSpan(ctx, "embeddings", CompletionParams{Model: params.Model, Provider: params.Provider})
	defer func() { rspan.EndEmbed(result, err) }()

	var lastErr error
	for _, target := range chain {
		p, ok := s.registry.Get(target.Provider)
		if !ok {
			lastErr = fmt.Errorf("provider %q not configured", target.Provider)
			continue
		}
		b := s.breakerFor(target.Provider)
		if !b.allow(time.Now()) {
			lastErr = fmt.Errorf("provider %s: circuit breaker open", target.Provider)
			continue
		}
		targetParams := params
		targetParams.Provider = target.Provider
		targetParams.Model = target.Model
		result, err := s.Embed(ctx, p, targetParams)
		if err == nil {
			b.success()
			return result, nil
		}
		lastErr = err
		if !retryable(err) {
			b.success()
			return nil, err
		}
		b.failure(time.Now())
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no targets available")
	}
	return nil, lastErr
}

// completeStreamChain streams a completion with failover. If a target's
// stream fails - even mid-stream - the next target is started and its stream
// is forwarded from its beginning; the client always receives a validly
// terminated stream when any target can complete one. Only after every
// target has failed does the consumer see an Err chunk.
func (s *RuntimeService) completeStreamChain(ctx context.Context, chain []RouteTarget, params CompletionParams) (<-chan StreamChunk, error) {
	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		var lastErr error
		for i, target := range chain {
			p, ok := s.registry.Get(target.Provider)
			if !ok {
				lastErr = fmt.Errorf("provider %q not configured", target.Provider)
				continue
			}
			b := s.breakerFor(target.Provider)
			if !b.allow(time.Now()) {
				lastErr = fmt.Errorf("provider %s: circuit breaker open", target.Provider)
				continue
			}

			targetParams := params
			targetParams.Provider = target.Provider
			targetParams.Model = target.Model

			chunks, err := s.CompleteStream(ctx, p, targetParams)
			if err != nil {
				lastErr = err
				if !retryable(err) {
					b.success()
					out <- StreamChunk{Err: err, Provider: target.Provider, Model: target.Model}
					return
				}
				b.failure(time.Now())
				s.logger.WarnContext(ctx, "stream start failed, failing over",
					"target", target.String(), "error", err)
				continue
			}

			failed := false
			for chunk := range chunks {
				if chunk.Err != nil {
					lastErr = chunk.Err
					failed = true
					b.failure(time.Now())
					hasNext := i+1 < len(chain)
					s.logger.WarnContext(ctx, "stream broke mid-flight",
						"target", target.String(), "error", chunk.Err, "failover", hasNext)
					if !hasNext {
						out <- chunk
						return
					}
					break
				}
				out <- chunk
				if chunk.Done {
					b.success()
					return
				}
			}
			if !failed {
				// Channel closed without Done or Err: treat as a broken
				// stream and fail over.
				lastErr = fmt.Errorf("provider %s: stream ended without completing", target.Provider)
				b.failure(time.Now())
			}
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no targets available")
		}
		out <- StreamChunk{Err: lastErr}
	}()
	return out, nil
}
