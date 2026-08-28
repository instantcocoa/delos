package runtime

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// closeEnough compares dollar amounts. Costs are sums of products of small
// floats, so exact equality is not a fair assertion.
func closeEnough(got, want float64) bool { return math.Abs(got-want) <= 1e-12 }

// TestPricingBillsEveryPath walks the four paths that used to feed AddUsage a
// cost of exactly $0.00, plus the input/output split that made every priced
// request wrong in the same direction. A USD budget is only enforceable if all
// five produce a correct non-zero number.
func TestPricingBillsEveryPath(t *testing.T) {
	tests := []struct {
		name  string
		table *PriceTable
		model string
		usage Usage
		want  float64
	}{
		{
			// gpt-4o is $2.50/1M in and $10.00/1M out. Under one blended rate
			// an output-heavy request was under-billed by up to that 4x.
			name:  "input and output are priced separately",
			table: NewPriceTable("openai", openaiPricing),
			model: "gpt-4o",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 10_000},
			want:  (1_000*2.50 + 10_000*10.00) / 1e6,
		},
		{
			// Only eight OpenAI models used to be listed; o3 was not one, so
			// every o3 request billed nothing.
			name:  "o3 is priced",
			table: NewPriceTable("openai", openaiPricing),
			model: "o3",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 1_000},
			want:  (1_000*2.00 + 1_000*8.00) / 1e6,
		},
		{
			name:  "dated gpt-4o snapshot inherits the family rate",
			table: NewPriceTable("openai", openaiPricing),
			model: "gpt-4o-2024-11-20",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 1_000},
			want:  (1_000*2.50 + 1_000*10.00) / 1e6,
		},
		{
			name:  "chatgpt-4o-latest is priced at its own higher rate",
			table: NewPriceTable("openai", openaiPricing),
			model: "chatgpt-4o-latest",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 1_000},
			want:  (1_000*5.00 + 1_000*15.00) / 1e6,
		},
		{
			// gpt-4o-mini must not fall back to gpt-4o: longest prefix wins.
			name:  "longest prefix wins over a shorter family key",
			table: NewPriceTable("openai", openaiPricing),
			model: "gpt-4o-mini-2024-07-18",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 1_000},
			want:  (1_000*0.15 + 1_000*0.60) / 1e6,
		},
		{
			// The pricing fallback used to be HasPrefix(model, catalogKey),
			// so "us.anthropic.claude-..." never matched "anthropic.claude-..."
			// and billed $0 — and the cross-region inference profile is the
			// only way to reach Sonnet 4.5 in most regions.
			name:  "bedrock cross-region inference profile",
			table: bedrockPriceTable(),
			model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0",
			usage: Usage{PromptTokens: 10_000, CompletionTokens: 2_000},
			want:  (10_000*3.00 + 2_000*15.00) / 1e6,
		},
		{
			name:  "bedrock eu inference profile",
			table: bedrockPriceTable(),
			model: "eu.anthropic.claude-haiku-4-5-20251001-v1:0",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 1_000},
			want:  (1_000*1.00 + 1_000*5.00) / 1e6,
		},
		{
			name:  "gemini strips the models/ resource prefix",
			table: NewPriceTable("gemini", geminiPricing).withNormalizer(geminiTrimModelPrefix),
			model: "models/gemini-2.5-pro",
			usage: Usage{PromptTokens: 1_000, CompletionTokens: 1_000},
			want:  (1_000*1.25 + 1_000*10.00) / 1e6,
		},
		{
			name:  "anthropic prompt cache tokens bill at cache rates",
			table: NewPriceTable("anthropic", anthropicPricing),
			model: "claude-sonnet-4-5-20250929",
			usage: Usage{
				PromptTokens:        1_000,
				CompletionTokens:    500,
				CacheCreationTokens: 10_000,
				CacheReadTokens:     20_000,
			},
			want: (1_000*3.00 + 500*15.00 + 10_000*3.75 + 20_000*0.30) / 1e6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.table.Cost(tc.model, tc.usage)
			if !closeEnough(got, tc.want) {
				t.Errorf("Cost(%q) = %v, want %v", tc.model, got, tc.want)
			}
			if got == 0 {
				t.Errorf("Cost(%q) is zero; a $0 request never counts toward a USD budget", tc.model)
			}
		})
	}
}

// geminiTrimModelPrefix mirrors the normalizer the Gemini provider installs.
func geminiTrimModelPrefix(model string) string {
	if len(model) > 7 && model[:7] == "models/" {
		return model[7:]
	}
	return model
}

// An unpriced model must resolve to "no rate", not to a silent zero that looks
// like a priced free model, and the miss is logged once.
func TestPricingUnpricedModelIsVisible(t *testing.T) {
	table := NewPriceTable("openai", openaiPricing)
	if _, ok := table.Rate("some-model-we-have-never-heard-of"); ok {
		t.Error("unknown model reported as priced")
	}
	if got := table.Cost("some-model-we-have-never-heard-of", Usage{PromptTokens: 1e6}); got != 0 {
		t.Errorf("unknown model cost = %v, want 0", got)
	}
	// warnUnpriced records the model so the warning is not repeated per request.
	table.warnUnpriced("some-model-we-have-never-heard-of")
	table.mu.RLock()
	warned := table.warned["some-model-we-have-never-heard-of"]
	table.mu.RUnlock()
	if !warned {
		t.Error("unpriced model was not recorded for warn-once")
	}

	// A model listed at $0 is priced, not unpriced.
	free := NewPriceTable("gemini", geminiPricing)
	if _, ok := free.Rate("text-embedding-004"); !ok {
		t.Error("explicitly free model reported as unpriced")
	}
}

