package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/instantcocoa/delos/pkg/semconv"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry: every request through the gateway produces one OTLP span using
// the OTel GenAI semantic conventions. Attribute names come exclusively from
// pkg/semconv. The gateway exports wherever it is pointed - it has no
// knowledge of the Delos control plane.
//
// Content capture (prompts and completions on the span) is off by default and
// passes through a Redactor when enabled.

// TracerName is the instrumentation scope for gateway spans.
const TracerName = "delos-gateway"

// ---- redaction ----

// Redactor applies an ordered list of regex substitutions to captured
// content before it is attached to a span.
type Redactor struct {
	rules []redactRule
}

type redactRule struct {
	re   *regexp.Regexp
	repl string
}

// NewRedactor compiles redaction rules. Each rule is "regex=>replacement",
// for example `sk-[A-Za-z0-9]+=>[REDACTED]`. Rules are applied in order.
// An empty rule list yields a no-op redactor.
func NewRedactor(rules []string) (*Redactor, error) {
	r := &Redactor{}
	for i, raw := range rules {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		pattern, repl, ok := strings.Cut(raw, "=>")
		if !ok {
			return nil, fmt.Errorf("redaction rule %d (%q): want \"regex=>replacement\"", i, raw)
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("redaction rule %d (%q): %w", i, raw, err)
		}
		r.rules = append(r.rules, redactRule{re: re, repl: repl})
	}
	return r, nil
}

// ParseRedactionRules splits a semicolon-separated rule string, as accepted by
// DELOS_TRACE_REDACT, and compiles it.
func ParseRedactionRules(raw string) (*Redactor, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	return NewRedactor(strings.Split(raw, ";"))
}

// Apply runs every rule in order over s.
func (r *Redactor) Apply(s string) string {
	if r == nil {
		return s
	}
	for _, rule := range r.rules {
		s = rule.re.ReplaceAllString(s, rule.repl)
	}
	return s
}

// ---- authed key context ----

// WithAuthedKey attaches an authenticated virtual key to the context so that
// downstream spans can record its name (never its secret). AuthedKey reads it
// back.
func WithAuthedKey(ctx context.Context, key *VirtualKey) context.Context {
	return context.WithValue(ctx, authedKeyCtx{}, key)
}

// ---- tracer ----

// GatewayTracer turns gateway requests into gen_ai.* spans.
type GatewayTracer struct {
	tracer         trace.Tracer
	captureContent bool
	redactor       *Redactor
}

// NewGatewayTracer builds a tracer over the global TracerProvider. Content
// capture attaches gen_ai.prompt / gen_ai.completion to spans, passed through
// redactor first; it is off unless captureContent is true.
func NewGatewayTracer(captureContent bool, redactor *Redactor) *GatewayTracer {
	return &GatewayTracer{
		tracer:         otel.Tracer(TracerName),
		captureContent: captureContent,
		redactor:       redactor,
	}
}

// SetTracer installs the gateway tracer on the service. With no tracer set,
// span emission is a no-op and costs nothing on the hot path.
func (s *RuntimeService) SetTracer(t *GatewayTracer) { s.tracer = t }

// startSpan begins a request span, or returns a nil *RequestSpan (whose
// methods are no-ops) when tracing is not configured.
func (s *RuntimeService) startSpan(ctx context.Context, operation string, params CompletionParams) (context.Context, *RequestSpan) {
	if s == nil || s.tracer == nil {
		return ctx, nil
	}
	return s.tracer.StartSpan(ctx, operation, params)
}

// RequestSpan is one in-flight gateway request span.
type RequestSpan struct {
	span trace.Span
	gt   *GatewayTracer
	once sync.Once
}

