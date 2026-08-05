// Package deploy provides quality gates: named sets of thresholds that the
// latest eval run for a prompt must satisfy. Delos does not deploy anything —
// it emits a verdict and CI/CD systems act on it.
package deploy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrGateNotFound is returned when no gate exists with the requested name.
var ErrGateNotFound = errors.New("quality gate not found")

// Operator is the comparison a condition applies to a metric.
type Operator int

const (
	OperatorUnspecified Operator = iota
	// OperatorGTE requires metric >= threshold.
	OperatorGTE
	// OperatorLTE requires metric <= threshold.
	OperatorLTE
)

// String renders the operator the way it is written in a condition string.
func (o Operator) String() string {
	switch o {
	case OperatorGTE:
		return ">="
	case OperatorLTE:
		return "<="
	default:
		return "?"
	}
}

// Supported metric names. These map one-to-one onto eval run summary fields.
const (
	MetricOverallScore = "overall_score"
	MetricPassRate     = "pass_rate"
	MetricAvgLatencyMs = "avg_latency_ms"
	MetricTotalCostUSD = "total_cost_usd"
)

// SupportedMetrics lists every metric a condition may reference.
func SupportedMetrics() []string {
	return []string{MetricOverallScore, MetricPassRate, MetricAvgLatencyMs, MetricTotalCostUSD}
}

// QualityGate is a named set of conditions evaluated against the most recent
// completed eval run for a prompt.
type QualityGate struct {
	ID          string
	Name        string
	Description string
	PromptID    string
	Conditions  []GateCondition
	CreatedAt   time.Time
	CreatedBy   string
}

// GateCondition is a single threshold check against an eval run summary.
type GateCondition struct {
	Metric    string
	Operator  Operator
	Threshold float64
}

// String renders the condition the way the CLI accepts it, e.g.
// "overall_score>=0.8".
func (c GateCondition) String() string {
	return fmt.Sprintf("%s%s%s", c.Metric, c.Operator, formatNumber(c.Threshold))
}

// ParseCondition parses a condition string such as "overall_score>=0.8" or
// "avg_latency_ms <= 2000".
func ParseCondition(s string) (GateCondition, error) {
	trimmed := strings.TrimSpace(s)

	var op Operator
	var idx int
	switch {
	case strings.Contains(trimmed, ">="):
		op, idx = OperatorGTE, strings.Index(trimmed, ">=")
	case strings.Contains(trimmed, "<="):
		op, idx = OperatorLTE, strings.Index(trimmed, "<=")
	default:
		return GateCondition{}, fmt.Errorf("condition %q must contain >= or <= (e.g. %q)", s, "overall_score>=0.8")
	}

	metric := strings.TrimSpace(trimmed[:idx])
	rest := strings.TrimSpace(trimmed[idx+2:])

	if metric == "" {
		return GateCondition{}, fmt.Errorf("condition %q is missing a metric name", s)
	}
	if !isSupportedMetric(metric) {
		return GateCondition{}, fmt.Errorf("unknown metric %q in condition %q: supported metrics are %s",
			metric, s, strings.Join(SupportedMetrics(), ", "))
	}

	threshold, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		return GateCondition{}, fmt.Errorf("condition %q has an unparseable threshold %q: %w", s, rest, err)
	}

	return GateCondition{Metric: metric, Operator: op, Threshold: threshold}, nil
}

func isSupportedMetric(metric string) bool {
	for _, m := range SupportedMetrics() {
		if m == metric {
			return true
		}
	}
	return false
}

// RunSummary is the slice of an eval run that a quality gate needs. It is
// deliberately narrow so the deploy package never imports the eval package.
type RunSummary struct {
	RunID        string
	PromptID     string
	OverallScore float64
	PassRate     float64
	AvgLatencyMs float64
	TotalCostUSD float64
	CompletedAt  time.Time
}

// Metric returns the named metric from the summary.
func (s *RunSummary) Metric(name string) (float64, bool) {
	switch name {
	case MetricOverallScore:
		return s.OverallScore, true
	case MetricPassRate:
		return s.PassRate, true
	case MetricAvgLatencyMs:
		return s.AvgLatencyMs, true
	case MetricTotalCostUSD:
		return s.TotalCostUSD, true
	default:
		return 0, false
	}
}

// EvalResults is the only thing gates need from the eval module. The control
// plane wires an adapter over the eval store; calls are direct, not networked.
type EvalResults interface {
	// LatestCompletedRun returns the most recent completed eval run for a
	// prompt, or (nil, nil) when the prompt has none.
	LatestCompletedRun(ctx context.Context, promptID string) (*RunSummary, error)
}

// GateVerdict is the answer `delos gate check` and CI act on.
type GateVerdict struct {
	Gate        *QualityGate
	Pass        bool
	Reasons     []string
	EvalRunID   string
	EvaluatedAt time.Time
}

// CreateQualityGateInput contains input for creating a quality gate.
type CreateQualityGateInput struct {
	Name        string
	Description string
	PromptID    string
	Conditions  []GateCondition
	CreatedBy   string
}

// formatNumber renders a float without trailing zeros so reasons read like
// "overall_score 0.82 >= 0.8: pass" rather than "0.820000 >= 0.800000".
func formatNumber(v float64) string {
	s := strconv.FormatFloat(v, 'f', 4, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
