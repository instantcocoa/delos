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

const geminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GeminiProvider speaks Google's Generative Language API (the "Gemini API",
// as distinct from Vertex AI). Auth is an API key sent in the x-goog-api-key
// header rather than the ?key= query parameter, so keys never land in logs or
// proxy access records.
type GeminiProvider struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	models     []string
	pricing    map[string]float64 // model -> USD per 1K total tokens
}

// NewGeminiProvider creates the provider for generativelanguage.googleapis.com.
// GeminiOption configures the provider.
type GeminiOption func(*GeminiProvider)

// WithGeminiBaseURL overrides the API base URL (testing and proxies).
func WithGeminiBaseURL(url string) GeminiOption {
	return func(p *GeminiProvider) { p.baseURL = url }
}

func NewGeminiProvider(apiKey string, opts ...GeminiOption) *GeminiProvider {
	p := &GeminiProvider{
		apiKey:     apiKey,
		baseURL:    geminiBaseURL,
		httpClient: &http.Client{},
		models: []string{
			"gemini-2.5-pro",
			"gemini-2.5-flash",
			"gemini-2.5-flash-lite",
			"gemini-2.0-flash",
			"text-embedding-004",
		},
		pricing: map[string]float64{
			"gemini-2.5-pro":        0.00125,
			"gemini-2.5-flash":      0.0003,
			"gemini-2.5-flash-lite": 0.0001,
			"gemini-2.0-flash":      0.0001,
			"text-embedding-004":    0.00001,
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

func (p *GeminiProvider) Name() string                        { return "gemini" }
func (p *GeminiProvider) Models(ctx context.Context) []string { return p.models }

// ---- wire types (Generative Language API) ----

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inline_data,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"` // base64
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"` // "user" | "model"
	Parts []geminiPart `json:"parts"`
}

type geminiFunctionDeclaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode"` // AUTO | NONE | ANY
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig geminiFunctionCallingConfig `json:"functionCallingConfig"`
}

type geminiGenerationConfig struct {
	Temperature      *float64        `json:"temperature,omitempty"`
	TopP             *float64        `json:"topP,omitempty"`
	MaxOutputTokens  int             `json:"maxOutputTokens,omitempty"`
	StopSequences    []string        `json:"stopSequences,omitempty"`
	ResponseMimeType string          `json:"responseMimeType,omitempty"`
	ResponseSchema   json.RawMessage `json:"responseSchema,omitempty"`
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiResponse struct {
	ResponseID    string               `json:"responseId"`
	ModelVersion  string               `json:"modelVersion"`
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
}

// ---- schema scrubbing ----

// geminiScrubSchema removes JSON Schema keywords the Gemini schema dialect
// rejects outright ($schema, additionalProperties). Anything it cannot parse
// is passed through untouched so the backend, not us, reports the problem.
func geminiScrubSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(geminiScrubValue(v))
	if err != nil {
		return raw
	}
	return out
}

func geminiScrubValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "$schema" || k == "additionalProperties" {
				continue
			}
			out[k] = geminiScrubValue(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = geminiScrubValue(t[i])
		}
		return t
	default:
		return v
	}
}

// ---- request translation ----

// geminiFromMessages converts internal messages into (systemInstruction,
// contents). Gemini keys function results by tool *name*, not by call ID, so
// the ID->name mapping is recovered from the assistant tool calls earlier in
// the conversation.
func geminiFromMessages(messages []Message) (*geminiContent, []geminiContent, error) {
	var system *geminiContent
	var contents []geminiContent
	toolNameByID := map[string]string{}

	appendParts := func(role string, parts []geminiPart) {
		if n := len(contents); n > 0 && contents[n-1].Role == role {
			contents[n-1].Parts = append(contents[n-1].Parts, parts...)
			return
		}
		contents = append(contents, geminiContent{Role: role, Parts: parts})
	}

	for _, m := range messages {
		switch m.Role {
		case "system":
			parts, err := geminiPartsFromContent(m.Content)
			if err != nil {
				return nil, nil, err
			}
			if system == nil {
				system = &geminiContent{}
			}
			system.Parts = append(system.Parts, parts...)

		case "tool":
			name := toolNameByID[m.ToolCallID]
			if name == "" {
				name = m.Name
			}
			if name == "" {
				return nil, nil, fmt.Errorf("gemini: cannot map tool result for call %q to a function name; gemini keys function responses by name and no prior assistant tool call declared it", m.ToolCallID)
			}
			appendParts("user", []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					Name:     name,
					Response: geminiToolResponsePayload(m.Text()),
				},
			}})

		case "assistant":
			parts, err := geminiPartsFromContent(m.Content)
			if err != nil {
				return nil, nil, err
			}
			for _, tc := range m.ToolCalls {
				if tc.ID != "" {
					toolNameByID[tc.ID] = tc.Name
				}
				args := json.RawMessage(strings.TrimSpace(tc.Arguments))
				if len(args) == 0 {
					args = json.RawMessage("{}")
				}
				if !json.Valid(args) {
					return nil, nil, fmt.Errorf("gemini: tool call %q has invalid JSON arguments", tc.Name)
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{Name: tc.Name, Args: args},
				})
			}
			if len(parts) == 0 {
				parts = []geminiPart{{Text: ""}}
			}
			appendParts("model", parts)

		default: // user
			parts, err := geminiPartsFromContent(m.Content)
			if err != nil {
				return nil, nil, err
			}
			if len(parts) == 0 {
				parts = []geminiPart{{Text: ""}}
			}
			appendParts("user", parts)
		}
	}
	return system, contents, nil
}

