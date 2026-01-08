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
	openAIBaseURL = "https://api.openai.com/v1"
)

// OpenAIProvider implements the Provider interface for OpenAI.
type OpenAIProvider struct {
	apiKey     string
	httpClient *http.Client
	models     []string
}

// NewOpenAIProvider creates a new OpenAI provider.
func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		models: []string{
			"gpt-4o",
			"gpt-4o-mini",
			"gpt-4-turbo",
			"gpt-4",
			"gpt-3.5-turbo",
		},
	}
}

func (p *OpenAIProvider) Name() string {
	return "openai"
}

func (p *OpenAIProvider) Models() []string {
	return p.models
}

func (p *OpenAIProvider) Available(ctx context.Context) bool {
	return p.apiKey != ""
}

func (p *OpenAIProvider) CostPer1KTokens() map[string]float64 {
	return map[string]float64{
		"gpt-4o":        0.005,
		"gpt-4o-mini":   0.00015,
		"gpt-4-turbo":   0.01,
		"gpt-4":         0.03,
		"gpt-3.5-turbo": 0.0005,
	}
}

// OpenAI API types
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	TopP        float64         `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	FinishReason string        `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Created int64          `json:"created"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

type openAIError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func (p *OpenAIProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	messages := make([]openAIMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		messages = append(messages, openAIMessage{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		})
	}

	reqBody := openAIRequest{
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

	req, err := http.NewRequestWithContext(ctx, "POST", openAIBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
		var apiErr openAIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("OpenAI API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	choice := openAIResp.Choices[0]
	cost := p.calculateCost(model, openAIResp.Usage)

	return &CompletionResult{
		ID:      openAIResp.ID,
		Content: choice.Message.Content,
		Message: Message{
			Role:    choice.Message.Role,
			Content: choice.Message.Content,
		},
		Provider: p.Name(),
		Model:    openAIResp.Model,
		Usage: Usage{
			PromptTokens:     openAIResp.Usage.PromptTokens,
			CompletionTokens: openAIResp.Usage.CompletionTokens,
			TotalTokens:      openAIResp.Usage.TotalTokens,
			CostUSD:          cost,
		},
	}, nil
}

func (p *OpenAIProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "gpt-4o-mini"
	}

	messages := make([]openAIMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		messages = append(messages, openAIMessage{
			Role:    m.Role,
			Content: m.Content,
			Name:    m.Name,
		})
	}

	reqBody := openAIRequest{
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

	req, err := http.NewRequestWithContext(ctx, "POST", openAIBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var apiErr openAIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("OpenAI API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

// openAIStreamChoice represents a choice in a streaming response.
type openAIStreamChoice struct {
	Index int `json:"index"`
	Delta struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// openAIStreamResponse represents a streaming response chunk.
type openAIStreamResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIStreamChoice `json:"choices"`
	Usage   *openAIUsage         `json:"usage,omitempty"`
}

func (p *OpenAIProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
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

		// Check for end of stream
		if event.Data == "[DONE]" {
			break
		}

		var streamResp openAIStreamResponse
		if err := json.Unmarshal([]byte(event.Data), &streamResp); err != nil {
			// Report JSON parse error but continue - might be a partial chunk
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

		// Capture usage if present (OpenAI includes it in the final chunk)
		if streamResp.Usage != nil {
			promptTokens = streamResp.Usage.PromptTokens
			completionTokens = streamResp.Usage.CompletionTokens
		}
	}

	// Calculate cost
	totalTokens := promptTokens + completionTokens
	cost := p.calculateCost(model, openAIUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	})

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
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}
}

func (p *OpenAIProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	reqBody := map[string]interface{}{
		"input": params.Texts,
		"model": model,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", openAIBaseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
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
		var apiErr openAIError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("OpenAI API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("OpenAI API error: status %d", resp.StatusCode)
	}

	var embedResp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Model string `json:"model"`
		Usage struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(respBody, &embedResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	embeddings := make([]Embedding, len(embedResp.Data))
	for i, data := range embedResp.Data {
		embeddings[i] = Embedding{
			Values:     data.Embedding,
			Dimensions: len(data.Embedding),
		}
	}

	return &EmbedResult{
		Embeddings: embeddings,
		Model:      embedResp.Model,
		Provider:   p.Name(),
		Usage: Usage{
			PromptTokens: embedResp.Usage.PromptTokens,
			TotalTokens:  embedResp.Usage.TotalTokens,
		},
	}, nil
}

func (p *OpenAIProvider) calculateCost(model string, usage openAIUsage) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.01 // default
	}
	return float64(usage.TotalTokens) / 1000 * costPer1K
}
