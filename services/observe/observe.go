// Package observe provides the observability service for trace and metric
// collection. It is an OTLP consumer: spans arrive over OTLP/HTTP (protobuf or
// JSON) from any source that emits OpenTelemetry `gen_ai.*` semantic
// conventions -- delos-gateway, an OTel-SDK-instrumented app, or another
// gateway -- and are stored in Postgres (or memory) for trace, cost and
// latency queries.
package observe

import (
	"strconv"
	"time"
)

// SpanStatus represents the status of a span.
type SpanStatus int

const (
	SpanStatusUnspecified SpanStatus = iota
	SpanStatusOK
	SpanStatusError
)

// String returns the storage representation of the status.
func (s SpanStatus) String() string {
	switch s {
	case SpanStatusOK:
		return "ok"
	case SpanStatusError:
		return "error"
	default:
		return "unset"
	}
}

// ParseSpanStatus converts a stored status string back to a SpanStatus.
func ParseSpanStatus(s string) SpanStatus {
	switch s {
	case "ok":
		return SpanStatusOK
	case "error":
		return SpanStatusError
	default:
		return SpanStatusUnspecified
	}
}

// Span represents a single operation in a distributed trace.
//
// Attributes holds every attribute the emitter sent (span attributes merged
// with resource attributes), stringified. The gen_ai fields below are promoted
// copies of the attributes named in attrs.go so cost and latency queries do not
// have to dig through JSONB.
type Span struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Name         string
	Kind         string
	ServiceName  string
	StartTime    time.Time
	Duration     time.Duration
	Status       SpanStatus
	StatusMsg    string
	Attributes   map[string]string
	Events       []SpanEvent

	// Promoted gen_ai.* essentials.
	GenAISystem   string
	RequestModel  string
	ResponseModel string
	InputTokens   int64
	OutputTokens  int64
	CostUSD       float64
}

// EndTime returns the span end timestamp.
func (s *Span) EndTime() time.Time {
	return s.StartTime.Add(s.Duration)
}

// DurationMillis returns the span duration in fractional milliseconds.
func (s *Span) DurationMillis() float64 {
	return float64(s.Duration) / float64(time.Millisecond)
}

// IsGenAI reports whether the span describes an LLM call, i.e. whether it
// carries enough gen_ai.* attributes to participate in cost/latency stats.
func (s *Span) IsGenAI() bool {
	return s.GenAISystem != "" || s.RequestModel != "" || s.ResponseModel != ""
}

// Model returns the model to attribute the span to, preferring the model the
// provider actually served over the one requested.
func (s *Span) Model() string {
	if s.ResponseModel != "" {
		return s.ResponseModel
	}
	return s.RequestModel
}

// PromoteAttributes fills the promoted gen_ai fields from the attribute bag.
// It is idempotent and never clears a field that is already set.
func (s *Span) PromoteAttributes() {
	if s.Attributes == nil {
		return
	}
	if s.GenAISystem == "" {
		s.GenAISystem = s.Attributes[AttrGenAISystem]
	}
	if s.RequestModel == "" {
		s.RequestModel = firstNonEmpty(s.Attributes, genAIRequestModelKeys)
	}
	if s.ResponseModel == "" {
		s.ResponseModel = firstNonEmpty(s.Attributes, genAIResponseModelKeys)
	}
	if s.InputTokens == 0 {
		s.InputTokens = firstInt(s.Attributes, genAIInputTokenKeys)
	}
	if s.OutputTokens == 0 {
		s.OutputTokens = firstInt(s.Attributes, genAIOutputTokenKeys)
	}
	if s.CostUSD == 0 {
		if v, ok := s.Attributes[AttrDelosCostUSD]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				s.CostUSD = f
			}
		}
	}
	if s.ServiceName == "" {
		s.ServiceName = s.Attributes[AttrServiceName]
	}
}

func firstNonEmpty(attrs map[string]string, keys []string) string {
	for _, k := range keys {
		if v := attrs[k]; v != "" {
			return v
		}
	}
	return ""
}

func firstInt(attrs map[string]string, keys []string) int64 {
	for _, k := range keys {
		v, ok := attrs[k]
		if !ok || v == "" {
			continue
		}
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
		// Tolerate emitters that send token counts as doubles.
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return int64(f)
		}
	}
	return 0
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

// SpanQuery contains filters for querying individual spans, which is what the
// cost/latency views and flat request listings want.
type SpanQuery struct {
	ServiceName string
	Model       string
	GenAISystem string
	StartTime   time.Time
	EndTime     time.Time
	OnlyGenAI   bool
	Limit       int
	Offset      int
}

// StatsQuery selects the spans an aggregate rollup covers.
type StatsQuery struct {
	StartTime   time.Time
	EndTime     time.Time
	ServiceName string
	Model       string
	// Bucket is the width of each cost-over-time point. Zero means one hour.
	Bucket time.Duration
}

// ModelStats aggregates request volume, tokens, cost and latency for one model.
type ModelStats struct {
	Model        string
	GenAISystem  string
	RequestCount int64
	ErrorCount   int64
	InputTokens  int64
	OutputTokens int64
	CostUSD      float64
	P50LatencyMs float64
	P95LatencyMs float64
}

// CostBucket is one point in the cost-over-time series for a model.
type CostBucket struct {
	BucketStart  time.Time
	Model        string
	CostUSD      float64
	RequestCount int64
	InputTokens  int64
	OutputTokens int64
}

// Stats is the aggregate rollup powering the cost/latency view.
type Stats struct {
	StartTime         time.Time
	EndTime           time.Time
	TotalRequests     int64
	TotalCostUSD      float64
	TotalInputTokens  int64
	TotalOutputTokens int64
	ByModel           []ModelStats
	CostOverTime      []CostBucket
}

// DefaultStatsBucket is the cost-over-time bucket width when unspecified.
const DefaultStatsBucket = time.Hour

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
