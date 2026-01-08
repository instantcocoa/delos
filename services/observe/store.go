package observe

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// SpanStore defines the interface for span storage operations.
type SpanStore interface {
	IngestSpans(ctx context.Context, spans []Span) (int, error)
	GetTrace(ctx context.Context, traceID string) (*Trace, error)
	QueryTraces(ctx context.Context, query TraceQuery) ([]Trace, int, error)
}

// MetricStore defines the interface for metric storage operations.
type MetricStore interface {
	RecordMetric(ctx context.Context, name, service string, value float64) error
	QueryMetrics(ctx context.Context, query MetricQuery) ([]MetricDataPoint, error)
}

// MemorySpanStore is an in-memory implementation of SpanStore.
type MemorySpanStore struct {
	mu     sync.RWMutex
	spans  map[string][]Span  // traceID -> spans
	traces map[string]*Trace  // traceID -> computed trace
}

// NewMemorySpanStore creates a new in-memory span store.
func NewMemorySpanStore() *MemorySpanStore {
	return &MemorySpanStore{
		spans:  make(map[string][]Span),
		traces: make(map[string]*Trace),
	}
}

func (s *MemorySpanStore) IngestSpans(ctx context.Context, spans []Span) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, span := range spans {
		s.spans[span.TraceID] = append(s.spans[span.TraceID], span)
		s.updateTrace(span.TraceID)
	}

	return len(spans), nil
}

func (s *MemorySpanStore) updateTrace(traceID string) {
	spans := s.spans[traceID]
	if len(spans) == 0 {
		delete(s.traces, traceID)
		return
	}

	var rootSpan *Span
	var minStart, maxEnd time.Time

	for i := range spans {
		span := &spans[i]
		if span.ParentSpanID == "" {
			rootSpan = span
		}
		if minStart.IsZero() || span.StartTime.Before(minStart) {
			minStart = span.StartTime
		}
		endTime := span.StartTime.Add(span.Duration)
		if maxEnd.IsZero() || endTime.After(maxEnd) {
			maxEnd = endTime
		}
	}

	trace := &Trace{
		TraceID:   traceID,
		Spans:     spans,
		StartTime: minStart,
		Duration:  maxEnd.Sub(minStart),
	}

	if rootSpan != nil {
		trace.RootService = rootSpan.ServiceName
		trace.RootOperation = rootSpan.Name
	}

	s.traces[traceID] = trace
}

func (s *MemorySpanStore) GetTrace(ctx context.Context, traceID string) (*Trace, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	trace, ok := s.traces[traceID]
	if !ok {
		return nil, nil
	}

	result := *trace
	result.Spans = make([]Span, len(trace.Spans))
	copy(result.Spans, trace.Spans)
	return &result, nil
}

func (s *MemorySpanStore) QueryTraces(ctx context.Context, query TraceQuery) ([]Trace, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []Trace

	for _, trace := range s.traces {
		if s.matchesQuery(trace, query) {
			results = append(results, *trace)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].StartTime.After(results[j].StartTime)
	})

	totalCount := len(results)

	if query.Offset > 0 {
		if query.Offset >= len(results) {
			results = nil
		} else {
			results = results[query.Offset:]
		}
	}

	if query.Limit > 0 && len(results) > query.Limit {
		results = results[:query.Limit]
	}

	return results, totalCount, nil
}

func (s *MemorySpanStore) matchesQuery(trace *Trace, query TraceQuery) bool {
	if query.ServiceName != "" && trace.RootService != query.ServiceName {
		found := false
		for _, span := range trace.Spans {
			if span.ServiceName == query.ServiceName {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if query.OperationName != "" {
		found := false
		for _, span := range trace.Spans {
			if strings.Contains(span.Name, query.OperationName) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	if !query.StartTime.IsZero() && trace.StartTime.Before(query.StartTime) {
		return false
	}
	if !query.EndTime.IsZero() && trace.StartTime.After(query.EndTime) {
		return false
	}

	if query.MinDuration > 0 && trace.Duration < query.MinDuration {
		return false
	}
	if query.MaxDuration > 0 && trace.Duration > query.MaxDuration {
		return false
	}

	if len(query.Tags) > 0 {
		for key, value := range query.Tags {
			found := false
			for _, span := range trace.Spans {
				if span.Attributes[key] == value {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}

	return true
}

// MemoryMetricStore is an in-memory implementation of MetricStore.
type MemoryMetricStore struct {
	mu      sync.RWMutex
	metrics map[string][]MetricDataPoint // "name:service" -> data points
}

// NewMemoryMetricStore creates a new in-memory metric store.
func NewMemoryMetricStore() *MemoryMetricStore {
	return &MemoryMetricStore{
		metrics: make(map[string][]MetricDataPoint),
	}
}

func (s *MemoryMetricStore) RecordMetric(ctx context.Context, name, service string, value float64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := name + ":" + service
	s.metrics[key] = append(s.metrics[key], MetricDataPoint{
		Timestamp: time.Now(),
		Value:     value,
	})

	return nil
}

func (s *MemoryMetricStore) QueryMetrics(ctx context.Context, query MetricQuery) ([]MetricDataPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := query.MetricName + ":" + query.ServiceName
	points := s.metrics[key]

	var results []MetricDataPoint
	for _, p := range points {
		if !query.StartTime.IsZero() && p.Timestamp.Before(query.StartTime) {
			continue
		}
		if !query.EndTime.IsZero() && p.Timestamp.After(query.EndTime) {
			continue
		}
		results = append(results, p)
	}

	return results, nil
}

// Ensure implementations satisfy interfaces
var (
	_ SpanStore   = (*MemorySpanStore)(nil)
	_ MetricStore = (*MemoryMetricStore)(nil)
)
