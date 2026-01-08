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
	geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

// GeminiProvider implements the Provider interface for Google Gemini.
type GeminiProvider struct {
	apiKey     string
	httpClient *http.Client
	models     []string
}

// NewGeminiProvider creates a new Gemini provider.
func NewGeminiProvider(apiKey string) *GeminiProvider {
	return &GeminiProvider{
		apiKey:     apiKey,
		httpClient: &http.Client{},
		models: []string{
			"gemini-1.5-pro",
			"gemini-1.5-flash",
			"gemini-1.5-flash-8b",
			"gemini-pro",
		},
	}
}

func (p *GeminiProvider) Name() string {
	return "gemini"
}

func (p *GeminiProvider) Models() []string {
	return p.models
}

func (p *GeminiProvider) Available(ctx context.Context) bool {
	return p.apiKey != ""
}

func (p *GeminiProvider) CostPer1KTokens() map[string]float64 {
	return map[string]float64{
		"gemini-1.5-pro":      0.00125,   // $1.25/M input
		"gemini-1.5-flash":    0.000075,  // $0.075/M input
		"gemini-1.5-flash-8b": 0.0000375, // $0.0375/M input
		"gemini-pro":          0.0005,    // $0.50/M input
	}
}

// Gemini API types
type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiGenerationConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
}

type geminiError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (p *GeminiProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "gemini-1.5-flash"
	}

	// Convert messages to Gemini format
	var contents []geminiContent
	var systemInstruction *geminiContent

	for _, m := range params.Messages {
		if m.Role == "system" {
			systemInstruction = &geminiContent{
				Role:  "user", // System instructions use "user" role in Gemini
				Parts: []geminiPart{{Text: m.Content}},
			}
			continue
		}

		// Map roles: assistant -> model
		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	reqBody := geminiRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}

	// Add generation config if any params set
	if params.Temperature > 0 || params.MaxTokens > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.GenerationConfig = &geminiGenerationConfig{
			Temperature:     params.Temperature,
			MaxOutputTokens: params.MaxTokens,
			TopP:            params.TopP,
			StopSequences:   params.Stop,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", geminiBaseURL, model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
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
		var apiErr geminiError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("Gemini API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("Gemini API error: status %d", resp.StatusCode)
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(respBody, &geminiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(geminiResp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	candidate := geminiResp.Candidates[0]
	var content string
	for _, part := range candidate.Content.Parts {
		content += part.Text
	}

	var usage Usage
	if geminiResp.UsageMetadata != nil {
		usage = Usage{
			PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
		}
		usage.CostUSD = p.calculateCost(model, usage.TotalTokens)
	}

	return &CompletionResult{
		ID:      "", // Gemini doesn't return an ID
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

func (p *GeminiProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "gemini-1.5-flash"
	}

	// Convert messages to Gemini format
	var contents []geminiContent
	var systemInstruction *geminiContent

	for _, m := range params.Messages {
		if m.Role == "system" {
			systemInstruction = &geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			}
			continue
		}

		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	reqBody := geminiRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}

	if params.Temperature > 0 || params.MaxTokens > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.GenerationConfig = &geminiGenerationConfig{
			Temperature:     params.Temperature,
			MaxOutputTokens: params.MaxTokens,
			TopP:            params.TopP,
			StopSequences:   params.Stop,
		}
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?key=%s&alt=sse", geminiBaseURL, model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
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
		var apiErr geminiError
		if err := json.Unmarshal(respBody, &apiErr); err == nil {
			return nil, fmt.Errorf("Gemini API error: %s", apiErr.Error.Message)
		}
		return nil, fmt.Errorf("Gemini API error: status %d", resp.StatusCode)
	}

	ch := make(chan StreamChunk, 100)
	go p.streamResponse(ctx, resp, model, ch)
	return ch, nil
}

func (p *GeminiProvider) streamResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
	defer close(ch)
	defer resp.Body.Close()

	events := make(chan SSEEvent, 100)
	go ParseSSE(resp.Body, events)

	var fullContent strings.Builder
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

		var geminiResp geminiResponse
		if err := json.Unmarshal([]byte(event.Data), &geminiResp); err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("failed to parse stream chunk: %w", err),
				Provider: p.Name(),
				Model:    model,
			}
			continue
		}

		// Extract text from candidates
		for _, candidate := range geminiResp.Candidates {
			for _, part := range candidate.Content.Parts {
				if part.Text != "" {
					fullContent.WriteString(part.Text)
					ch <- StreamChunk{
						Delta:    part.Text,
						Done:     false,
						Provider: p.Name(),
						Model:    model,
					}
				}
			}
		}

		// Capture usage if present
		if geminiResp.UsageMetadata != nil {
			promptTokens = geminiResp.UsageMetadata.PromptTokenCount
			completionTokens = geminiResp.UsageMetadata.CandidatesTokenCount
		}
	}

	totalTokens := promptTokens + completionTokens
	cost := p.calculateCost(model, totalTokens)

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
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}
}

func (p *GeminiProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = "text-embedding-004"
	}

	// Gemini embeds one text at a time, so we need to loop
	embeddings := make([]Embedding, 0, len(params.Texts))

	for _, text := range params.Texts {
		reqBody := map[string]interface{}{
			"model": fmt.Sprintf("models/%s", model),
			"content": map[string]interface{}{
				"parts": []map[string]string{
					{"text": text},
				},
			},
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		url := fmt.Sprintf("%s/models/%s:embedContent?key=%s", geminiBaseURL, model, p.apiKey)
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
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
			var apiErr geminiError
			if err := json.Unmarshal(respBody, &apiErr); err == nil {
				return nil, fmt.Errorf("Gemini API error: %s", apiErr.Error.Message)
			}
			return nil, fmt.Errorf("Gemini API error: status %d", resp.StatusCode)
		}

		var embedResp struct {
			Embedding struct {
				Values []float32 `json:"values"`
			} `json:"embedding"`
		}

		if err := json.Unmarshal(respBody, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		embeddings = append(embeddings, Embedding{
			Values:     embedResp.Embedding.Values,
			Dimensions: len(embedResp.Embedding.Values),
		})
	}

	return &EmbedResult{
		Embeddings: embeddings,
		Model:      model,
		Provider:   p.Name(),
	}, nil
}

func (p *GeminiProvider) calculateCost(model string, totalTokens int) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.001 // default
	}
	return float64(totalTokens) / 1000 * costPer1K
}
