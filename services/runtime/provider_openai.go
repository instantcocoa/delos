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

	staticModels []string // fixed model list (openai.com)
	pricing      *PriceTable

	// dynamic model discovery for compat endpoints
	discoverModels bool
	modelsMu       sync.Mutex
	cachedModels   []string
	lastAttempt    time.Time // last discovery attempt, success or failure
	lastOK         bool      // whether that attempt succeeded
	discovering    bool      // a discovery call is in flight
}

// openaiPricing is USD per 1M tokens, input/output, from OpenAI's published
// price list. Dated snapshots (gpt-4o-2024-11-20) and unlisted members of a
// family fall back to the family key by longest-prefix match.
var openaiPricing = map[string]ModelRate{
	"gpt-5.1":                {Input: 1.25, Output: 10.00},
	"gpt-5":                  {Input: 1.25, Output: 10.00},
	"gpt-5-mini":             {Input: 0.25, Output: 2.00},
	"gpt-5-nano":             {Input: 0.05, Output: 0.40},
	"gpt-4.1":                {Input: 2.00, Output: 8.00},
	"gpt-4.1-mini":           {Input: 0.40, Output: 1.60},
	"gpt-4.1-nano":           {Input: 0.10, Output: 0.40},
	"gpt-4o":                 {Input: 2.50, Output: 10.00},
	"gpt-4o-mini":            {Input: 0.15, Output: 0.60},
	"chatgpt-4o-latest":      {Input: 5.00, Output: 15.00},
	"o3":                     {Input: 2.00, Output: 8.00},
	"o3-mini":                {Input: 1.10, Output: 4.40},
	"o4-mini":                {Input: 1.10, Output: 4.40},
	"text-embedding-3-small": {Input: 0.02},
	"text-embedding-3-large": {Input: 0.13},
	"text-embedding-ada-002": {Input: 0.10},
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
			"o3",
			"o4-mini",
			"text-embedding-3-small",
			"text-embedding-3-large",
		},
		pricing: NewPriceTable("openai", openaiPricing),
	}
}

// NewOpenAICompatProvider creates a provider for any OpenAI-compatible
// endpoint. baseURL includes the /v1 suffix (e.g. "http://localhost:11434/v1").
// The model list is discovered from GET {baseURL}/models and cached.
// The provider ships with an empty price table: the gateway cannot know what a
// self-hosted or third-party endpoint charges. Every model served through it
// therefore costs $0.00 until an operator prices it with SetPricing, and each
// unpriced model is logged once so the hole is visible rather than silent.
func NewOpenAICompatProvider(name, baseURL, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		name:           name,
		baseURL:        strings.TrimSuffix(baseURL, "/"),
		apiKey:         apiKey,
		httpClient:     &http.Client{},
		discoverModels: true,
		pricing:        NewPriceTable(name, nil),
	}
}

// SetPricing prices models this provider serves, in USD per 1M tokens. It is
// how an operator makes USD budgets enforceable for a self-hosted or
// third-party OpenAI-compatible endpoint.
func (p *OpenAIProvider) SetPricing(rates map[string]ModelRate) {
	p.pricing.SetAll(rates)
}

func (p *OpenAIProvider) Name() string { return p.name }

// Model-discovery cache lifetimes. A failed discovery is cached too: without
// negative caching a stopped Ollama makes every GET /v1/models pay the full
// dial timeout again.
const (
	modelDiscoveryTTL     = time.Minute
	modelDiscoveryFailTTL = 30 * time.Second
	modelDiscoveryTimeout = 5 * time.Second
)

// Models returns the provider's models, discovering them from the backend for
// compat endpoints.
//
// The discovery call never happens under the mutex: a single slow or dead
// backend would otherwise serialize every concurrent /v1/models request behind
// one 5s dial. At most one discovery is in flight; callers that arrive while
// it runs get the last known list immediately.
func (p *OpenAIProvider) Models(ctx context.Context) []string {
	if !p.discoverModels {
		return p.staticModels
	}

	p.modelsMu.Lock()
	ttl := modelDiscoveryTTL
	if !p.lastOK {
		ttl = modelDiscoveryFailTTL
	}
	if !p.lastAttempt.IsZero() && time.Since(p.lastAttempt) < ttl {
		cached := p.cachedModels
		p.modelsMu.Unlock()
		return cached
	}
	if p.discovering {
		cached := p.cachedModels
		p.modelsMu.Unlock()
		return cached
	}
	p.discovering = true
	cached := p.cachedModels
	p.modelsMu.Unlock()

	models, err := p.discover(ctx)

	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	p.discovering = false
	p.lastAttempt = time.Now()
	p.lastOK = err == nil
	if err != nil {
		return cached
	}
	p.cachedModels = models
	return models
}

// discover performs one GET {baseURL}/models. It holds no locks.
func (p *OpenAIProvider) discover(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, modelDiscoveryTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.setAuth(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: model discovery returned %d", p.name, resp.StatusCode)
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(list.Data))
	for _, m := range list.Data {
		models = append(models, m.ID)
	}
	return models, nil
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

// usageFrom converts wire usage, deriving the total the backend may have
// omitted and pricing input and output separately.
func (p *OpenAIProvider) usageFrom(model string, wire oaiUsage) Usage {
	u := Usage{
		PromptTokens:     wire.PromptTokens,
		CompletionTokens: wire.CompletionTokens,
		TotalTokens:      wire.TotalTokens,
	}.withDerivedTotals()
	u.CostUSD = p.pricing.Cost(model, u)
	return u
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

	// Backends routinely report finish_reason "stop" alongside a populated
	// tool_calls array (several OpenAI-compatible servers always do). Agent
	// loops branch on the finish reason, so an uncorrected "stop" means the
	// tool never runs. Gemini already applies this correction; do it here too.
	finish := oaiFinishReason(choice.FinishReason)
	if len(msg.ToolCalls) > 0 && finish == FinishStop {
		finish = FinishToolCalls
	}

	result := &CompletionResult{
		ID:           out.ID,
		Message:      msg,
		FinishReason: finish,
		Provider:     p.name,
		Model:        out.Model,
	}
	if out.Usage != nil {
		result.Usage = p.usageFrom(params.Model, *out.Usage)
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
		sawToolCall := false
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
				u := p.usageFrom(params.Model, *chunk.Usage)
				usage = &u
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
				sawToolCall = true
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

		// Same finish_reason correction as the non-streaming path.
		if sawToolCall && finish == FinishStop {
			finish = FinishToolCalls
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
		result.Usage = p.usageFrom(params.Model, *out.Usage)
	}
	return result, nil
}
