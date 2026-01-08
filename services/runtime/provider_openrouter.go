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
	openRouterBaseURL = "https://openrouter.ai/api/v1"
)

// OpenRouterProvider implements the Provider interface for OpenRouter.
// OpenRouter provides access to multiple LLM providers through a single API.
type OpenRouterProvider struct {
	apiKey     string
	httpClient *http.Client
	siteURL    string // Optional: for rankings
	siteName   string // Optional: for rankings
	models     []string
}

// NewOpenRouterProvider creates a new OpenRouter provider.
func NewOpenRouterProvider(apiKey string, opts ...OpenRouterOption) *OpenRouterProvider {
	p := &OpenRouterProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		models: []string{
			// Popular models available on OpenRouter
			"anthropic/claude-3.5-sonnet",
			"anthropic/claude-3-opus",
			"anthropic/claude-3-haiku",
			"openai/gpt-4o",
			"openai/gpt-4o-mini",
			"openai/gpt-4-turbo",
			"google/gemini-pro-1.5",
			"google/gemini-flash-1.5",
			"meta-llama/llama-3.1-405b-instruct",
			"meta-llama/llama-3.1-70b-instruct",
			"meta-llama/llama-3.1-8b-instruct",
			"mistralai/mistral-large",
			"mistralai/mixtral-8x7b-instruct",
			"deepseek/deepseek-chat",
			"qwen/qwen-2.5-72b-instruct",
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// OpenRouterOption configures the OpenRouter provider.
type OpenRouterOption func(*OpenRouterProvider)

// WithSiteInfo sets the site URL and name for OpenRouter rankings.
func WithSiteInfo(url, name string) OpenRouterOption {
	return func(p *OpenRouterProvider) {
		p.siteURL = url
		p.siteName = name
	}
}

func (p *OpenRouterProvider) Name() string {
	return "openrouter"
}

func (p *OpenRouterProvider) Models() []string {
	return p.models
}

func (p *OpenRouterProvider) Available(ctx context.Context) bool {
	return p.apiKey != ""
}

func (p *OpenRouterProvider) CostPer1KTokens() map[string]float64 {
	// OpenRouter pricing varies by model - these are approximate
	return map[string]float64{
		"anthropic/claude-3.5-sonnet":        0.003,
		"anthropic/claude-3-opus":            0.015,
		"anthropic/claude-3-haiku":           0.00025,
		"openai/gpt-4o":                      0.005,
		"openai/gpt-4o-mini":                 0.00015,
		"openai/gpt-4-turbo":                 0.01,
		"google/gemini-pro-1.5":              0.00125,
		"google/gemini-flash-1.5":            0.000075,
		"meta-llama/llama-3.1-405b-instruct": 0.003,
		"meta-llama/llama-3.1-70b-instruct":  0.0008,
		"meta-llama/llama-3.1-8b-instruct":   0.0001,
		"mistralai/mistral-large":            0.002,
		"mistralai/mixtral-8x7b-instruct":    0.0005,
		"deepseek/deepseek-chat":             0.00014,
		"qwen/qwen-2.5-72b-instruct":         0.0004,
	}
}

// OpenRouter uses OpenAI-compatible API types
type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type openRouterRequest struct {
	Model       string              `json:"model"`
	Messages    []openRouterMessage `json:"messages"`
	Temperature float64             `json:"temperature,omitempty"`
	MaxTokens   int                 `json:"max_tokens,omitempty"`
	TopP        float64             `json:"top_p,omitempty"`
	Stop        []string            `json:"stop,omitempty"`
	Stream      bool                `json:"stream,omitempty"`
}

type openRouterChoice struct {
	Index        int               `json:"index"`
	Message      openRouterMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type openRouterUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openRouterResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Choices []openRouterChoice `json:"choices"`
	Usage   openRouterUsage    `json:"usage"`
}

type openRouterError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (p *OpenRouterProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "anthropic/claude-3.5-sonnet" // Default to Claude 3.5 Sonnet
	}

	messages := make([]openRouterMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		messages = append(messages, openRouterMessage{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		})
	}

	reqBody := openRouterRequest{
		Model:       model,
		Messages:    messages,
		Temperature: params.Temperature,
		MaxTokens:   params.MaxTokens,
		TopP:        params.TopP,
		Stop:        params.Stop,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openRouterBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if p.siteURL != "" {
		req.Header.Set("HTTP-Referer", p.siteURL)
	}
	if p.siteName != "" {
		req.Header.Set("X-Title", p.siteName)
	}

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
		var apiErr openRouterError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("OpenRouter API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("OpenRouter API error: status %d", resp.StatusCode)
	}

	var orResp openRouterResponse
	if err := json.Unmarshal(respBody, &orResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(orResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := orResp.Choices[0]
	cost := p.calculateCost(model, orResp.Usage)

	return &CompletionResult{
		ID:      orResp.ID,
		Content: choice.Message.Content,
		Message: Message{
			Role:    choice.Message.Role,
			Content: choice.Message.Content,
		},
		Provider: p.Name(),
		Model:    orResp.Model,
		Usage: Usage{
			PromptTokens:     orResp.Usage.PromptTokens,
			CompletionTokens: orResp.Usage.CompletionTokens,
			TotalTokens:      orResp.Usage.TotalTokens,
			CostUSD:          cost,
		},
	}, nil
}

func (p *OpenRouterProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "anthropic/claude-3.5-sonnet"
	}

	messages := make([]openRouterMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		messages = append(messages, openRouterMessage{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		})
	}

	reqBody := openRouterRequest{
		Model:       model,
		Messages:    messages,
		Temperature: params.Temperature,
		MaxTokens:   params.MaxTokens,
		TopP:        params.TopP,
		Stop:        params.Stop,
		Stream:      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openRouterBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	if p.siteURL != "" {
		req.Header.Set("HTTP-Referer", p.siteURL)
	}
	if p.siteName != "" {
		req.Header.Set("X-Title", p.siteName)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var apiErr openRouterError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("OpenRouter API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("OpenRouter API error: status %d", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

// openRouterStreamChoice represents a choice in a streaming response.
type openRouterStreamChoice struct {
	Index int `json:"index"`
	Delta struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// openRouterStreamResponse represents a streaming response chunk.
type openRouterStreamResponse struct {
	ID      string                   `json:"id"`
	Model   string                   `json:"model"`
	Choices []openRouterStreamChoice `json:"choices"`
	Usage   *openRouterUsage         `json:"usage,omitempty"`
}

func (p *OpenRouterProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	events := make(chan SSEEvent, 100)
	go ParseSSE(resp.Body, events)

	var fullContent strings.Builder
	var id string
	var promptTokens, completionTokens int

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

		if event.Data == "[DONE]" {
			break
		}

		var streamResp openRouterStreamResponse
		if err := json.Unmarshal([]byte(event.Data), &streamResp); err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("failed to parse stream chunk: %w", err),
				Provider: p.Name(),
				Model:    model,
			}
			continue
		}

		if id == "" && streamResp.ID != "" {
			id = streamResp.ID
		}

		if len(streamResp.Choices) > 0 {
			delta := streamResp.Choices[0].Delta.Content
			if delta != "" {
				fullContent.WriteString(delta)
				ch <- StreamChunk{
					ID:       id,
					Delta:    delta,
					Done:     false,
					Provider: p.Name(),
					Model:    model,
				}
			}
		}

		if streamResp.Usage != nil {
			promptTokens = streamResp.Usage.PromptTokens
			completionTokens = streamResp.Usage.CompletionTokens
		}
	}

	totalTokens := promptTokens + completionTokens
	cost := p.calculateCost(model, openRouterUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	})

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
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}
}

func (p *OpenRouterProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	// OpenRouter doesn't have a dedicated embeddings endpoint
	// Some models support it through the completions API but it's not standard
	return nil, fmt.Errorf("OpenRouter does not support embeddings directly - use a dedicated embedding provider")
}

func (p *OpenRouterProvider) calculateCost(model string, usage openRouterUsage) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.001 // default fallback
	}
	return float64(usage.TotalTokens) / 1000 * costPer1K
}
