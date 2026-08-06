package observe

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// maxOTLPBody bounds a single OTLP export request. OTLP exporters batch, so a
// few MB is generous; the limit exists to keep a bad client from exhausting
// memory.
const maxOTLPBody = 16 << 20

const (
	contentTypeProtobuf = "application/x-protobuf"
	contentTypeJSON     = "application/json"
)

// OTLPHandler returns the OTLP/HTTP trace ingest endpoint. Mount it at
// POST /v1/traces -- the path every OTLP/HTTP exporter uses by default.
//
// It accepts both OTLP encodings: application/x-protobuf and application/json
// (protojson), optionally gzip-encoded, and replies with an
// ExportTraceServiceResponse in the same encoding as the request.
func (h *Handler) OTLPHandler() http.Handler {
	return http.HandlerFunc(h.serveOTLPTraces)
}

func (h *Handler) serveOTLPTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	isJSON, err := otlpEncoding(r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	}

	body, err := readOTLPBody(r)
	if err != nil {
		h.logger.WarnContext(r.Context(), "failed to read OTLP body", "error", err)
		http.Error(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	req := &coltracepb.ExportTraceServiceRequest{}
	if isJSON {
		// The OTLP/JSON spec encodes trace/span IDs as hex strings, but
		// protojson decodes bytes fields as base64 - rewrite them first.
		body = hexIDsToBase64(body)
		// Ignore unknown fields so a newer exporter is not a hard failure.
		err = protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(body, req)
	} else {
		err = proto.Unmarshal(body, req)
	}
	if err != nil {
		h.logger.WarnContext(r.Context(), "failed to decode OTLP request", "error", err, "json", isJSON)
		http.Error(w, "failed to decode OTLP request", http.StatusBadRequest)
		return
	}

	spans := SpansFromOTLP(req)

	count, err := h.spanStore.IngestSpans(r.Context(), spans)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to ingest OTLP spans", "error", err, "count", len(spans))
		http.Error(w, "failed to store spans", http.StatusInternalServerError)
		return
	}

	h.logger.DebugContext(r.Context(), "otlp spans ingested", "received", len(spans), "stored", count)

	resp := &coltracepb.ExportTraceServiceResponse{}
	if rejected := int64(len(spans) - count); rejected > 0 {
		resp.PartialSuccess = &coltracepb.ExportTracePartialSuccess{
			RejectedSpans: rejected,
			ErrorMessage:  "some spans were not stored",
		}
	}

	var out []byte
	if isJSON {
		out, err = protojson.Marshal(resp)
		w.Header().Set("Content-Type", contentTypeJSON)
	} else {
		out, err = proto.Marshal(resp)
		w.Header().Set("Content-Type", contentTypeProtobuf)
	}
	if err != nil {
		h.logger.ErrorContext(r.Context(), "failed to encode OTLP response", "error", err)
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(out); err != nil {
		h.logger.DebugContext(r.Context(), "failed to write OTLP response", "error", err)
	}
}

// otlpEncoding reports whether the content type selects the JSON encoding.
func otlpEncoding(contentType string) (bool, error) {
	// Strip any parameters, e.g. "application/json; charset=utf-8".
	mediaType := strings.TrimSpace(strings.ToLower(contentType))
	if i := strings.IndexByte(mediaType, ';'); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}

	switch mediaType {
	case contentTypeProtobuf, "application/protobuf", "application/octet-stream":
		return false, nil
	case contentTypeJSON:
		return true, nil
	case "":
		// OTLP requires a content type, but defaulting to protobuf is friendlier
		// than rejecting a client that merely forgot the header.
		return false, nil
	default:
		return false, fmt.Errorf("unsupported content type %q: want %s or %s", contentType, contentTypeProtobuf, contentTypeJSON)
	}
}

func readOTLPBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = http.MaxBytesReader(nil, r.Body, maxOTLPBody)

	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to open gzip stream: %w", err)
		}
		defer gz.Close()
		// MaxBytesReader bounds the compressed stream; bound the inflated one
		// too, or a small gzip bomb expands without limit. Read one byte past
		// the cap so an over-limit body is rejected rather than silently cut.
		reader = io.LimitReader(gz, maxOTLPBody+1)
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}
	if len(body) > maxOTLPBody {
		return nil, fmt.Errorf("request body exceeds %d bytes after decompression", maxOTLPBody)
	}
	if len(body) == 0 {
		return nil, errors.New("empty request body")
	}
	return body, nil
}

// SpansFromOTLP flattens an OTLP export request into observe spans. Resource
// attributes are merged into each span's attribute bag (span attributes win on
// conflict) and the gen_ai.* essentials are promoted to typed fields.
//
// Nothing here is Delos-specific: any emitter following the OpenTelemetry
// gen_ai semantic conventions produces usable spans.
func SpansFromOTLP(req *coltracepb.ExportTraceServiceRequest) []Span {
	if req == nil {
		return nil
	}

	var out []Span
	for _, rs := range req.GetResourceSpans() {
		resourceAttrs := attributesToMap(rs.GetResource().GetAttributes())
		serviceName := resourceAttrs[AttrServiceName]

		for _, ss := range rs.GetScopeSpans() {
			scopeName := ss.GetScope().GetName()
			for _, s := range ss.GetSpans() {
				out = append(out, spanFromOTLP(s, resourceAttrs, serviceName, scopeName))
			}
		}
	}
	return out
}

