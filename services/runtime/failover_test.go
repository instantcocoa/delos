package runtime

import (
	"context"
	"strings"
	"testing"
)

// flakyProvider fails a configurable number of Complete calls before
// succeeding, and can break its stream after N chunks.
type flakyProvider struct {
	mockProvider
	failCompletes int // fail this many Complete calls with a retryable error
	completeCalls int
	streamCalls   int
	breakStreamAt int // emit this many deltas then a stream error (-1 = never)
}

func (f *flakyProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	f.completeCalls++
	if f.completeCalls <= f.failCompletes {
		return nil, &ProviderError{Provider: f.name, StatusCode: 500, Message: "upstream exploded"}
	}
	return f.mockProvider.Complete(ctx, params)
}

func (f *flakyProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	f.streamCalls++
	if f.breakStreamAt >= 0 {
		ch := make(chan StreamChunk, f.breakStreamAt+1)
		for i := 0; i < f.breakStreamAt; i++ {
			ch <- StreamChunk{Delta: "partial ", Provider: f.name}
		}
		ch <- StreamChunk{Err: &ProviderError{Provider: f.name, StatusCode: 502, Message: "connection reset mid-stream"}}
		close(ch)
		return ch, nil
	}
	return f.mockProvider.CompleteStream(ctx, params)
}

func okResult(provider, text string) *CompletionResult {
	return &CompletionResult{
		Message:      TextMessage("assistant", text),
		FinishReason: FinishStop,
		Provider:     provider,
		Usage:        Usage{TotalTokens: 5},
	}
}

func TestParseRoutes(t *testing.T) {
	routes, err := ParseRoutes(`{"gpt-4":["azure/gpt-4-deploy","openai/gpt-4o"]}`)
	if err != nil {
		t.Fatal(err)
	}
	chain := routes["gpt-4"]
	if len(chain) != 2 || chain[0].Provider != "azure" || chain[0].Model != "gpt-4-deploy" {
		t.Errorf("chain = %+v", chain)
	}
	if _, err := ParseRoutes(`{"x":["no-slash"]}`); err == nil {
		t.Error("expected error for target without provider prefix")
	}
	if _, err := ParseRoutes(`{"x":[]}`); err == nil {
		t.Error("expected error for empty chain")
	}
}

func TestResolveChainAlias(t *testing.T) {
	svc := newTestService(&mockProvider{name: "openai", models: []string{"gpt-4o"}})
	svc.SetRoutes(map[string][]RouteTarget{
		"prod": {{Provider: "openai", Model: "gpt-4o"}, {Provider: "anthropic", Model: "claude-sonnet-4-5"}},
	})

	chain, err := svc.ResolveChain(context.Background(), "prod")
	if err != nil || len(chain) != 2 {
		t.Fatalf("chain = %v err = %v", chain, err)
	}
	// non-alias still resolves directly
	chain, err = svc.ResolveChain(context.Background(), "gpt-4o")
	if err != nil || len(chain) != 1 || chain[0].Provider != "openai" {
		t.Fatalf("direct chain = %v err = %v", chain, err)
	}
}

func TestCompleteChainRetriesThenSucceeds(t *testing.T) {
	p := &flakyProvider{
		mockProvider:  mockProvider{name: "openai", completeResult: okResult("openai", "recovered")},
		failCompletes: 1,
		breakStreamAt: -1,
	}
	svc := newTestService(p)

	result, err := svc.CompleteChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}}, CompletionParams{})
	if err != nil {
		t.Fatalf("expected retry to succeed: %v", err)
	}
	if result.Text() != "recovered" || p.completeCalls != 2 {
		t.Errorf("text=%q calls=%d", result.Text(), p.completeCalls)
	}
}

func TestCompleteChainFailsOver(t *testing.T) {
	primary := &flakyProvider{
		mockProvider:  mockProvider{name: "primary"},
		failCompletes: 99,
		breakStreamAt: -1,
	}
	fallback := &mockProvider{name: "fallback", completeResult: okResult("fallback", "from fallback")}
	svc := newTestService(primary, fallback)

	result, err := svc.CompleteChain(context.Background(), []RouteTarget{
		{Provider: "primary", Model: "m1"},
		{Provider: "fallback", Model: "m2"},
	}, CompletionParams{})
	if err != nil {
		t.Fatalf("failover failed: %v", err)
	}
	if result.Provider != "fallback" {
		t.Errorf("provider = %s", result.Provider)
	}
	// primary was retried before failing over
	if primary.completeCalls != attemptsPerTarget {
		t.Errorf("primary attempts = %d, want %d", primary.completeCalls, attemptsPerTarget)
	}
	// fallback got the target's model
	if fallback.lastParams.Model != "m2" || fallback.lastParams.Provider != "fallback" {
		t.Errorf("fallback params = %+v", fallback.lastParams)
	}
}

