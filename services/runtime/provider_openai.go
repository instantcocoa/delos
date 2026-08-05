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

// OpenAIProvider speaks the OpenAI Chat Completions wire format. It also
// backs any OpenAI-compatible endpoint (vLLM, SGLang, Ollama, Together,
// OpenRouter, Azure-style gateways) via NewOpenAICompatProvider.
type OpenAIProvider struct {
	name       string
	baseURL    string
	apiKey     string
	httpClient *http.Client

	staticModels []string           // fixed model list (openai.com)
	pricing      map[string]float64 // model -> USD per 1K total tokens

	// dynamic model discovery for compat endpoints
	discoverModels bool
	modelsMu       sync.Mutex
	cachedModels   []string
	lastDiscovery  time.Time
}

// NewOpenAIProvider creates the provider for api.openai.com.
func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		name:       "openai",
		baseURL:    "https://api.openai.com/v1",
		apiKey:     apiKey,
		httpClient: &http.Client{},
		staticModels: []string{
			"gpt-5.1",
			"gpt-5",
			"gpt-5-mini",
			"gpt-4.1",
			"gpt-4o",
			"gpt-4o-mini",
			"text-embedding-3-small",
			"text-embedding-3-large",
		},
		pricing: map[string]float64{
			"gpt-5.1":                0.00125,
			"gpt-5":                  0.00125,
			"gpt-5-mini":             0.00025,
			"gpt-4.1":                0.002,
			"gpt-4o":                 0.005,
			"gpt-4o-mini":            0.00015,
			"text-embedding-3-small": 0.00002,
			"text-embedding-3-large": 0.00013,
		},
	}
}

// NewOpenAICompatProvider creates a provider for any OpenAI-compatible
// endpoint. baseURL includes the /v1 suffix (e.g. "http://localhost:11434/v1").
// The model list is discovered from GET {baseURL}/models and cached.
func NewOpenAICompatProvider(name, baseURL, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		name:           name,
		baseURL:        strings.TrimSuffix(baseURL, "/"),
		apiKey:         apiKey,
		httpClient:     &http.Client{},
		discoverModels: true,
	}
}

func (p *OpenAIProvider) Name() string { return p.name }

func (p *OpenAIProvider) Models(ctx context.Context) []string {
	if !p.discoverModels {
		return p.staticModels
	}

	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if time.Since(p.lastDiscovery) < time.Minute && p.cachedModels != nil {
		return p.cachedModels
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return p.cachedModels
	}
	p.setAuth(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return p.cachedModels
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&list) != nil {
		return p.cachedModels
	}
	models := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		models = append(models, m.ID)
	}
	p.cachedModels = models
	p.lastDiscovery = time.Now()
	return models
}

func (p *OpenAIProvider) setAuth(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// ---- wire types (OpenAI Chat Completions format) ----

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    any           `json:"content"` // string, []oaiContentPart, or nil
	Name       string        `json:"name,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"`
}

type oaiToolCall struct {
	Index    *int            `json:"index,omitempty"` // streaming deltas only
	ID       string          `json:"id,omitempty"`
	Type     string          `json:"type,omitempty"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiTool struct {
	Type     string        `json:"type"`
	Function oaiToolSchema `json:"function"`
}

type oaiToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type oaiRequest struct {
	Model          string          `json:"model"`
	Messages       []oaiMessage    `json:"messages"`
	Temperature    *float64        `json:"temperature,omitempty"`
	TopP           *float64        `json:"top_p,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stop           []string        `json:"stop,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	StreamOptions  json.RawMessage `json:"stream_options,omitempty"`
	Tools          []oaiTool       `json:"tools,omitempty"`
	ToolChoice     any             `json:"tool_choice,omitempty"`
	ResponseFormat any             `json:"response_format,omitempty"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type oaiResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      oaiRespMessage `json:"message"`
		FinishReason string         `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage"`
}

type oaiRespMessage struct {
	Role      string        `json:"role"`
	Content   *string       `json:"content"`
	ToolCalls []oaiToolCall `json:"tool_calls"`
}

type oaiStreamChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta        oaiRespMessage `json:"delta"`
		FinishReason *string        `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage"`
}

// ---- request translation ----

func oaiFromMessages(messages []Message) ([]oaiMessage, error) {
	out := make([]oaiMessage, 0, len(messages))
	for _, m := range messages {
		om := oaiMessage{Role: m.Role, Name: m.Name, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaiToolCall{
				ID:       tc.ID,
				Type:     "function",
				Function: oaiToolFunction{Name: tc.Name, Arguments: tc.Arguments},
			})
		}

		hasImage := false
		for _, part := range m.Content {
			if part.Type == "image" {
				hasImage = true
			}
		}
		switch {
		case hasImage:
			parts := make([]oaiContentPart, 0, len(m.Content))
			for _, part := range m.Content {
				switch part.Type {
				case "text":
					parts = append(parts, oaiContentPart{Type: "text", Text: part.Text})
				case "image":
					url := part.ImageURL
					if url == "" {
						url = fmt.Sprintf("data:%s;base64,%s", part.MediaType, part.ImageData)
					}
					parts = append(parts, oaiContentPart{Type: "image_url", ImageURL: &oaiImageURL{URL: url}})
				default:
					return nil, fmt.Errorf("unsupported content part type %q", part.Type)
				}
			}
			om.Content = parts
		case len(m.Content) > 0:
			om.Content = m.Text()
		case len(om.ToolCalls) > 0:
			om.Content = nil
		default:
			om.Content = ""
		}
		out = append(out, om)
	}
	return out, nil
}

