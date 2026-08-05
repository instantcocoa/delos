-- Observe stores OTLP spans flat, one row per span, with the gen_ai.*
-- essentials promoted to typed columns so cost/latency queries never dig
-- through JSONB. Traces are derived by aggregating spans at query time.

CREATE TABLE IF NOT EXISTS spans (
    trace_id       TEXT NOT NULL,
    span_id        TEXT NOT NULL,
    parent_span_id TEXT NOT NULL DEFAULT '',
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT '',
    service_name   TEXT NOT NULL DEFAULT '',
    start_time     TIMESTAMPTZ NOT NULL,
    duration_ns    BIGINT NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT '',
    status_msg     TEXT NOT NULL DEFAULT '',
    attributes     JSONB NOT NULL DEFAULT '{}',
    events         JSONB NOT NULL DEFAULT '[]',

    -- promoted gen_ai.* essentials
    gen_ai_system  TEXT NOT NULL DEFAULT '',
    request_model  TEXT NOT NULL DEFAULT '',
    response_model TEXT NOT NULL DEFAULT '',
    input_tokens   BIGINT NOT NULL DEFAULT 0,
    output_tokens  BIGINT NOT NULL DEFAULT 0,
    cost_usd       DOUBLE PRECISION NOT NULL DEFAULT 0,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (trace_id, span_id)
);

CREATE INDEX IF NOT EXISTS idx_spans_service_time ON spans (service_name, start_time DESC);
CREATE INDEX IF NOT EXISTS idx_spans_trace ON spans (trace_id);
CREATE INDEX IF NOT EXISTS idx_spans_model_time ON spans (request_model, start_time DESC) WHERE request_model <> '';

CREATE TABLE IF NOT EXISTS metrics (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name         TEXT NOT NULL,
    service_name TEXT NOT NULL DEFAULT '',
    value        DOUBLE PRECISION NOT NULL,
    timestamp    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_metrics_name_time ON metrics (name, timestamp DESC);
