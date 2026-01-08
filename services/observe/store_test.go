package observe

import (
	"context"
	"testing"
	"time"
)

func TestMemorySpanStore_IngestAndGet(t *testing.T) {
	store := NewMemorySpanStore()
	ctx := context.Background()

	spans := []Span{
		{
			TraceID:     "trace-1",
			SpanID:      "span-1",
			Name:        "root-operation",
			ServiceName: "test-service",
			StartTime:   time.Now(),
			Duration:    100 * time.Millisecond,
			Status:      SpanStatusOK,
		},
		{
			TraceID:      "trace-1",
			SpanID:       "span-2",
			ParentSpanID: "span-1",
			Name:         "child-operation",
			ServiceName:  "test-service",
			StartTime:    time.Now(),
			Duration:     50 * time.Millisecond,
			Status:       SpanStatusOK,
		},
	}

	count, err := store.IngestSpans(ctx, spans)
	if err != nil {
		t.Fatalf("IngestSpans() error = %v", err)
	}
	if count != 2 {
		t.Errorf("IngestSpans() count = %d, want 2", count)
	}

	trace, err := store.GetTrace(ctx, "trace-1")
	if err != nil {
		t.Fatalf("GetTrace() error = %v", err)
	}
	if trace == nil {
		t.Fatal("GetTrace() returned nil")
	}
	if len(trace.Spans) != 2 {
		t.Errorf("trace.Spans count = %d, want 2", len(trace.Spans))
	}
	if trace.RootService != "test-service" {
		t.Errorf("trace.RootService = %q, want %q", trace.RootService, "test-service")
	}
	if trace.RootOperation != "root-operation" {
		t.Errorf("trace.RootOperation = %q, want %q", trace.RootOperation, "root-operation")
	}
}

func TestMemorySpanStore_GetTrace_NotFound(t *testing.T) {
	store := NewMemorySpanStore()
	ctx := context.Background()

	trace, err := store.GetTrace(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetTrace() error = %v", err)
	}
	if trace != nil {
		t.Errorf("GetTrace() = %v, want nil", trace)
	}
}

func TestMemorySpanStore_QueryTraces(t *testing.T) {
	store := NewMemorySpanStore()
	ctx := context.Background()

	// Create multiple traces
	for i := 0; i < 5; i++ {
		spans := []Span{
			{
				TraceID:     "trace-" + string(rune('a'+i)),
				SpanID:      "span-1",
				Name:        "operation",
				ServiceName: "test-service",
				StartTime:   time.Now().Add(time.Duration(i) * time.Second),
				Duration:    100 * time.Millisecond,
			},
		}
		store.IngestSpans(ctx, spans)
	}

	traces, total, err := store.QueryTraces(ctx, TraceQuery{Limit: 10})
	if err != nil {
		t.Fatalf("QueryTraces() error = %v", err)
	}
	if total != 5 {
		t.Errorf("QueryTraces() total = %d, want 5", total)
	}
	if len(traces) != 5 {
		t.Errorf("QueryTraces() count = %d, want 5", len(traces))
	}
}

func TestMemorySpanStore_QueryTraces_WithFilters(t *testing.T) {
	store := NewMemorySpanStore()
	ctx := context.Background()

	// Create traces with different services
	spans1 := []Span{{TraceID: "t1", SpanID: "s1", Name: "op", ServiceName: "service-a", StartTime: time.Now()}}
	spans2 := []Span{{TraceID: "t2", SpanID: "s1", Name: "op", ServiceName: "service-b", StartTime: time.Now()}}
	spans3 := []Span{{TraceID: "t3", SpanID: "s1", Name: "op", ServiceName: "service-a", StartTime: time.Now()}}

	store.IngestSpans(ctx, spans1)
	store.IngestSpans(ctx, spans2)
	store.IngestSpans(ctx, spans3)

	// Filter by service
	traces, total, err := store.QueryTraces(ctx, TraceQuery{
		ServiceName: "service-a",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("QueryTraces() error = %v", err)
	}
	if total != 2 {
		t.Errorf("QueryTraces() total = %d, want 2", total)
	}
	if len(traces) != 2 {
		t.Errorf("QueryTraces() count = %d, want 2", len(traces))
	}
}

func TestMemorySpanStore_QueryTraces_Pagination(t *testing.T) {
	store := NewMemorySpanStore()
	ctx := context.Background()

	for i := 0; i < 10; i++ {
		spans := []Span{{TraceID: "t" + string(rune('0'+i)), SpanID: "s1", Name: "op", ServiceName: "svc", StartTime: time.Now()}}
		store.IngestSpans(ctx, spans)
	}

	traces, total, err := store.QueryTraces(ctx, TraceQuery{
		Limit:  3,
		Offset: 2,
	})
	if err != nil {
		t.Fatalf("QueryTraces() error = %v", err)
	}
	if total != 10 {
		t.Errorf("QueryTraces() total = %d, want 10", total)
	}
	if len(traces) != 3 {
		t.Errorf("QueryTraces() count = %d, want 3", len(traces))
	}
}

func TestMemoryMetricStore_RecordAndQuery(t *testing.T) {
	store := NewMemoryMetricStore()
	ctx := context.Background()

	err := store.RecordMetric(ctx, "latency", "test-service", 100.5)
	if err != nil {
		t.Fatalf("RecordMetric() error = %v", err)
	}

	err = store.RecordMetric(ctx, "latency", "test-service", 150.0)
	if err != nil {
		t.Fatalf("RecordMetric() error = %v", err)
	}

	points, err := store.QueryMetrics(ctx, MetricQuery{
		MetricName:  "latency",
		ServiceName: "test-service",
	})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if len(points) != 2 {
		t.Errorf("QueryMetrics() count = %d, want 2", len(points))
	}
}

func TestMemoryMetricStore_QueryMetrics_TimeFilter(t *testing.T) {
	store := NewMemoryMetricStore()
	ctx := context.Background()

	now := time.Now()

	// Record metrics
	store.RecordMetric(ctx, "latency", "svc", 100)
	store.RecordMetric(ctx, "latency", "svc", 200)

	// Query with time filter (should get all since they were just recorded)
	points, err := store.QueryMetrics(ctx, MetricQuery{
		MetricName:  "latency",
		ServiceName: "svc",
		StartTime:   now.Add(-time.Minute),
		EndTime:     now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("QueryMetrics() error = %v", err)
	}
	if len(points) != 2 {
		t.Errorf("QueryMetrics() count = %d, want 2", len(points))
	}
}