func geminiPartsFromContent(content []ContentPart) ([]geminiPart, error) {
	parts := make([]geminiPart, 0, len(content))
	for _, part := range content {
		switch part.Type {
		case "text":
			parts = append(parts, geminiPart{Text: part.Text})
		case "image":
			if part.ImageData == "" {
				return nil, fmt.Errorf("gemini: remote image URLs are not supported (%q); fetch the image client-side and pass base64 image data", part.ImageURL)
			}
			mime := part.MediaType
			if mime == "" {
				mime = "image/png"
			}
			parts = append(parts, geminiPart{
				InlineData: &geminiInlineData{MimeType: mime, Data: part.ImageData},
			})
		default:
			return nil, fmt.Errorf("unsupported content part type %q", part.Type)
		}
	}
	return parts, nil
}

// geminiToolResponsePayload wraps a tool result for functionResponse.response,
// which must be a JSON object. Results that already are one pass through.
func geminiToolResponsePayload(text string) json.RawMessage {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	payload, err := json.Marshal(map[string]string{"result": text})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

func (p *GeminiProvider) buildRequest(params CompletionParams) (*geminiRequest, error) {
	system, contents, err := geminiFromMessages(params.Messages)
	if err != nil {
		return nil, err
	}
	req := &geminiRequest{Contents: contents, SystemInstruction: system}

	if len(params.Tools) > 0 {
		decls := make([]geminiFunctionDeclaration, 0, len(params.Tools))
		for _, t := range params.Tools {
			decls = append(decls, geminiFunctionDeclaration{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  geminiScrubSchema(t.Parameters),
			})
		}
		req.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	if tc := params.ToolChoice; tc != nil {
		cfg := geminiFunctionCallingConfig{}
		switch tc.Mode {
		case "auto":
			cfg.Mode = "AUTO"
		case "none":
			cfg.Mode = "NONE"
		case "required":
			cfg.Mode = "ANY"
		case "tool":
			cfg.Mode = "ANY"
			cfg.AllowedFunctionNames = []string{tc.Name}
		}
		if cfg.Mode != "" {
			req.ToolConfig = &geminiToolConfig{FunctionCallingConfig: cfg}
		}
	}

	gc := geminiGenerationConfig{
		Temperature:     params.Temperature,
		TopP:            params.TopP,
		MaxOutputTokens: params.MaxTokens,
		StopSequences:   params.Stop,
	}
	if rf := params.ResponseFormat; rf != nil {
		switch rf.Type {
		case "json_object":
			gc.ResponseMimeType = "application/json"
		case "json_schema":
			gc.ResponseMimeType = "application/json"
			gc.ResponseSchema = geminiScrubSchema(rf.Schema)
		}
	}
	if gc.Temperature != nil || gc.TopP != nil || gc.MaxOutputTokens > 0 ||
		len(gc.StopSequences) > 0 || gc.ResponseMimeType != "" {
		req.GenerationConfig = &gc
	}
	return req, nil
}

// ---- transport ----

// geminiModelPath renders a bare model name ("gemini-2.5-flash") as the
// resource path segment the API expects ("models/gemini-2.5-flash").
func geminiModelPath(model string) string {
	return "models/" + strings.TrimPrefix(model, "models/")
}

func (p *GeminiProvider) post(ctx context.Context, path string, body any) (*http.Response, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("x-goog-api-key", p.apiKey)
	}
	return p.httpClient.Do(req)
}

func (p *GeminiProvider) apiError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return &ProviderError{Provider: "gemini", StatusCode: resp.StatusCode, Message: envelope.Error.Message}
	}
	return &ProviderError{Provider: "gemini", StatusCode: resp.StatusCode, Message: strings.TrimSpace(string(body))}
}

func (p *GeminiProvider) cost(model string, totalTokens int) float64 {
	return p.pricing[strings.TrimPrefix(model, "models/")] * float64(totalTokens) / 1000
}

func geminiFinishReason(reason string) string {
	switch reason {
	case "MAX_TOKENS":
		return FinishLength
	case "SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII", "RECITATION":
		return FinishContentFilter
	default:
		return FinishStop
	}
}

// geminiToolCallID synthesizes an ID for a tool call: the API returns function
// calls without one, but every internal consumer keys results by ID.
func geminiToolCallID(index int) string {
	return fmt.Sprintf("call_%d", index+1)
}

// ---- Provider implementation ----

