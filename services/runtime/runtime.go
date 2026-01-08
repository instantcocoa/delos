// Package runtime provides the LLM gateway service for completion and embedding requests.
package runtime

// Message represents a chat message.
type Message struct {
	Role    string // system, user, assistant
	Content string
	Name    string // optional
}

// RoutingStrategy determines how to select providers.
type RoutingStrategy int

const (
	RoutingUnspecified RoutingStrategy = iota
	RoutingCostOptimized
	RoutingLatencyOptimized
	RoutingQualityOptimized
	RoutingSpecificProvider
)

// CompletionParams contains parameters for a completion request.
type CompletionParams struct {
	PromptRef    string            // e.g., "summarizer:v2.1"
	Messages     []Message
	Variables    map[string]string
	Routing      RoutingStrategy
	Provider     string // required if RoutingSpecificProvider
	Model        string
	Temperature  float64
	MaxTokens    int
	TopP         float64
	Stop         []string
	OutputSchema string // JSON schema for structured output
	UseCache     bool
	CacheTTL     int // seconds
	Metadata     map[string]string
}

// CompletionResult contains the result of a completion request.
type CompletionResult struct {
	ID               string
	Content          string
	Message          Message
	StructuredOutput map[string]interface{}
	Provider         string
	Model            string
	Usage            Usage
	Cached           bool
	TraceID          string
}

// Usage contains token usage information.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CostUSD          float64
}

// StreamChunk represents a chunk of a streaming response.
type StreamChunk struct {
	ID       string
	Delta    string
	Done     bool
	Message  *Message // only on final chunk
	Usage    *Usage   // only on final chunk
	Provider string
	Model    string
	TraceID  string
	Err      error // non-nil if stream encountered an error
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

// ProviderInfo represents information about an LLM provider.
type ProviderInfo struct {
	Name            string
	Models          []string
	Available       bool
	CostPer1KTokens map[string]float64 // model -> cost
}
