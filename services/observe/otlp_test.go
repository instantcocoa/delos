package observe

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func testHandler(t *testing.T) (*Handler, *MemorySpanStore) {
	t.Helper()
	spans := NewMemorySpanStore()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	return NewHandler(spans, NewMemoryMetricStore(), logger), spans
}

const genAISpanJSON = `{"resourceSpans":[{"resource":{"attributes":[
  {"key":"service.name","value":{"stringValue":"my-app"}}]},
 "scopeSpans":[{"spans":[{
   "traceId":"5b8efff798038103d269b633813fc60c",
   "spanId":"eee19b7ec3c1b174",
   "name":"chat gpt-4o","kind":3,
   "startTimeUnixNano":"1785740000000000000",
   "endTimeUnixNano":"1785740001500000000",
   "attributes":[
     {"key":"gen_ai.system","value":{"stringValue":"openai"}},
     {"key":"gen_ai.request.model","value":{"stringValue":"gpt-4o"}},
     {"key":"gen_ai.usage.input_tokens","value":{"intValue":"42"}},
     {"key":"gen_ai.usage.output_tokens","value":{"intValue":"12"}},
     {"key":"delos.cost_usd","value":{"doubleValue":0.00027}}]}]}]}]}`

// OTLP/JSON encodes trace and span IDs as hex, but protojson decodes bytes
// fields as base64 - the ingest path rewrites them. Without that, IDs are
// silently corrupted and traces cannot be fetched back by ID.
func TestOTLPJSONIngestHexIDs(t *testing.T) {
	h, store := testHandler(t)

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader(genAISpanJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.OTLPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	trace, err := store.GetTrace(t.Context(), "5b8efff798038103d269b633813fc60c")
	if err != nil || trace == nil {
		t.Fatalf("trace not retrievable by its hex id: %v", err)
	}
	span := trace.Spans[0]
	if span.SpanID != "eee19b7ec3c1b174" {
		t.Errorf("SpanID = %q", span.SpanID)
	}
	if span.ServiceName != "my-app" {
		t.Errorf("ServiceName = %q (resource attributes should merge in)", span.ServiceName)
	}
	// gen_ai.* essentials are promoted to typed fields for cost/latency queries.
	if span.GenAISystem != "openai" || span.RequestModel != "gpt-4o" {
		t.Errorf("promoted model fields = %q %q", span.GenAISystem, span.RequestModel)
	}
	if span.InputTokens != 42 || span.OutputTokens != 12 {
		t.Errorf("promoted tokens = %d %d", span.InputTokens, span.OutputTokens)
	}
	if span.CostUSD != 0.00027 {
		t.Errorf("promoted cost = %v", span.CostUSD)
	}
	if span.DurationMillis() != 1500 {
		t.Errorf("duration = %v ms", span.DurationMillis())
	}
}

// A value that merely looks like an ID field name must not be rewritten.
func TestOTLPJSONLeavesAttributeValuesAlone(t *testing.T) {
	h, store := testHandler(t)
	body := `{"resourceSpans":[{"scopeSpans":[{"spans":[{
	  "traceId":"5b8efff798038103d269b633813fc60d","spanId":"eee19b7ec3c1b175","name":"x",
	  "attributes":[{"key":"traceId","value":{"stringValue":"deadbeefdeadbeefdeadbeefdeadbeef"}}]}]}]}]}`

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.OTLPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}

	trace, err := store.GetTrace(t.Context(), "5b8efff798038103d269b633813fc60d")
	if err != nil || trace == nil {
		t.Fatalf("trace missing: %v", err)
	}
	if got := trace.Spans[0].Attributes["traceId"]; got != "deadbeefdeadbeefdeadbeefdeadbeef" {
		t.Errorf("attribute value was rewritten: %q", got)
	}
}

// The body cap must apply to the DECOMPRESSED stream: a small gzip bomb
// otherwise inflates without bound and exhausts memory.
func TestOTLPGzipBombRejected(t *testing.T) {
	h, _ := testHandler(t)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	chunk := bytes.Repeat([]byte("A"), 1<<20)
	for i := 0; i < 64; i++ { // 64MB in, well under 16MB compressed
		if _, err := gz.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	gz.Close()
	if buf.Len() > maxOTLPBody {
		t.Fatalf("compressed payload %d is already over the cap; test would pass vacuously", buf.Len())
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.OTLPHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an over-large decompressed body", rec.Code)
	}
}

func TestOTLPUnsupportedContentType(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/xml")
	rec := httptest.NewRecorder()
	h.OTLPHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d, want 415", rec.Code)
	}
}

func TestOTLPResponseIsExportServiceResponse(t *testing.T) {
	h, _ := testHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/traces", strings.NewReader(genAISpanJSON))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.OTLPHandler().ServeHTTP(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %v (%s)", err, rec.Body.String())
	}
	if _, rejected := resp["partialSuccess"]; rejected {
		t.Errorf("unexpected partial success: %s", rec.Body.String())
	}
}