func spanFromOTLP(s *tracepb.Span, resourceAttrs map[string]string, serviceName, scopeName string) Span {
	attrs := make(map[string]string, len(resourceAttrs)+len(s.GetAttributes())+1)
	for k, v := range resourceAttrs {
		attrs[k] = v
	}
	for k, v := range attributesToMap(s.GetAttributes()) {
		attrs[k] = v
	}
	if scopeName != "" {
		attrs["otel.scope.name"] = scopeName
	}

	start := unixNano(s.GetStartTimeUnixNano())
	end := unixNano(s.GetEndTimeUnixNano())

	var duration time.Duration
	if !start.IsZero() && !end.IsZero() && end.After(start) {
		duration = end.Sub(start)
	}

	span := Span{
		TraceID:      hexID(s.GetTraceId()),
		SpanID:       hexID(s.GetSpanId()),
		ParentSpanID: hexID(s.GetParentSpanId()),
		Name:         s.GetName(),
		Kind:         spanKindString(s.GetKind()),
		ServiceName:  serviceName,
		StartTime:    start,
		Duration:     duration,
		Status:       statusFromOTLP(s.GetStatus()),
		StatusMsg:    s.GetStatus().GetMessage(),
		Attributes:   attrs,
	}

	for _, e := range s.GetEvents() {
		span.Events = append(span.Events, SpanEvent{
			Name:       e.GetName(),
			Timestamp:  unixNano(e.GetTimeUnixNano()),
			Attributes: attributesToMap(e.GetAttributes()),
		})
	}

	span.PromoteAttributes()
	return span
}

func statusFromOTLP(st *tracepb.Status) SpanStatus {
	switch st.GetCode() {
	case tracepb.Status_STATUS_CODE_OK:
		return SpanStatusOK
	case tracepb.Status_STATUS_CODE_ERROR:
		return SpanStatusError
	default:
		return SpanStatusUnspecified
	}
}

func spanKindString(k tracepb.Span_SpanKind) string {
	switch k {
	case tracepb.Span_SPAN_KIND_INTERNAL:
		return "internal"
	case tracepb.Span_SPAN_KIND_SERVER:
		return "server"
	case tracepb.Span_SPAN_KIND_CLIENT:
		return "client"
	case tracepb.Span_SPAN_KIND_PRODUCER:
		return "producer"
	case tracepb.Span_SPAN_KIND_CONSUMER:
		return "consumer"
	default:
		return "unspecified"
	}
}

func unixNano(ns uint64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(ns)).UTC()
}

// hexID renders an OTLP trace/span ID. All-zero IDs (the OTLP encoding of
// "absent", used for root spans' parent) render as the empty string.
func hexID(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	for _, c := range b {
		if c != 0 {
			return hex.EncodeToString(b)
		}
	}
	return ""
}

func attributesToMap(kvs []*commonpb.KeyValue) map[string]string {
	if len(kvs) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if kv == nil || kv.GetKey() == "" {
			continue
		}
		out[kv.GetKey()] = anyValueToString(kv.GetValue())
	}
	return out
}

// anyValueToString renders an OTLP AnyValue as a string. Attributes are stored
// stringified so a single JSONB column can hold any emitter's attribute set;
// the typed gen_ai columns are parsed back out of these.
func anyValueToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch val := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_BoolValue:
		return strconv.FormatBool(val.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return strconv.FormatInt(val.IntValue, 10)
	case *commonpb.AnyValue_DoubleValue:
		return strconv.FormatFloat(val.DoubleValue, 'f', -1, 64)
	case *commonpb.AnyValue_BytesValue:
		return hex.EncodeToString(val.BytesValue)
	case *commonpb.AnyValue_ArrayValue:
		parts := make([]string, 0, len(val.ArrayValue.GetValues()))
		for _, item := range val.ArrayValue.GetValues() {
			parts = append(parts, anyValueToString(item))
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *commonpb.AnyValue_KvlistValue:
		parts := make([]string, 0, len(val.KvlistValue.GetValues()))
		for _, item := range val.KvlistValue.GetValues() {
			parts = append(parts, item.GetKey()+"="+anyValueToString(item.GetValue()))
		}
		return "{" + strings.Join(parts, ",") + "}"
	default:
		return ""
	}
}

// hexIDsToBase64 rewrites traceId/spanId/parentSpanId hex strings (the
// OTLP/JSON encoding) into base64 so protojson can decode them into bytes
// fields. Values that are not plausible hex IDs are left untouched, which
// also tolerates senders that already used base64.
func hexIDsToBase64(body []byte) []byte {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return body
	}
	rewriteIDs(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		return body
	}
	return out
}

func rewriteIDs(node any) {
	switch v := node.(type) {
	case map[string]any:
		for key, val := range v {
			switch key {
			case "traceId", "trace_id", "spanId", "span_id", "parentSpanId", "parent_span_id":
				if s, ok := val.(string); ok {
					if raw, err := hex.DecodeString(s); err == nil && (len(raw) == 16 || len(raw) == 8) {
						v[key] = base64.StdEncoding.EncodeToString(raw)
					}
				}
			default:
				rewriteIDs(val)
			}
		}
	case []any:
		for _, item := range v {
			rewriteIDs(item)
		}
	}
}
