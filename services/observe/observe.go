// Package observe provides the observability service for trace and metric collection.
package observe

import (
	"time"
)

// SpanStatus represents the status of a span.
type SpanStatus int

const (
	SpanStatusUnspecified SpanStatus = iota
	SpanStatusOK
	SpanStatusError
)

// Span represents a single operation in a distributed trace.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	ServiceName  string
	StartTime    time.Time
	Duration     time.Duration
	Status       SpanStatus
	Attributes   map[string]string
	Events       []SpanEvent
}

// SpanEvent represents an event that occurred during a span.
type SpanEvent struct {
	Name       string
	Timestamp  time.Time
	Attributes map[string]string
}

// Trace represents a complete distributed trace.
type Trace struct {
	TraceID       string
	Spans         []Span
	StartTime     time.Time
	Duration      time.Duration
	RootService   string
	RootOperation string
}

// TraceQuery contains filters for querying traces.
type TraceQuery struct {
	ServiceName   string
	OperationName string
	StartTime     time.Time
	EndTime       time.Time
	MinDuration   time.Duration
	MaxDuration   time.Duration
	Tags          map[string]string
	Limit         int
	Offset        int
}

// MetricDataPoint represents a single metric measurement.
type MetricDataPoint struct {
	Timestamp time.Time
	Value     float64
}

// MetricQuery contains filters for querying metrics.
type MetricQuery struct {
	MetricName  string
	ServiceName string
	StartTime   time.Time
	EndTime     time.Time
	Aggregation string // sum, avg, min, max, count
	Step        time.Duration
}
