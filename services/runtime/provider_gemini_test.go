package runtime

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// geminiTestProvider returns a provider pointed at an httptest server running
// handler. The server is torn down when the test ends.
func geminiTestProvider(t *testing.T, handler http.HandlerFunc) *GeminiProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	p := NewGeminiProvider("test-key")
	p.baseURL = srv.URL
	return p
}

// geminiCapture records the request the provider sent and replies with body.
func geminiCapture(t *testing.T, status int, body string) (*GeminiProvider, *capturedRequest) {
	t.Helper()
	cap := &capturedRequest{}
	p := geminiTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		cap.count++
		cap.path = r.URL.Path
		cap.query = r.URL.RawQuery
		cap.apiKey = r.Header.Get("x-goog-api-key")
		cap.body = raw
		if strings.Contains(body, "data:") {
			w.Header().Set("Content-Type", "text/event-stream")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return p, cap
}

// geminiCostClose compares costs with a tolerance: the provider computes
// rate*tokens/1000 at runtime, which differs from the exactly-folded constant
// in the last ULP.
func geminiCostClose(got, want float64) bool {
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	return diff < 1e-12
}

type capturedRequest struct {
	count  int
	path   string
	query  string
	apiKey string
	body   []byte
}

// decode unmarshals the captured body into a generic map.
func (c *capturedRequest) decode(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(c.body, &out); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, c.body)
	}
	return out
}

func geminiOKBody(text string) string {
	return `{"responseId":"resp-1","modelVersion":"gemini-2.5-flash","candidates":[{"content":{"role":"model","parts":[{"text":"` + text + `"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`
}

// ---- request translation ----

func TestGeminiSystemInstruction(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("hi"))
	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			TextMessage("system", "be terse"),
			TextMessage("system", "answer in english"),
			TextMessage("user", "hello"),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if cap.path != "/models/gemini-2.5-flash:generateContent" {
		t.Errorf("path = %q", cap.path)
	}
	if cap.apiKey != "test-key" {
		t.Errorf("x-goog-api-key = %q", cap.apiKey)
	}

	body := cap.decode(t)
	sys, ok := body["systemInstruction"].(map[string]any)
	if !ok {
		t.Fatalf("no systemInstruction: %s", cap.body)
	}
	parts := sys["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("systemInstruction parts = %v", parts)
	}
	if parts[0].(map[string]any)["text"] != "be terse" {
		t.Errorf("first system part = %v", parts[0])
	}
	contents := body["contents"].([]any)
	if len(contents) != 1 || contents[0].(map[string]any)["role"] != "user" {
		t.Errorf("contents = %v", contents)
	}
}

