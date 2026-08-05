package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// ---- Anthropic Messages surface (POST /v1/messages) ----

type anthMessagesRequest struct {
	Model         string          `json:"model"`
	Messages      []anthMessageIn `json:"messages"`
	System        json.RawMessage `json:"system,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Tools         []anthToolIn    `json:"tools,omitempty"`
	ToolChoice    *anthToolChoice `json:"tool_choice,omitempty"`
}

type anthToolIn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type anthToolChoice struct {
	Type string `json:"type"` // auto | any | tool | none
	Name string `json:"name,omitempty"`
}

type anthMessageIn struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// anthBlockIn is a request content block on the Anthropic surface.
type anthBlockIn struct {
	Type string `json:"type"`

	Text string `json:"text,omitempty"`

	Source *anthImageSource `json:"source,omitempty"`

	// tool_use (assistant history)
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result (user turn)
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// anthBlockOut is a response content block on the Anthropic surface.
type anthBlockOut struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthMessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Model        string         `json:"model"`
	Content      []anthBlockOut `json:"content"`
	StopReason   *string        `json:"stop_reason"` // null until the message completes
	StopSequence *string        `json:"stop_sequence"`
	Usage        anthUsage      `json:"usage"`
}

type anthUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// ---- surface → internal translation ----

// anthSystemToText flattens the system field (string or text blocks).
func anthSystemToText(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var blocks []anthBlockIn
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", fmt.Errorf("system: must be a string or an array of text blocks")
	}
	out := ""
	for _, b := range blocks {
		if b.Type != "text" {
			return "", fmt.Errorf("system: block type %q is not allowed", b.Type)
		}
		out += b.Text
	}
	return out, nil
}

// anthImagePart converts an Anthropic image source into an internal part.
func anthImagePart(src *anthImageSource, where string) (ContentPart, error) {
	if src == nil {
		return ContentPart{}, fmt.Errorf("%s: image block missing source", where)
	}
	switch src.Type {
	case "base64":
		return ContentPart{Type: "image", ImageData: src.Data, MediaType: src.MediaType}, nil
	case "url":
		return ContentPart{Type: "image", ImageURL: src.URL}, nil
	default:
		return ContentPart{}, fmt.Errorf("%s: unsupported image source type %q", where, src.Type)
	}
}

// anthConvertMessages maps Anthropic-surface messages to internal messages.
// tool_result blocks become separate role=="tool" messages.
func anthConvertMessages(in []anthMessageIn) ([]Message, error) {
	var out []Message
	for i, m := range in {
		where := fmt.Sprintf("messages[%d]", i)

		var text string
		if err := json.Unmarshal(m.Content, &text); err == nil {
			out = append(out, TextMessage(m.Role, text))
			continue
		}
		var blocks []anthBlockIn
		if err := json.Unmarshal(m.Content, &blocks); err != nil {
			return nil, fmt.Errorf("%s: content must be a string or an array of content blocks", where)
		}

		msg := Message{Role: m.Role}
		flush := func() {
			if len(msg.Content) > 0 || len(msg.ToolCalls) > 0 {
				out = append(out, msg)
				msg = Message{Role: m.Role}
			}
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				msg.Content = append(msg.Content, TextPart(b.Text))
			case "image":
				part, err := anthImagePart(b.Source, where)
				if err != nil {
					return nil, err
				}
				msg.Content = append(msg.Content, part)
			case "tool_use":
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{
					ID:        b.ID,
					Name:      b.Name,
					Arguments: string(b.Input),
				})
			case "tool_result":
				flush()
				content := ""
				if len(b.Content) > 0 {
					if err := json.Unmarshal(b.Content, &content); err != nil {
						var inner []anthBlockIn
						if err := json.Unmarshal(b.Content, &inner); err != nil {
							return nil, fmt.Errorf("%s: invalid tool_result content", where)
						}
						for _, ib := range inner {
							if ib.Type == "text" {
								content += ib.Text
							}
						}
					}
				}
				out = append(out, Message{
					Role:       "tool",
					ToolCallID: b.ToolUseID,
					Content:    []ContentPart{TextPart(content)},
				})
			default:
				return nil, fmt.Errorf("%s: unsupported content block type %q", where, b.Type)
			}
		}
		flush()
	}
	return out, nil
}

// anthStopReason maps internal finish reasons to Anthropic stop reasons.
func anthStopReason(finish string) string {
	switch finish {
	case FinishToolCalls:
		return "tool_use"
	case FinishLength:
		return "max_tokens"
	case FinishContentFilter:
		return "refusal"
	default:
		return "end_turn"
	}
}

// anthBlocksFromResult converts an internal assistant message to response blocks.
func anthBlocksFromResult(msg Message) []anthBlockOut {
	var blocks []anthBlockOut
	if text := msg.Text(); text != "" {
		blocks = append(blocks, anthBlockOut{Type: "text", Text: text})
	}
	for _, tc := range msg.ToolCalls {
		input := json.RawMessage(tc.Arguments)
		if len(input) == 0 || !json.Valid(input) {
			input = json.RawMessage("{}")
		}
		blocks = append(blocks, anthBlockOut{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: input})
	}
	if blocks == nil {
		blocks = []anthBlockOut{}
	}
	return blocks
}

func (s *HTTPServer) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	var req anthMessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "could not parse request body: "+err.Error())
		return
	}
	if req.Model == "" {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	if len(req.Messages) == 0 {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "messages is required")
		return
	}
	if req.MaxTokens <= 0 {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", "max_tokens is required and must be positive")
		return
	}
	s.tailModel(r.Context(), req.Model)

	var messages []Message
	if len(req.System) > 0 && string(req.System) != "null" {
		sys, err := anthSystemToText(req.System)
		if err != nil {
			s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		messages = append(messages, TextMessage("system", sys))
	}
	converted, err := anthConvertMessages(req.Messages)
	if err != nil {
		s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	messages = append(messages, converted...)

	key, ok := s.authenticate(w, r, true)
	if !ok {
		return
	}

	chain, err := s.service.ResolveChain(r.Context(), req.Model)
	if err != nil {
		s.writeAnthropicError(w, r, http.StatusNotFound, "not_found_error", err.Error())
		return
	}

	if be := s.checkKeyLimits(r.Context(), key, req.Model); be != nil {
		typ := "rate_limit_error"
		if be.code == "model_not_allowed" {
			typ = "permission_error"
		}
		s.writeAnthropicError(w, r, budgetStatus(be), typ, be.msg)
		return
	}

	params := CompletionParams{
		Messages:    messages,
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Stop:        req.StopSequences,
		Temperature: req.Temperature,
		TopP:        req.TopP,
	}
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.InputSchema,
		})
	}
	if tc := req.ToolChoice; tc != nil {
		switch tc.Type {
		case "auto":
			params.ToolChoice = &ToolChoice{Mode: "auto"}
		case "any":
			params.ToolChoice = &ToolChoice{Mode: "required"}
		case "none":
			params.ToolChoice = &ToolChoice{Mode: "none"}
		case "tool":
			params.ToolChoice = &ToolChoice{Mode: "tool", Name: tc.Name}
		default:
			s.writeAnthropicError(w, r, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("unsupported tool_choice type %q", tc.Type))
			return
		}
	}

	if req.Stream {
		s.streamAnthropicMessages(w, r, chain, params, key)
		return
	}

	result, err := s.service.CompleteChain(r.Context(), chain, params)
	if err != nil {
		s.writeAnthropicProviderError(w, r, err)
		return
	}
	s.recordUsage(context.WithoutCancel(r.Context()), key, result.Usage)
	s.tailComplete(r, key, result.Model, result.Provider, result.Usage, result.Cached)

	writeJSON(w, http.StatusOK, anthMessagesResponse{
		ID:         orDefault(result.ID, "msg_"+uuid.NewString()),
		Type:       "message",
		Role:       "assistant",
		Model:      result.Model,
		Content:    anthBlocksFromResult(result.Message),
		StopReason: ptr(anthStopReason(result.FinishReason)),
		Usage: anthUsage{
			InputTokens:  result.Usage.PromptTokens,
			OutputTokens: result.Usage.CompletionTokens,
		},
	})
}

// writeAnthropicProviderError maps provider failures onto the Anthropic
// error envelope, preserving the upstream message.
func (s *HTTPServer) writeAnthropicProviderError(w http.ResponseWriter, r *http.Request, err error) {
	if pe, ok := errAs[*ProviderError](err); ok {
		status := http.StatusBadGateway
		typ := "api_error"
		switch pe.StatusCode {
		case http.StatusTooManyRequests:
			status, typ = http.StatusTooManyRequests, "rate_limit_error"
		case http.StatusBadRequest, http.StatusNotFound, http.StatusConflict, http.StatusUnprocessableEntity:
			status, typ = http.StatusBadRequest, "invalid_request_error"
		}
		s.writeAnthropicError(w, r, status, typ, fmt.Sprintf("provider %s: %s", pe.Provider, pe.Message))
		return
	}
	s.writeAnthropicError(w, r, http.StatusBadGateway, "api_error", err.Error())
}

func (s *HTTPServer) streamAnthropicMessages(w http.ResponseWriter, r *http.Request, chain []RouteTarget, params CompletionParams, key *VirtualKey) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeAnthropicError(w, r, http.StatusInternalServerError, "api_error", "response writer does not support streaming")
		return
	}

	chunks, err := s.service.CompleteStreamChain(r.Context(), chain, params)
	if err != nil {
		s.writeAnthropicProviderError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	emit := func(event string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	msgID := "msg_" + uuid.NewString()
	emit("message_start", map[string]any{
		"type": "message_start",
		"message": anthMessagesResponse{
			ID: msgID, Type: "message", Role: "assistant", Model: params.Model,
			Content: []anthBlockOut{},
		},
	})

	// Block state machine: text deltas share one block; each tool call gets
	// its own block. blockIndex is the Anthropic-surface content index.
	blockIndex := -1
	blockOpen := false
	currentToolIndex := -1 // internal tool index of the open tool block, -1 = text

	closeBlock := func() {
		if blockOpen {
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": blockIndex})
			blockOpen = false
		}
	}
	// openBlock emits content_block_start with the block as a literal map:
	// the real API always includes "text":"" on an opening text block, and
	// official SDKs rely on it to initialize their accumulators.
	openBlock := func(block map[string]any) {
		closeBlock()
		blockIndex++
		blockOpen = true
		emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": blockIndex, "content_block": block,
		})
	}

	var usage *Usage
	provider := ""
	finish := FinishStop
	for chunk := range chunks {
		if chunk.Err != nil {
			emit("error", anthErrorEnvelope{Type: "error", Error: anthErrorBody{
				Type: "api_error", Message: chunk.Err.Error(),
			}})
			s.tailFailure(r, http.StatusBadGateway, "provider_stream_error", chunk.Err.Error())
			return
		}
		if chunk.Provider != "" {
			provider = chunk.Provider
		}
		if chunk.Done {
			usage = chunk.Usage
			if usage != nil {
				s.recordUsage(context.WithoutCancel(r.Context()), key, *usage)
			}
			if chunk.FinishReason != "" {
				finish = chunk.FinishReason
			}
			break
		}
		if chunk.ToolCall != nil {
			tc := chunk.ToolCall
			if tc.Index != currentToolIndex {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("toolu_%s", uuid.NewString())
				}
				openBlock(map[string]any{
					"type": "tool_use", "id": id, "name": tc.Name, "input": map[string]any{},
				})
				currentToolIndex = tc.Index
			}
			if tc.ArgumentsDelta != "" {
				emit("content_block_delta", map[string]any{
					"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]string{"type": "input_json_delta", "partial_json": tc.ArgumentsDelta},
				})
			}
			continue
		}
		if chunk.Delta != "" {
			if !blockOpen || currentToolIndex != -1 {
				openBlock(map[string]any{"type": "text", "text": ""})
				currentToolIndex = -1
			}
			emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": blockIndex,
				"delta": map[string]string{"type": "text_delta", "text": chunk.Delta},
			})
		}
	}

	closeBlock()
	deltaEvent := map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": anthStopReason(finish), "stop_sequence": nil},
	}
	if usage != nil {
		deltaEvent["usage"] = anthUsage{InputTokens: usage.PromptTokens, OutputTokens: usage.CompletionTokens}
	}
	emit("message_delta", deltaEvent)
	emit("message_stop", map[string]any{"type": "message_stop"})

	streamUsage := Usage{}
	if usage != nil {
		streamUsage = *usage
	}
	s.tailComplete(r, key, params.Model, provider, streamUsage, false)
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }
