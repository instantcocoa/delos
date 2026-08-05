# OpenTelemetry

The gateway emits one OTLP span per request using the OpenTelemetry GenAI
semantic conventions (`gen_ai.*`, pinned pre-stable version — see
`pkg/semconv`). There is no proprietary trace format: anything that speaks
OTLP can consume Delos traces, and the Delos control plane's observe module
accepts `gen_ai.*` spans from any source, not just the gateway.

## Span attributes

Every completion span carries:

- `gen_ai.operation.name`, `gen_ai.system` (provider), `gen_ai.request.model`,
  `gen_ai.response.model`, `gen_ai.response.id`, `gen_ai.response.finish_reasons`
- `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`
- Delos extensions: `delos.cost_usd`, `delos.cache_hit`, `delos.request_id`,
  `delos.virtual_key` (key name, never the key), `delos.provider_attempts`

Prompt/completion content (`gen_ai.prompt`, `gen_ai.completion`) is captured
**only** when `DELOS_TRACE_CONTENT=true`, and redaction rules run before
export.

## Configuration

```bash
DELOS_OTLP_ENDPOINT=localhost:4318      # where to export; unset = no export
DELOS_OTLP_PROTOCOL=http                # http (default) or grpc
DELOS_TRACE_STDOUT=true                 # dev mode: print spans to stdout
DELOS_TRACE_CONTENT=true                # capture prompts/completions (off by default)
DELOS_TRACE_REDACT='sk-[A-Za-z0-9]+=>[REDACTED];user_\d+=>[USER]'
```

## Pointing at your own backend

The gateway has zero knowledge of the Delos control plane; it exports plain
OTLP wherever you point it.

**Jaeger** (all-in-one listens for OTLP on 4318):

```bash
docker run -p 16686:16686 -p 4318:4318 jaegertracing/all-in-one:1.54
DELOS_OTLP_ENDPOINT=localhost:4318 delos-gateway
```

**Grafana Tempo / Alloy**: point `DELOS_OTLP_ENDPOINT` at your Alloy or Tempo
distributor OTLP HTTP port.

**Datadog**: run the Datadog Agent with OTLP ingest enabled
(`DD_OTLP_CONFIG_RECEIVER_PROTOCOLS_HTTP_ENDPOINT=0.0.0.0:4318`) and set
`DELOS_OTLP_ENDPOINT` to it.

**Delos observe** (the batteries-included option): the control plane ingests
OTLP/HTTP on its own port:

```bash
DELOS_OTLP_ENDPOINT=localhost:8081 delos-gateway
```

## Instrumented apps

Applications instrumented with any OTel SDK (or Langfuse's OTLP export) can
send their spans to the control plane's `/v1/traces` endpoint too; observe
stores and aggregates anything carrying `gen_ai.*` attributes, with token
counts and cost attributed per model.
