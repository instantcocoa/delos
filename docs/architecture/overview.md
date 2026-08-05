# Architecture Overview

Delos is two binaries plus one PostgreSQL database: a **data plane** that serves
LLM traffic and a **control plane** that manages prompts, datasets, evaluations,
deployments, and traces.

## System Diagram

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Client Applications                          │
│        (any OpenAI- or Anthropic-compatible client, curl)           │
└─────────────────────────────────────────────────────────────────────┘
                                    │  HTTP
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│  DATA PLANE - delos-gateway  :8080                                  │
│  POST /v1/chat/completions   POST /v1/embeddings                    │
│  GET  /v1/models  /v1/models/{model}                                │
│  POST /v1/messages (Anthropic)          GET /v1/events (SSE)        │
│  GET  /healthz                                                      │
│  No control-plane dependency. Postgres only to enforce keys.        │
└─────────────────────────────────────────────────────────────────────┘
                                    │
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│                           LLM Providers                             │
│  OpenAI │ Anthropic │ Gemini │ Bedrock │ Ollama │                   │
│  any OpenAI-compatible endpoint (vLLM, SGLang, …)                   │
└─────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────┐
│                     delos CLI  /  gRPC clients                      │
└─────────────────────────────────────────────────────────────────────┘
                                    │  gRPC (h2c)
                                    ▼
┌─────────────────────────────────────────────────────────────────────┐
│  CONTROL PLANE - delos serve  :8081                                 │
│  observe │ prompt │ datasets │ eval │ gates    (one process)        │
│  GET /healthz                                                       │
└─────────────────────────────────────────────────────────────────────┘
        │                                        │
        │ DELOS_GATEWAY_URL (HTTP)               │ SQL
        ▼                                        ▼
   delos-gateway :8080                      PostgreSQL :5432
```

## Data Plane: `delos-gateway`

The gateway is the only component your application talks to. It is a stateless
HTTP server on port **8080** (`DELOS_GATEWAY_PORT`) with no database and no
knowledge that a control plane exists.

**Endpoints**

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible completions, streaming supported |
| `POST /v1/embeddings` | Embeddings |
| `GET /v1/models` | List models across configured providers |
| `GET /v1/models/{model}` | Look up a single model |
| `POST /v1/messages` | Anthropic-compatible messages |
| `GET /v1/events` | Live request stream (SSE) — what `delos tail` reads |
| `GET /healthz` | Status plus the number of configured providers |

**Providers** are registered at startup, one per credential found in the
environment: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY` (or
`GOOGLE_API_KEY`), AWS credentials (or `AWS_PROFILE`) plus `AWS_REGION` for
Bedrock, `DELOS_RUNTIME_OLLAMA_ENABLED=true` / `DELOS_RUNTIME_OLLAMA_URL` for
Ollama, and `DELOS_COMPAT_BASE_URL` for any other OpenAI-compatible endpoint.
The older `DELOS_RUNTIME_<PROVIDER>_KEY` names still work as fallbacks. See
[docs/providers.md](../providers.md).

**Model routing.** A model can be addressed as `provider/model` (for example
`openai/gpt-4o`) to force a provider; otherwise the gateway matches the model
name against each provider's listing. Route aliases (`gateway.routes` /
`DELOS_ROUTES`) map one name onto an ordered fallback chain, with jittered
retry and a per-provider circuit breaker.

**Virtual keys** are the one piece of state the gateway keeps. When Postgres is
configured, every `/v1` request must present a `dk_...` key scoped to models and
within its monthly budget; with no database the gateway runs in dev mode and
enforces nothing.

Because the gateway is stateless, it scales horizontally behind any load
balancer and keeps serving traffic when the control plane is down.

## Control Plane: `delos serve`

The `delos` binary is both the control-plane server and the CLI. `delos serve`
listens on port **8081** (`DELOS_PORT`) and hosts five modules in a single
process:

| Module | Purpose |
|--------|---------|
| **observe** | Trace ingestion, metric aggregation, query API |
| **prompt** | Git-like prompt versioning, history, semantic diff, template variables |
| **datasets** | Test datasets, examples (input/expected output), dataset-to-prompt links |
| **eval** | Evaluators (exact match, contains, regex, JSON schema), eval runs, run comparison |
| **deploy** | Quality gates only: threshold conditions over the latest eval run, evaluated into a pass/fail verdict for CI. Delos does not deploy, roll out or roll back anything. |

Calls between modules are direct Go function calls, not network hops. The port
serves gRPC over h2c (HTTP/2 without TLS) alongside `GET /healthz` on HTTP/1.

**Storage** is selected by `DELOS_STORAGE_BACKEND`: `memory` (the default,
ephemeral) or `postgres` with `DELOS_DB_HOST`, `DELOS_DB_PORT`, `DELOS_DB_USER`,
`DELOS_DB_PASSWORD`, `DELOS_DB_NAME`, and `DELOS_DB_SSLMODE`. Migrations are
applied at startup; there is no separate migrate step. Today prompt and observe
are wired to the Postgres store (as are the gateway's virtual keys, in the
separate `gateway` schema) - datasets, eval, and deploy still use in-memory
stores.

**Reaching the gateway.** The control plane never embeds provider credentials.
When it needs a completion or an embedding it calls the gateway's public
OpenAI-compatible API at `DELOS_GATEWAY_URL` (`http://gateway:8080` in Docker
Compose, `http://localhost:8080` locally), optionally authenticating with
`DELOS_GATEWAY_API_KEY`.

## CLI

Every `delos` subcommand other than `serve` is a client:

- `delos prompt|datasets|eval|gate|observe` dial the control plane over gRPC at
  `DELOS_CONTROL_PLANE_ADDR` (default `localhost:8081`). `delos deploy` is an
  alias for `delos gate`.
- `delos key` manages gateway virtual keys directly in Postgres via the
  `DELOS_DB_*` settings — it contacts neither server.
- `delos gateway` (alias `delos runtime`) and `delos tail` drive the gateway's
  HTTP API at `DELOS_GATEWAY_URL` (default `http://localhost:8080`).
- `delos config validate|doctor` check configuration, the latter by probing what
  it points at.

## Data Flow

### Completion Request Flow

```
1. Client POSTs /v1/chat/completions to delos-gateway
2. Gateway assigns a request ID and resolves the model to a provider
   (explicit "provider/model" prefix wins, then exact model listing,
    then well-known model name prefixes)
3. Gateway translates the request to the provider's API and calls it
4. Response is translated back to the OpenAI shape, with usage and cost;
   streaming responses are relayed as SSE frames
5. A trace is emitted if tracing is enabled
```

The control plane is not involved.

### Evaluation Flow

```
1. Client creates an EvalRun via the control plane (CLI or gRPC)
2. The eval runner picks up the run
3. It fetches the prompt and dataset examples in-process (direct calls
   into the prompt and datasets modules)
4. For each example it calls delos-gateway at DELOS_GATEWAY_URL
   (POST /v1/chat/completions, POST /v1/embeddings) for a completion
5. Evaluators score the output against the expected value
6. Results are aggregated and stored
```

### Quality Gate Flow

```
1. A gate is defined once: a prompt plus threshold conditions
   (delos gate create nightly --prompt summarizer
    --condition 'overall_score>=0.8')
2. CI runs `delos gate check nightly` (or GETs
   /v1/gates/nightly/verdict on :8081)
3. The gate reads the latest completed eval run for that prompt,
   in-process, and compares each condition
4. A verdict comes back with pass/fail and a reason per condition;
   the CLI exits 0 or 1 and CI acts on it
```

Delos stops at the verdict. It does not promote, roll out, or roll back
anything.

## Technology Stack

| Component | Technology |
|-----------|------------|
| Language | Go 1.25+ |
| Gateway API | HTTP (OpenAI- and Anthropic-compatible), SSE for streaming |
| Control-plane API | gRPC over h2c + Protocol Buffers |
| Database | PostgreSQL 15+ |
| Tracing | OpenTelemetry (OTLP) |
| Clients | Any OpenAI/Anthropic-compatible client; `delos` CLI |

## Code Layout

The `services/<name>/` directories are **libraries**, not processes. They are
linked into the binaries by `controlplane/controlplane.go` and `cmd/`:

```
cmd/delos/            # control plane + CLI binary
cmd/delos-gateway/    # data plane binary
controlplane/         # wires observe, prompt, datasets, eval, deploy together
services/<name>/
├── handler.go        # gRPC handlers (control-plane modules)
├── store.go          # storage interface + memory/postgres implementations
├── migrations/       # embedded SQL migrations
└── <name>.go         # types + business logic
```

`services/runtime/` is the exception: it holds the provider implementations and
the gateway's HTTP API (`httpapi.go`, `openai_api.go`, `anthropic_api.go`), and
is linked into `delos-gateway` rather than the control plane.

## Deployment

`deploy/local/docker-compose.yaml` starts three containers by default:
PostgreSQL, `gateway` on 8080, and `delos` on 8081. Optional profiles, none on by
default, add Ollama (`ollama`, 11434), Jaeger (`observability`, UI on 16686), and
LocalStack (`test`, 4566). Images are built from the root `Dockerfile.gateway`
and `Dockerfile.delos`.

## Scalability

- The gateway is stateless: run as many replicas as you need behind an HTTP load
  balancer.
- The control plane is stateless too; state lives in PostgreSQL.
- Connection pooling for the database is configured at startup.

## Security

- Provider API keys live only in the gateway's environment, never in the control
  plane or in configuration files.
- The control plane authenticates to the gateway with `DELOS_GATEWAY_API_KEY`
  when set.
- Local Docker Compose runs gRPC without TLS (h2c); terminate TLS at a proxy in
  production.
- Role-based access control is planned.
