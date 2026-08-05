# Configuration Reference

Delos runs with no configuration at all. When you need to change something,
there are two ways to do it, and one wins:

> **environment variable > `delos.yaml` > built-in default**

There are three configuration surfaces:

| Surface | Binary | Port |
|---------|--------|------|
| Gateway (data plane) | `delos-gateway` | 8080 |
| Control plane | `delos serve` | 8081 |
| CLI | `delos <subcommand>` | - |

## The config file

Delos reads `$DELOS_CONFIG`, else `./delos.yaml` if it exists. Nothing is
required in it: any key you leave out falls back to the environment and then to
the default.

```bash
# docs-test
cp delos.yaml.example delos.yaml
delos config validate delos.yaml
```

[`delos.yaml.example`](../../delos.yaml.example) documents every option with the
matching environment variable next to it. `delos config validate` reports
syntax errors, unknown keys, bad enums and malformed durations in plain
language and exits 1 when the file is invalid; `delos config doctor`
additionally probes the control plane, the gateway, PostgreSQL and provider
connectivity.

**Secrets are environment-only and cannot be set in the file**: provider API
keys and `DELOS_DB_PASSWORD`. That is what makes `delos.yaml` safe to commit.

For Docker Compose, [`deploy/local/.env.example`](../../deploy/local/.env.example)
is the template. Copy it to `deploy/local/.env` and keep secrets in
`deploy/local/.env.local` (gitignored). Compose loads both files.

## Common Settings

These apply to both binaries:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_ENV` | `development` | Environment name (development, staging, production) |
| `DELOS_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `DELOS_LOG_FORMAT` | `json` | Log format (json, text) |
| `DELOS_VERSION` | `dev` | Version string reported in telemetry |

## Gateway (`delos-gateway`, port 8080)

The gateway is stateless: no database, no control plane, no dependency other
than the upstream providers.

| Variable | File key | Default | Description |
|----------|----------|---------|-------------|
| `DELOS_GATEWAY_PORT` | `gateway.port` | `8080` | HTTP listen port |
| `DELOS_REQUEST_TIMEOUT` | `gateway.request_timeout` | `5m` | End-to-end budget for one request, including retries and fallbacks |
| `DELOS_ROUTES` | `gateway.routes` | - | Model aliases with fallback chains. The env var is JSON: `{"gpt-4":["openai/gpt-4o","anthropic/claude-sonnet-4-5"]}` |
| `DELOS_CACHE` | `gateway.cache.enabled` | on | `off` disables the response cache |
| `DELOS_CACHE_TTL` | `gateway.cache.ttl` | `5m` | Cache entry lifetime |
| `DELOS_REDIS_URL` | `gateway.cache.redis_url` | - | Shared cache across replicas; empty means an in-process LRU |
| `DELOS_TAIL` | - | on | `off` disables `GET /v1/events` (the `delos tail` stream) |
| `DELOS_TAIL_BUFFER` | - | `200` | Events kept for `delos tail --replay` |
| `DELOS_STORAGE_BACKEND` | `storage` | `memory` | `postgres` turns on virtual-key enforcement (with the `DELOS_DB_*` settings below) |

### Provider Keys

The gateway registers one provider for every key it finds at startup. If none
are set it still starts and logs a warning; completions then fail.

| Variable | Provider | Notes |
|----------|----------|-------|
| `OPENAI_API_KEY` | OpenAI | |
| `ANTHROPIC_API_KEY` | Anthropic | |
| `GEMINI_API_KEY` or `GOOGLE_API_KEY` | Google Gemini | Generative Language API |
| `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`, or `AWS_PROFILE` | AWS Bedrock | Uses the AWS SDK's default credential chain (Converse API) |
| `DELOS_RUNTIME_OLLAMA_ENABLED=true` | Ollama | Served through Ollama's OpenAI-compatible endpoint. Set `DELOS_RUNTIME_OLLAMA_URL` if it is not on `localhost:11434`. |
| `DELOS_COMPAT_BASE_URL` | Any OpenAI-compatible endpoint | Covers vLLM, SGLang, Together, OpenRouter, LM Studio and most gateways |

That is the whole provider set. There are no separate OpenRouter, Together or
Vertex AI providers — reach those (and anything else that speaks the OpenAI
API) through `DELOS_COMPAT_*`.

The older `DELOS_RUNTIME_<PROVIDER>_KEY` names (`DELOS_RUNTIME_OPENAI_KEY`,
`DELOS_RUNTIME_ANTHROPIC_KEY`, `DELOS_RUNTIME_GEMINI_KEY`) still work as
fallbacks; the standard names take priority.

### Provider Extras

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_RUNTIME_OLLAMA_URL` | `http://localhost:11434` | Ollama server URL (`http://ollama:11434` in Docker). Setting it also enables the provider. |
| `DELOS_COMPAT_NAME` | `compat` | Provider name for the OpenAI-compatible endpoint; also the `provider/model` prefix |
| `DELOS_COMPAT_API_KEY` | - | Bearer token for that endpoint, if it needs one |
| `AWS_REGION` | `us-east-1` | Bedrock region |
| `AWS_SESSION_TOKEN` | - | Bedrock temporary credentials |

```bash
# Together, via the compat slot
DELOS_COMPAT_NAME=together
DELOS_COMPAT_BASE_URL=https://api.together.xyz/v1
DELOS_COMPAT_API_KEY=...
# then: "model": "together/meta-llama/Llama-3-70b-chat-hf"
```

See [docs/providers.md](../providers.md) for the per-provider feature matrix.

### Authentication (virtual keys)

With no database configured the gateway runs in **dev mode**: any
`Authorization: Bearer ...` or `x-api-key` value is accepted (`delos-dev` in the
examples is only a convention) and nothing is metered. The gateway says so at
startup:

```
dev mode: no database configured, virtual keys are NOT enforced - any api_key ... is accepted
```

Set `DELOS_STORAGE_BACKEND=postgres` (plus the `DELOS_DB_*` settings) and every
`/v1` request must then present a valid virtual key, scoped to models and within
its monthly token/dollar budget:

```bash
delos key create web-prod --usd-budget 250 --models 'openai/*'
delos key list
delos key revoke <id>          # the id from `delos key list`
```

Keys are stored as argon2id hashes and printed exactly once, at creation. Do not
expose the gateway on an untrusted network without keys enabled and TLS in front
of it - see [deploy.md](../deploy.md).

## Control Plane (`delos serve`, port 8081)

Runs observe, prompt, datasets, eval, and deploy in one process: gRPC over h2c
plus `GET /healthz` on the same port.

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_PORT` | `8081` | Listen port |
| `DELOS_STORAGE_BACKEND` | `memory` | `memory` (ephemeral) or `postgres` |
| `DELOS_GATEWAY_URL` | `http://localhost:8080` | Gateway base URL used for eval completions. `http://gateway:8080` in Docker Compose. |
| `DELOS_GATEWAY_API_KEY` | - | Bearer token the eval runner presents to the gateway |

### Database

Used when `DELOS_STORAGE_BACKEND=postgres`. Migrations are applied at startup -
there is no separate migrate step.

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_DB_HOST` | `localhost` | PostgreSQL host (`postgres` in Docker Compose) |
| `DELOS_DB_PORT` | `5432` | PostgreSQL port |
| `DELOS_DB_USER` | `delos` | PostgreSQL username |
| `DELOS_DB_PASSWORD` | - | PostgreSQL password |
| `DELOS_DB_NAME` | `delos` | PostgreSQL database name |
| `DELOS_DB_SSLMODE` | `disable` | SSL mode (disable, require, verify-full) |

## CLI (`delos <subcommand>`)

Every subcommand other than `serve` is a client.

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_CONTROL_PLANE_ADDR` | `localhost:8081` | gRPC target for the control plane (host:port, no scheme) |
| `DELOS_GATEWAY_URL` | `http://localhost:8080` | Gateway base URL used by `delos gateway` |
| `DELOS_FORMAT` | `table` | Output format (table, json, yaml). Overridden by `-o/--output`. |
| `DELOS_VERBOSE` | `false` | Verbose output. Overridden by `-v/--verbose`. |

## Tracing

The gateway emits one OTLP span per request using `gen_ai.*` semantic
conventions. Export is off until you give it an endpoint or ask for stdout.

| Variable | File key | Default | Description |
|----------|----------|---------|-------------|
| `DELOS_OTLP_ENDPOINT` | `telemetry.otlp_endpoint` | - | Where spans go. Any OTLP backend - the gateway never requires the control plane. |
| `DELOS_OTLP_PROTOCOL` | `telemetry.otlp_protocol` | `http` | `http` (endpoint is a URL) or `grpc` (endpoint is host:port) |
| `DELOS_TRACE_STDOUT` | `telemetry.stdout` | `false` | Print spans to stdout (dev) |
| `DELOS_TRACING_SAMPLING` | `telemetry.sampling` | `1.0` | Head sampling fraction, 0.0-1.0 |
| `DELOS_TRACE_CONTENT` | `telemetry.trace_content` | `false` | Capture prompts and completions as span attributes. Off by default: this puts user content in your traces. |
| `DELOS_TRACE_REDACT` | `telemetry.trace_redact` | - | Comma-separated regexes applied to captured content before export |

The `observability` Compose profile starts Jaeger with an OTLP collector on
`:4317` and a UI on `http://localhost:16686`. The control plane hosts the
observe module itself and does not export traces over the network.

See [otel.md](../otel.md) for pointing exporters at Jaeger, Grafana Tempo or
Datadog.

## Example .env File

```bash
# General
DELOS_ENV=development
DELOS_LOG_LEVEL=debug
DELOS_LOG_FORMAT=text

# Gateway providers (real keys go in .env.local)
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
GEMINI_API_KEY=AIza...
# DELOS_GATEWAY_PORT=8080

# Control plane
DELOS_PORT=8081
DELOS_GATEWAY_URL=http://gateway:8080
DELOS_STORAGE_BACKEND=postgres
DELOS_DB_HOST=postgres
DELOS_DB_PORT=5432
DELOS_DB_USER=delos
DELOS_DB_PASSWORD=delos
DELOS_DB_NAME=delos
DELOS_DB_SSLMODE=disable

# CLI
# DELOS_CONTROL_PLANE_ADDR=localhost:8081

# Tracing (export is off until an endpoint is set)
DELOS_TRACING_SAMPLING=1.0
# DELOS_OTLP_ENDPOINT=http://jaeger:4318
# DELOS_OTLP_PROTOCOL=http
# DELOS_TRACE_STDOUT=true
```

## Docker Compose Overrides

Docker Compose reads `deploy/local/.env` and then `deploy/local/.env.local`, so
put local overrides and every secret in the latter:

```bash
# deploy/local/.env.local - never committed
OPENAI_API_KEY=sk-your-actual-key
ANTHROPIC_API_KEY=sk-ant-your-actual-key
```

When you run the binaries locally instead of in Docker, switch the two hostnames
that differ:

```bash
DELOS_GATEWAY_URL=http://localhost:8080
DELOS_DB_HOST=localhost
```