// StartSpan opens a span named "<operation> <model>" carrying the request-side
// gen_ai.* attributes. operation is "chat" or "embeddings".
func (g *GatewayTracer) StartSpan(ctx context.Context, operation string, params CompletionParams) (context.Context, *RequestSpan) {
	if g == nil {
		return ctx, nil
	}
	attrs := []attribute.KeyValue{
		attribute.String(semconv.GenAIOperationName, operation),
		attribute.String(semconv.GenAIRequestModel, params.Model),
	}
	if params.MaxTokens > 0 {
		attrs = append(attrs, attribute.Int(semconv.GenAIRequestMaxTokens, params.MaxTokens))
	}
	if params.Temperature != nil {
		attrs = append(attrs, attribute.Float64(semconv.GenAIRequestTemperature, *params.Temperature))
	}
	if params.TopP != nil {
		attrs = append(attrs, attribute.Float64(semconv.GenAIRequestTopP, *params.TopP))
	}
	if id := RequestID(ctx); id != "" {
		attrs = append(attrs, attribute.String(semconv.DelosRequestID, id))
	}
	if key := AuthedKey(ctx); key != nil {
		// The key's NAME, never the secret or its hash.
		attrs = append(attrs, attribute.String(semconv.DelosVirtualKey, key.Name))
	}
	if g.captureContent {
		if prompt := g.encodeMessages(params.Messages); prompt != "" {
			attrs = append(attrs, attribute.String(semconv.GenAIPrompt, prompt))
		}
	}

	ctx, span := g.tracer.Start(ctx, operation+" "+params.Model,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
	return ctx, &RequestSpan{span: span, gt: g}
}

// promptMessage is the on-span JSON shape for a captured message.
type promptMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content,omitempty"`
	ToolCalls []struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"tool_calls,omitempty"`
}

func (g *GatewayTracer) encodeMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	out := make([]promptMessage, 0, len(messages))
	for _, m := range messages {
		pm := promptMessage{Role: m.Role, Content: m.Text()}
		for _, tc := range m.ToolCalls {
			pm.ToolCalls = append(pm.ToolCalls, struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments})
		}
		out = append(out, pm)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return g.redactor.Apply(string(encoded))
}

// SetAttempts records how many provider targets were actually attempted.
func (rs *RequestSpan) SetAttempts(n int) {
	if rs == nil {
		return
	}
	rs.span.SetAttributes(attribute.Int(semconv.DelosProviderAttempts, n))
}

// End finishes a non-streaming completion span. Exactly one of result and err
// is meaningful; the span is always ended.
func (rs *RequestSpan) End(result *CompletionResult, err error) {
	if rs == nil {
		return
	}
	rs.once.Do(func() {
		defer rs.span.End()
		if err != nil || result == nil {
			rs.fail(err)
			return
		}
		attrs := []attribute.KeyValue{
			attribute.String(semconv.GenAISystem, result.Provider),
			attribute.String(semconv.GenAIResponseModel, result.Model),
			attribute.String(semconv.GenAIResponseID, result.ID),
			attribute.StringSlice(semconv.GenAIResponseFinishReasons, []string{result.FinishReason}),
			attribute.Int(semconv.GenAIUsageInputTokens, result.Usage.PromptTokens),
			attribute.Int(semconv.GenAIUsageOutputTokens, result.Usage.CompletionTokens),
			attribute.Float64(semconv.DelosCostUSD, result.Usage.CostUSD),
			attribute.Bool(semconv.DelosCacheHit, result.Cached),
		}
		if rs.gt.captureContent {
			attrs = append(attrs, attribute.String(semconv.GenAICompletion,
				rs.gt.redactor.Apply(result.Text())))
		}
		rs.span.SetAttributes(attrs...)
		rs.span.SetStatus(codes.Ok, "")
	})
}

// EndEmbed finishes an embeddings span.
func (rs *RequestSpan) EndEmbed(result *EmbedResult, err error) {
	if rs == nil {
		return
	}
	rs.once.Do(func() {
		defer rs.span.End()
		if err != nil || result == nil {
			rs.fail(err)
			return
		}
		rs.span.SetAttributes(
			attribute.String(semconv.GenAISystem, result.Provider),
			attribute.String(semconv.GenAIResponseModel, result.Model),
			attribute.Int(semconv.GenAIUsageInputTokens, result.Usage.PromptTokens),
			attribute.Int(semconv.GenAIUsageOutputTokens, result.Usage.CompletionTokens),
			attribute.Float64(semconv.DelosCostUSD, result.Usage.CostUSD),
			attribute.Bool(semconv.DelosCacheHit, false),
		)
		rs.span.SetStatus(codes.Ok, "")
	})
}

// EndStream finishes a streaming completion span from the stream's final
// chunk, which carries the provider, model, finish reason, and usage. A nil
// finalChunk with a non-nil err marks the span failed.
func (rs *RequestSpan) EndStream(finalChunk *StreamChunk, err error) {
	if rs == nil {
		return
	}
	rs.once.Do(func() {
		defer rs.span.End()
		if err != nil || finalChunk == nil {
			rs.fail(err)
			return
		}
		attrs := []attribute.KeyValue{
			attribute.String(semconv.GenAISystem, finalChunk.Provider),
			attribute.String(semconv.GenAIResponseModel, finalChunk.Model),
			attribute.String(semconv.GenAIResponseID, finalChunk.ID),
			attribute.StringSlice(semconv.GenAIResponseFinishReasons, []string{finalChunk.FinishReason}),
			attribute.Bool(semconv.DelosCacheHit, false),
		}
		if u := finalChunk.Usage; u != nil {
			attrs = append(attrs,
				attribute.Int(semconv.GenAIUsageInputTokens, u.PromptTokens),
				attribute.Int(semconv.GenAIUsageOutputTokens, u.CompletionTokens),
				attribute.Float64(semconv.DelosCostUSD, u.CostUSD),
			)
		}
		rs.span.SetAttributes(attrs...)
		rs.span.SetStatus(codes.Ok, "")
	})
}

// fail marks the span as errored. Must be called from inside rs.once.
func (rs *RequestSpan) fail(err error) {
	if err == nil {
		err = errors.New("request produced no result")
	}
	rs.span.RecordError(err)
	rs.span.SetStatus(codes.Error, err.Error())
}

// ---- streaming ----

// forwardStream relays chunks to the caller while watching for the terminal
// chunk, and ends the span once the stream is finished. With a nil span the
// input channel is passed straight through, adding no goroutine.
func (rs *RequestSpan) forwardStream(ctx context.Context, in <-chan StreamChunk) <-chan StreamChunk {
	if rs == nil {
		return in
	}
	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		var last StreamChunk
		seen := false
		for chunk := range in {
			last, seen = chunk, true
			select {
			case out <- chunk:
			case <-ctx.Done():
				rs.EndStream(nil, ctx.Err())
				return
			}
		}
		switch {
		case !seen:
			rs.EndStream(nil, errors.New("stream produced no chunks"))
		case last.Err != nil:
			rs.EndStream(nil, last.Err)
		default:
			rs.EndStream(&last, nil)
		}
	}()
	return out
}

// CompleteStreamChain streams a completion with failover (see
// completeStreamChain) and wraps the stream in a request span that ends when
// the stream terminates.
func (s *RuntimeService) CompleteStreamChain(ctx context.Context, chain []RouteTarget, params CompletionParams) (<-chan StreamChunk, error) {
	ctx, rspan := s.startSpan(ctx, "chat", params)
	chunks, err := s.completeStreamChain(ctx, chain, params)
	if err != nil {
		rspan.EndStream(nil, err)
		return nil, err
	}
	return rspan.forwardStream(ctx, chunks), nil
}
