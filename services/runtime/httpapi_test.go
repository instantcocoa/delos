package runtime

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestHTTPServer(providers ...Provider) *HTTPServer {
	return NewHTTPServer(newTestService(providers...), newTestLogger())
}

func chatProvider() *mockProvider {
	return &mockProvider{
		name:   "openai",
		models: []string{"gpt-4o", "text-embedding-3-small"},
		completeResult: &CompletionResult{
			ID:           "cmpl-123",
			Message:      TextMessage("assistant", "Hello there!"),
			FinishReason: FinishStop,
			Model:        "gpt-4o",
			Usage:        Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15, CostUSD: 0.001},
		},
		streamChunks: []StreamChunk{
			{Delta: "Hello "},
			{Delta: "world"},
			{Done: true, Usage: &Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}},
		},
		embedResult: &EmbedResult{
			Embeddings: []Embedding{{Values: []float32{0.1, 0.2, 0.3}, Dimensions: 3}},
			Model:      "text-embedding-3-small",
			Usage:      Usage{PromptTokens: 4, TotalTokens: 4},
		},
	}
}

func doJSON(t *testing.T, srv *HTTPServer, method, path, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 && strings.Contains(rec.Header().Get("Content-Type"), "json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("response is not JSON: %v\n%s", err, rec.Body.String())
		}
	}
	return rec, out
}

func TestChatCompletions(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())

	rec, out := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if out["object"] != "chat.completion" {
		t.Errorf("object = %v", out["object"])
	}
	choices := out["choices"].([]any)
	msg := choices[0].(map[string]any)["message"].(map[string]any)
	if msg["content"] != "Hello there!" || msg["role"] != "assistant" {
		t.Errorf("unexpected message: %v", msg)
	}
	usage := out["usage"].(map[string]any)
	if usage["total_tokens"].(float64) != 15 {
		t.Errorf("usage = %v", usage)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id header")
	}
}

func TestChatCompletionsContentParts(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"text","text":"hi"}]}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsValidation(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())
	cases := []struct {
		name, body string
		wantStatus int
		wantCode   string
	}{
		{"missing model", `{"messages":[{"role":"user","content":"hi"}]}`, 400, "missing_model"},
		{"missing messages", `{"model":"gpt-4o"}`, 400, "missing_messages"},
		{"unknown model", `{"model":"nope-1","messages":[{"role":"user","content":"hi"}]}`, 404, "model_not_found"},
		{"bad tool type", `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"tools":[{"type":"retrieval","function":{"name":"x"}}]}`, 400, "invalid_request"},
		{"bad content part", `{"model":"gpt-4o","messages":[{"role":"user","content":[{"type":"audio"}]}]}`, 400, "invalid_request"},
		{"bad json", `{`, 400, "invalid_json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, out := doJSON(t, srv, http.MethodPost, "/v1/chat/completions", tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			errObj := out["error"].(map[string]any)
			if errObj["code"] != tc.wantCode {
				t.Errorf("code = %v, want %s", errObj["code"], tc.wantCode)
			}
			if !strings.Contains(errObj["message"].(string), "request id:") {
				t.Errorf("error message should carry the request id, got %v", errObj["message"])
			}
		})
	}
}

