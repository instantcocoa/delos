// Package runtime implements the Delos LLM gateway: OpenAI- and
// Anthropic-compatible HTTP surfaces translated onto a curated set of
// backend providers.
package runtime

import (
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// ContentPart is one piece of a message: text or an image.
type ContentPart struct {
	Type string // "text" | "image"

	// Text is set when Type == "text".
	Text string

	// Image fields, set when Type == "image". Exactly one of ImageURL or
	// ImageData is set: ImageURL is a remote http(s) URL; ImageData is
	// base64-encoded bytes with MediaType describing them (e.g. "image/png").
	ImageURL  string
	ImageData string
	MediaType string

	// CacheControl marks this part as a prompt-cache breakpoint. The only
	// value providers currently honour is "ephemeral" (Anthropic). Providers
	// that do not support prompt caching ignore it.
	CacheControl string
}

// TextPart returns a text content part.
func TextPart(text string) ContentPart {
	return ContentPart{Type: "text", Text: text}
}

// Message represents a chat message.
type Message struct {
	Role    string // system, user, assistant, tool
	Content []ContentPart
	Name    string // optional participant name

	// ToolCalls is set on assistant messages that request tool invocations.
	ToolCalls []ToolCall

	// ToolCallID is set on role=="tool" messages carrying a tool result;
	// it references the ToolCall.ID being answered.
	ToolCallID string
}

// TextMessage builds a message with a single text part.
func TextMessage(role, text string) Message {
	return Message{Role: role, Content: []ContentPart{TextPart(text)}}
}

// Text returns the concatenation of the message's text parts.
func (m Message) Text() string {
	out := ""
	for _, p := range m.Content {
		if p.Type == "text" {
			out += p.Text
		}
	}
	return out
}

// ToolCall is a provider-requested invocation of a client-defined tool.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON
}

// Tool declares a callable tool (function) the model may invoke.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema for the arguments

	// CacheControl marks the tool definition as a prompt-cache breakpoint
	// ("ephemeral"). See ContentPart.CacheControl.
	CacheControl string
}

// ToolChoice constrains how the model may use tools.
type ToolChoice struct {
	Mode string // "auto" | "none" | "required" | "tool"
	Name string // target tool name when Mode == "tool"
}

// ResponseFormat requests structured output.
type ResponseFormat struct {
	Type   string // "json_object" | "json_schema"
	Name   string
	Schema json.RawMessage
	Strict bool
}

// CompletionParams contains parameters for a completion request.
type CompletionParams struct {
	Messages       []Message
	Provider       string // resolved provider name (informational)
	Model          string
	Temperature    *float64
	TopP           *float64
	MaxTokens      int
	Stop           []string
	Tools          []Tool
	ToolChoice     *ToolChoice
	ResponseFormat *ResponseFormat
}

// FinishReason values are normalized across providers.
const (
	FinishStop          = "stop"
	FinishLength        = "length"
	FinishToolCalls     = "tool_calls"
	FinishContentFilter = "content_filter"
)

// CompletionResult contains the result of a completion request.
type CompletionResult struct {
	ID           string
	Message      Message // assistant message: text parts and/or tool calls
	FinishReason string  // one of the Finish* constants
	Provider     string
	Model        string
	Usage        Usage
	Cached       bool // served from the response cache
}

// Text returns the text content of the result message.
func (r *CompletionResult) Text() string { return r.Message.Text() }

// Usage contains token usage information.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64

	// CacheCreationTokens and CacheReadTokens are prompt-cache accounting.
	// Providers that report them (Anthropic) exclude them from PromptTokens,
	// so the token total is the sum of all four counters.
	CacheCreationTokens int
	CacheReadTokens     int
}

// withDerivedTotals returns u with TotalTokens set to at least the sum of the
// per-direction counters. Backends are not required to send a total, and some
// OpenAI-compatible servers (vLLM, Ollama, several proxies) send
// prompt_tokens and completion_tokens with total_tokens absent or zero.
// Trusting that zero silently zeroes both the token budget and, before the
// per-direction pricing split, the dollar cost of the request.
func (u Usage) withDerivedTotals() Usage {
	sum := u.PromptTokens + u.CompletionTokens + u.CacheCreationTokens + u.CacheReadTokens
	if u.TotalTokens < sum {
		u.TotalTokens = sum
	}
	return u
}

// ToolCallDelta is an incremental piece of a streamed tool call.
type ToolCallDelta struct {
	Index          int    // which tool call this delta belongs to
	ID             string // set on the first delta of a call
	Name           string // set on the first delta of a call
	ArgumentsDelta string // JSON fragment to append
}

// StreamChunk represents a chunk of a streaming response.
type StreamChunk struct {
	ID           string
	Delta        string         // text delta
	ToolCall     *ToolCallDelta // tool-call delta, if any
	Done         bool           // final chunk
	FinishReason string         // set on the final chunk
	Usage        *Usage         // set on the final chunk when known
	Provider     string
	Model        string
	Err          error // non-nil if the stream broke; no Done chunk follows
}

// EmbedParams contains parameters for an embedding request.
type EmbedParams struct {
	Texts    []string
	Model    string
	Provider string
}

// EmbedResult contains the result of an embedding request.
type EmbedResult struct {
	Embeddings []Embedding
	Model      string
	Provider   string
	Usage      Usage
}

// Embedding represents a single embedding vector.
type Embedding struct {
	Values     []float32
	Dimensions int
}

// ProviderInfo describes a registered provider for health and listing APIs.
type ProviderInfo struct {
	Name   string
	Models []string
}

// ---- pricing ----

// ModelRate is a model's published price in USD per 1,000,000 tokens, split by
// direction.
//
// A single blended "USD per 1K total tokens" rate — what every provider here
// used previously — is wrong by the input/output spread of the model, which is
// 4-5x on current frontier models (gpt-4o is $2.50/1M in versus $10.00/1M out,
// claude-sonnet-4-5 is $3/1M in versus $15/1M out). Output-heavy traffic was
// therefore under-billed by up to 5x, which makes a USD budget unenforceable.
type ModelRate struct {
	Input  float64 // USD per 1M prompt (input) tokens
	Output float64 // USD per 1M completion (output) tokens

	// CacheWrite and CacheRead price prompt-cache tokens. Zero means "same as
	// Input", the right default for providers that do not price caching
	// separately. A provider with genuinely free cache reads must say so with
	// a tiny non-zero value rather than 0.
	CacheWrite float64
	CacheRead  float64
}

// PriceTable resolves a model name to a ModelRate and turns a Usage into a
// dollar cost. Lookup is:
//
//  1. the provider's name normalizer (strips "models/" on Gemini, cross-region
//     inference-profile prefixes on Bedrock),
//  2. exact match,
//  3. longest matching table key that is a prefix of the model, so dated
//     variants (claude-sonnet-4-5-20250929, gpt-4o-2024-11-20) inherit the
//     family rate. Longest-first with a lexicographic tiebreak, so the answer
//     does not depend on Go's randomized map iteration order.
//
// A model that resolves to nothing costs $0, which silently defeats USD
// budgets, so the miss is logged once per model name.
type PriceTable struct {
	provider  string
	normalize func(string) string

	mu     sync.RWMutex
	rates  map[string]ModelRate
	keys   []string // table keys, longest first
	warned map[string]bool
}

// NewPriceTable builds a price table for a provider. rates may be nil, which
// is the honest state for a self-hosted or third-party OpenAI-compatible
// endpoint whose prices the gateway cannot know.
func NewPriceTable(provider string, rates map[string]ModelRate) *PriceTable {
	t := &PriceTable{
		provider: provider,
		rates:    make(map[string]ModelRate, len(rates)),
		warned:   make(map[string]bool),
	}
	for model, rate := range rates {
		t.rates[model] = rate
	}
	t.reindex()
	return t
}

// withNormalizer installs a provider-specific model-name normalizer.
func (t *PriceTable) withNormalizer(f func(string) string) *PriceTable {
	t.normalize = f
	return t
}

// Set adds or replaces one model's rate. It exists so operators can price
// models the gateway does not ship rates for (self-hosted endpoints above all).
func (t *PriceTable) Set(model string, rate ModelRate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rates[model] = rate
	t.reindexLocked()
}

// SetAll adds or replaces several models' rates.
func (t *PriceTable) SetAll(rates map[string]ModelRate) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for model, rate := range rates {
		t.rates[model] = rate
	}
	t.reindexLocked()
}

func (t *PriceTable) reindex() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reindexLocked()
}

func (t *PriceTable) reindexLocked() {
	t.keys = t.keys[:0]
	for k := range t.rates {
		t.keys = append(t.keys, k)
	}
	sort.Slice(t.keys, func(i, j int) bool {
		if len(t.keys[i]) != len(t.keys[j]) {
			return len(t.keys[i]) > len(t.keys[j])
		}
		return t.keys[i] < t.keys[j]
	})
}

// Rate resolves a model to its rate. The bool reports whether the model is
// priced at all; an explicitly-listed $0 model returns (zero rate, true).
func (t *PriceTable) Rate(model string) (ModelRate, bool) {
	if t == nil {
		return ModelRate{}, false
	}
	name := model
	if t.normalize != nil {
		name = t.normalize(model)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if rate, ok := t.rates[name]; ok {
		return rate, true
	}
	for _, key := range t.keys { // longest first, deterministic
		if strings.HasPrefix(name, key) {
			return t.rates[key], true
		}
	}
	return ModelRate{}, false
}

// Cost prices a request. Usage counters are taken as reported; the caller is
// expected to have run withDerivedTotals first when the total matters.
func (t *PriceTable) Cost(model string, u Usage) float64 {
	rate, ok := t.Rate(model)
	if !ok {
		t.warnUnpriced(model)
		return 0
	}
	cacheWrite := rate.CacheWrite
	if cacheWrite == 0 {
		cacheWrite = rate.Input
	}
	cacheRead := rate.CacheRead
	if cacheRead == 0 {
		cacheRead = rate.Input
	}
	return (float64(u.PromptTokens)*rate.Input +
		float64(u.CompletionTokens)*rate.Output +
		float64(u.CacheCreationTokens)*cacheWrite +
		float64(u.CacheReadTokens)*cacheRead) / 1e6
}

// warnUnpriced logs an unpriced model once per model name. Billing zero is
// invisible in a usage total, so it has to be visible in the log: a key with a
// USD budget spending on an unpriced model never hits that budget.
func (t *PriceTable) warnUnpriced(model string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	if t.warned[model] {
		t.mu.Unlock()
		return
	}
	t.warned[model] = true
	t.mu.Unlock()
	slog.Warn("model has no configured price; requests against it are billed $0.00 and do not count toward USD budgets",
		"provider", t.provider, "model", model)
}
