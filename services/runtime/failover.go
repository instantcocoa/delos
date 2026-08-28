package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"sync"
	"syscall"
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

	// lastErr is the most recent failure that counted against this
	// breaker. While the breaker is open no provider is contacted, so it
	// is the only remaining explanation the caller can be given for why
	// its request was refused.
	lastErr error
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
	b.lastErr = nil
}

func (b *breaker) failure(now time.Time, cause error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.probing = false
	if cause != nil {
		b.lastErr = cause
	}
	if b.failures >= breakerThreshold {
		b.openUntil = now.Add(breakerCooldown)
	}
}

// lastFailure returns the failure that most recently counted against the
// breaker, if any.
func (b *breaker) lastFailure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastErr
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

// retryable reports whether an error is worth another attempt on the same
// target or a failover to the next one.
//
// The policy is an allowlist: an error is retried only when it is positively
// identified as transient. Everything else is final. That matters most for
// failures raised before the request ever left this process - a malformed
// tool_result, an unsupported content part, a response_format the backend
// cannot express. Those fail identically on every attempt and every fallback,
// so retrying them only spends latency and money, and each attempt counts
// against the provider's circuit breaker, letting one bad client request
// degrade that provider for every other tenant.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	// A cancelled or timed-out caller is final: the client is already gone,
	// and failing over would start a fresh paid request nobody will read.
	// This check comes first so it also catches a context error wrapped
	// inside a ProviderError.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// An upstream answered: its status (and quota code) decides.
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	// Transport-level failures - connection refused or reset, TLS handshake,
	// dial timeouts - surface as net.Error (*url.Error implements it), and a
	// truncated body is a broken connection by another name.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Anything else is a translation, validation, or programming failure:
	// deterministic, so there is nothing to gain from trying again.
	return false
}

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
//
// An absent temperature is NOT deterministic. Both the OpenAI and Anthropic
// APIs default it to 1.0, so treating "unset" as 0 made every plain request
// - the overwhelming majority of traffic - cacheable, and two identical
// prompts came back byte-identical. Only an explicit temperature of 0
// qualifies.
func cacheable(params CompletionParams) bool {
	return params.Temperature != nil && *params.Temperature == 0
}

// breakerOpenError explains a request refused because a provider's circuit
// breaker is open. The breaker exists to stop hammering a broken provider,
// but "circuit breaker open" on its own erases the only useful thing the
// caller could learn - that their key is out of credit, say - so the last
// real upstream failure is carried through, status, code and all.
func breakerOpenError(provider string, last error) error {
	const reason = "circuit breaker open after repeated failures"
	if pe, ok := errAs[*ProviderError](last); ok {
		return &ProviderError{
			Provider:   provider,
			StatusCode: pe.StatusCode,
			Code:       pe.Code,
			Message:    reason + "; last upstream error: " + pe.Message,
			Wrapped:    last,
		}
	}
	if last != nil {
		return fmt.Errorf("provider %s: %s; last error: %w", provider, reason, last)
	}
	return fmt.Errorf("provider %s: circuit breaker open", provider)
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
			hit := cachedResult(cached)
			s.logger.InfoContext(ctx, "cache hit", "model", params.Model)
			return hit, nil
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
			lastErr = breakerOpenError(target.Provider, b.lastFailure())
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
			b.failure(time.Now(), err)
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
			lastErr = breakerOpenError(target.Provider, b.lastFailure())
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
		b.failure(time.Now(), err)
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

		// send delivers a chunk unless the caller has gone away. Every send
		// in this goroutine must go through it: an unguarded send blocks
		// forever once the consumer stops reading, stranding this goroutine,
		// the provider's reader, and its upstream connection.
		send := func(chunk StreamChunk) bool {
			select {
			case out <- chunk:
				return true
			case <-ctx.Done():
				return false
			}
		}

		var lastErr error
		for i, target := range chain {
			p, ok := s.registry.Get(target.Provider)
			if !ok {
				lastErr = fmt.Errorf("provider %q not configured", target.Provider)
				continue
			}
			b := s.breakerFor(target.Provider)
			if !b.allow(time.Now()) {
				lastErr = breakerOpenError(target.Provider, b.lastFailure())
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
					send(StreamChunk{Err: err, Provider: target.Provider, Model: target.Model})
					return
				}
				b.failure(time.Now(), err)
				s.logger.WarnContext(ctx, "stream start failed, failing over",
					"target", target.String(), "error", err)
				continue
			}

			failed := false
			// Once any content is on the wire we are committed to this
			// target: bytes already flushed to the client cannot be
			// retracted, so restarting a different provider here would
			// splice two different completions into one response. After
			// that point a break is surfaced as an error, not failed over.
			committed := false
			for chunk := range chunks {
				if chunk.Err != nil {
					lastErr = chunk.Err
					failed = true
					b.failure(time.Now(), chunk.Err)
					canFailover := i+1 < len(chain) && !committed
					s.logger.WarnContext(ctx, "stream broke mid-flight",
						"target", target.String(), "error", chunk.Err,
						"failover", canFailover, "committed", committed)
					if !canFailover {
						if !send(chunk) {
							return
						}
						return
					}
					break
				}
				if !send(chunk) {
					return
				}
				if chunk.Delta != "" || chunk.ToolCall != nil {
					committed = true
				}
				if chunk.Done {
					b.success()
					return
				}
			}
			if !failed {
				// Channel closed without Done or Err: treat as a broken
				// stream and fail over.
				lastErr = fmt.Errorf("provider %s: stream ended without completing", target.Provider)
				b.failure(time.Now(), lastErr)
			}
		}
		if lastErr == nil {
			lastErr = fmt.Errorf("no targets available")
		}
		send(StreamChunk{Err: lastErr})
	}()
	return out, nil
}
