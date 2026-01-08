-- Traces table
CREATE TABLE traces (
    trace_id TEXT PRIMARY KEY,
    root_service TEXT NOT NULL,
    root_operation TEXT NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    duration_ns BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_traces_root_service ON traces(root_service);
CREATE INDEX idx_traces_root_operation ON traces(root_operation);
CREATE INDEX idx_traces_start_time ON traces(start_time);
CREATE INDEX idx_traces_duration ON traces(duration_ns);

-- Spans table
CREATE TABLE spans (
    span_id TEXT NOT NULL,
    trace_id TEXT NOT NULL REFERENCES traces(trace_id) ON DELETE CASCADE,
    parent_span_id TEXT,
    name TEXT NOT NULL,
    service_name TEXT NOT NULL,
    start_time TIMESTAMPTZ NOT NULL,
    duration_ns BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'ok',
    PRIMARY KEY(trace_id, span_id)
);

CREATE INDEX idx_spans_trace_id ON spans(trace_id);
CREATE INDEX idx_spans_service_name ON spans(service_name);
CREATE INDEX idx_spans_start_time ON spans(start_time);

-- Span attributes
CREATE TABLE span_attributes (
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY(trace_id, span_id, key),
    FOREIGN KEY(trace_id, span_id) REFERENCES spans(trace_id, span_id) ON DELETE CASCADE
);

-- Span events
CREATE TABLE span_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    name TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    FOREIGN KEY(trace_id, span_id) REFERENCES spans(trace_id, span_id) ON DELETE CASCADE
);

CREATE INDEX idx_span_events_span ON span_events(trace_id, span_id);

-- Span event attributes
CREATE TABLE span_event_attributes (
    event_id UUID NOT NULL REFERENCES span_events(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    PRIMARY KEY(event_id, key)
);

-- Metrics table (for aggregated metrics)
CREATE TABLE metrics (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    service_name TEXT,
    value DOUBLE PRECISION NOT NULL,
    unit TEXT,
    timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_metrics_name ON metrics(name);
CREATE INDEX idx_metrics_service ON metrics(service_name);
CREATE INDEX idx_metrics_timestamp ON metrics(timestamp);

-- Partitioning hint for future: could partition traces/spans by time
-- For now, add a cleanup policy via scheduled job

-- Create a function to clean old traces (called by cron)
CREATE OR REPLACE FUNCTION cleanup_old_traces(retention_days INTEGER DEFAULT 7)
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    DELETE FROM traces WHERE created_at < NOW() - (retention_days || ' days')::INTERVAL;
    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;