func TestChatCompletionsProviderPrefix(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions",
		`{"model":"openai/some-future-model","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("provider/model addressing failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestChatCompletionsStreaming(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true,"stream_options":{"include_usage":true}}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %s", ct)
	}

	var deltas []string
	var sawDone, sawUsage, sawFinish bool
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			sawDone = true
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("bad chunk %q: %v", payload, err)
		}
		if chunk["object"] != "chat.completion.chunk" {
			t.Errorf("object = %v", chunk["object"])
		}
		if u, ok := chunk["usage"].(map[string]any); ok {
			sawUsage = true
			if u["total_tokens"].(float64) != 12 {
				t.Errorf("usage = %v", u)
			}
		}
		choices := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice := choices[0].(map[string]any)
		if fr, ok := choice["finish_reason"].(string); ok && fr == "stop" {
			sawFinish = true
		}
		if delta, ok := choice["delta"].(map[string]any); ok {
			if c, ok := delta["content"].(string); ok && c != "" {
				deltas = append(deltas, c)
			}
		}
	}
	if got := strings.Join(deltas, ""); got != "Hello world" {
		t.Errorf("streamed content = %q", got)
	}
	if !sawDone || !sawUsage || !sawFinish {
		t.Errorf("sawDone=%v sawUsage=%v sawFinish=%v", sawDone, sawUsage, sawFinish)
	}
}

func TestChatCompletionsStreamError(t *testing.T) {
	p := chatProvider()
	p.streamChunks = []StreamChunk{
		{Delta: "partial"},
		{Err: errStreamBroken},
	}
	srv := newTestHTTPServer(p)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	if strings.Contains(body, "[DONE]") {
		t.Error("broken stream must not be terminated with [DONE]")
	}
	if !strings.Contains(body, "provider_stream_error") {
		t.Errorf("expected stream error event, got %s", body)
	}
}

func TestEmbeddings(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())

	rec, out := doJSON(t, srv, http.MethodPost, "/v1/embeddings",
		`{"model":"text-embedding-3-small","input":"hello"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	data := out["data"].([]any)
	emb := data[0].(map[string]any)["embedding"].([]any)
	if len(emb) != 3 {
		t.Errorf("embedding len = %d", len(emb))
	}

	// base64 encoding, as the official openai client requests by default
	rec, out = doJSON(t, srv, http.MethodPost, "/v1/embeddings",
		`{"model":"text-embedding-3-small","input":["hello"],"encoding_format":"base64"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	b64 := out["data"].([]any)[0].(map[string]any)["embedding"].(string)
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("embedding is not valid base64: %v", err)
	}
	if len(raw) != 12 { // 3 float32s
		t.Errorf("decoded %d bytes, want 12", len(raw))
	}
}

func TestListModels(t *testing.T) {
	srv := newTestHTTPServer(chatProvider())
	rec, out := doJSON(t, srv, http.MethodGet, "/v1/models", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	data := out["data"].([]any)
	if len(data) != 2 {
		t.Errorf("models = %v", data)
	}

	rec, _ = doJSON(t, srv, http.MethodGet, "/v1/models/gpt-4o", "")
	if rec.Code != http.StatusOK {
		t.Errorf("get model status = %d", rec.Code)
	}
	rec, _ = doJSON(t, srv, http.MethodGet, "/v1/models/nope", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("get missing model status = %d", rec.Code)
	}
}

func TestAnthropicMessages(t *testing.T) {
	p := chatProvider()
	p.name = "anthropic"
	p.models = []string{"claude-sonnet-4-5"}
	srv := newTestHTTPServer(p)

	rec, out := doJSON(t, srv, http.MethodPost, "/v1/messages",
		`{"model":"claude-sonnet-4-5","max_tokens":100,"system":"be brief","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if out["type"] != "message" || out["role"] != "assistant" {
		t.Errorf("envelope = %v", out)
	}
	content := out["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "Hello there!" {
		t.Errorf("content = %v", content)
	}
	usage := out["usage"].(map[string]any)
	if usage["input_tokens"].(float64) != 10 || usage["output_tokens"].(float64) != 5 {
		t.Errorf("usage = %v", usage)
	}

	// missing max_tokens is an Anthropic-surface error
	rec, out = doJSON(t, srv, http.MethodPost, "/v1/messages",
		`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if out["type"] != "error" {
		t.Errorf("error envelope = %v", out)
	}
}

func TestAnthropicMessagesStreaming(t *testing.T) {
	p := chatProvider()
	p.name = "anthropic"
	p.models = []string{"claude-sonnet-4-5"}
	srv := newTestHTTPServer(p)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, event := range []string{"message_start", "content_block_start", "content_block_delta", "content_block_stop", "message_delta", "message_stop"} {
		if !strings.Contains(body, "event: "+event) {
			t.Errorf("missing SSE event %s in\n%s", event, body)
		}
	}
	if !strings.Contains(body, `"text":"Hello "`) {
		t.Errorf("missing delta text in %s", body)
	}
}

var errStreamBroken = errBroken{}

type errBroken struct{}

func (errBroken) Error() string { return "connection reset by provider" }

func TestChatCompletionsToolCalls(t *testing.T) {
	p := chatProvider()
	p.completeResult = &CompletionResult{
		ID: "cmpl-tools",
		Message: Message{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Paris"}`},
			},
		},
		FinishReason: FinishToolCalls,
		Model:        "gpt-4o",
	}
	srv := newTestHTTPServer(p)

	body := `{
		"model": "gpt-4o",
		"messages": [{"role":"user","content":"weather in paris?"}],
		"tools": [{"type":"function","function":{"name":"get_weather","description":"Get weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],
		"tool_choice": "auto"
	}`
	rec, out := doJSON(t, srv, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	// tools reached the provider
	if p.lastParams == nil || len(p.lastParams.Tools) != 1 || p.lastParams.Tools[0].Name != "get_weather" {
		t.Fatalf("tools not passed through: %+v", p.lastParams)
	}
	if p.lastParams.ToolChoice == nil || p.lastParams.ToolChoice.Mode != "auto" {
		t.Errorf("tool_choice not passed through: %+v", p.lastParams.ToolChoice)
	}

	// tool calls came back in wire format
	choice := out["choices"].([]any)[0].(map[string]any)
	if choice["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason = %v", choice["finish_reason"])
	}
	calls := choice["message"].(map[string]any)["tool_calls"].([]any)
	call := calls[0].(map[string]any)
	if call["id"] != "call_1" || call["type"] != "function" {
		t.Errorf("tool call = %v", call)
	}
	fn := call["function"].(map[string]any)
	if fn["name"] != "get_weather" || fn["arguments"] != `{"city":"Paris"}` {
		t.Errorf("function = %v", fn)
	}
}

