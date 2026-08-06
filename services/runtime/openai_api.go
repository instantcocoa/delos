package runtime

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

// ---- OpenAI-compatible surface types ----
//
// These structs are the compatibility contract of the gateway: they mirror the
// OpenAI API wire format.

type chatCompletionRequest struct {
	Model         string          `json:"model"`
	Messages      []chatMessageIn `json:"messages"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	N             *int            `json:"n,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	StreamOptions *streamOptions  `json:"stream_options,omitempty"`
	Stop          stringOrSlice   `json:"stop,omitempty"`
	MaxTokens     int             `json:"max_tokens,omitempty"`
	MaxComplete   int             `json:"max_completion_tokens,omitempty"`
	Tools         []surfaceTool   `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	ResponseFmt   json.RawMessage `json:"response_format,omitempty"`
	User          string          `json:"user,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type surfaceTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

// chatMessageIn accepts both string content and content-part arrays.
type chatMessageIn struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content"`
	Name       string            `json:"name,omitempty"`
	ToolCalls  []surfaceToolCall `json:"tool_calls,omitempty"`
	ToolCallID string            `json:"tool_call_id,omitempty"`
}

type surfaceToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type contentPartIn struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// stringOrSlice unmarshals either a JSON string or an array of strings.
type stringOrSlice []string

func (s *stringOrSlice) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *usageOut    `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int             `json:"index"`
	Message      *chatMessageOut `json:"message,omitempty"`
	Delta        *chatMessageOut `json:"delta,omitempty"`
	FinishReason *string         `json:"finish_reason"`
}

type chatMessageOut struct {
	Role      string            `json:"role,omitempty"`
	Content   *string           `json:"content,omitempty"`
	ToolCalls []surfaceToolCall `json:"tool_calls,omitempty"`
}

type usageOut struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	// CostUSD is a Delos extension; standard OpenAI clients ignore it.
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// ---- surface → internal translation ----

// parseImageURL splits an OpenAI image_url (remote URL or data: URI) into an
// internal image part.
func parseImageURL(url string) (ContentPart, error) {
	if strings.HasPrefix(url, "data:") {
		meta, data, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
		if !ok {
			return ContentPart{}, fmt.Errorf("malformed data: URI in image_url")
		}
		mediaType := strings.TrimSuffix(meta, ";base64")
		return ContentPart{Type: "image", ImageData: data, MediaType: mediaType}, nil
	}
	return ContentPart{Type: "image", ImageURL: url}, nil
}

// convertMessages maps surface messages to internal messages.
func convertMessages(in []chatMessageIn) ([]Message, error) {
	out := make([]Message, 0, len(in))
	for i, m := range in {
		msg := Message{Role: m.Role, Name: m.Name, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}

		if len(m.Content) == 0 || string(m.Content) == "null" {
			out = append(out, msg)
			continue
		}
		var text string
		if err := json.Unmarshal(m.Content, &text); err == nil {
			if text != "" {
				msg.Content = []ContentPart{TextPart(text)}
			}
			out = append(out, msg)
			continue
		}
		var parts []contentPartIn
		if err := json.Unmarshal(m.Content, &parts); err != nil {
			return nil, fmt.Errorf("messages[%d]: content must be a string or an array of content parts", i)
		}
		for _, p := range parts {
			switch p.Type {
			case "text":
				msg.Content = append(msg.Content, TextPart(p.Text))
			case "image_url":
				if p.ImageURL == nil {
					return nil, fmt.Errorf("messages[%d]: image_url part missing image_url object", i)
				}
				part, err := parseImageURL(p.ImageURL.URL)
				if err != nil {
					return nil, fmt.Errorf("messages[%d]: %w", i, err)
				}
				msg.Content = append(msg.Content, part)
			default:
				return nil, fmt.Errorf("messages[%d]: unsupported content part type %q", i, p.Type)
			}
		}
		out = append(out, msg)
	}
	return out, nil
}

// convertToolChoice parses the OpenAI tool_choice field.
func convertToolChoice(raw json.RawMessage) (*ToolChoice, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var mode string
	if err := json.Unmarshal(raw, &mode); err == nil {
		switch mode {
		case "auto", "none", "required":
			return &ToolChoice{Mode: mode}, nil
		}
		return nil, fmt.Errorf("invalid tool_choice %q", mode)
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil || obj.Function.Name == "" {
		return nil, fmt.Errorf("invalid tool_choice object")
	}
	return &ToolChoice{Mode: "tool", Name: obj.Function.Name}, nil
}

// convertResponseFormat parses the OpenAI response_format field.
func convertResponseFormat(raw json.RawMessage) (*ResponseFormat, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var obj struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Schema json.RawMessage `json:"schema"`
			Strict bool            `json:"strict"`
		} `json:"json_schema"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("invalid response_format")
	}
	switch obj.Type {
	case "", "text":
		return nil, nil
	case "json_object":
		return &ResponseFormat{Type: "json_object"}, nil
	case "json_schema":
		return &ResponseFormat{
			Type:   "json_schema",
			Name:   obj.JSONSchema.Name,
			Schema: obj.JSONSchema.Schema,
			Strict: obj.JSONSchema.Strict,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported response_format type %q", obj.Type)
	}
}

