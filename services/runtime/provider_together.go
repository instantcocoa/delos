package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TogetherProvider implements the Provider interface for Together AI.
// Together AI provides an OpenAI-compatible API with access to many open-source models.
type TogetherProvider struct {
	apiKey     string
	httpClient *http.Client
	models     []string
}

// NewTogetherProvider creates a new Together AI provider.
func NewTogetherProvider(apiKey string) *TogetherProvider {
	return &TogetherProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 120 * time.Second},
		models: []string{
			// Meta Llama models
			"meta-llama/Llama-3.3-70B-Instruct-Turbo",
			"meta-llama/Meta-Llama-3.1-405B-Instruct-Turbo",
			"meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo",
			"meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo",
			// Qwen models
			"Qwen/Qwen2.5-72B-Instruct-Turbo",
			"Qwen/Qwen2.5-7B-Instruct-Turbo",
			"Qwen/QwQ-32B-Preview",
			// Mistral models
			"mistralai/Mixtral-8x22B-Instruct-v0.1",
			"mistralai/Mixtral-8x7B-Instruct-v0.1",
			"mistralai/Mistral-7B-Instruct-v0.3",
			// DeepSeek models
			"deepseek-ai/DeepSeek-V3",
			"deepseek-ai/DeepSeek-R1",
			"deepseek-ai/DeepSeek-R1-Distill-Llama-70B",
			// Google models
			"google/gemma-2-27b-it",
			"google/gemma-2-9b-it",
			// Databricks
			"databricks/dbrx-instruct",
		},
	}
}

func (p *TogetherProvider) Name() string {
	return "together"
}

func (p *TogetherProvider) Models() []string {
	return p.models
}

func (p *TogetherProvider) Available(ctx context.Context) bool {
	return p.apiKey != ""
}

func (p *TogetherProvider) CostPer1KTokens() map[string]float64 {
	return map[string]float64{
		// Llama models
		"meta-llama/Llama-3.3-70B-Instruct-Turbo":       0.00088,
		"meta-llama/Meta-Llama-3.1-405B-Instruct-Turbo": 0.0035,
		"meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo":  0.00088,
		"meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo":   0.00018,
		// Qwen models
		"Qwen/Qwen2.5-72B-Instruct-Turbo": 0.0012,
		"Qwen/Qwen2.5-7B-Instruct-Turbo":  0.0003,
		"Qwen/QwQ-32B-Preview":            0.0012,
		// Mistral models
		"mistralai/Mixtral-8x22B-Instruct-v0.1": 0.0012,
		"mistralai/Mixtral-8x7B-Instruct-v0.1":  0.0006,
		"mistralai/Mistral-7B-Instruct-v0.3":    0.0002,
		// DeepSeek models
		"deepseek-ai/DeepSeek-V3":                  0.0009,
		"deepseek-ai/DeepSeek-R1":                  0.003,
		"deepseek-ai/DeepSeek-R1-Distill-Llama-70B": 0.0012,
		// Google models
		"google/gemma-2-27b-it": 0.0008,
		"google/gemma-2-9b-it":  0.0003,
		// Databricks
		"databricks/dbrx-instruct": 0.0012,
	}
}

// Together API uses OpenAI-compatible format
type togetherRequest struct {
	Model       string            `json:"model"`
	Messages    []togetherMessage `json:"messages"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
	Temperature float64           `json:"temperature,omitempty"`
	TopP        float64           `json:"top_p,omitempty"`
	Stop        []string          `json:"stop,omitempty"`
	Stream      bool              `json:"stream,omitempty"`
}

type togetherMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type togetherResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

type togetherStreamResponse struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

func (p *TogetherProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "meta-llama/Llama-3.3-70B-Instruct-Turbo"
	}

	messages := make([]togetherMessage, len(params.Messages))
	for i, m := range params.Messages {
		messages[i] = togetherMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	reqBody := togetherRequest{
		Model:       model,
		Messages:    messages,
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		Stop:        params.Stop,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.together.xyz/v1/chat/completions", bytes.NewReader(body))
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
		return nil, fmt.Errorf("Together AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var togetherResp togetherResponse
	if err := json.Unmarshal(respBody, &togetherResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(togetherResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}

	content := togetherResp.Choices[0].Message.Content
	usage := Usage{
		PromptTokens:     togetherResp.Usage.PromptTokens,
		CompletionTokens: togetherResp.Usage.CompletionTokens,
		TotalTokens:      togetherResp.Usage.TotalTokens,
		CostUSD:          p.calculateCost(model, togetherResp.Usage.TotalTokens),
	}

	return &CompletionResult{
		ID:      togetherResp.ID,
		Content: content,
		Message: Message{
			Role:    "assistant",
			Content: content,
		},
		Provider: p.Name(),
		Model:    model,
		Usage:    usage,
	}, nil
}

func (p *TogetherProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "meta-llama/Llama-3.3-70B-Instruct-Turbo"
	}

	messages := make([]togetherMessage, len(params.Messages))
	for i, m := range params.Messages {
		messages[i] = togetherMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	reqBody := togetherRequest{
		Model:       model,
		Messages:    messages,
		MaxTokens:   params.MaxTokens,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		Stop:        params.Stop,
		Stream:      true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.together.xyz/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Together AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

func (p *TogetherProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
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

		var streamResp togetherStreamResponse
		if err := json.Unmarshal([]byte(event.Data), &streamResp); err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("failed to parse stream chunk: %w", err),
				Provider: p.Name(),
				Model:    model,
			}
			continue
		}

		if id == "" {
			id = streamResp.ID
		}

		for _, choice := range streamResp.Choices {
			if choice.Delta.Content != "" {
				fullContent.WriteString(choice.Delta.Content)
				ch <- StreamChunk{
					ID:       id,
					Delta:    choice.Delta.Content,
					Done:     false,
					Provider: p.Name(),
					Model:    model,
				}
			}
		}

		// Capture usage if provided
		if streamResp.Usage != nil {
			promptTokens = streamResp.Usage.PromptTokens
			completionTokens = streamResp.Usage.CompletionTokens
		}
	}

	totalTokens := promptTokens + completionTokens
	cost := p.calculateCost(model, totalTokens)

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

func (p *TogetherProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = "togethercomputer/m2-bert-80M-8k-retrieval"
	}

	reqBody := map[string]interface{}{
		"model": model,
		"input": params.Texts,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.together.xyz/v1/embeddings", bytes.NewReader(body))
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
		return nil, fmt.Errorf("Together AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var embedResp struct {
		Object string `json:"object"`
		Data   []struct {
			Object    string    `json:"object"`
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
		Model string `json:"model"`
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
		Model:      model,
		Provider:   p.Name(),
	}, nil
}

func (p *TogetherProvider) calculateCost(model string, totalTokens int) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.001
	}
	return float64(totalTokens) / 1000 * costPer1K
}
