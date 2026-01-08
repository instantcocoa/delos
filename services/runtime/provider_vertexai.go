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

// VertexAIProvider implements the Provider interface for Google Cloud Vertex AI.
type VertexAIProvider struct {
	projectID   string
	location    string
	accessToken string // OAuth2 access token
	httpClient  *http.Client
	models      []string
}

// NewVertexAIProvider creates a new Vertex AI provider.
// accessToken should be a valid Google Cloud OAuth2 access token.
func NewVertexAIProvider(projectID, location, accessToken string) *VertexAIProvider {
	if location == "" {
		location = "us-central1"
	}
	return &VertexAIProvider{
		projectID:   projectID,
		location:    location,
		accessToken: accessToken,
		httpClient:  &http.Client{Timeout: 120 * time.Second},
		models: []string{
			"gemini-1.5-pro-002",
			"gemini-1.5-flash-002",
			"gemini-1.5-pro",
			"gemini-1.5-flash",
			"gemini-1.0-pro",
			"claude-3-5-sonnet-v2@20241022",
			"claude-3-5-haiku@20241022",
			"claude-3-opus@20240229",
			"claude-3-sonnet@20240229",
		},
	}
}

func (p *VertexAIProvider) Name() string {
	return "vertexai"
}

func (p *VertexAIProvider) Models() []string {
	return p.models
}

func (p *VertexAIProvider) Available(ctx context.Context) bool {
	return p.projectID != "" && p.accessToken != ""
}

func (p *VertexAIProvider) CostPer1KTokens() map[string]float64 {
	return map[string]float64{
		"gemini-1.5-pro-002":            0.00125,
		"gemini-1.5-flash-002":          0.000075,
		"gemini-1.5-pro":                0.00125,
		"gemini-1.5-flash":              0.000075,
		"gemini-1.0-pro":                0.0005,
		"claude-3-5-sonnet-v2@20241022": 0.003,
		"claude-3-5-haiku@20241022":     0.0008,
		"claude-3-opus@20240229":        0.015,
		"claude-3-sonnet@20240229":      0.003,
	}
}

// Vertex AI Gemini API types
type vertexContent struct {
	Role  string       `json:"role"`
	Parts []vertexPart `json:"parts"`
}

type vertexPart struct {
	Text string `json:"text"`
}

type vertexGenerationConfig struct {
	Temperature     float64  `json:"temperature,omitempty"`
	MaxOutputTokens int      `json:"maxOutputTokens,omitempty"`
	TopP            float64  `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type vertexRequest struct {
	Contents          []vertexContent         `json:"contents"`
	SystemInstruction *vertexContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *vertexGenerationConfig `json:"generationConfig,omitempty"`
}

type vertexCandidate struct {
	Content      vertexContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type vertexUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type vertexResponse struct {
	Candidates    []vertexCandidate    `json:"candidates"`
	UsageMetadata *vertexUsageMetadata `json:"usageMetadata"`
}

func (p *VertexAIProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	model := params.Model
	if model == "" {
		model = "gemini-1.5-flash-002"
	}

	// Check if it's a Claude model (Vertex AI also hosts Claude)
	if strings.HasPrefix(model, "claude") {
		return p.completeAnthropic(ctx, params, model)
	}

	// Gemini model
	var contents []vertexContent
	var systemInstruction *vertexContent

	for _, m := range params.Messages {
		if m.Role == "system" {
			systemInstruction = &vertexContent{
				Role:  "user",
				Parts: []vertexPart{{Text: m.Content}},
			}
			continue
		}

		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, vertexContent{
			Role:  role,
			Parts: []vertexPart{{Text: m.Content}},
		})
	}

	reqBody := vertexRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}

	if params.Temperature > 0 || params.MaxTokens > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.GenerationConfig = &vertexGenerationConfig{
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

	endpoint := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		p.location, p.projectID, p.location, model)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.accessToken)
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
		return nil, fmt.Errorf("Vertex AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var vertexResp vertexResponse
	if err := json.Unmarshal(respBody, &vertexResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(vertexResp.Candidates) == 0 {
		return nil, fmt.Errorf("no candidates in response")
	}

	candidate := vertexResp.Candidates[0]
	var content string
	for _, part := range candidate.Content.Parts {
		content += part.Text
	}

	var usage Usage
	if vertexResp.UsageMetadata != nil {
		usage = Usage{
			PromptTokens:     vertexResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: vertexResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      vertexResp.UsageMetadata.TotalTokenCount,
		}
		usage.CostUSD = p.calculateCost(model, usage.TotalTokens)
	}

	return &CompletionResult{
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

// completeAnthropic handles Claude models on Vertex AI
func (p *VertexAIProvider) completeAnthropic(ctx context.Context, params CompletionParams, model string) (*CompletionResult, error) {
	maxTokens := params.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	var system string
	var messages []map[string]string

	for _, m := range params.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	reqBody := map[string]interface{}{
		"anthropic_version": "vertex-2023-10-16",
		"max_tokens":        maxTokens,
		"messages":          messages,
	}
	if system != "" {
		reqBody["system"] = system
	}
	if params.Temperature > 0 {
		reqBody["temperature"] = params.Temperature
	}
	if params.TopP > 0 {
		reqBody["top_p"] = params.TopP
	}
	if len(params.Stop) > 0 {
		reqBody["stop_sequences"] = params.Stop
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:rawPredict",
		p.location, p.projectID, p.location, model)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.accessToken)
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
		return nil, fmt.Errorf("Vertex AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	var anthropicResp struct {
		ID      string `json:"id"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}

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

	return &CompletionResult{
		ID:      anthropicResp.ID,
		Content: content,
		Message: Message{
			Role:    "assistant",
			Content: content,
		},
		Provider: p.Name(),
		Model:    model,
		Usage: Usage{
			PromptTokens:     anthropicResp.Usage.InputTokens,
			CompletionTokens: anthropicResp.Usage.OutputTokens,
			TotalTokens:      totalTokens,
			CostUSD:          p.calculateCost(model, totalTokens),
		},
	}, nil
}

func (p *VertexAIProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	model := params.Model
	if model == "" {
		model = "gemini-1.5-flash-002"
	}

	// For Claude models, use different streaming endpoint
	if strings.HasPrefix(model, "claude") {
		return p.streamAnthropic(ctx, params, model)
	}

	// Gemini streaming
	var contents []vertexContent
	var systemInstruction *vertexContent

	for _, m := range params.Messages {
		if m.Role == "system" {
			systemInstruction = &vertexContent{
				Role:  "user",
				Parts: []vertexPart{{Text: m.Content}},
			}
			continue
		}

		role := m.Role
		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, vertexContent{
			Role:  role,
			Parts: []vertexPart{{Text: m.Content}},
		})
	}

	reqBody := vertexRequest{
		Contents:          contents,
		SystemInstruction: systemInstruction,
	}

	if params.Temperature > 0 || params.MaxTokens > 0 || params.TopP > 0 || len(params.Stop) > 0 {
		reqBody.GenerationConfig = &vertexGenerationConfig{
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

	endpoint := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:streamGenerateContent?alt=sse",
		p.location, p.projectID, p.location, model)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Vertex AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 100)
	go p.streamGeminiResponse(ctx, resp, model, ch)
	return ch, nil
}

func (p *VertexAIProvider) streamGeminiResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
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

		var vertexResp vertexResponse
		if err := json.Unmarshal([]byte(event.Data), &vertexResp); err != nil {
			ch <- StreamChunk{
				Err:      fmt.Errorf("failed to parse stream chunk: %w", err),
				Provider: p.Name(),
				Model:    model,
			}
			continue
		}

		for _, candidate := range vertexResp.Candidates {
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

		if vertexResp.UsageMetadata != nil {
			promptTokens = vertexResp.UsageMetadata.PromptTokenCount
			completionTokens = vertexResp.UsageMetadata.CandidatesTokenCount
		}
	}

	totalTokens := promptTokens + completionTokens
	cost := p.calculateCost(model, totalTokens)

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

func (p *VertexAIProvider) streamAnthropic(ctx context.Context, params CompletionParams, model string) (<-chan StreamChunk, error) {
	maxTokens := params.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	var system string
	var messages []map[string]string

	for _, m := range params.Messages {
		if m.Role == "system" {
			system = m.Content
			continue
		}
		messages = append(messages, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}

	reqBody := map[string]interface{}{
		"anthropic_version": "vertex-2023-10-16",
		"max_tokens":        maxTokens,
		"messages":          messages,
		"stream":            true,
	}
	if system != "" {
		reqBody["system"] = system
	}
	if params.Temperature > 0 {
		reqBody["temperature"] = params.Temperature
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	endpoint := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/anthropic/models/%s:streamRawPredict",
		p.location, p.projectID, p.location, model)

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Vertex AI error: status %d: %s", resp.StatusCode, string(respBody))
	}

	ch := make(chan StreamChunk, 100)
	go p.streamAnthropicResponse(ctx, resp, model, ch)
	return ch, nil
}

func (p *VertexAIProvider) streamAnthropicResponse(ctx context.Context, resp *http.Response, model string, ch chan<- StreamChunk) {
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
		}
	}

	totalTokens := inputTokens + outputTokens
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
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      totalTokens,
			CostUSD:          cost,
		},
	}
}

func (p *VertexAIProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := params.Model
	if model == "" {
		model = "text-embedding-004"
	}

	embeddings := make([]Embedding, 0, len(params.Texts))

	for _, text := range params.Texts {
		reqBody := map[string]interface{}{
			"instances": []map[string]string{
				{"content": text},
			},
		}

		body, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}

		endpoint := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:predict",
			p.location, p.projectID, p.location, model)

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+p.accessToken)
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
			return nil, fmt.Errorf("Vertex AI error: status %d: %s", resp.StatusCode, string(respBody))
		}

		var embedResp struct {
			Predictions []struct {
				Embeddings struct {
					Values []float32 `json:"values"`
				} `json:"embeddings"`
			} `json:"predictions"`
		}

		if err := json.Unmarshal(respBody, &embedResp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}

		if len(embedResp.Predictions) > 0 {
			values := embedResp.Predictions[0].Embeddings.Values
			embeddings = append(embeddings, Embedding{
				Values:     values,
				Dimensions: len(values),
			})
		}
	}

	return &EmbedResult{
		Embeddings: embeddings,
		Model:      model,
		Provider:   p.Name(),
	}, nil
}

func (p *VertexAIProvider) calculateCost(model string, totalTokens int) float64 {
	costs := p.CostPer1KTokens()
	costPer1K, ok := costs[model]
	if !ok {
		costPer1K = 0.001
	}
	return float64(totalTokens) / 1000 * costPer1K
}