func TestChatCompletionsToolResultRoundTrip(t *testing.T) {
	p := chatProvider()
	srv := newTestHTTPServer(p)

	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role":"user","content":"weather?"},
			{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"sunny"}
		]
	}`
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	msgs := p.lastParams.Messages
	if len(msgs) != 3 {
		t.Fatalf("got %d messages", len(msgs))
	}
	if len(msgs[1].ToolCalls) != 1 || msgs[1].ToolCalls[0].ID != "call_1" {
		t.Errorf("assistant tool call lost: %+v", msgs[1])
	}
	if msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" || msgs[2].Text() != "sunny" {
		t.Errorf("tool result lost: %+v", msgs[2])
	}
}

func TestChatCompletionsVision(t *testing.T) {
	p := chatProvider()
	srv := newTestHTTPServer(p)

	body := `{
		"model": "gpt-4o",
		"messages": [{"role":"user","content":[
			{"type":"text","text":"what is this?"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,aGk="}},
			{"type":"image_url","image_url":{"url":"https://example.com/cat.png"}}
		]}]
	}`
	rec, _ := doJSON(t, srv, http.MethodPost, "/v1/chat/completions", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	parts := p.lastParams.Messages[0].Content
	if len(parts) != 3 {
		t.Fatalf("got %d parts", len(parts))
	}
	if parts[1].Type != "image" || parts[1].ImageData != "aGk=" || parts[1].MediaType != "image/png" {
		t.Errorf("data URI image part = %+v", parts[1])
	}
	if parts[2].Type != "image" || parts[2].ImageURL != "https://example.com/cat.png" {
		t.Errorf("remote image part = %+v", parts[2])
	}
}

func TestChatCompletionsStreamingToolCalls(t *testing.T) {
	p := chatProvider()
	p.streamChunks = []StreamChunk{
		{ToolCall: &ToolCallDelta{Index: 0, ID: "call_1", Name: "get_weather"}},
		{ToolCall: &ToolCallDelta{Index: 0, ArgumentsDelta: `{"city":`}},
		{ToolCall: &ToolCallDelta{Index: 0, ArgumentsDelta: `"Paris"}`}},
		{Done: true, FinishReason: FinishToolCalls},
	}
	srv := newTestHTTPServer(p)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `"finish_reason":"tool_calls"`) {
		t.Errorf("missing tool_calls finish reason:\n%s", body)
	}
	if !strings.Contains(body, `"name":"get_weather"`) {
		t.Errorf("missing tool call name:\n%s", body)
	}
	var args string
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var chunk map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk) != nil {
			continue
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		delta, _ := choices[0].(map[string]any)["delta"].(map[string]any)
		calls, _ := delta["tool_calls"].([]any)
		for _, c := range calls {
			fn := c.(map[string]any)["function"].(map[string]any)
			if a, ok := fn["arguments"].(string); ok {
				args += a
			}
		}
	}
	if args != `{"city":"Paris"}` {
		t.Errorf("reassembled arguments = %q", args)
	}
}

func TestAnthropicSurfaceToolUse(t *testing.T) {
	p := chatProvider()
	p.name = "anthropic"
	p.models = []string{"claude-sonnet-4-5"}
	p.completeResult = &CompletionResult{
		ID: "msg_1",
		Message: Message{
			Role:      "assistant",
			Content:   []ContentPart{TextPart("Let me check.")},
			ToolCalls: []ToolCall{{ID: "toolu_1", Name: "get_weather", Arguments: `{"city":"Paris"}`}},
		},
		FinishReason: FinishToolCalls,
		Model:        "claude-sonnet-4-5",
	}
	srv := newTestHTTPServer(p)

	body := `{
		"model":"claude-sonnet-4-5","max_tokens":100,
		"tools":[{"name":"get_weather","description":"d","input_schema":{"type":"object"}}],
		"tool_choice":{"type":"auto"},
		"messages":[
			{"role":"user","content":"weather?"},
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_0","name":"get_weather","input":{"city":"Berlin"}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_0","content":"rainy"}]}
		]
	}`
	rec, out := doJSON(t, srv, http.MethodPost, "/v1/messages", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}

	// request side: tool_use history and tool_result mapped to internal form
	var sawToolMsg, sawAssistantCall bool
	for _, m := range p.lastParams.Messages {
		if m.Role == "tool" && m.ToolCallID == "toolu_0" && m.Text() == "rainy" {
			sawToolMsg = true
		}
		if m.Role == "assistant" && len(m.ToolCalls) == 1 {
			sawAssistantCall = true
		}
	}
	if !sawToolMsg || !sawAssistantCall {
		t.Errorf("history mapping failed: %+v", p.lastParams.Messages)
	}

	// response side: tool_use block + stop_reason
	if out["stop_reason"] != "tool_use" {
		t.Errorf("stop_reason = %v", out["stop_reason"])
	}
	blocks := out["content"].([]any)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %v", blocks)
	}
	toolBlock := blocks[1].(map[string]any)
	if toolBlock["type"] != "tool_use" || toolBlock["name"] != "get_weather" {
		t.Errorf("tool block = %v", toolBlock)
	}
}

func TestAnthropicSurfaceStreamingToolUse(t *testing.T) {
	p := chatProvider()
	p.name = "anthropic"
	p.models = []string{"claude-sonnet-4-5"}
	p.streamChunks = []StreamChunk{
		{Delta: "Checking. "},
		{ToolCall: &ToolCallDelta{Index: 0, ID: "toolu_1", Name: "get_weather"}},
		{ToolCall: &ToolCallDelta{Index: 0, ArgumentsDelta: `{"city":"Paris"}`}},
		{Done: true, FinishReason: FinishToolCalls, Usage: &Usage{PromptTokens: 5, CompletionTokens: 7}},
	}
	srv := newTestHTTPServer(p)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages",
		strings.NewReader(`{"model":"claude-sonnet-4-5","max_tokens":100,"stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{
		`"text_delta"`,
		`"input_json_delta"`,
		`"name":"get_weather"`,
		`"stop_reason":"tool_use"`,
		"event: message_stop",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in stream:\n%s", want, body)
		}
	}
	if got := strings.Count(body, "event: content_block_start"); got != 2 {
		t.Errorf("expected 2 content blocks (text + tool_use), got %d:\n%s", got, body)
	}
	if got := strings.Count(body, "event: content_block_stop"); got != 2 {
		t.Errorf("expected 2 content_block_stop events, got %d", got)
	}
}

func jsonReq(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func doReq(srv *HTTPServer, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}
