package observe

import (
	"context"
	"log/slog"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	observev1 "github.com/instantcocoa/delos/gen/go/observe/v1"
)

// Handler implements the ObserveService gRPC interface.
type Handler struct {
	observev1.UnimplementedObserveServiceServer
	spanStore   SpanStore
	metricStore MetricStore
	logger      *slog.Logger
}

// NewHandler creates a new observe service handler.
func NewHandler(spanStore SpanStore, metricStore MetricStore, logger *slog.Logger) *Handler {
	return &Handler{
		spanStore:   spanStore,
		metricStore: metricStore,
		logger:      logger.With("component", "observe"),
	}
}

// Register registers the handler with a gRPC server.
func (h *Handler) Register(s *grpc.Server) {
	observev1.RegisterObserveServiceServer(s, h)
}

// IngestTraces ingests trace spans.
func (h *Handler) IngestTraces(ctx context.Context, req *observev1.IngestTracesRequest) (*observev1.IngestTracesResponse, error) {
	spans := make([]Span, 0, len(req.Spans))
	for _, s := range req.Spans {
		spans = append(spans, protoToSpan(s))
	}

	h.logger.DebugContext(ctx, "ingesting spans", "count", len(spans))

	count, err := h.spanStore.IngestSpans(ctx, spans)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to ingest spans", "error", err)
		return nil, err
	}

	h.logger.InfoContext(ctx, "spans ingested", "count", count)

	return &observev1.IngestTracesResponse{
		AcceptedCount: int32(count),
	}, nil
}

// QueryTraces queries traces with filters.
func (h *Handler) QueryTraces(ctx context.Context, req *observev1.QueryTracesRequest) (*observev1.QueryTracesResponse, error) {
	query := TraceQuery{
		ServiceName:   req.ServiceName,
		OperationName: req.OperationName,
		Tags:          req.Tags,
		Limit:         int(req.Limit),
		Offset:        int(req.Offset),
	}

	if req.StartTime != nil {
		query.StartTime = req.StartTime.AsTime()
	}
	if req.EndTime != nil {
		query.EndTime = req.EndTime.AsTime()
	}
	if req.MinDuration != nil {
		query.MinDuration = req.MinDuration.AsDuration()
	}
	if req.MaxDuration != nil {
		query.MaxDuration = req.MaxDuration.AsDuration()
	}

	h.logger.DebugContext(ctx, "querying traces",
		"service", query.ServiceName,
		"operation", query.OperationName,
	)

	traces, total, err := h.spanStore.QueryTraces(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to query traces", "error", err)
		return nil, err
	}

	h.logger.DebugContext(ctx, "traces found", "count", len(traces), "total", total)

	protoTraces := make([]*observev1.Trace, 0, len(traces))
	for _, t := range traces {
		protoTraces = append(protoTraces, toProtoTrace(&t))
	}

	return &observev1.QueryTracesResponse{
		Traces:     protoTraces,
		TotalCount: int32(total),
	}, nil
}

// GetTrace retrieves a specific trace by ID.
func (h *Handler) GetTrace(ctx context.Context, req *observev1.GetTraceRequest) (*observev1.GetTraceResponse, error) {
	h.logger.DebugContext(ctx, "getting trace", "trace_id", req.TraceId)

	trace, err := h.spanStore.GetTrace(ctx, req.TraceId)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to get trace", "trace_id", req.TraceId, "error", err)
		return nil, err
	}

	var protoTrace *observev1.Trace
	if trace != nil {
		protoTrace = toProtoTrace(trace)
	}

	return &observev1.GetTraceResponse{
		Trace: protoTrace,
	}, nil
}