func TestGeminiRolesAndGenerationConfig(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("hi"))
	temp, topP := 0.3, 0.9
	_, err := p.Complete(context.Background(), CompletionParams{
		Model:       "gemini-2.5-flash",
		Temperature: &temp,
		TopP:        &topP,
		MaxTokens:   256,
		Stop:        []string{"END"},
		Messages: []Message{
			TextMessage("user", "hi"),
			TextMessage("assistant", "hello"),
			TextMessage("user", "bye"),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	body := cap.decode(t)
	contents := body["contents"].([]any)
	wantRoles := []string{"user", "model", "user"}
	if len(contents) != len(wantRoles) {
		t.Fatalf("contents = %v", contents)
	}
	for i, want := range wantRoles {
		if got := contents[i].(map[string]any)["role"]; got != want {
			t.Errorf("contents[%d].role = %v, want %s", i, got, want)
		}
	}

	gc := body["generationConfig"].(map[string]any)
	if gc["temperature"].(float64) != 0.3 || gc["topP"].(float64) != 0.9 {
		t.Errorf("generationConfig = %v", gc)
	}
	if gc["maxOutputTokens"].(float64) != 256 {
		t.Errorf("maxOutputTokens = %v", gc["maxOutputTokens"])
	}
	if stop := gc["stopSequences"].([]any); len(stop) != 1 || stop[0] != "END" {
		t.Errorf("stopSequences = %v", stop)
	}
}

func TestGeminiToolsSchemaScrubbed(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("hi"))
	schema := json.RawMessage(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"city": {"type": "string"},
			"opts": {"type": "object", "additionalProperties": false, "properties": {"units": {"type": "string"}}}
		}
	}`)
	_, err := p.Complete(context.Background(), CompletionParams{
		Model:      "gemini-2.5-flash",
		Messages:   []Message{TextMessage("user", "weather?")},
		Tools:      []Tool{{Name: "get_weather", Description: "look up weather", Parameters: schema}},
		ToolChoice: &ToolChoice{Mode: "tool", Name: "get_weather"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if strings.Contains(string(cap.body), "$schema") {
		t.Errorf("$schema survived scrubbing: %s", cap.body)
	}
	if strings.Contains(string(cap.body), "additionalProperties") {
		t.Errorf("additionalProperties survived scrubbing (incl. nested): %s", cap.body)
	}

	body := cap.decode(t)
	tools := body["tools"].([]any)
	decls := tools[0].(map[string]any)["functionDeclarations"].([]any)
	decl := decls[0].(map[string]any)
	if decl["name"] != "get_weather" || decl["description"] != "look up weather" {
		t.Errorf("declaration = %v", decl)
	}
	params := decl["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("scrubbing dropped real schema content: %v", params)
	}
	if _, ok := params["properties"].(map[string]any)["city"]; !ok {
		t.Errorf("scrubbing dropped properties: %v", params)
	}

	cfg := body["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
	if cfg["mode"] != "ANY" {
		t.Errorf("mode = %v", cfg["mode"])
	}
	allowed := cfg["allowedFunctionNames"].([]any)
	if len(allowed) != 1 || allowed[0] != "get_weather" {
		t.Errorf("allowedFunctionNames = %v", allowed)
	}
}

func TestGeminiToolChoiceModes(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"auto", "AUTO"},
		{"none", "NONE"},
		{"required", "ANY"},
	} {
		p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("hi"))
		_, err := p.Complete(context.Background(), CompletionParams{
			Model:      "gemini-2.5-flash",
			Messages:   []Message{TextMessage("user", "hi")},
			ToolChoice: &ToolChoice{Mode: tc.mode},
		})
		if err != nil {
			t.Fatalf("mode %s: %v", tc.mode, err)
		}
		cfg := cap.decode(t)["toolConfig"].(map[string]any)["functionCallingConfig"].(map[string]any)
		if cfg["mode"] != tc.want {
			t.Errorf("mode %s -> %v, want %s", tc.mode, cfg["mode"], tc.want)
		}
	}
}

func TestGeminiResponseFormat(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("hi"))
	_, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
		ResponseFormat: &ResponseFormat{
			Type:   "json_schema",
			Name:   "answer",
			Schema: json.RawMessage(`{"$schema":"x","type":"object","additionalProperties":false}`),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	gc := cap.decode(t)["generationConfig"].(map[string]any)
	if gc["responseMimeType"] != "application/json" {
		t.Errorf("responseMimeType = %v", gc["responseMimeType"])
	}
	schema := gc["responseSchema"].(map[string]any)
	if _, ok := schema["$schema"]; ok {
		t.Errorf("responseSchema not scrubbed: %v", schema)
	}
	if schema["type"] != "object" {
		t.Errorf("responseSchema = %v", schema)
	}
}

func TestGeminiInlineImage(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("a cat"))
	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "gemini-2.5-flash",
		Messages: []Message{{Role: "user", Content: []ContentPart{
			TextPart("what is this?"),
			{Type: "image", ImageData: "aGVsbG8=", MediaType: "image/png"},
		}}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	parts := cap.decode(t)["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 {
		t.Fatalf("parts = %v", parts)
	}
	inline := parts[1].(map[string]any)["inline_data"].(map[string]any)
	if inline["mime_type"] != "image/png" || inline["data"] != "aGVsbG8=" {
		t.Errorf("inline_data = %v", inline)
	}
}

func TestGeminiRemoteImageURLRejected(t *testing.T) {
	p := NewGeminiProvider("k")
	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "gemini-2.5-flash",
		Messages: []Message{{Role: "user", Content: []ContentPart{
			{Type: "image", ImageURL: "https://example.com/cat.png"},
		}}},
	})
	if err == nil {
		t.Fatal("expected an error for a remote image URL")
	}
	if !strings.Contains(err.Error(), "remote image URLs") {
		t.Errorf("error = %v", err)
	}
}

func TestGeminiToolCallRoundTrip(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("it is 72F"))
	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			TextMessage("user", "weather in SF?"),
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "get_weather", Arguments: `{"city":"SF"}`}}},
			{Role: "tool", ToolCallID: "call_1", Content: []ContentPart{TextPart(`{"temp":72}`)}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	contents := cap.decode(t)["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %v", contents)
	}

	model := contents[1].(map[string]any)
	if model["role"] != "model" {
		t.Errorf("assistant role = %v", model["role"])
	}
	fc := model["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	if fc["name"] != "get_weather" {
		t.Errorf("functionCall = %v", fc)
	}
	if args := fc["args"].(map[string]any); args["city"] != "SF" {
		t.Errorf("args = %v (must be a parsed object, not a string)", args)
	}

	result := contents[2].(map[string]any)
	if result["role"] != "user" {
		t.Errorf("tool result role = %v", result["role"])
	}
	fr := result["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	// Gemini keys function results by NAME, recovered from the prior call ID.
	if fr["name"] != "get_weather" {
		t.Errorf("functionResponse name = %v, want get_weather", fr["name"])
	}
	if resp := fr["response"].(map[string]any); resp["temp"].(float64) != 72 {
		t.Errorf("functionResponse response = %v", resp)
	}
}

func TestGeminiToolResultWithoutKnownName(t *testing.T) {
	p := NewGeminiProvider("k")
	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			TextMessage("user", "hi"),
			{Role: "tool", ToolCallID: "call_unknown", Content: []ContentPart{TextPart("ok")}},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "function name") {
		t.Fatalf("expected a name-mapping error, got %v", err)
	}
}

func TestGeminiNonJSONToolResultWrapped(t *testing.T) {
	p, cap := geminiCapture(t, http.StatusOK, geminiOKBody("ok"))
	_, err := p.Complete(context.Background(), CompletionParams{
		Model: "gemini-2.5-flash",
		Messages: []Message{
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "ping"}}},
			{Role: "tool", ToolCallID: "c1", Content: []ContentPart{TextPart("pong")}},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	contents := cap.decode(t)["contents"].([]any)
	fr := contents[1].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
	if resp := fr["response"].(map[string]any); resp["result"] != "pong" {
		t.Errorf("response = %v, want it wrapped in an object", resp)
	}
	// A tool call with no arguments still sends a valid args object.
	fc := contents[0].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionCall"].(map[string]any)
	if args, ok := fc["args"].(map[string]any); !ok || len(args) != 0 {
		t.Errorf("args = %v, want {}", fc["args"])
	}
}

// ---- Complete ----

func TestGeminiComplete(t *testing.T) {
	p, _ := geminiCapture(t, http.StatusOK, geminiOKBody("Hello there!"))
	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.ID != "resp-1" {
		t.Errorf("ID = %q", result.ID)
	}
	if result.Text() != "Hello there!" {
		t.Errorf("Text() = %q", result.Text())
	}
	if result.FinishReason != FinishStop {
		t.Errorf("FinishReason = %q", result.FinishReason)
	}
	if result.Provider != "gemini" || result.Model != "gemini-2.5-flash" {
		t.Errorf("provider/model = %q/%q", result.Provider, result.Model)
	}
	if result.Usage.PromptTokens != 10 || result.Usage.CompletionTokens != 5 || result.Usage.TotalTokens != 15 {
		t.Errorf("usage = %+v", result.Usage)
	}
	if want := 0.0003 * 15 / 1000; !geminiCostClose(result.Usage.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", result.Usage.CostUSD, want)
	}
}

func TestGeminiCompleteToolCalls(t *testing.T) {
	body := `{"responseId":"resp-2","modelVersion":"gemini-2.5-flash","candidates":[{"content":{"role":"model","parts":[
		{"text":"looking that up"},
		{"functionCall":{"name":"get_weather","args":{"city":"SF"}}},
		{"functionCall":{"name":"get_time","args":{"tz":"PT"}}}
	]},"finishReason":"STOP"}]}`
	p, _ := geminiCapture(t, http.StatusOK, body)
	result, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "weather?")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.FinishReason != FinishToolCalls {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, FinishToolCalls)
	}
	if result.Text() != "looking that up" {
		t.Errorf("Text() = %q", result.Text())
	}
	if len(result.Message.ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", result.Message.ToolCalls)
	}
	first := result.Message.ToolCalls[0]
	if first.ID != "call_1" || first.Name != "get_weather" {
		t.Errorf("first tool call = %+v", first)
	}
	if !strings.Contains(first.Arguments, `"city"`) {
		t.Errorf("arguments = %q", first.Arguments)
	}
	if result.Message.ToolCalls[1].ID != "call_2" {
		t.Errorf("second tool call ID = %q", result.Message.ToolCalls[1].ID)
	}
}

func TestGeminiFinishReasonMapping(t *testing.T) {
	for _, tc := range []struct{ upstream, want string }{
		{"STOP", FinishStop},
		{"MAX_TOKENS", FinishLength},
		{"SAFETY", FinishContentFilter},
		{"PROHIBITED_CONTENT", FinishContentFilter},
	} {
		body := `{"candidates":[{"content":{"parts":[{"text":"x"}]},"finishReason":"` + tc.upstream + `"}]}`
		p, _ := geminiCapture(t, http.StatusOK, body)
		result, err := p.Complete(context.Background(), CompletionParams{
			Model:    "gemini-2.5-flash",
			Messages: []Message{TextMessage("user", "hi")},
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.upstream, err)
		}
		if result.FinishReason != tc.want {
			t.Errorf("%s -> %q, want %q", tc.upstream, result.FinishReason, tc.want)
		}
	}
}

func TestGeminiAPIErrorPassthrough(t *testing.T) {
	body := `{"error":{"code":429,"message":"Resource has been exhausted (e.g. check quota).","status":"RESOURCE_EXHAUSTED"}}`
	p, _ := geminiCapture(t, http.StatusTooManyRequests, body)
	_, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	provErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("error type = %T (%v), want *ProviderError", err, err)
	}
	if provErr.Provider != "gemini" || provErr.StatusCode != http.StatusTooManyRequests {
		t.Errorf("provider error = %+v", provErr)
	}
	if provErr.Message != "Resource has been exhausted (e.g. check quota)." {
		t.Errorf("message = %q, want the upstream text verbatim", provErr.Message)
	}
	if !provErr.Retryable() {
		t.Error("429 should be retryable")
	}
}

func TestGeminiAPIErrorNonJSON(t *testing.T) {
	p, _ := geminiCapture(t, http.StatusBadGateway, "upstream exploded")
	_, err := p.Complete(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	provErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("error type = %T (%v)", err, err)
	}
	if provErr.Message != "upstream exploded" {
		t.Errorf("message = %q", provErr.Message)
	}
}

// ---- streaming ----

func collectGeminiChunks(t *testing.T, ch <-chan StreamChunk) []StreamChunk {
	t.Helper()
	var out []StreamChunk
	for chunk := range ch {
		out = append(out, chunk)
	}
	return out
}

func TestGeminiCompleteStream(t *testing.T) {
	body := strings.Join([]string{
		`data: {"responseId":"resp-3","modelVersion":"gemini-2.5-flash","candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]}}]}`,
		``,
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"SF"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`,
		``,
		``,
	}, "\n")

	p, cap := geminiCapture(t, http.StatusOK, body)
	ch, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	chunks := collectGeminiChunks(t, ch)

	if cap.path != "/models/gemini-2.5-flash:streamGenerateContent" || cap.query != "alt=sse" {
		t.Errorf("path/query = %q %q", cap.path, cap.query)
	}
	if len(chunks) != 4 {
		t.Fatalf("chunks = %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Delta != "Hello" || chunks[1].Delta != " world" {
		t.Errorf("text deltas = %q %q", chunks[0].Delta, chunks[1].Delta)
	}
	if chunks[0].ID != "resp-3" {
		t.Errorf("chunk ID = %q", chunks[0].ID)
	}

	tc := chunks[2].ToolCall
	if tc == nil {
		t.Fatalf("expected a tool-call chunk, got %+v", chunks[2])
	}
	if tc.Index != 0 || tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("tool call delta = %+v", tc)
	}
	if !strings.Contains(tc.ArgumentsDelta, `"city"`) {
		t.Errorf("ArgumentsDelta = %q, want the whole arguments object", tc.ArgumentsDelta)
	}

	final := chunks[3]
	if !final.Done {
		t.Fatalf("last chunk not Done: %+v", final)
	}
	if final.FinishReason != FinishToolCalls {
		t.Errorf("FinishReason = %q, want %q", final.FinishReason, FinishToolCalls)
	}
	if final.Usage == nil || final.Usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v", final.Usage)
	}
	if want := 0.0003 * 15 / 1000; !geminiCostClose(final.Usage.CostUSD, want) {
		t.Errorf("CostUSD = %v, want %v", final.Usage.CostUSD, want)
	}
}

func TestGeminiCompleteStreamTextOnly(t *testing.T) {
	body := "data: " + `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"MAX_TOKENS"}]}` + "\n\n"
	p, _ := geminiCapture(t, http.StatusOK, body)
	ch, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	chunks := collectGeminiChunks(t, ch)
	if len(chunks) != 2 || chunks[0].Delta != "hi" {
		t.Fatalf("chunks = %+v", chunks)
	}
	if !chunks[1].Done || chunks[1].FinishReason != FinishLength {
		t.Errorf("final chunk = %+v", chunks[1])
	}
}

func TestGeminiCompleteStreamMalformedEvent(t *testing.T) {
	body := "data: {not json}\n\n"
	p, _ := geminiCapture(t, http.StatusOK, body)
	ch, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	chunks := collectGeminiChunks(t, ch)
	if len(chunks) != 1 {
		t.Fatalf("chunks = %+v, want a single error chunk", chunks)
	}
	if chunks[0].Err == nil {
		t.Errorf("expected Err, got %+v", chunks[0])
	}
	if chunks[0].Done {
		t.Error("no Done chunk may follow (or accompany) an Err chunk")
	}
}

func TestGeminiCompleteStreamAPIError(t *testing.T) {
	p, _ := geminiCapture(t, http.StatusBadRequest, `{"error":{"message":"bad model"}}`)
	_, err := p.CompleteStream(context.Background(), CompletionParams{
		Model:    "gemini-2.5-flash",
		Messages: []Message{TextMessage("user", "hi")},
	})
	provErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("error type = %T (%v)", err, err)
	}
	if provErr.Message != "bad model" || provErr.StatusCode != http.StatusBadRequest {
		t.Errorf("provider error = %+v", provErr)
	}
}

// ---- embeddings ----

func TestGeminiEmbedBatch(t *testing.T) {
	body := `{"embeddings":[{"values":[0.1,0.2,0.3]},{"values":[0.4,0.5,0.6]}]}`
	p, cap := geminiCapture(t, http.StatusOK, body)
	result, err := p.Embed(context.Background(), EmbedParams{
		Model: "text-embedding-004",
		Texts: []string{"hello", "world"},
	})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if cap.count != 1 {
		t.Errorf("HTTP calls = %d, want 1 batched call", cap.count)
	}
	if cap.path != "/models/text-embedding-004:batchEmbedContents" {
		t.Errorf("path = %q", cap.path)
	}

	requests := cap.decode(t)["requests"].([]any)
	if len(requests) != 2 {
		t.Fatalf("requests = %v", requests)
	}
	first := requests[0].(map[string]any)
	if first["model"] != "models/text-embedding-004" {
		t.Errorf("request model = %v", first["model"])
	}
	parts := first["content"].(map[string]any)["parts"].([]any)
	if parts[0].(map[string]any)["text"] != "hello" {
		t.Errorf("request parts = %v", parts)
	}

	if len(result.Embeddings) != 2 {
		t.Fatalf("embeddings = %+v", result.Embeddings)
	}
	if result.Embeddings[0].Dimensions != 3 || result.Embeddings[0].Values[0] != 0.1 {
		t.Errorf("first embedding = %+v", result.Embeddings[0])
	}
	if result.Provider != "gemini" || result.Model != "text-embedding-004" {
		t.Errorf("provider/model = %q/%q", result.Provider, result.Model)
	}
}

func TestGeminiEmbedError(t *testing.T) {
	p, _ := geminiCapture(t, http.StatusForbidden, `{"error":{"message":"API key not valid"}}`)
	_, err := p.Embed(context.Background(), EmbedParams{Model: "text-embedding-004", Texts: []string{"hi"}})
	provErr, ok := err.(*ProviderError)
	if !ok {
		t.Fatalf("error type = %T (%v)", err, err)
	}
	if provErr.Message != "API key not valid" {
		t.Errorf("message = %q", provErr.Message)
	}
}

// ---- misc ----

func TestGeminiNameAndModels(t *testing.T) {
	p := NewGeminiProvider("k")
	if p.Name() != "gemini" {
		t.Errorf("Name() = %q", p.Name())
	}
	models := p.Models(context.Background())
	if len(models) != 5 || models[0] != "gemini-2.5-pro" {
		t.Errorf("Models() = %v", models)
	}
	for _, m := range models {
		if _, ok := p.pricing[m]; !ok {
			t.Errorf("model %q has no pricing entry", m)
		}
	}
}

func TestGeminiModelPath(t *testing.T) {
	if got := geminiModelPath("gemini-2.5-flash"); got != "models/gemini-2.5-flash" {
		t.Errorf("geminiModelPath = %q", got)
	}
	if got := geminiModelPath("models/gemini-2.5-flash"); got != "models/gemini-2.5-flash" {
		t.Errorf("geminiModelPath double-prefixed: %q", got)
	}
}