// toSurfaceToolCalls converts internal tool calls to the wire shape.
func toSurfaceToolCalls(calls []ToolCall) []surfaceToolCall {
	out := make([]surfaceToolCall, 0, len(calls))
	for _, tc := range calls {
		stc := surfaceToolCall{ID: tc.ID, Type: "function"}
		stc.Function.Name = tc.Name
		stc.Function.Arguments = tc.Arguments
		out = append(out, stc)
	}
	return out
}

// buildCompletionParams converts a validated surface request. Provider and
// Model are set per fallback target by the chain executor.
func buildCompletionParams(req chatCompletionRequest) (CompletionParams, error) {
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return CompletionParams{}, err
	}
	params := CompletionParams{
		Messages:    messages,
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.Stop,
		MaxTokens:   req.MaxTokens,
	}
	if req.MaxComplete > 0 {
		params.MaxTokens = req.MaxComplete
	}
	for _, t := range req.Tools {
		if t.Type != "function" {
			return CompletionParams{}, fmt.Errorf("unsupported tool type %q", t.Type)
		}
		params.Tools = append(params.Tools, Tool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
		})
	}
	if params.ToolChoice, err = convertToolChoice(req.ToolChoice); err != nil {
		return CompletionParams{}, err
	}
	if params.ResponseFormat, err = convertResponseFormat(req.ResponseFmt); err != nil {
		return CompletionParams{}, err
	}
	return params, nil
}

// ---- handlers ----

func (s *HTTPServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Authenticate before spending memory on the body.
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	limitBody(w, r)

	var req chatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_json", "could not parse request body: "+err.Error())
		return
	}
	if req.Model == "" {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "missing_model", "you must provide a model parameter")
		return
	}
	if len(req.Messages) == 0 {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "missing_messages", "you must provide a messages parameter")
		return
	}
	if req.N != nil && *req.N > 1 {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "unsupported_parameter", "n > 1 is not supported")
		return
	}
	s.tailModel(r.Context(), req.Model)

	chain, err := s.service.ResolveChain(r.Context(), req.Model)
	if err != nil {
		s.writeOpenAIError(w, r, http.StatusNotFound, "model_not_found", err.Error())
		return
	}

	// Budgets and model scope are enforced before any provider is contacted.
	if be := s.checkKeyLimits(r.Context(), key, req.Model); be != nil {
		s.writeOpenAIError(w, r, budgetStatus(be), be.code, be.msg)
		return
	}

	params, err := buildCompletionParams(req)
	if err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	if req.Stream {
		s.streamChatCompletion(w, r, chain, params, req, key)
		return
	}

	result, err := s.service.CompleteChain(r.Context(), chain, params)
	if err != nil {
		s.writeProviderError(w, r, err)
		return
	}
	s.recordUsage(context.WithoutCancel(r.Context()), key, result.Usage)
	s.tailComplete(r, key, result.Model, result.Provider, result.Usage, result.Cached)

	if result.Cached {
		w.Header().Set("X-Delos-Cache", "hit")
	}
	finish := result.FinishReason
	msgOut := &chatMessageOut{Role: "assistant"}
	content := result.Text()
	if content != "" || len(result.Message.ToolCalls) == 0 {
		msgOut.Content = &content
	}
	msgOut.ToolCalls = toSurfaceToolCalls(result.Message.ToolCalls)

	writeJSON(w, http.StatusOK, chatCompletionResponse{
		ID:      orDefault(result.ID, "chatcmpl-"+RequestID(r.Context())),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   result.Model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      msgOut,
			FinishReason: &finish,
		}},
		Usage: &usageOut{
			PromptTokens:     result.Usage.PromptTokens,
			CompletionTokens: result.Usage.CompletionTokens,
			TotalTokens:      result.Usage.TotalTokens,
			CostUSD:          result.Usage.CostUSD,
		},
	})
}

