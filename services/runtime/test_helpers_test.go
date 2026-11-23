package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Shared HTTP test helpers. These are used by both the legacy translator tests
// (compiled behind -tags=delos_translators) and the always-on key/budget tests
// (keys_test.go, DL-K), so they live untagged.

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