// QueryMetrics queries metrics.
func (h *Handler) QueryMetrics(ctx context.Context, req *observev1.QueryMetricsRequest) (*observev1.QueryMetricsResponse, error) {
	query := MetricQuery{
		MetricName:  req.MetricName,
		ServiceName: req.ServiceName,
		Aggregation: req.Aggregation,
	}

	if req.StartTime != nil {
		query.StartTime = req.StartTime.AsTime()
	}
	if req.EndTime != nil {
		query.EndTime = req.EndTime.AsTime()
	}
	if req.Step != nil {
		query.Step = req.Step.AsDuration()
	}

	h.logger.DebugContext(ctx, "querying metrics",
		"metric", query.MetricName,
		"service", query.ServiceName,
	)

	points, err := h.metricStore.QueryMetrics(ctx, query)
	if err != nil {
		h.logger.ErrorContext(ctx, "failed to query metrics", "error", err)
		return nil, err
	}

	protoPoints := make([]*observev1.MetricDataPoint, 0, len(points))
	for _, p := range points {
		protoPoints = append(protoPoints, &observev1.MetricDataPoint{
			Timestamp: timestamppb.New(p.Timestamp),
			Value:     p.Value,
		})
	}

	return &observev1.QueryMetricsResponse{
		DataPoints: protoPoints,
	}, nil
}

// Health returns the service health status.
func (h *Handler) Health(ctx context.Context, req *observev1.HealthRequest) (*observev1.HealthResponse, error) {
	return &observev1.HealthResponse{
		Status:  "healthy",
		Version: "0.1.0",
	}, nil
}

// Proto conversion helpers

func protoToSpan(s *observev1.Span) Span {
	span := Span{
		TraceID:      s.TraceId,
		SpanID:       s.SpanId,
		ParentSpanID: s.ParentSpanId,
		Name:         s.Name,
		ServiceName:  s.ServiceName,
		Attributes:   s.Attributes,
	}

	if s.StartTime != nil {
		span.StartTime = s.StartTime.AsTime()
	}
	if s.Duration != nil {
		span.Duration = s.Duration.AsDuration()
	}

	switch s.Status {
	case observev1.SpanStatus_SPAN_STATUS_OK:
		span.Status = SpanStatusOK
	case observev1.SpanStatus_SPAN_STATUS_ERROR:
		span.Status = SpanStatusError
	default:
		span.Status = SpanStatusUnspecified
	}

	for _, e := range s.Events {
		event := SpanEvent{
			Name:       e.Name,
			Attributes: e.Attributes,
		}
		if e.Timestamp != nil {
			event.Timestamp = e.Timestamp.AsTime()
		}
		span.Events = append(span.Events, event)
	}

	return span
}

func toProtoSpan(s *Span) *observev1.Span {
	span := &observev1.Span{
		TraceId:      s.TraceID,
		SpanId:       s.SpanID,
		ParentSpanId: s.ParentSpanID,
		Name:         s.Name,
		ServiceName:  s.ServiceName,
		StartTime:    timestamppb.New(s.StartTime),
		Duration:     durationpb.New(s.Duration),
		Attributes:   s.Attributes,
	}

	switch s.Status {
	case SpanStatusOK:
		span.Status = observev1.SpanStatus_SPAN_STATUS_OK
	case SpanStatusError:
		span.Status = observev1.SpanStatus_SPAN_STATUS_ERROR
	default:
		span.Status = observev1.SpanStatus_SPAN_STATUS_UNSPECIFIED
	}

	for _, e := range s.Events {
		span.Events = append(span.Events, &observev1.SpanEvent{
			Name:       e.Name,
			Timestamp:  timestamppb.New(e.Timestamp),
			Attributes: e.Attributes,
		})
	}

	return span
}

func toProtoTrace(t *Trace) *observev1.Trace {
	spans := make([]*observev1.Span, 0, len(t.Spans))
	for i := range t.Spans {
		spans = append(spans, toProtoSpan(&t.Spans[i]))
	}

	return &observev1.Trace{
		TraceId:       t.TraceID,
		Spans:         spans,
		StartTime:     timestamppb.New(t.StartTime),
		Duration:      durationpb.New(t.Duration),
		RootService:   t.RootService,
		RootOperation: t.RootOperation,
	}
}
