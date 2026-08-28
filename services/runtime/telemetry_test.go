package runtime

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Telemetry tests. These are the semconv golden tests referenced from
// pkg/semconv: the attribute names asserted here are written out as string
// literals on purpose, so that renaming a constant in pkg/semconv (or adopting
// a new spec revision) fails loudly here instead of silently changing the wire
// format every Delos trace consumer depends on.

// ---- harness ----

// newTestExporter installs a synchronous in-memory TracerProvider as the
// global provider and restores the previous one when the test ends.
// GatewayTracer resolves its tracer from the global provider at construction
// time, so this must run before NewGatewayTracer.
func newTestExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return exporter
}

// tracedService builds a service with the given providers and an installed
// gateway tracer, returning the exporter that collects its spans.
func tracedService(t *testing.T, captureContent bool, redactor *Redactor, providers ...Provider) (*RuntimeService, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter := newTestExporter(t)
	svc := newTestService(providers...)
	svc.SetTracer(NewGatewayTracer(captureContent, redactor))
	return svc, exporter
}

// onlySpan asserts exactly one span was exported and returns it.
func onlySpan(t *testing.T, exporter *tracetest.InMemoryExporter) tracetest.SpanStub {
	t.Helper()
	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want exactly 1: %v", len(spans), spanNames(spans))
	}
	return spans[0]
}

func spanNames(spans tracetest.SpanStubs) []string {
	names := make([]string, 0, len(spans))
	for _, s := range spans {
		names = append(names, s.Name)
	}
	return names
}

func attrs(span tracetest.SpanStub) map[string]attribute.Value {
	out := make(map[string]attribute.Value, len(span.Attributes))
	for _, kv := range span.Attributes {
		out[string(kv.Key)] = kv.Value
	}
	return out
}

func attrKeys(span tracetest.SpanStub) []string {
	keys := make([]string, 0, len(span.Attributes))
	for _, kv := range span.Attributes {
		keys = append(keys, string(kv.Key))
	}
	sort.Strings(keys)
	return keys
}

func requireAttr(t *testing.T, a map[string]attribute.Value, key string) attribute.Value {
	t.Helper()
	v, ok := a[key]
	if !ok {
		t.Fatalf("span is missing attribute %q (have %v)", key, sortedKeys(a))
	}
	return v
}

