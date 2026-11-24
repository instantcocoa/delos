// Command mockupstream is a deterministic provider used by the
// client-compatibility and agent end-to-end tests. It answers the OpenAI
// surface (/v1/models, /v1/chat/completions, /v1/responses, /v1/embeddings)
// and the Anthropic surface (/v1/messages, /v1/messages/count_tokens) with
// fixed, echoing responses, so the suites exercise real client libraries and
// agent CLIs against the real gateway without spending a provider token.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Model is the single model this upstream serves. The gateway discovers it
// from /v1/models, so clients can request it by name.
const Model = "delos-mock"

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	Tools []json.RawMessage `json:"tools"`
}

// lastUserText extracts the final user message as plain text, accepting both
// string content and content-part arrays.
func (r chatRequest) lastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role != "user" {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			return s
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &parts) == nil {
			var b strings.Builder
			for _, p := range parts {
				if p.Type == "text" {
					b.WriteString(p.Text)
				}
			}
			return b.String()
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"message": msg, "type": "invalid_request_error"},
	})
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": Model, "object": "model", "created": 1730000000, "owned_by": "delos-mock"},
		},
	})
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse request body: "+err.Error())
		return
	}
	if req.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required")
		return
	}

	reply := "echo: " + req.lastUserText()
	id := "chatcmpl-mock-1"
	created := time.Now().Unix()

	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "object": "chat.completion", "created": created, "model": req.Model,
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18,
			},
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	frame := func(v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{
			"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
			"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
		}
	}

	frame(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	// Stream word by word so clients see several deltas.
	for _, word := range strings.SplitAfter(reply, " ") {
		if word == "" {
			continue
		}
		frame(chunk(map[string]any{"content": word}, nil))
	}
	frame(chunk(map[string]any{}, "stop"))
	frame(map[string]any{
		"id": id, "object": "chat.completion.chunk", "created": created, "model": req.Model,
		"choices": []map[string]any{},
		"usage": map[string]any{
			"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18,
		},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse request body: "+err.Error())
		return
	}
	var texts []string
	var single string
	if json.Unmarshal(req.Input, &single) == nil {
		texts = []string{single}
	} else if err := json.Unmarshal(req.Input, &texts); err != nil {
		writeError(w, http.StatusBadRequest, "input must be a string or an array of strings")
		return
	}

	data := make([]map[string]any, 0, len(texts))
	for i, text := range texts {
		// Deterministic, content-derived vector: clients only check shape.
		vec := make([]float64, 4)
		for j := range vec {
			vec[j] = float64((len(text)+j)%10) / 10
		}
		data = append(data, map[string]any{"object": "embedding", "index": i, "embedding": vec})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list", "data": data, "model": req.Model,
		"usage": map[string]any{"prompt_tokens": 4, "total_tokens": 4},
	})
}

// ---- Anthropic Messages surface ----

type anthRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
	Messages  []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	System json.RawMessage `json:"system"`
}

// lastAnthUserText extracts the final user turn as plain text.
func (r anthRequest) lastAnthUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		m := r.Messages[i]
		if m.Role != "user" {
			continue
		}
		var s string
		if json.Unmarshal(m.Content, &s) == nil {
			return s
		}
		var parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(m.Content, &parts) == nil {
			var b strings.Builder
			for _, p := range parts {
				if p.Type == "text" {
					b.WriteString(p.Text)
				}
			}
			return b.String()
		}
	}
	return ""
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	var req anthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse request body: "+err.Error())
		return
	}
	model := req.Model
	if model == "" {
		model = Model
	}
	reply := "echo: " + req.lastAnthUserText()
	id := "msg_mock_1"

	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": model,
			"content":     []map[string]any{{"type": "text", "text": reply}},
			"stop_reason": "end_turn",
			"usage": map[string]any{
				"input_tokens": 11, "output_tokens": 7,
				"cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
			},
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
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
	emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": id, "type": "message", "role": "assistant", "model": model,
			"content": []map[string]any{},
			"usage":   map[string]any{"input_tokens": 11, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0},
		},
	})
	emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": reply}})
	emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 7}})
	emit("message_stop", map[string]any{"type": "message_stop"})
}

func handleCountTokens(w http.ResponseWriter, r *http.Request) {
	var req anthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse request body: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"input_tokens": 42})
}

// ---- OpenAI Responses surface ----

func handleResponses(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model  string `json:"model"`
		Input  any    `json:"input"`
		Stream bool   `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse request body: "+err.Error())
		return
	}
	model := req.Model
	if model == "" {
		model = Model
	}
	reply := "echo: responses"
	id := "resp_mock_1"

	if !req.Stream {
		writeJSON(w, http.StatusOK, map[string]any{
			"id": id, "object": "response", "status": "completed", "model": model,
			"output": []map[string]any{{
				"type": "message", "id": "msg_mock_1", "role": "assistant", "status": "completed",
				"content": []map[string]any{{"type": "output_text", "text": reply}},
			}},
			"usage": map[string]any{"input_tokens": 11, "output_tokens": 7, "total_tokens": 18},
		})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
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
	emit("response.created", map[string]any{"type": "response.created", "response": map[string]any{"id": id, "object": "response", "status": "in_progress", "model": model}})
	emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "item_id": "msg_mock_1", "output_index": 0, "content_index": 0, "delta": reply})
	emit("response.completed", map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "object": "response", "status": "completed", "model": model}})
}

func main() {
	addr := flag.String("addr", "127.0.0.1:9099", "listen address")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", handleModels)
	mux.HandleFunc("POST /v1/chat/completions", handleChat)
	mux.HandleFunc("POST /v1/embeddings", handleEmbeddings)
	mux.HandleFunc("POST /v1/messages", handleMessages)
	mux.HandleFunc("POST /v1/messages/count_tokens", handleCountTokens)
	mux.HandleFunc("POST /v1/responses", handleResponses)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "model": Model})
	})

	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("mockupstream listening on %s (model %s)", *addr, Model)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("mockupstream: %v", err)
	}
}