func (p *OpenAIProvider) buildRequest(params CompletionParams, stream bool) (*oaiRequest, error) {
	messages, err := oaiFromMessages(params.Messages)
	if err != nil {
		return nil, err
	}
	req := &oaiRequest{
		Model:       params.Model,
		Messages:    messages,
		Temperature: params.Temperature,
		TopP:        params.TopP,
		MaxTokens:   params.MaxTokens,
		Stop:        params.Stop,
		Stream:      stream,
	}
	if stream {
		req.StreamOptions = json.RawMessage(`{"include_usage":true}`)
	}
	for _, t := range params.Tools {
		req.Tools = append(req.Tools, oaiTool{
			Type:     "function",
			Function: oaiToolSchema{Name: t.Name, Description: t.Description, Parameters: t.Parameters},
		})
	}
	if tc := params.ToolChoice; tc != nil {
		switch tc.Mode {
		case "auto", "none", "required":
			req.ToolChoice = tc.Mode
		case "tool":
			req.ToolChoice = map[string]any{
				"type":     "function",
				"function": map[string]string{"name": tc.Name},
			}
		}
	}
	if rf := params.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_object":
			req.ResponseFormat = map[string]string{"type": "json_object"}
		case "json_schema":
			req.ResponseFormat = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   rf.Name,
					"schema": rf.Schema,
					"strict": rf.Strict,
				},
			}
		}
	}
	return req, nil
}

func (p *OpenAIProvider) post(ctx context.Context, path string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAuth(req)
	return p.httpClient.Do(req)
}

// apiError extracts the backend's error message verbatim.
func (p *OpenAIProvider) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return &ProviderError{Provider: p.name, StatusCode: resp.StatusCode, Message: envelope.Error.Message}
	}
	return &ProviderError{Provider: p.name, StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

func (p *OpenAIProvider) cost(model string, usage oaiUsage) float64 {
	return p.pricing[model] * float64(usage.TotalTokens) / 1000
}

func oaiFinishReason(reason string) string {
	switch reason {
	case "tool_calls", "function_call":
		return FinishToolCalls
	case "length":
		return FinishLength
	case "content_filter":
		return FinishContentFilter
	default:
		return FinishStop
	}
}

// ---- Provider implementation ----

func (p *OpenAIProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	req, err := p.buildRequest(params, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/chat/completions", req)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out oaiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: invalid response: %w", p.name, err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s: response contained no choices", p.name)
	}

	choice := out.Choices[0]
	msg := Message{Role: "assistant"}
	if choice.Message.Content != nil && *choice.Message.Content != "" {
		msg.Content = []ContentPart{TextPart(*choice.Message.Content)}
	}
	for _, tc := range choice.Message.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	result := &CompletionResult{
		ID:           out.ID,
		Message:      msg,
		FinishReason: oaiFinishReason(choice.FinishReason),
		Provider:     p.name,
		Model:        out.Model,
	}
	if out.Usage != nil {
		result.Usage = Usage{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
			CostUSD:          p.cost(params.Model, *out.Usage),
		}
	}
	return result, nil
}

func (p *OpenAIProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	req, err := p.buildRequest(params, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/chat/completions", req)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", p.name, err)
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

		var usage *Usage
		finish := FinishStop
		id, model := "", params.Model

		for event := range events {
			if event.Err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("%s stream error: %w", p.name, event.Err), Provider: p.name, Model: model}
				return
			}
			data := strings.TrimSpace(event.Data)
			if data == "" || data == "[DONE]" {
				continue
			}
			var chunk oaiStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("%s stream: invalid chunk: %w", p.name, err), Provider: p.name, Model: model}
				return
			}
			if chunk.ID != "" {
				id = chunk.ID
			}
			if chunk.Model != "" {
				model = chunk.Model
			}
			if chunk.Usage != nil {
				usage = &Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
					CostUSD:          p.cost(params.Model, *chunk.Usage),
				}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				finish = oaiFinishReason(*choice.FinishReason)
			}
			if choice.Delta.Content != nil && *choice.Delta.Content != "" {
				chunks <- StreamChunk{ID: id, Delta: *choice.Delta.Content, Provider: p.name, Model: model}
			}
			for _, tc := range choice.Delta.ToolCalls {
				index := 0
				if tc.Index != nil {
					index = *tc.Index
				}
				chunks <- StreamChunk{
					ID: id,
					ToolCall: &ToolCallDelta{
						Index:          index,
						ID:             tc.ID,
						Name:           tc.Function.Name,
						ArgumentsDelta: tc.Function.Arguments,
					},
					Provider: p.name,
					Model:    model,
				}
			}
		}

		chunks <- StreamChunk{ID: id, Done: true, FinishReason: finish, Usage: usage, Provider: p.name, Model: model}
	}()
	return chunks, nil
}

func (p *OpenAIProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	body := map[string]any{"model": params.Model, "input": params.Texts}
	resp, err := p.post(ctx, "/embeddings", body)
	if err != nil {
		return nil, fmt.Errorf("%s request failed: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out struct {
		Model string `json:"model"`
		Data  []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage *oaiUsage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s: invalid response: %w", p.name, err)
	}

	result := &EmbedResult{Model: out.Model, Provider: p.name}
	if result.Model == "" {
		result.Model = params.Model
	}
	for _, d := range out.Data {
		result.Embeddings = append(result.Embeddings, Embedding{Values: d.Embedding, Dimensions: len(d.Embedding)})
	}
	if out.Usage != nil {
		result.Usage = Usage{
			PromptTokens: out.Usage.PromptTokens,
			TotalTokens:  out.Usage.TotalTokens,
			CostUSD:      p.cost(params.Model, *out.Usage),
		}
	}
	return result, nil
}