// A self-hosted or third-party OpenAI-compatible endpoint ships with no
// pricing at all: every vLLM/Ollama/OpenRouter/Together request used to hit a
// nil map and cost $0.00. It still costs $0 until an operator prices it, but
// pricing it must now be possible — and must produce a correct number.
func TestCompatProviderPricing(t *testing.T) {
	p := NewOpenAICompatProvider("vllm", "http://localhost:8000/v1", "")

	unpriced := p.usageFrom("llama-3.3-70b", oaiUsage{PromptTokens: 1_000, CompletionTokens: 1_000, TotalTokens: 2_000})
	if unpriced.CostUSD != 0 {
		t.Errorf("unpriced compat model cost = %v, want 0", unpriced.CostUSD)
	}

	p.SetPricing(map[string]ModelRate{"llama-3.3-70b": {Input: 0.60, Output: 0.90}})
	priced := p.usageFrom("llama-3.3-70b", oaiUsage{PromptTokens: 1_000, CompletionTokens: 1_000, TotalTokens: 2_000})
	want := (1_000*0.60 + 1_000*0.90) / 1e6
	if !closeEnough(priced.CostUSD, want) {
		t.Errorf("priced compat model cost = %v, want %v", priced.CostUSD, want)
	}
	if priced.CostUSD == 0 {
		t.Error("priced compat model still bills zero")
	}
}

// Several OpenAI-compatible servers report prompt_tokens and
// completion_tokens without total_tokens. Trusting the zero zeroed the token
// budget for the request.
func TestUsageTotalIsDerivedNotTrusted(t *testing.T) {
	p := NewOpenAIProvider("k")

	missing := p.usageFrom("gpt-4o", oaiUsage{PromptTokens: 700, CompletionTokens: 300})
	if missing.TotalTokens != 1000 {
		t.Errorf("TotalTokens = %d, want 1000 derived from the parts", missing.TotalTokens)
	}
	if want := (700*2.50 + 300*10.00) / 1e6; !closeEnough(missing.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", missing.CostUSD, want)
	}

	// An under-reported total is also corrected; a larger reported total (a
	// backend that counts something we do not) is left alone.
	if got := (Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 3}).withDerivedTotals(); got.TotalTokens != 10 {
		t.Errorf("under-reported total = %d, want 10", got.TotalTokens)
	}
	if got := (Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 99}).withDerivedTotals(); got.TotalTokens != 99 {
		t.Errorf("over-reported total = %d, want 99 (left alone)", got.TotalTokens)
	}

	// Anthropic reports cache tokens outside input_tokens, so they belong in
	// the total too.
	cached := Usage{PromptTokens: 10, CompletionTokens: 20, CacheCreationTokens: 30, CacheReadTokens: 40}.withDerivedTotals()
	if cached.TotalTokens != 100 {
		t.Errorf("total with cache tokens = %d, want 100", cached.TotalTokens)
	}
}

// Prefix fallback must not depend on Go's randomized map iteration order.
func TestPricingPrefixFallbackIsDeterministic(t *testing.T) {
	table := NewPriceTable("t", map[string]ModelRate{
		"model":       {Input: 1},
		"model-large": {Input: 2},
		"model-l":     {Input: 3},
	})
	for i := range 50 {
		rate, ok := table.Rate("model-large-v2")
		if !ok || rate.Input != 2 {
			t.Fatalf("iteration %d resolved to %+v (ok=%v), want the longest match", i, rate, ok)
		}
	}
}

// A cache rate of zero means "same as input", so a provider that does not
// price caching separately still bills cached tokens.
func TestPricingCacheRatesDefaultToInput(t *testing.T) {
	table := NewPriceTable("t", map[string]ModelRate{"m": {Input: 10, Output: 20}})
	got := table.Cost("m", Usage{CacheCreationTokens: 1_000, CacheReadTokens: 1_000})
	if want := 2_000 * 10.0 / 1e6; !closeEnough(got, want) {
		t.Errorf("cost = %v, want %v", got, want)
	}
}

// The whole point of the pricing work: a request that reaches recordUsage
// carries a non-zero dollar figure, so a USD budget actually moves.
func TestUSDBudgetSeesNonZeroCost(t *testing.T) {
	body, _ := json.Marshal(map[string]any{
		"id":      "chatcmpl-1",
		"model":   "gpt-4o",
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 1000, "completion_tokens": 5000},
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	p := NewOpenAIProvider("k")
	p.baseURL = srv.URL

	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gpt-4o",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Usage.TotalTokens != 6000 {
		t.Errorf("TotalTokens = %d, want 6000 (backend sent no total)", result.Usage.TotalTokens)
	}
	want := (1000*2.50 + 5000*10.00) / 1e6
	if !closeEnough(result.Usage.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", result.Usage.CostUSD, want)
	}
}
