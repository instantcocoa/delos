package observe

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Migrations contains the observe schema, applied by the control plane at
// startup when Postgres storage is enabled.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// PostgresSpanStore stores spans one row each, with gen_ai.* essentials in
// typed columns. Traces are derived by aggregation at query time.
type PostgresSpanStore struct {
	db *sql.DB
}

// NewPostgresSpanStore wraps an open database handle.
func NewPostgresSpanStore(db *sql.DB) *PostgresSpanStore {
	return &PostgresSpanStore{db: db}
}

func (s *PostgresSpanStore) IngestSpans(ctx context.Context, spans []Span) (int, error) {
	count := 0
	for _, span := range spans {
		attrs, err := json.Marshal(span.Attributes)
		if err != nil {
			attrs = []byte("{}")
		}
		events, err := json.Marshal(span.Events)
		if err != nil {
			events = []byte("[]")
		}
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO spans (trace_id, span_id, parent_span_id, name, kind,
				service_name, start_time, duration_ns, status, status_msg,
				attributes, events, gen_ai_system, request_model, response_model,
				input_tokens, output_tokens, cost_usd)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
			ON CONFLICT (trace_id, span_id) DO NOTHING`,
			span.TraceID, span.SpanID, span.ParentSpanID, span.Name, span.Kind,
			span.ServiceName, span.StartTime, span.Duration.Nanoseconds(),
			span.Status.String(), span.StatusMsg, attrs, events,
			span.GenAISystem, span.RequestModel, span.ResponseModel,
			span.InputTokens, span.OutputTokens, span.CostUSD,
		)
		if err != nil {
			return count, fmt.Errorf("failed to insert span %s/%s: %w", span.TraceID, span.SpanID, err)
		}
		count++
	}
	return count, nil
}

func scanSpanRow(rows *sql.Rows) (Span, error) {
	var span Span
	var durationNS int64
	var status string
	var attrs, events []byte
	err := rows.Scan(&span.TraceID, &span.SpanID, &span.ParentSpanID, &span.Name,
		&span.Kind, &span.ServiceName, &span.StartTime, &durationNS, &status,
		&span.StatusMsg, &attrs, &events, &span.GenAISystem, &span.RequestModel,
		&span.ResponseModel, &span.InputTokens, &span.OutputTokens, &span.CostUSD)
	if err != nil {
		return span, err
	}
	span.Duration = time.Duration(durationNS)
	span.Status = ParseSpanStatus(status)
	_ = json.Unmarshal(attrs, &span.Attributes)
	_ = json.Unmarshal(events, &span.Events)
	return span, nil
}

const spanColumns = `trace_id, span_id, parent_span_id, name, kind, service_name,
	start_time, duration_ns, status, status_msg, attributes, events,
	gen_ai_system, request_model, response_model, input_tokens, output_tokens, cost_usd`

func (s *PostgresSpanStore) GetTrace(ctx context.Context, traceID string) (*Trace, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+spanColumns+` FROM spans WHERE trace_id = $1 ORDER BY start_time`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var spans []Span
	for rows.Next() {
		span, err := scanSpanRow(rows)
		if err != nil {
			return nil, err
		}
		spans = append(spans, span)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		return nil, nil
	}
	return traceFromSpans(traceID, spans), nil
}

// traceFromSpans derives trace-level fields the same way MemorySpanStore does.
func traceFromSpans(traceID string, spans []Span) *Trace {
	trace := &Trace{TraceID: traceID, Spans: spans}
	var end time.Time
	for i, span := range spans {
		if i == 0 || span.StartTime.Before(trace.StartTime) {
			trace.StartTime = span.StartTime
		}
		if e := span.EndTime(); e.After(end) {
			end = e
		}
		if span.ParentSpanID == "" {
			trace.RootService = span.ServiceName
			trace.RootOperation = span.Name
		}
	}
	if trace.RootService == "" && len(spans) > 0 {
		trace.RootService = spans[0].ServiceName
		trace.RootOperation = spans[0].Name
	}
	if !end.IsZero() {
		trace.Duration = end.Sub(trace.StartTime)
	}
	return trace
}

func (s *PostgresSpanStore) QueryTraces(ctx context.Context, query TraceQuery) ([]Trace, int, error) {
	// Find matching trace IDs first, then load their spans.
	where := []string{"TRUE"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if query.ServiceName != "" {
		where = append(where, "service_name = "+arg(query.ServiceName))
	}
	if query.OperationName != "" {
		where = append(where, "name = "+arg(query.OperationName))
	}
	if !query.StartTime.IsZero() {
		where = append(where, "start_time >= "+arg(query.StartTime))
	}
	if !query.EndTime.IsZero() {
		where = append(where, "start_time <= "+arg(query.EndTime))
	}
	for k, v := range query.Tags {
		where = append(where, "attributes->>"+arg(k)+" = "+arg(v))
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}

	baseFilter := strings.Join(where, " AND ")
	idQuery := `
		SELECT trace_id, MIN(start_time) AS ts, MAX(duration_ns) AS dur
		FROM spans WHERE ` + baseFilter + `
		GROUP BY trace_id`
	having := []string{}
	if query.MinDuration > 0 {
		having = append(having, "MAX(duration_ns) >= "+arg(query.MinDuration.Nanoseconds()))
	}
	if query.MaxDuration > 0 {
		having = append(having, "MAX(duration_ns) <= "+arg(query.MaxDuration.Nanoseconds()))
	}
	if len(having) > 0 {
		idQuery += " HAVING " + strings.Join(having, " AND ")
	}

	countQuery := "SELECT COUNT(*) FROM (" + idQuery + ") ids"
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	pagedQuery := idQuery + " ORDER BY ts DESC LIMIT " + arg(limit) + " OFFSET " + arg(query.Offset)
	rows, err := s.db.QueryContext(ctx, pagedQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		var ts time.Time
		var dur int64
		if err := rows.Scan(&id, &ts, &dur); err != nil {
			return nil, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	traces := make([]Trace, 0, len(ids))
	for _, id := range ids {
		trace, err := s.GetTrace(ctx, id)
		if err != nil {
			return nil, 0, err
		}
		if trace != nil {
			traces = append(traces, *trace)
		}
	}
	return traces, total, nil
}

// PostgresMetricStore stores flat metric data points.
type PostgresMetricStore struct {
	db *sql.DB
}

// NewPostgresMetricStore wraps an open database handle.
func NewPostgresMetricStore(db *sql.DB) *PostgresMetricStore {
	return &PostgresMetricStore{db: db}
}

func (s *PostgresMetricStore) RecordMetric(ctx context.Context, name, service string, value float64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO metrics (name, service_name, value) VALUES ($1, $2, $3)`,
		name, service, value)
	return err
}

func (s *PostgresMetricStore) QueryMetrics(ctx context.Context, query MetricQuery) ([]MetricDataPoint, error) {
	where := []string{"name = $1"}
	args := []any{query.MetricName}
	if query.ServiceName != "" {
		args = append(args, query.ServiceName)
		where = append(where, fmt.Sprintf("service_name = $%d", len(args)))
	}
	if !query.StartTime.IsZero() {
		args = append(args, query.StartTime)
		where = append(where, fmt.Sprintf("timestamp >= $%d", len(args)))
	}
	if !query.EndTime.IsZero() {
		args = append(args, query.EndTime)
		where = append(where, fmt.Sprintf("timestamp <= $%d", len(args)))
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT timestamp, value FROM metrics WHERE `+strings.Join(where, " AND ")+` ORDER BY timestamp`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var points []MetricDataPoint
	for rows.Next() {
		var p MetricDataPoint
		if err := rows.Scan(&p.Timestamp, &p.Value); err != nil {
			return nil, err
		}
		points = append(points, p)
	}
	return points, rows.Err()
}