// writeProviderError maps provider failures onto the OpenAI error envelope,
// preserving the upstream message and status.
func (s *HTTPServer) writeProviderError(w http.ResponseWriter, r *http.Request, err error) {
	if pe, ok := errAs[*ProviderError](err); ok {
		status := http.StatusBadGateway
		switch pe.StatusCode {
		case http.StatusTooManyRequests:
			status = http.StatusTooManyRequests
		case http.StatusUnauthorized, http.StatusForbidden:
			// The gateway's provider credential is bad - that's our 500-class
			// problem, not the caller's auth.
			status = http.StatusBadGateway
		case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
			status = http.StatusBadRequest
		}
		s.writeOpenAIError(w, r, status, "provider_error",
			fmt.Sprintf("provider %s: %s", pe.Provider, pe.Message))
		return
	}
	s.writeOpenAIError(w, r, http.StatusBadGateway, "provider_error", err.Error())
}

func (s *HTTPServer) streamChatCompletion(w http.ResponseWriter, r *http.Request, chain []RouteTarget, params CompletionParams, req chatCompletionRequest, key *VirtualKey) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeOpenAIError(w, r, http.StatusInternalServerError, "streaming_unsupported", "response writer does not support streaming")
		return
	}

	chunks, err := s.service.CompleteStreamChain(r.Context(), chain, params)
	if err != nil {
		s.writeProviderError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	id := "chatcmpl-" + RequestID(r.Context())
	created := time.Now().Unix()
	model := params.Model
	provider := ""
	streamUsage := Usage{}
	sentRole := false

	writeChunk := func(c chatCompletionResponse) {
		data, _ := json.Marshal(c)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}

	for chunk := range chunks {
		if chunk.Err != nil {
			// The stream broke mid-flight; SSE has no status code left to
			// change, so emit an error event and stop without [DONE] so the
			// client does not mistake this for a complete response.
			body, _ := json.Marshal(oaiErrorEnvelope{Error: oaiErrorBody{
				Message: chunk.Err.Error(), Type: "api_error", Code: "provider_stream_error",
			}})
			fmt.Fprintf(w, "data: %s\n\n", body)
			flusher.Flush()
			s.tailFailure(r, http.StatusBadGateway, "provider_stream_error", chunk.Err.Error())
			return
		}
		if chunk.Provider != "" {
			provider = chunk.Provider
		}
		if chunk.Model != "" {
			model = chunk.Model
		}
		if chunk.Done {
			if chunk.Usage != nil {
				s.recordUsage(context.WithoutCancel(r.Context()), key, *chunk.Usage)
				streamUsage = *chunk.Usage
			}
			finish := chunk.FinishReason
			if finish == "" {
				finish = FinishStop
			}
			writeChunk(chatCompletionResponse{
				ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
				Choices: []chatChoice{{Index: 0, Delta: &chatMessageOut{}, FinishReason: &finish}},
			})
			if req.StreamOptions != nil && req.StreamOptions.IncludeUsage && chunk.Usage != nil {
				writeChunk(chatCompletionResponse{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []chatChoice{},
					Usage: &usageOut{
						PromptTokens:     chunk.Usage.PromptTokens,
						CompletionTokens: chunk.Usage.CompletionTokens,
						TotalTokens:      chunk.Usage.TotalTokens,
						CostUSD:          chunk.Usage.CostUSD,
					},
				})
			}
			break
		}

		delta := &chatMessageOut{}
		if !sentRole {
			delta.Role = "assistant"
			sentRole = true
		}
		if chunk.ToolCall != nil {
			stc := surfaceToolCall{Index: &chunk.ToolCall.Index, ID: chunk.ToolCall.ID}
			if chunk.ToolCall.ID != "" || chunk.ToolCall.Name != "" {
				stc.Type = "function"
			}
			stc.Function.Name = chunk.ToolCall.Name
			stc.Function.Arguments = chunk.ToolCall.ArgumentsDelta
			delta.ToolCalls = []surfaceToolCall{stc}
		} else {
			content := chunk.Delta
			delta.Content = &content
		}
		writeChunk(chatCompletionResponse{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chatChoice{{Index: 0, Delta: delta, FinishReason: nil}},
		})
	}

	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
	s.tailComplete(r, key, model, provider, streamUsage, false)
}