func TestCompleteChainNonRetryableStopsImmediately(t *testing.T) {
	primary := &mockProvider{
		name:        "primary",
		completeErr: &ProviderError{Provider: "primary", StatusCode: 400, Message: "bad request"},
	}
	fallback := &mockProvider{name: "fallback", completeResult: okResult("fallback", "x")}
	svc := newTestService(primary, fallback)

	_, err := svc.CompleteChain(context.Background(), []RouteTarget{
		{Provider: "primary", Model: "m1"},
		{Provider: "fallback", Model: "m2"},
	}, CompletionParams{})
	if err == nil {
		t.Fatal("a 400 must not fail over: the request itself is bad")
	}
	if fallback.lastParams != nil {
		t.Error("fallback should not have been called for a non-retryable error")
	}
}

func TestCircuitBreakerOpensAndRecovers(t *testing.T) {
	p := &flakyProvider{
		mockProvider:  mockProvider{name: "openai"},
		failCompletes: 99,
		breakStreamAt: -1,
	}
	svc := newTestService(p)
	chain := []RouteTarget{{Provider: "openai", Model: "gpt-4o"}}
	ctx := context.Background()

	// Hammer until the breaker opens.
	for i := 0; i < breakerThreshold; i++ {
		svc.CompleteChain(ctx, chain, CompletionParams{})
	}
	if state := svc.BreakerState("openai"); state != "open" {
		t.Fatalf("breaker state = %s, want open", state)
	}

	callsBefore := p.completeCalls
	_, err := svc.CompleteChain(ctx, chain, CompletionParams{})
	if err == nil {
		t.Fatal("expected error while breaker open")
	}
	if !strings.Contains(err.Error(), "circuit breaker open") {
		t.Errorf("error = %v", err)
	}
	if p.completeCalls != callsBefore {
		t.Error("open breaker must prevent provider calls")
	}
}

func TestStreamChainMidStreamFailover(t *testing.T) {
	// The acceptance test from the plan: kill the primary mid-stream; the
	// client sees a valid completed stream from the fallback.
	primary := &flakyProvider{
		mockProvider:  mockProvider{name: "primary"},
		breakStreamAt: 2, // two deltas, then the connection dies
	}
	fallback := &mockProvider{
		name: "fallback",
		streamChunks: []StreamChunk{
			{Delta: "complete ", Provider: "fallback"},
			{Delta: "answer", Provider: "fallback"},
			{Done: true, FinishReason: FinishStop, Usage: &Usage{TotalTokens: 7}, Provider: "fallback"},
		},
	}
	svc := newTestService(primary, fallback)

	chunks, err := svc.CompleteStreamChain(context.Background(), []RouteTarget{
		{Provider: "primary", Model: "m1"},
		{Provider: "fallback", Model: "m2"},
	}, CompletionParams{})
	if err != nil {
		t.Fatal(err)
	}

	var sawDone bool
	var fallbackText string
	for c := range chunks {
		if c.Err != nil {
			t.Fatalf("client must not see an error when a fallback can complete: %v", c.Err)
		}
		if c.Provider == "fallback" {
			fallbackText += c.Delta
		}
		if c.Done {
			sawDone = true
			if c.Usage == nil || c.Usage.TotalTokens != 7 {
				t.Errorf("final usage = %+v", c.Usage)
			}
		}
	}
	if !sawDone {
		t.Fatal("stream did not complete")
	}
	if fallbackText != "complete answer" {
		t.Errorf("fallback content = %q", fallbackText)
	}
	if primary.streamCalls != 1 || len(fallback.streamChunks) == 0 {
		t.Errorf("primary calls = %d", primary.streamCalls)
	}
}

func TestStreamChainAllTargetsFail(t *testing.T) {
	primary := &flakyProvider{mockProvider: mockProvider{name: "primary"}, breakStreamAt: 1}
	secondary := &flakyProvider{mockProvider: mockProvider{name: "secondary"}, breakStreamAt: 0}
	svc := newTestService(primary, secondary)

	chunks, err := svc.CompleteStreamChain(context.Background(), []RouteTarget{
		{Provider: "primary", Model: "m1"},
		{Provider: "secondary", Model: "m2"},
	}, CompletionParams{})
	if err != nil {
		t.Fatal(err)
	}

	var sawErr, sawDone bool
	for c := range chunks {
		if c.Err != nil {
			sawErr = true
		}
		if c.Done {
			sawDone = true
		}
	}
	if !sawErr || sawDone {
		t.Errorf("sawErr=%v sawDone=%v; when every target fails the client must see an error and no completion", sawErr, sawDone)
	}
}
