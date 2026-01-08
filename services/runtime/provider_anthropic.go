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
	anthropicBaseURL = "https://api.anthropic.com/v1"
	anthropicVersion = "2023-06-01"
)

// AnthropicProvider implements the Provider interface for Anthropic.
type AnthropicProvider struct {
	apiKey     string
	httpClient *http.Client
	models     []string
}

// NewAnthropicProvider creates a new Anthropic provider.
func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		models: []string{
			"claude-sonnet-4-20250514",
			"claude-opus-4-20250514",
			"claude-3-5-sonnet-20241022",
			"claude-3-5-haiku-20241022",
			"claude-3-opus-20240229",
		},
	}
}

func (p *AnthropicProvider) Name() string {
	return "anthropic"
}

func (p *AnthropicProvider) Models() []string {
	return p.models
}

func (p *AnthropicProvider) Available(ctx context.Context) bool {
	return p.apiKey != ""
}

func (p *AnthropicProvider) CostPer1KTokens() map[string]float64 {
	return map[string]float64{
		"claude-sonnet-4-20250514":   0.003,
		"claude-opus-4-20250514":     0.015,
		"claude-3-5-sonnet-20241022": 0.003,
		"claude-3-5-haiku-20241022":  0.0008,
		"claude-3-opus-20240229":     0.015,
	}
}

// Anthropic API types
type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicRequest struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	Messages    []anthropicMessage `json:"messages"`
	System      string             `json:"system,omitempty"`
	Temperature float64            `json:"temperature,omitempty"`
	TopP        float64            `json:"top_p,omitempty"`
	StopSeqs    []string           `json:"stop_sequences,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicResponse struct {
	ID           string                  `json:"id"`
	Type         string                  `json:"type"`
	Role         string                  `json:"role"`
	Content      []anthropicContentBlock `json:"content"`
	Model        string                  `json:"model"`
	StopReason   string                  `json:"stop_reason"`
	StopSequence string                  `json:"stop_sequence"`
	Usage        anthropicUsage          `json:"usage"`
}

type anthropicError struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *AnthropicProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	maxTokens := params.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	var system string
	messages := make([]anthropicMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, anthropicMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	reqBody := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Messages:    messages,
		System:      system,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		StopSeqs:    params.Stop,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr anthropicError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("Anthropic API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("Anthropic API error: status %d", resp.StatusCode)
	}

	var anthropicResp anthropicResponse
	if err := json.Unmarshal(respBody, &anthropicResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	var content string
	for _, block := range anthropicResp.Content {
		if block.Type == "text" {
			content += block.Text
		}
	}

	totalTokens := anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens
	cost := p.calculateCost(model, totalTokens)

	return &CompletionResult{
		ID:      anthropicResp.ID,
		Content: content,
		Message: Message{
			Role:    anthropicResp.Role,
			Content: content,
		},
		Provider: p.Name(),
		Model:    anthropicResp.Model,
		Usage: Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}, nil
}

func (p *AnthropicProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "claude-3-5-sonnet-20241022"
	}

	maxTokens := params.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	var system string
	messages := make([]anthropicMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, anthropicMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	reqBody := anthropicRequest{
		Model:       model,
		MaxTokens:   maxTokens,
		Messages:    messages,
		System:      system,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		StopSeqs:    params.Stop,
		Stream:      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", anthropicBaseURL+"/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var apiErr anthropicError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("Anthropic API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("Anthropic API error: status %d", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

// Anthropic streaming event types
type anthropicMessageStart struct {
	Type    string `json:"type"`
	Message struct {
		ID    string         `json:"id"`
		Type  string         `json:"type"`
		Role  string         `json:"role"`
		Model string         `json:"model"`
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

type anthropicContentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

type anthropicMessageDelta struct {
	Type  string `json:"type"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (p *AnthropicProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	events := make(chan SSEEvent, 100)
	go ParseSSE(resp.Body, events)

	var fullContent strings.Builder
	var id string
	var inputTokens, outputTokens int

	for event := range events {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check for SSE parse/read errors
		if event.Err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("stream read error: %w", event.Err),
				Done:     true,
				Provider: p.Name(),
				Model:    model,
			}
			return
		}

		switch event.Event {
		case "message_start":
			var msg anthropicMessageStart
			if err := json.Unmarshal([]byte(event.Data), &msg); err != nil {
				ch <- StreamChunk{
					Err:      fmt.Errorf("failed to parse message_start: %w", err),
					Provider: p.Name(),
					Model:    model,
				}
				continue
			}
			id = msg.Message.ID
			inputTokens = msg.Message.Usage.InputTokens

		case "content_block_delta":
			var delta anthropicContentBlockDelta
			if err := json.Unmarshal([]byte(event.Data), &delta); err != nil {
				ch <- StreamChunk{
					Err:      fmt.Errorf("failed to parse content_block_delta: %w", err),
					Provider: p.Name(),
					Model:    model,
				}
				continue
			}
			if delta.Delta.Text != "" {
				fullContent.WriteString(delta.Delta.Text)
				ch <- StreamChunk{
					ID:       id,
					Delta:    delta.Delta.Text,
					Done:     false,
					Provider: p.Name(),
					Model:    model,
				}
			}

		case "message_delta":
			var msg anthropicMessageDelta
			if err := json.Unmarshal([]byte(event.Data), &msg); err != nil {
				ch <- StreamChunk{
					Err:      fmt.Errorf("failed to parse message_delta: %w", err),
					Provider: p.Name(),
					Model:    model,
				}
				continue
			}
			outputTokens = msg.Usage.OutputTokens

		case "message_stop":
			// End of message - will exit loop
		}
	}

	totalTokens := inputTokens + outputTokens
	cost := p.calculateCost(model, totalTokens)

	// Send final chunk
	ch <- StreamChunk{
		ID:       id,
		Done:     true,
		Provider: p.Name(),
		Model:    model,
		Message: &Message{
			Role:    "assistant",
			Content: fullContent.String(),
		},
		Usage: &Usage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}
}

func (p *AnthropicProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	// Anthropic doesn't have an embeddings API
	return nil, fmt.Errorf("Anthropic does not support embeddings")
}

func (p *AnthropicProvider) calculateCost(model string, totalTokens int) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.003 // default
	}
	return float64(totalTokens) / 1000 * costPer1K
}