// ---- embeddings ----

type embeddingsRequest struct {
	Model          string          `json:"model"`
	Input          json.RawMessage `json:"input"`
	EncodingFormat string          `json:"encoding_format,omitempty"`
	User           string          `json:"user,omitempty"`
}

type embeddingsResponse struct {
	Object string          `json:"object"`
	Data   []embeddingItem `json:"data"`
	Model  string          `json:"model"`
	Usage  usageOut        `json:"usage"`
}

type embeddingItem struct {
	Object    string `json:"object"`
	Index     int    `json:"index"`
	Embedding any    `json:"embedding"` // []float32 or base64 string
}

func (s *HTTPServer) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	key, ok := s.authenticate(w, r, false)
	if !ok {
		return
	}
	limitBody(w, r)

	var req embeddingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_json", "could not parse request body: "+err.Error())
		return
	}
	if req.Model == "" {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "missing_model", "you must provide a model parameter")
		return
	}
	s.tailModel(r.Context(), req.Model)

	var texts []string
	var single string
	if err := json.Unmarshal(req.Input, &single); err == nil {
		texts = []string{single}
	} else if err := json.Unmarshal(req.Input, &texts); err != nil {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_input", "input must be a string or an array of strings (token arrays are not supported)")
		return
	}
	if len(texts) == 0 {
		s.writeOpenAIError(w, r, http.StatusBadRequest, "invalid_input", "input must not be empty")
		return
	}

	chain, err := s.service.ResolveChain(r.Context(), req.Model)
	if err != nil {
		s.writeOpenAIError(w, r, http.StatusNotFound, "model_not_found", err.Error())
		return
	}

	if be := s.checkKeyLimits(r.Context(), key, req.Model); be != nil {
		s.writeOpenAIError(w, r, budgetStatus(be), be.code, be.msg)
		return
	}

	result, err := s.service.EmbedChain(r.Context(), chain, EmbedParams{Texts: texts, Model: req.Model})
	if err != nil {
		s.writeProviderError(w, r, err)
		return
	}
	s.recordUsage(context.WithoutCancel(r.Context()), key, result.Usage)
	s.tailComplete(r, key, result.Model, result.Provider, result.Usage, false)

	data := make([]embeddingItem, len(result.Embeddings))
	for i, e := range result.Embeddings {
		item := embeddingItem{Object: "embedding", Index: i}
		if req.EncodingFormat == "base64" {
			item.Embedding = encodeFloat32Base64(e.Values)
		} else {
			item.Embedding = e.Values
		}
		data[i] = item
	}
	writeJSON(w, http.StatusOK, embeddingsResponse{
		Object: "list",
		Data:   data,
		Model:  result.Model,
		Usage: usageOut{
			PromptTokens: result.Usage.PromptTokens,
			TotalTokens:  result.Usage.TotalTokens,
			CostUSD:      result.Usage.CostUSD,
		},
	})
}

// encodeFloat32Base64 encodes embeddings the way the OpenAI API does:
// little-endian float32 bytes, base64-encoded.
func encodeFloat32Base64(values []float32) string {
	buf := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// ---- models ----

type modelObject struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (s *HTTPServer) handleListModels(w http.ResponseWriter, r *http.Request) {
	var models []modelObject
	for _, p := range s.service.Registry().List() {
		for _, m := range p.Models(r.Context()) {
			models = append(models, modelObject{
				ID: m, Object: "model", Created: 0, OwnedBy: p.Name(),
			})
		}
	}
	if models == nil {
		models = []modelObject{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": models})
}

func (s *HTTPServer) handleGetModel(w http.ResponseWriter, r *http.Request) {
	want := r.PathValue("model")
	for _, p := range s.service.Registry().List() {
		for _, m := range p.Models(r.Context()) {
			if m == want {
				writeJSON(w, http.StatusOK, modelObject{ID: m, Object: "model", OwnedBy: p.Name()})
				return
			}
		}
	}
	s.writeOpenAIError(w, r, http.StatusNotFound, "model_not_found", fmt.Sprintf("model %q does not exist", want))
}

func orDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