func sortedKeys(a map[string]attribute.Value) []string {
	keys := make([]string, 0, len(a))
	for k := range a {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fullResult() *CompletionResult {
	return &CompletionResult{
		ID:           "resp-1",
		Message:      TextMessage("assistant", "the answer"),
		FinishReason: FinishStop,
		Provider:     "openai",
		Model:        "gpt-4o-2024-11-20",
		Usage: Usage{
			PromptTokens:     11,
			CompletionTokens: 7,
			TotalTokens:      18,
			CostUSD:          0.00042,
		},
	}
}

// ---- 1. golden attribute set ----

// TestTelemetryGoldenAttributeSet pins the exact attribute-key set of a
// successful chat span. Every optional request attribute is populated so the
// golden list covers every name the gateway can emit on the success path
// (content capture excepted; see TestTelemetryContentCapture*).
func TestTelemetryGoldenAttributeSet(t *testing.T) {
	p := &mockProvider{name: "openai", completeResult: fullResult()}
	svc, exporter := tracedService(t, false, nil, p)

	temp, topP := 0.7, 0.95
	ctx := context.WithValue(context.Background(), requestIDKey{}, "req-abc")
	ctx = WithAuthedKey(ctx, &VirtualKey{ID: "vk-1", Name: "ci-key", Hash: "secret-hash"})

	result, err := svc.CompleteChain(ctx, []RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{
			Model:       "gpt-4o",
			Messages:    []Message{TextMessage("user", "q")},
			MaxTokens:   256,
			Temperature: &temp,
			TopP:        &topP,
		})
	if err != nil {
		t.Fatalf("CompleteChain: %v", err)
	}
	if result.Text() != "the answer" {
		t.Fatalf("result text = %q", result.Text())
	}

	span := onlySpan(t, exporter)

	if span.Name != "chat gpt-4o" {
		t.Errorf("span name = %q, want %q", span.Name, "chat gpt-4o")
	}
	if span.InstrumentationScope.Name != TracerName {
		t.Errorf("instrumentation scope = %q, want %q", span.InstrumentationScope.Name, TracerName)
	}
	if span.Status.Code != codes.Ok {
		t.Errorf("status = %v (%q), want Ok", span.Status.Code, span.Status.Description)
	}

	// GOLDEN: the exact sorted attribute-key set of a successful chat span.
	// Do not "fix" this list by copying whatever the code now emits - a diff
	// here is a deliberate wire-format change (see pkg/semconv.Version).
	want := []string{
		"delos.cache_hit",
		"delos.cost_usd",
		"delos.provider_attempts",
		"delos.request_id",
		"delos.virtual_key",
		"gen_ai.operation.name",
		"gen_ai.request.max_tokens",
		"gen_ai.request.model",
		"gen_ai.request.temperature",
		"gen_ai.request.top_p",
		"gen_ai.response.finish_reasons",
		"gen_ai.response.id",
		"gen_ai.response.model",
		"gen_ai.system",
		"gen_ai.usage.input_tokens",
		"gen_ai.usage.output_tokens",
	}
	got := attrKeys(span)
	if len(got) != len(want) {
		t.Fatalf("attribute keys:\n got %v\nwant %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attribute keys:\n got %v\nwant %v", got, want)
		}
	}

	a := attrs(span)
	if v := requireAttr(t, a, "gen_ai.system").AsString(); v != "openai" {
		t.Errorf("gen_ai.system = %q, want the provider name %q", v, "openai")
	}
	if v := requireAttr(t, a, "gen_ai.operation.name").AsString(); v != "chat" {
		t.Errorf("gen_ai.operation.name = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.request.model").AsString(); v != "gpt-4o" {
		t.Errorf("gen_ai.request.model = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.model").AsString(); v != "gpt-4o-2024-11-20" {
		t.Errorf("gen_ai.response.model = %q, want the model the provider actually served", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.id").AsString(); v != "resp-1" {
		t.Errorf("gen_ai.response.id = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.finish_reasons").AsStringSlice(); len(v) != 1 || v[0] != FinishStop {
		t.Errorf("gen_ai.response.finish_reasons = %v, want [%s]", v, FinishStop)
	}
	if v := requireAttr(t, a, "gen_ai.usage.input_tokens").AsInt64(); v != 11 {
		t.Errorf("gen_ai.usage.input_tokens = %d, want 11", v)
	}
	if v := requireAttr(t, a, "gen_ai.usage.output_tokens").AsInt64(); v != 7 {
		t.Errorf("gen_ai.usage.output_tokens = %d, want 7", v)
	}
	if v := requireAttr(t, a, "delos.cost_usd").AsFloat64(); v != 0.00042 {
		t.Errorf("delos.cost_usd = %v, want 0.00042", v)
	}
	if v := requireAttr(t, a, "delos.cache_hit").AsBool(); v {
		t.Error("delos.cache_hit = true on a fresh completion")
	}
	if v := requireAttr(t, a, "delos.provider_attempts").AsInt64(); v != 1 {
		t.Errorf("delos.provider_attempts = %d, want 1", v)
	}
	if v := requireAttr(t, a, "delos.request_id").AsString(); v != "req-abc" {
		t.Errorf("delos.request_id = %q", v)
	}
	if v := requireAttr(t, a, "delos.virtual_key").AsString(); v != "ci-key" {
		t.Errorf("delos.virtual_key = %q, want the key NAME", v)
	}
	if v := requireAttr(t, a, "gen_ai.request.max_tokens").AsInt64(); v != 256 {
		t.Errorf("gen_ai.request.max_tokens = %d", v)
	}
	if v := requireAttr(t, a, "gen_ai.request.temperature").AsFloat64(); v != 0.7 {
		t.Errorf("gen_ai.request.temperature = %v", v)
	}
	if v := requireAttr(t, a, "gen_ai.request.top_p").AsFloat64(); v != 0.95 {
		t.Errorf("gen_ai.request.top_p = %v", v)
	}
}

// TestTelemetryNoTracerNoSpans guards the hot path: with no tracer installed,
// nothing is emitted and nothing panics.
func TestTelemetryNoTracerNoSpans(t *testing.T) {
	exporter := newTestExporter(t)
	svc := newTestService(&mockProvider{name: "openai", completeResult: fullResult()})

	if _, err := svc.CompleteChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{Model: "gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	if n := len(exporter.GetSpans()); n != 0 {
		t.Errorf("got %d spans with no tracer installed, want 0", n)
	}
}

// ---- 2. error path ----

func TestTelemetrySpanRecordsError(t *testing.T) {
	p := &mockProvider{
		name:        "openai",
		completeErr: &ProviderError{Provider: "openai", StatusCode: 400, Message: "bad request"},
	}
	svc, exporter := tracedService(t, false, nil, p)

	_, err := svc.CompleteChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{Model: "gpt-4o", Messages: []Message{TextMessage("user", "q")}})
	if err == nil {
		t.Fatal("expected a 400 to surface as an error")
	}

	span := onlySpan(t, exporter)
	if span.Status.Code != codes.Error {
		t.Errorf("status = %v, want Error", span.Status.Code)
	}
	if !strings.Contains(span.Status.Description, "bad request") {
		t.Errorf("status description = %q, want it to carry the provider error", span.Status.Description)
	}

	var recorded bool
	for _, ev := range span.Events {
		if ev.Name == "exception" {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("span did not record the error as an exception event (events: %d)", len(span.Events))
	}

	// A failed request must not claim response-side attributes.
	a := attrs(span)
	for _, key := range []string{"gen_ai.usage.input_tokens", "gen_ai.response.model", "gen_ai.system"} {
		if _, ok := a[key]; ok {
			t.Errorf("failed span must not carry %q", key)
		}
	}
}

// ---- 3. content capture off by default ----

func TestTelemetryContentCaptureOffByDefault(t *testing.T) {
	p := &mockProvider{name: "openai", completeResult: fullResult()}
	svc, exporter := tracedService(t, false, nil, p)

	if _, err := svc.CompleteChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{
			Model:    "gpt-4o",
			Messages: []Message{TextMessage("user", "my key is sk-abc123, do not leak it")},
		}); err != nil {
		t.Fatal(err)
	}

	a := attrs(onlySpan(t, exporter))
	if _, ok := a["gen_ai.prompt"]; ok {
		t.Error("gen_ai.prompt must not be captured unless content capture is enabled")
	}
	if _, ok := a["gen_ai.completion"]; ok {
		t.Error("gen_ai.completion must not be captured unless content capture is enabled")
	}
	// And nothing else may smuggle the prompt onto the span either.
	for k, v := range a {
		if v.Type() == attribute.STRING && strings.Contains(v.AsString(), "sk-abc123") {
			t.Errorf("attribute %q leaked prompt content: %q", k, v.AsString())
		}
	}
}

// ---- 4. content capture on, with redaction ----

func TestTelemetryContentCaptureWithRedaction(t *testing.T) {
	redactor, err := ParseRedactionRules("sk-[A-Za-z0-9]+=>[REDACTED]")
	if err != nil {
		t.Fatalf("ParseRedactionRules: %v", err)
	}
	if redactor == nil {
		t.Fatal("ParseRedactionRules returned a nil redactor for a non-empty rule")
	}

	result := fullResult()
	result.Message = TextMessage("assistant", "rotated to sk-def456 for you")
	p := &mockProvider{name: "openai", completeResult: result}
	svc, exporter := tracedService(t, true, redactor, p)

	if _, err := svc.CompleteChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{
			Model:    "gpt-4o",
			Messages: []Message{TextMessage("user", "my key is sk-abc123, rotate it")},
		}); err != nil {
		t.Fatal(err)
	}

	a := attrs(onlySpan(t, exporter))

	prompt := requireAttr(t, a, "gen_ai.prompt").AsString()
	if strings.Contains(prompt, "sk-abc123") {
		t.Errorf("gen_ai.prompt was not redacted: %q", prompt)
	}
	if !strings.Contains(prompt, "[REDACTED]") {
		t.Errorf("gen_ai.prompt = %q, want the secret replaced by [REDACTED]", prompt)
	}
	if !strings.Contains(prompt, "rotate it") {
		t.Errorf("gen_ai.prompt = %q, want the non-secret content preserved", prompt)
	}
	if !strings.Contains(prompt, `"role":"user"`) {
		t.Errorf("gen_ai.prompt = %q, want role-tagged JSON messages", prompt)
	}

	completion := requireAttr(t, a, "gen_ai.completion").AsString()
	if completion != "rotated to [REDACTED] for you" {
		t.Errorf("gen_ai.completion = %q, want the redacted completion text", completion)
	}
}

func TestTelemetryParseRedactionRules(t *testing.T) {
	if r, err := ParseRedactionRules("   "); err != nil || r != nil {
		t.Errorf("empty rules = (%v, %v), want (nil, nil)", r, err)
	}
	r, err := ParseRedactionRules("sk-[A-Za-z0-9]+=>[SK];\\d{3}-\\d{2}-\\d{4}=>[SSN]")
	if err != nil {
		t.Fatalf("ParseRedactionRules: %v", err)
	}
	if got := r.Apply("sk-abc123 and 123-45-6789"); got != "[SK] and [SSN]" {
		t.Errorf("Apply() = %q, want rules applied in order", got)
	}
	if _, err := ParseRedactionRules("no-arrow-here"); err == nil {
		t.Error("expected an error for a rule without \"=>\"")
	}
	if _, err := ParseRedactionRules("([unclosed=>x"); err == nil {
		t.Error("expected an error for an uncompilable regex")
	}
	// A nil redactor is a no-op, not a panic.
	var nilRedactor *Redactor
	if got := nilRedactor.Apply("sk-abc123"); got != "sk-abc123" {
		t.Errorf("nil redactor Apply() = %q", got)
	}
}

// ---- 5. cache hit ----

func TestTelemetryCacheHitAttribute(t *testing.T) {
	p := &mockProvider{name: "openai", completeResult: fullResult()}
	svc, exporter := tracedService(t, false, nil, p)
	svc.SetCache(NewLRUCache(10), time.Minute)

	chain := []RouteTarget{{Provider: "openai", Model: "gpt-4o"}}
	// Only an explicit temperature of 0 is deterministic, and only
	// deterministic requests are cacheable: an absent temperature is the
	// API default of 1.0. See cacheable().
	zeroTemp := 0.0
	params := CompletionParams{
		Model:       "gpt-4o",
		Temperature: &zeroTemp,
		Messages:    []Message{TextMessage("user", "q")},
	}
	ctx := context.Background()

	if _, err := svc.CompleteChain(ctx, chain, params); err != nil {
		t.Fatal(err)
	}
	// The provider must not be consulted for the second request.
	p.completeResult = nil
	p.completeErr = &ProviderError{Provider: "openai", StatusCode: 500, Message: "should not be called"}

	second, err := svc.CompleteChain(ctx, chain, params)
	if err != nil {
		t.Fatalf("cache hit failed: %v", err)
	}
	if !second.Cached {
		t.Fatal("second request should have been served from cache")
	}

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2 (one per request)", len(spans))
	}
	if v := requireAttr(t, attrs(spans[0]), "delos.cache_hit").AsBool(); v {
		t.Error("first span: delos.cache_hit = true, want false (cache miss)")
	}
	hit := attrs(spans[1])
	if v := requireAttr(t, hit, "delos.cache_hit").AsBool(); !v {
		t.Error("second span: delos.cache_hit = false, want true")
	}
	if v := requireAttr(t, hit, "delos.provider_attempts").AsInt64(); v != 0 {
		t.Errorf("second span: delos.provider_attempts = %d, want 0 (nothing was dialed)", v)
	}
	if v := requireAttr(t, hit, "gen_ai.usage.input_tokens").AsInt64(); v != 11 {
		t.Errorf("second span: usage should be reported from the cached result, got %d", v)
	}
}

// ---- 6. streaming ----

func TestTelemetryStreamingSpan(t *testing.T) {
	p := &mockProvider{
		name: "openai",
		streamChunks: []StreamChunk{
			{Delta: "the ", Provider: "openai", Model: "gpt-4o-2024-11-20"},
			{Delta: "answer", Provider: "openai", Model: "gpt-4o-2024-11-20"},
			{
				ID:           "resp-stream-1",
				Done:         true,
				FinishReason: FinishStop,
				Provider:     "openai",
				Model:        "gpt-4o-2024-11-20",
				Usage:        &Usage{PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7, CostUSD: 0.5},
			},
		},
	}
	svc, exporter := tracedService(t, false, nil, p)

	chunks, err := svc.CompleteStreamChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{Model: "gpt-4o", Messages: []Message{TextMessage("user", "q")}})
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	for c := range chunks {
		if c.Err != nil {
			t.Fatalf("unexpected stream error: %v", c.Err)
		}
		text += c.Delta
	}
	if text != "the answer" {
		t.Fatalf("streamed text = %q", text)
	}

	// The span is ended by forwardStream before it closes the output channel,
	// so draining the stream is enough to guarantee the span was exported.
	span := onlySpan(t, exporter)
	if span.Name != "chat gpt-4o" {
		t.Errorf("span name = %q", span.Name)
	}
	if span.Status.Code != codes.Ok {
		t.Errorf("status = %v (%q), want Ok", span.Status.Code, span.Status.Description)
	}
	a := attrs(span)
	if v := requireAttr(t, a, "gen_ai.usage.input_tokens").AsInt64(); v != 3 {
		t.Errorf("gen_ai.usage.input_tokens = %d, want 3", v)
	}
	if v := requireAttr(t, a, "gen_ai.usage.output_tokens").AsInt64(); v != 4 {
		t.Errorf("gen_ai.usage.output_tokens = %d, want 4", v)
	}
	if v := requireAttr(t, a, "delos.cost_usd").AsFloat64(); v != 0.5 {
		t.Errorf("delos.cost_usd = %v, want 0.5", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.id").AsString(); v != "resp-stream-1" {
		t.Errorf("gen_ai.response.id = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.model").AsString(); v != "gpt-4o-2024-11-20" {
		t.Errorf("gen_ai.response.model = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.finish_reasons").AsStringSlice(); len(v) != 1 || v[0] != FinishStop {
		t.Errorf("gen_ai.response.finish_reasons = %v", v)
	}
	if v := requireAttr(t, a, "gen_ai.system").AsString(); v != "openai" {
		t.Errorf("gen_ai.system = %q", v)
	}
	if v := requireAttr(t, a, "delos.cache_hit").AsBool(); v {
		t.Error("streamed responses are never cache hits")
	}
}

func TestTelemetryStreamingErrorSpan(t *testing.T) {
	// breakStreamAt: 0 => the stream's first and only chunk is an error.
	p := &flakyProvider{mockProvider: mockProvider{name: "openai"}, breakStreamAt: 0}
	svc, exporter := tracedService(t, false, nil, p)

	chunks, err := svc.CompleteStreamChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "gpt-4o"}},
		CompletionParams{Model: "gpt-4o", Messages: []Message{TextMessage("user", "q")}})
	if err != nil {
		t.Fatal(err)
	}
	var sawErr bool
	for c := range chunks {
		if c.Err != nil {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected the client to see the terminal error chunk")
	}

	span := onlySpan(t, exporter)
	if span.Status.Code != codes.Error {
		t.Errorf("status = %v, want Error", span.Status.Code)
	}
	if !strings.Contains(span.Status.Description, "connection reset mid-stream") {
		t.Errorf("status description = %q, want the stream error", span.Status.Description)
	}
	var recorded bool
	for _, ev := range span.Events {
		if ev.Name == "exception" {
			recorded = true
		}
	}
	if !recorded {
		t.Error("broken stream did not record an exception event")
	}
}

// ---- 7. embeddings ----

func TestTelemetryEmbedSpan(t *testing.T) {
	p := &mockProvider{
		name: "openai",
		embedResult: &EmbedResult{
			Embeddings: []Embedding{{Values: []float32{1, 2}, Dimensions: 2}},
			Model:      "text-embedding-3-small",
			Provider:   "openai",
			Usage:      Usage{PromptTokens: 6, TotalTokens: 6, CostUSD: 0.000012},
		},
	}
	svc, exporter := tracedService(t, false, nil, p)

	if _, err := svc.EmbedChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "text-embedding-3-small"}},
		EmbedParams{Texts: []string{"x"}, Model: "text-embedding-3-small"}); err != nil {
		t.Fatal(err)
	}

	span := onlySpan(t, exporter)
	if span.Name != "embeddings text-embedding-3-small" {
		t.Errorf("span name = %q, want %q", span.Name, "embeddings text-embedding-3-small")
	}
	if span.Status.Code != codes.Ok {
		t.Errorf("status = %v (%q), want Ok", span.Status.Code, span.Status.Description)
	}

	a := attrs(span)
	if v := requireAttr(t, a, "gen_ai.operation.name").AsString(); v != "embeddings" {
		t.Errorf("gen_ai.operation.name = %q, want %q", v, "embeddings")
	}
	if v := requireAttr(t, a, "gen_ai.request.model").AsString(); v != "text-embedding-3-small" {
		t.Errorf("gen_ai.request.model = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.response.model").AsString(); v != "text-embedding-3-small" {
		t.Errorf("gen_ai.response.model = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.system").AsString(); v != "openai" {
		t.Errorf("gen_ai.system = %q", v)
	}
	if v := requireAttr(t, a, "gen_ai.usage.input_tokens").AsInt64(); v != 6 {
		t.Errorf("gen_ai.usage.input_tokens = %d, want 6", v)
	}
	if v := requireAttr(t, a, "delos.cost_usd").AsFloat64(); v != 0.000012 {
		t.Errorf("delos.cost_usd = %v", v)
	}
	// An embeddings span carries no chat-only response attributes.
	for _, key := range []string{"gen_ai.response.finish_reasons", "gen_ai.request.max_tokens"} {
		if _, ok := a[key]; ok {
			t.Errorf("embeddings span must not carry %q", key)
		}
	}
}

func TestTelemetryEmbedErrorSpan(t *testing.T) {
	p := &mockProvider{
		name:     "openai",
		embedErr: &ProviderError{Provider: "openai", StatusCode: 400, Message: "bad embedding input"},
	}
	svc, exporter := tracedService(t, false, nil, p)

	if _, err := svc.EmbedChain(context.Background(),
		[]RouteTarget{{Provider: "openai", Model: "text-embedding-3-small"}},
		EmbedParams{Texts: []string{"x"}, Model: "text-embedding-3-small"}); err == nil {
		t.Fatal("expected an error")
	}

	span := onlySpan(t, exporter)
	if span.Status.Code != codes.Error {
		t.Errorf("status = %v, want Error", span.Status.Code)
	}
	if !strings.Contains(span.Status.Description, "bad embedding input") {
		t.Errorf("status description = %q", span.Status.Description)
	}
}
