package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
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

func TestStreamChainFailoverBeforeContent(t *testing.T) {
	// A stream that breaks before delivering any content can still fail over:
	// nothing has reached the client, so the fallback's stream is the only one
	// it ever sees.
	primary := &flakyProvider{
		mockProvider:  mockProvider{name: "primary"},
		breakStreamAt: 0, // errors immediately, no deltas
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

	// Accumulate every delta the client would see, regardless of provider -
	// an SSE client cannot filter by provider, so neither does this test.
	var text string
	var sawDone bool
	for c := range chunks {
		if c.Err != nil {
			t.Fatalf("client must not see an error when a fallback can complete: %v", c.Err)
		}
		text += c.Delta
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
	if text != "complete answer" {
		t.Errorf("client-visible content = %q, want the fallback's stream alone", text)
	}
}

func TestStreamChainNoSpliceAfterContent(t *testing.T) {
	// Once deltas are on the wire they cannot be retracted. Failing over at
	// that point would splice two different completions into one response, so
	// the break must surface as an error instead.
	primary := &flakyProvider{
		mockProvider:  mockProvider{name: "primary"},
		breakStreamAt: 2, // two deltas, then the connection dies
	}
	fallback := &mockProvider{
		name: "fallback",
		streamChunks: []StreamChunk{
			{Delta: "a completely different answer", Provider: "fallback"},
			{Done: true, FinishReason: FinishStop, Provider: "fallback"},
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

	var text string
	var sawErr, sawDone bool
	for c := range chunks {
		if c.Err != nil {
			sawErr = true
			continue
		}
		text += c.Delta
		if c.Done {
			sawDone = true
		}
	}

	if !sawErr {
		t.Error("a break after content was delivered must surface as an error")
	}
	if sawDone {
		t.Error("a spliced stream must not be reported as complete")
	}
	if strings.Contains(text, "a completely different answer") {
		t.Errorf("fallback content was spliced onto the primary's partial output: %q", text)
	}
	if fallback.lastParams != nil {
		t.Error("fallback must not be started once the client has partial content")
	}
}

func TestStreamChainClientDisconnect(t *testing.T) {
	// A client that stops reading must not strand the producing goroutine:
	// every send is guarded by the request context.
	p := &mockProvider{
		name: "primary",
		streamChunks: []StreamChunk{
			{Delta: "one", Provider: "primary"},
			{Delta: "two", Provider: "primary"},
			{Delta: "three", Provider: "primary"},
			{Done: true, FinishReason: FinishStop, Provider: "primary"},
		},
	}
	svc := newTestService(p)

	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := svc.CompleteStreamChain(ctx, []RouteTarget{{Provider: "primary", Model: "m1"}}, CompletionParams{})
	if err != nil {
		t.Fatal(err)
	}

	<-chunks // read one chunk, then walk away
	cancel()

	// The producer must finish and close the channel rather than blocking
	// forever on a send nobody is receiving.
	done := make(chan struct{})
	go func() {
		for range chunks {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream goroutine did not terminate after the client disconnected")
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

func TestCompleteChainDoesNotRetryTranslationFailures(t *testing.T) {
	// A malformed client request fails identically on every attempt and
	// every fallback. Retrying it produced a 502 describing a caller
	// mistake, and - worse - three breaker failures per request, so one bad
	// client could open the circuit for every tenant on that provider.
	primary := &mockProvider{
		name:        "primary",
		completeErr: fmt.Errorf("messages[0]: unsupported content part type %q", "audio"),
	}
	fallback := &mockProvider{name: "fallback", completeResult: okResult("fallback", "x")}
	svc := newTestService(primary, fallback)

	_, err := svc.CompleteChain(context.Background(), []RouteTarget{
		{Provider: "primary", Model: "m1"},
		{Provider: "fallback", Model: "m2"},
	}, CompletionParams{})
	if err == nil {
		t.Fatal("a translation failure must be reported, not retried into a fallback")
	}
	if fallback.lastParams != nil {
		t.Error("a deterministic failure must not fail over to a paid fallback")
	}
	if state := svc.BreakerState("primary"); state != "closed" {
		t.Errorf("breaker = %s; a caller's mistake is not the provider's unhealthiness", state)
	}
}

func TestCompleteChainDoesNotRetryQuotaExhaustion(t *testing.T) {
	// HTTP 429 covers both "slow down" and "you are out of credit". Only
	// the first is worth retrying; the second is permanent, and retrying it
	// trips the breaker so that every later request gets a generic "circuit
	// breaker open" instead of the billing message the operator needs.
	quota := &ProviderError{
		Provider: "openai", StatusCode: 429, Code: "insufficient_quota",
		Message: "You exceeded your current quota, please check your plan and billing details.",
	}
	p := &mockProvider{name: "openai", completeErr: quota}
	svc := newTestService(p)
	chain := []RouteTarget{{Provider: "openai", Model: "gpt-4o"}}

	for i := 0; i < breakerThreshold+2; i++ {
		_, err := svc.CompleteChain(context.Background(), chain, CompletionParams{})
		if err == nil {
			t.Fatal("expected the quota error to surface")
		}
		if !strings.Contains(err.Error(), "check your plan and billing details") {
			t.Fatalf("the upstream billing message must reach the caller, got %v", err)
		}
	}
	if state := svc.BreakerState("openai"); state != "closed" {
		t.Errorf("breaker = %s; a permanent billing failure must not open the circuit", state)
	}
}

func TestBreakerOpenErrorCarriesLastUpstreamError(t *testing.T) {
	// "circuit breaker open" on its own erases the only useful thing the
	// caller could learn. The last real failure travels with it, status and
	// message intact.
	p := &flakyProvider{
		mockProvider:  mockProvider{name: "openai"},
		failCompletes: 99,
		breakStreamAt: -1,
	}
	svc := newTestService(p)
	chain := []RouteTarget{{Provider: "openai", Model: "gpt-4o"}}

	for i := 0; i < breakerThreshold; i++ {
		svc.CompleteChain(context.Background(), chain, CompletionParams{})
	}
	if state := svc.BreakerState("openai"); state != "open" {
		t.Fatalf("breaker state = %s, want open", state)
	}

	_, err := svc.CompleteChain(context.Background(), chain, CompletionParams{})
	if err == nil {
		t.Fatal("expected an error while the breaker is open")
	}
	if !strings.Contains(err.Error(), "circuit breaker open") {
		t.Errorf("error = %v, want it to still say the breaker is open", err)
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %v, want it to carry the last upstream failure", err)
	}
	pe, ok := errAs[*ProviderError](err)
	if !ok {
		t.Fatalf("error = %v (%T), want a ProviderError so surfaces map the upstream status", err, err)
	}
	if pe.StatusCode != 500 {
		t.Errorf("status = %d, want the last upstream status", pe.StatusCode)
	}
}

func TestCacheableRequiresExplicitZeroTemperature(t *testing.T) {
	// An absent temperature is the API default of 1.0, not 0. Treating it
	// as deterministic made almost all traffic cacheable, and two identical
	// "tell me a joke" requests came back byte-identical.
	if cacheable(CompletionParams{}) {
		t.Error("an unset temperature is 1.0 by default and must not be cacheable")
	}
	zero := 0.0
	if !cacheable(CompletionParams{Temperature: &zero}) {
		t.Error("an explicit temperature of 0 is deterministic and must be cacheable")
	}
	hot := 0.7
	if cacheable(CompletionParams{Temperature: &hot}) {
		t.Error("a non-zero temperature must not be cacheable")
	}
}
