package runtime

import (
	"context"
	"testing"
	"time"
)

func TestCacheKeyDeterministic(t *testing.T) {
	params := CompletionParams{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "hello")},
	}
	if CacheKey(params) != CacheKey(params) {
		t.Fatal("same params must produce the same key")
	}

	variants := []CompletionParams{
		{Model: "gpt-4o-mini", Messages: []Message{TextMessage("user", "hello")}},
		{Model: "gpt-4o", Messages: []Message{TextMessage("user", "goodbye")}},
		{Model: "gpt-4o", Messages: []Message{TextMessage("system", "hello")}},
		{Model: "gpt-4o", Messages: []Message{TextMessage("user", "hello")}, MaxTokens: 5},
		{Model: "gpt-4o", Messages: []Message{TextMessage("user", "hello")},
			Tools: []Tool{{Name: "t"}}},
	}
	base := CacheKey(params)
	for i, v := range variants {
		if CacheKey(v) == base {
			t.Errorf("variant %d must produce a different key", i)
		}
	}

	// trailing whitespace normalizes away
	trimmed := CompletionParams{Model: "gpt-4o", Messages: []Message{TextMessage("user", "hello  \n")}}
	if CacheKey(trimmed) != base {
		t.Error("trailing whitespace should not change the key")
	}
}

func TestLRUCacheEviction(t *testing.T) {
	c := NewLRUCache(2)
	ctx := context.Background()
	c.Set(ctx, "a", okResult("p", "a"), 0)
	c.Set(ctx, "b", okResult("p", "b"), 0)

	// touch "a" so "b" is the eviction candidate
	if _, ok := c.Get(ctx, "a"); !ok {
		t.Fatal("a should be present")
	}
	c.Set(ctx, "c", okResult("p", "c"), 0)

	if _, ok := c.Get(ctx, "b"); ok {
		t.Error("b should have been evicted (least recently used)")
	}
	if _, ok := c.Get(ctx, "a"); !ok {
		t.Error("a should have survived")
	}
	if _, ok := c.Get(ctx, "c"); !ok {
		t.Error("c should be present")
	}
}

func TestLRUCacheTTL(t *testing.T) {
	c := NewLRUCache(10)
	now := time.Now()
	c.now = func() time.Time { return now }
	ctx := context.Background()

	c.Set(ctx, "k", okResult("p", "x"), time.Minute)
	if _, ok := c.Get(ctx, "k"); !ok {
		t.Fatal("fresh entry should hit")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get(ctx, "k"); ok {
		t.Error("expired entry must miss")
	}
}

func TestCompleteChainCacheHit(t *testing.T) {
	p := &mockProvider{name: "openai", completeResult: okResult("openai", "cached answer")}
	svc := newTestService(p)
	svc.SetCache(NewLRUCache(10), time.Minute)
	chain := []RouteTarget{{Provider: "openai", Model: "gpt-4o"}}
	params := CompletionParams{Model: "gpt-4o", Messages: []Message{TextMessage("user", "q")}}
	ctx := context.Background()

	first, err := svc.CompleteChain(ctx, chain, params)
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached {
		t.Error("first request must be a miss")
	}

	p.completeResult = nil // provider must not be consulted again
	p.completeErr = &ProviderError{Provider: "openai", StatusCode: 500, Message: "should not be called"}

	second, err := svc.CompleteChain(ctx, chain, params)
	if err != nil {
		t.Fatalf("cache hit failed: %v", err)
	}
	if !second.Cached || second.Text() != "cached answer" {
		t.Errorf("cached=%v text=%q", second.Cached, second.Text())
	}
}

func TestCompleteChainCacheBypassOnTemperature(t *testing.T) {
	p := &mockProvider{name: "openai", completeResult: okResult("openai", "fresh")}
	svc := newTestService(p)
	svc.SetCache(NewLRUCache(10), time.Minute)
	chain := []RouteTarget{{Provider: "openai", Model: "gpt-4o"}}
	temp := 0.9
	params := CompletionParams{
		Model:       "gpt-4o",
		Temperature: &temp,
		Messages:    []Message{TextMessage("user", "q")},
	}
	ctx := context.Background()

	if _, err := svc.CompleteChain(ctx, chain, params); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.completeResult = okResult("openai", "fresh2")
	before := p.lastParams
	_ = before
	if _, err := svc.CompleteChain(ctx, chain, params); err != nil {
		t.Fatal(err)
	}
	_ = calls
	// The second call must have reached the provider (no caching at temp>0):
	// lastParams is overwritten on every provider call, so verify by forcing
	// an error and observing it surface.
	p.completeErr = &ProviderError{Provider: "openai", StatusCode: 500, Message: "boom"}
	p.completeResult = nil
	if _, err := svc.CompleteChain(ctx, chain, params); err == nil {
		t.Error("non-deterministic request must bypass the cache and hit the provider")
	}
}