func (p *GeminiProvider) Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error) {
	req, err := p.buildRequest(params)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/"+geminiModelPath(params.Model)+":generateContent", req)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini: invalid response: %w", err)
	}
	if len(out.Candidates) == 0 {
		return nil, fmt.Errorf("gemini: response contained no candidates")
	}

	candidate := out.Candidates[0]
	msg := Message{Role: "assistant"}
	for _, part := range candidate.Content.Parts {
		switch {
		case part.FunctionCall != nil:
			args := string(part.FunctionCall.Args)
			if args == "" {
				args = "{}"
			}
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:        geminiToolCallID(len(msg.ToolCalls)),
				Name:      part.FunctionCall.Name,
				Arguments: args,
			})
		case part.Text != "":
			msg.Content = append(msg.Content, TextPart(part.Text))
		}
	}

	finish := geminiFinishReason(candidate.FinishReason)
	if len(msg.ToolCalls) > 0 {
		finish = FinishToolCalls
	}

	model := out.ModelVersion
	if model == "" {
		model = params.Model
	}
	result := &CompletionResult{
		ID:           out.ResponseID,
		Message:      msg,
		FinishReason: finish,
		Provider:     "gemini",
		Model:        model,
	}
	if out.UsageMetadata != nil {
		result.Usage = Usage{
			PromptTokens:     out.UsageMetadata.PromptTokenCount,
			CompletionTokens: out.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      out.UsageMetadata.TotalTokenCount,
			CostUSD:          p.cost(params.Model, out.UsageMetadata.TotalTokenCount),
		}
	}
	return result, nil
}

func (p *GeminiProvider) CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error) {
	req, err := p.buildRequest(params)
	if err != nil {
		return nil, err
	}
	resp, err := p.post(ctx, "/"+geminiModelPath(params.Model)+":streamGenerateContent?alt=sse", req)
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
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
		sawToolCall := false
		nextToolIndex := 0
		var usage *Usage

		for event := range events {
			if event.Err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("gemini stream error: %w", event.Err), Provider: "gemini", Model: model}
				return
			}
			data := strings.TrimSpace(event.Data)
			if data == "" {
				continue
			}
			var ev geminiResponse
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				chunks <- StreamChunk{Err: fmt.Errorf("gemini stream: invalid chunk: %w", err), Provider: "gemini", Model: model}
				return
			}
			if ev.ResponseID != "" {
				id = ev.ResponseID
			}
			if ev.ModelVersion != "" {
				model = ev.ModelVersion
			}
			if ev.UsageMetadata != nil {
				usage = &Usage{
					PromptTokens:     ev.UsageMetadata.PromptTokenCount,
					CompletionTokens: ev.UsageMetadata.CandidatesTokenCount,
					TotalTokens:      ev.UsageMetadata.TotalTokenCount,
					CostUSD:          p.cost(params.Model, ev.UsageMetadata.TotalTokenCount),
				}
			}
			if len(ev.Candidates) == 0 {
				continue
			}
			candidate := ev.Candidates[0]
			if candidate.FinishReason != "" {
				finish = geminiFinishReason(candidate.FinishReason)
			}
			for _, part := range candidate.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					// Gemini delivers each function call whole in a single
					// event, so one delta carries the complete arguments.
					args := string(part.FunctionCall.Args)
					if args == "" {
						args = "{}"
					}
					chunks <- StreamChunk{
						ID: id,
						ToolCall: &ToolCallDelta{
							Index:          nextToolIndex,
							ID:             geminiToolCallID(nextToolIndex),
							Name:           part.FunctionCall.Name,
							ArgumentsDelta: args,
						},
						Provider: "gemini",
						Model:    model,
					}
					nextToolIndex++
					sawToolCall = true
				case part.Text != "":
					chunks <- StreamChunk{ID: id, Delta: part.Text, Provider: "gemini", Model: model}
				}
			}
		}

		if sawToolCall {
			finish = FinishToolCalls
		}
		chunks <- StreamChunk{ID: id, Done: true, FinishReason: finish, Usage: usage, Provider: "gemini", Model: model}
	}()
	return chunks, nil
}

func (p *GeminiProvider) Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error) {
	model := geminiModelPath(params.Model)
	requests := make([]map[string]any, 0, len(params.Texts))
	for _, text := range params.Texts {
		requests = append(requests, map[string]any{
			"model": model,
			"content": map[string]any{
				"parts": []map[string]string{{"text": text}},
			},
		})
	}

	resp, err := p.post(ctx, "/"+model+":batchEmbedContents", map[string]any{"requests": requests})
	if err != nil {
		return nil, fmt.Errorf("gemini request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, p.apiError(resp)
	}

	var out struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini: invalid response: %w", err)
	}

	result := &EmbedResult{Model: params.Model, Provider: "gemini"}
	for _, e := range out.Embeddings {
		result.Embeddings = append(result.Embeddings, Embedding{Values: e.Values, Dimensions: len(e.Values)})
	}
	return result, nil
}
