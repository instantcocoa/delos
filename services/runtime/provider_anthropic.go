package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	anthropicBaseURL    = "https://api.anthropic.com/v1"
	anthropicAPIVersion = "2023-06-01"
)

// AnthropicProvider speaks the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	models     []string
	pricing    map[string]float64
}

// AnthropicOption configures the provider.
type AnthropicOption func(*AnthropicProvider)

// WithAnthropicBaseURL overrides the API base URL (testing and proxies).
func WithAnthropicBaseURL(url string) AnthropicOption {
	return func(p *AnthropicProvider) { p.baseURL = url }
}

// NewAnthropicProvider creates the provider for api.anthropic.com.
func NewAnthropicProvider(apiKey string, opts ...AnthropicOption) *AnthropicProvider {
	p := &AnthropicProvider{
		apiKey:     apiKey,
		baseURL:    anthropicBaseURL,
		httpClient: &http.Client{},
		models: []string{
			"claude-opus-4-5",
			"claude-sonnet-4-5",
			"claude-haiku-4-5",
			"claude-opus-4-1",
			"claude-sonnet-4-20250514",
		},
		pricing: map[string]float64{
			"claude-opus-4-5":          0.0125,
			"claude-sonnet-4-5":        0.003,
			"claude-haiku-4-5":         0.001,
			"claude-opus-4-1":          0.015,
			"claude-sonnet-4-20250514": 0.003,
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *AnthropicProvider) Name() string                        { return "anthropic" }
func (p *AnthropicProvider) Models(ctx context.Context) []string { return p.models }

// ---- wire types (Anthropic Messages format) ----

type anthBlock struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// image
	Source *anthImageSource `json:"source,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   any    `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

type anthImageSource struct {
	Type      string `json:"type"` // "base64" | "url"
	MediaType string `json:"media_type,omitempty"`
	Data      string `json:"data,omitempty"`
	URL       string `json:"url,omitempty"`
}

type anthWireMessage struct {
	Role    string      `json:"role"`
	Content []anthBlock `json:"content"`
}

type anthTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthRequest struct {
	Model         string            `json:"model"`
	MaxTokens     int               `json:"max_tokens"`
	Messages      []anthWireMessage `json:"messages"`
	System        string            `json:"system,omitempty"`
	Temperature   *float64          `json:"temperature,omitempty"`
	TopP          *float64          `json:"top_p,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Tools         []anthTool        `json:"tools,omitempty"`
	ToolChoice    map[string]any    `json:"tool_choice,omitempty"`
}

type anthWireUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthResponse struct {
	ID         string        `json:"id"`
	Model      string        `json:"model"`
	Content    []anthBlock   `json:"content"`
	StopReason string        `json:"stop_reason"`
	Usage      anthWireUsage `json:"usage"`
}

// ---- request translation ----

// anthPartToBlock converts one internal content part.
func anthPartToBlock(part ContentPart) (anthBlock, error) {
	switch part.Type {
	case "text":
		return anthBlock{Type: "text", Text: part.Text}, nil
	case "image":
		if part.ImageURL != "" {
			return anthBlock{Type: "image", Source: &anthImageSource{Type: "url", URL: part.ImageURL}}, nil
		}
		return anthBlock{Type: "image", Source: &anthImageSource{
			Type: "base64", MediaType: part.MediaType, Data: part.ImageData,
		}}, nil
	default:
		return anthBlock{}, fmt.Errorf("unsupported content part type %q", part.Type)
	}
}

// anthFromMessages converts internal messages to (system, wire messages).
// System messages are extracted; tool results become tool_result blocks in
// user messages; consecutive same-role messages are merged because the
// Messages API requires alternating roles.
func anthFromMessages(messages []Message) (string, []anthWireMessage, error) {
	var system strings.Builder
	var out []anthWireMessage

	appendBlocks := func(role string, blocks []anthBlock) {
		if len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Content = append(out[len(out)-1].Content, blocks...)
			return
		}
		out = append(out, anthWireMessage{Role: role, Content: blocks})
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			if system.Len() > 0 {
				system.WriteString("\n\n")
			}
			system.WriteString(m.Text())

		case "tool":
			block := anthBlock{Type: "tool_result", ToolUseID: m.ToolCallID}
			if text := m.Text(); text != "" {
				block.Content = text
			}
			appendBlocks("user", []anthBlock{block})

		case "assistant":
			var blocks []anthBlock
			for _, part := range m.Content {
				b, err := anthPartToBlock(part)
				if err != nil {
					return "", nil, err
				}
				blocks = append(blocks, b)
			}
			for _, tc := range m.ToolCalls {
				input := json.RawMessage(tc.Arguments)
				if len(input) == 0 {
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, anthBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
			}
			if len(blocks) == 0 {
				blocks = []anthBlock{{Type: "text", Text: ""}}
			}
			appendBlocks("assistant", blocks)

		default: // user
			var blocks []anthBlock
			for _, part := range m.Content {
				b, err := anthPartToBlock(part)
				if err != nil {
					return "", nil, err
				}
				blocks = append(blocks, b)
			}
			if len(blocks) == 0 {
				blocks = []anthBlock{{Type: "text", Text: ""}}
			}
			appendBlocks("user", blocks)
		}
	}
	return system.String(), out, nil
}

func (p *AnthropicProvider) buildRequest(params CompletionParams, stream bool) (*anthRequest, error) {
	if params.ResponseFormat != nil {
		return nil, fmt.Errorf("anthropic: response_format is not supported for this backend")
	}
	system, messages, err := anthFromMessages(params.Messages)
	if err != nil {
		return nil, err
	}
	maxTokens := params.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	req := &anthRequest{
		Model:         params.Model,
		MaxTokens:     maxTokens,
		Messages:      messages,
		System:        system,
		Temperature:   params.Temperature,
		TopP:          params.TopP,
		StopSequences: params.Stop,
		Stream:        stream,
	}
	for _, t := range params.Tools {
		schema := t.Parameters
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		req.Tools = append(req.Tools, anthTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	if tc := params.ToolChoice; tc != nil {
		switch tc.Mode {
		case "auto":
			req.ToolChoice = map[string]any{"type": "auto"}
		case "required":
			req.ToolChoice = map[string]any{"type": "any"}
		case "none":
			req.ToolChoice = map[string]any{"type": "none"}
		case "tool":
			req.ToolChoice = map[string]any{"type": "tool", "name": tc.Name}
		}
	}
	return req, nil
}

func (p *AnthropicProvider) post(ctx context.Context, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	return p.httpClient.Do(req)
}

func (p *AnthropicProvider) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return &ProviderError{Provider: "anthropic", StatusCode: resp.StatusCode, Message: envelope.Error.Message}
	}
	return &ProviderError{Provider: "anthropic", StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

func (p *AnthropicProvider) cost(model string, usage anthWireUsage) float64 {
	rate, ok := p.pricing[model]
	if !ok {
		// Model IDs may carry a date suffix (claude-sonnet-4-5-20250929).
		for m, r := range p.pricing {
			if strings.HasPrefix(model, m) {
				rate = r
				break
			}
		}
	}
	return rate * float64(usage.InputTokens+usage.OutputTokens) / 1000
}

func anthFinishReason(stop string) string {
	switch stop {
	case "tool_use":
		return FinishToolCalls
	case "max_tokens":
		return FinishLength
	case "refusal":
		return FinishContentFilter
	default:
		return FinishStop
	}
}

// ---- Provider implementation ----

func (p *AnthropicProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	req, err := p.buildRequest(params, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out anthResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("anthropic: invalid response: %w", err)
	}

	msg := Message{Role: "assistant"}
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			msg.Content = append(msg.Content, TextPart(block.Text))
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}

	usage := Usage{
		PromptTokens:     out.Usage.InputTokens,
		CompletionTokens: out.Usage.OutputTokens,
		TotalTokens:      out.Usage.InputTokens + out.Usage.OutputTokens,
		CostUSD:          p.cost(out.Model, out.Usage),
	}
	return &CompletionResult{
		ID:           out.ID,
		Message:      msg,
		FinishReason: anthFinishReason(out.StopReason),
		Provider:     "anthropic",
		Model:        out.Model,
		Usage:        usage,
	}, nil
}

// anthStreamEvent covers every SSE event type we consume.
type anthStreamEvent struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string        `json:"id"`
		Model string        `json:"model"`
		Usage anthWireUsage `json:"usage"`
	} `json:"message"`
	Index        int        `json:"index"`
	ContentBlock *anthBlock `json:"content_block"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthWireUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *AnthropicProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	req, err := p.buildRequest(params, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("anthropic request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, p.apiError(resp)
	}

	chunks := make(chan StreamChunk)
	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		events := make(chan SSEEvent)
		go ParseSSE(resp.Body, events)

		id, model := "", params.Model
		finish := FinishStop
		var inputTokens, outputTokens int
		// Map Anthropic block indexes to tool-call indexes; text blocks do
		// not consume tool indexes.
		toolIndexByBlock := map[int]int{}
		nextToolIndex := 0

		for event := range events {
			if event.Err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("anthropic stream error: %w", event.Err), Provider: "anthropic", Model: model}
				return
			}
			data := strings.TrimSpace(event.Data)
			if data == "" {
				continue
			}
			var ev anthStreamEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("anthropic stream: invalid event: %w", err), Provider: "anthropic", Model: model}
				return
			}

			switch ev.Type {
			case "message_start":
				if ev.Message != nil {
					id = ev.Message.ID
					if ev.Message.Model != "" {
						model = ev.Message.Model
					}
					inputTokens = ev.Message.Usage.InputTokens
				}
			case "content_block_start":
				if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
					toolIndexByBlock[ev.Index] = nextToolIndex
					chunks <- StreamChunk{
						ID: id,
						ToolCall: &ToolCallDelta{
							Index: nextToolIndex,
							ID:    ev.ContentBlock.ID,
							Name:  ev.ContentBlock.Name,
						},
						Provider: "anthropic",
						Model:    model,
					}
					nextToolIndex++
				}
			case "content_block_delta":
				if ev.Delta == nil {
					continue
				}
				switch ev.Delta.Type {
				case "text_delta":
					if ev.Delta.Text != "" {
						chunks <- StreamChunk{ID: id, Delta: ev.Delta.Text, Provider: "anthropic", Model: model}
					}
				case "input_json_delta":
					if idx, ok := toolIndexByBlock[ev.Index]; ok && ev.Delta.PartialJSON != "" {
						chunks <- StreamChunk{
							ID:       id,
							ToolCall: &ToolCallDelta{Index: idx, ArgumentsDelta: ev.Delta.PartialJSON},
							Provider: "anthropic",
							Model:    model,
						}
					}
				}
			case "message_delta":
				if ev.Delta != nil && ev.Delta.StopReason != "" {
					finish = anthFinishReason(ev.Delta.StopReason)
				}
				if ev.Usage != nil {
					outputTokens = ev.Usage.OutputTokens
				}
			case "error":
				msg := "unknown stream error"
				if ev.Error != nil {
					msg = ev.Error.Message
				}
				chunks <- StreamChunk{Err: &ProviderError{Provider: "anthropic", StatusCode: 500, Message: msg}, Provider: "anthropic", Model: model}
				return
			case "message_stop":
				usage := &Usage{
					PromptTokens:     inputTokens,
					CompletionTokens: outputTokens,
					TotalTokens:      inputTokens + outputTokens,
					CostUSD:          p.cost(model, anthWireUsage{InputTokens: inputTokens, OutputTokens: outputTokens}),
				}
				chunks <- StreamChunk{ID: id, Done: true, FinishReason: finish, Usage: usage, Provider: "anthropic", Model: model}
				return
			}
		}

		// Stream ended without message_stop: incomplete.
		chunks <- StreamChunk{Err: &ProviderError{Provider: "anthropic", StatusCode: 502, Message: "stream ended before message_stop"}, Provider: "anthropic", Model: model}
	}()
	return chunks, nil
}

func (p *AnthropicProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	return nil, fmt.Errorf("anthropic does not provide an embeddings API")
}
