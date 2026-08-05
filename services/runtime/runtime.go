// Package runtime implements the Delos LLM gateway: OpenAI- and
// Anthropic-compatible HTTP surfaces translated onto a curated set of
// backend providers.
package runtime

import "encoding/json"

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
