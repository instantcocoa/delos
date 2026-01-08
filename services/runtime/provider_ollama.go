package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultOllamaURL = "http://localhost:11434"
)

// OllamaProvider implements the Provider interface for Ollama (local LLMs).
type OllamaProvider struct {
	baseURL    string
	httpClient *http.Client
	models     []string
	modelsMu   sync.RWMutex
	lastCheck  time.Time
}

// NewOllamaProvider creates a new Ollama provider.
func NewOllamaProvider(baseURL string) *OllamaProvider {
	if baseURL == "" {
		baseURL = defaultOllamaURL
	}
	return &OllamaProvider{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{Timeout: 300 * time.Second}, // Long timeout for local inference
		models:     []string{},
	}
}

func (p *OllamaProvider) Name() string {
	return "ollama"
}

func (p *OllamaProvider) Models() []string {
	p.modelsMu.RLock()
	if time.Since(p.lastCheck) < time.Minute && len(p.models) > 0 {
		models := p.models
		p.modelsMu.RUnlock()
		return models
	}
	p.modelsMu.RUnlock()

	p.refreshModels()

	p.modelsMu.RLock()
	defer p.modelsMu.RUnlock()
	return p.models
}

func (p *OllamaProvider) refreshModels() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()

	p.models = make([]string, len(result.Models))
	for i, m := range result.Models {
		p.models[i] = m.Name
	}
	p.lastCheck = time.Now()
}

func (p *OllamaProvider) Available(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", p.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (p *OllamaProvider) CostPer1KTokens() map[string]float64 {
	// All local models are free
	models := p.Models()
	costs := make(map[string]float64, len(models))
	for _, model := range models {
		costs[model] = 0.0
	}
	return costs
}

// Ollama API types
type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64  `json:"temperature,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"` // max_tokens equivalent
	TopP        float64  `json:"top_p,omitempty"`
	Stop        []string `json:"stop,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

type ollamaResponse struct {
	Model           string        `json:"model"`
	CreatedAt       string        `json:"created_at"`
	Message         ollamaMessage `json:"message"`
	Done            bool          `json:"done"`
	TotalDuration   int64         `json:"total_duration"`
	PromptEvalCount int           `json:"prompt_eval_count"`
	EvalCount       int           `json:"eval_count"`
}

type ollamaError struct {
	Error string `json:"error"`
}

func (p *OllamaProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "llama3.2" // Default model
	}

	messages := make([]ollamaMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		messages = append(messages, ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	reqBody := ollamaRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}

	// Add options if any params set
	if params.Temperature > 0 || params.MaxTokens > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.Options = &ollamaOptions{
			Temperature: params.Temperature,
			NumPredict:  params.MaxTokens,
			TopP:        params.TopP,
			Stop:        params.Stop,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

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
		var apiErr ollamaError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("Ollama API error: %s", apiErr.Error)
		}
		return nil, fmt.Errorf("Ollama API error: status %d", resp.StatusCode)
	}

	var ollamaResp ollamaResponse
	if err := json.Unmarshal(respBody, &ollamaResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &CompletionResult{
		ID:      "", // Ollama doesn't return an ID
		Content: ollamaResp.Message.Content,
		Message: Message{
			Role:    ollamaResp.Message.Role,
			Content: ollamaResp.Message.Content,
		},
		Provider: p.Name(),
		Model:    ollamaResp.Model,
		Usage: Usage{
			PromptTokens:     ollamaResp.PromptEvalCount,
			CompletionTokens: ollamaResp.EvalCount,
			TotalTokens:      ollamaResp.PromptEvalCount + ollamaResp.EvalCount,
			CostUSD:          0, // Local models are free
		},
	}, nil
}

func (p *OllamaProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "llama3.2"
	}

	messages := make([]ollamaMessage, 0, len(params.Messages))
	for _, m := range params.Messages {
		messages = append(messages, ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	reqBody := ollamaRequest{
		Model:    model,
		Messages: messages,
		Stream:   true,
	}

	if params.Temperature > 0 || params.MaxTokens > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.Options = &ollamaOptions{
			Temperature: params.Temperature,
			NumPredict:  params.MaxTokens,
			TopP:        params.TopP,
			Stop:        params.Stop,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		var apiErr ollamaError
		if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != "" {
			return nil, fmt.Errorf("Ollama API error: %s", apiErr.Error)
		}
		return nil, fmt.Errorf("Ollama API error: status %d", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

func (p *OllamaProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	// Ollama uses newline-delimited JSON (NDJSON), not SSE
	lines := make(chan NDJSONLine, 100)
	go ParseNDJSON(resp.Body, lines)

	var fullContent strings.Builder
	var promptTokens, completionTokens int

	for line := range lines {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check for read errors
		if line.Err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("stream read error: %w", line.Err),
				Done:     true,
				Provider: p.Name(),
				Model:    model,
			}
			return
		}

		var ollamaResp ollamaResponse
		if err := json.Unmarshal([]byte(line.Data), &ollamaResp); err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("failed to parse stream chunk: %w", err),
				Provider: p.Name(),
				Model:    model,
			}
			continue
		}

		if ollamaResp.Message.Content != "" {
			fullContent.WriteString(ollamaResp.Message.Content)
			ch <- StreamChunk{
				Delta:    ollamaResp.Message.Content,
				Done:     false,
				Provider: p.Name(),
				Model:    model,
			}
		}

		// Final response has done=true with usage stats
		if ollamaResp.Done {
			promptTokens = ollamaResp.PromptEvalCount
			completionTokens = ollamaResp.EvalCount
		}
	}

	// Send final chunk
	ch <- StreamChunk{
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
			TotalTokens:      promptTokens + completionTokens,
			CostUSD:          0, // Local models are free
		},
	}
}

func (p *OllamaProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = "nomic-embed-text" // Default embedding model
	}

	// Ollama only supports one text at a time
	embeddings := make([]Embedding, 0, len(params.Texts))

	for _, text := range params.Texts {
		reqBody := map[string]interface{}{
			"model":  model,
			"prompt": text,
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", p.baseURL+"/api/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

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
			var apiErr ollamaError
			if err := json.Unmarshal(respBody, &apiErr); err == nil && apiErr.Error != "" {
				return nil, fmt.Errorf("Ollama API error: %s", apiErr.Error)
			}
			return nil, fmt.Errorf("Ollama API error: status %d", resp.StatusCode)
		}

		var embedResp struct {
			Embedding []float32 `json:"embedding"`
		}

		if err := json.Unmarshal(respBody, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		embeddings = append(embeddings, Embedding{
			Values:     embedResp.Embedding,
			Dimensions: len(embedResp.Embedding),
		})
	}

	return &EmbedResult{
		Embeddings: embeddings,
		Model:      model,
		Provider:   p.Name(),
	}, nil
}
