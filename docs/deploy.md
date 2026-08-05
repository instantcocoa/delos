# Deploying Delos

Production shape: **N stateless gateways behind a load balancer, one control
plane, one PostgreSQL.** Redis is optional and only makes the response cache
shared. There is nothing else to run.

```
                  ┌──────────────┐
   clients ──TLS─►│ load balancer│──h1/h2──► delos-gateway ×N   :8080  (stateless)
                  └──────────────┘                 │
                                                   ├──► provider APIs
                                                   ├──► Redis (optional, shared cache)
                                                   └──► PostgreSQL (virtual keys + usage)

   CI / operators ──TLS──► LB ──h2c──► delos serve ×1  :8081 ──► PostgreSQL
```

The gateway never calls the control plane. If the control plane is down, traffic
keeps flowing; you lose evals, gates and the trace UI, not completions.

## TLS

**Terminate TLS at the load balancer.** Neither binary serves TLS itself, on
purpose — certificate rotation belongs to infrastructure you already operate
(ALB/NLB, nginx, Caddy, Envoy, an ingress controller).

- **Gateway (8080)** is ordinary HTTP/1.1 + HTTP/2. Any HTTP proxy works.
  Two things must be right for streaming (SSE): **disable response buffering**
  and **raise the read timeout above your longest completion** — nginx
  `proxy_buffering off` (the `/v1/events` stream also sends
  `X-Accel-Buffering: no`), Envoy `stream_idle_timeout`, ALB idle timeout ≥ 300s.
  A buffering proxy turns streaming into one delayed blob, which looks like a
  Delos bug and is not.
- **Control plane (8081)** speaks **gRPC over h2c** (HTTP/2 cleartext) *and*
  plain HTTP `/healthz` on the same port. Your proxy must forward HTTP/2 without
  requiring TLS on the backend leg: nginx `grpc_pass grpc://…`, Envoy with an
  explicit `http2_protocol_options`, ALB with protocol version `gRPC`. A proxy
  that downgrades to HTTP/1.1 breaks gRPC — that is the single most common
  deployment failure here.

Between the LB and the binaries, a private network is the expected boundary.
Neither port should ever be internet-facing without an authenticating proxy in
front — and on the gateway, without virtual keys enabled.

## PostgreSQL

One database serves both planes; the gateway uses it only for virtual keys and
monthly usage counters, the control plane for prompts, datasets, evals and
gates. Both apply their own migrations at startup — there is no migrate step.

Sizing, honestly, is dominated by the control plane's span/eval writes, not by
the gateway:

| Scale | Guidance |
|-------|----------|
| Getting started | 2 vCPU / 4 GB, 20 GB disk. Fine for millions of requests a month. |
| Steady production | 4 vCPU / 16 GB, SSD, `shared_buffers` ≈ 25% RAM. |
| Connections | The control plane opens up to 25 connections; each gateway up to 10. Budget `25 + 10×gateways + headroom`, and put PgBouncer (transaction pooling) in front once that exceeds ~200. |
| Growth | Trace/eval history is the only unbounded table family. Set a retention job before it matters. If Postgres ingest becomes the bottleneck, the answer is documented ClickHouse support later — not a custom store. |

Use `sslmode: require` (or stricter) for anything not on a private subnet, and
give the two planes the same role only if you are comfortable sharing it; the
schemas are independent.

## High availability

**Gateways: scale horizontally.** They hold no state. Run 2+ behind the LB,
health-check `GET /healthz` (returns `{"status":"ok","providers":N}`), and note
that `providers: 0` means credentials are missing — a gateway with no providers
is healthy but useless, so alert on that value, not just on 200.

Per-gateway state you should know about:

- **Response cache** — in-process LRU by default, so each replica warms its own.
  Set `gateway.cache.redis_url` to share one cache across replicas.
- **Circuit breakers** — per-process. A provider outage trips each replica
  independently, which is fine and slightly slower to converge.
- **`/v1/events`** — per-process. `delos tail` through a load balancer shows one
  replica's traffic. Tail a specific pod when debugging.
- **Budgets** — enforced against Postgres, so they are global across replicas.
  They are checked before the request and recorded after, so a burst can
  overshoot a budget slightly; they are a control, not a hard cap.

**Control plane: run one.** It is stateless (all state is in Postgres), so a
second replica is safe, but nothing today requires one — gates and evals are not
latency-critical. If you do run two, put them behind the same LB and expect
duplicated eval-runner work.

## Zero-downtime deploys

Both binaries handle SIGTERM by refusing new connections and letting in-flight
requests finish (30 s budget), so a rolling update loses nothing if you:

1. Set the pod/task **termination grace period above 30 s** (60 s is a good
   default; streaming responses can run long).
2. Remove the instance from the LB **before** SIGTERM (`preStop` sleep of 5–10 s,
   or connection draining on the target group).
3. Set the LB's deregistration delay ≥ your longest completion.

Migrations are additive and run at startup, so an old and a new version can
overlap during a rollout. Roll the gateways first, then the control plane.

## Environment variables

Everything below can also live in `delos.yaml` (see
[delos.yaml.example](../delos.yaml.example)) except the secrets, which are
environment-only. Precedence: **environment variable > file > default**.

### `delos-gateway` (data plane)

| Variable | Default | Purpose |
|----------|---------|---------|
| `DELOS_CONFIG` | `./delos.yaml` if present | Path to the config file |
| `DELOS_GATEWAY_PORT` | `8080` | HTTP listen port |
| `DELOS_ENV` | `development` | `development` / `staging` / `production` |
| `DELOS_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DELOS_LOG_FORMAT` | `json` | `json` or `text` |
| `DELOS_STORAGE_BACKEND` | `memory` | `postgres` enables virtual-key enforcement |
| `DELOS_DB_HOST` / `DELOS_DB_PORT` | `localhost` / `5432` | Key store |
| `DELOS_DB_USER` / `DELOS_DB_NAME` | `delos` / `delos` | Key store |
| `DELOS_DB_PASSWORD` | *(empty)* | **Secret — environment only** |
| `DELOS_DB_SSLMODE` | `disable` | `require` and stricter supported |
| `DELOS_REDIS_URL` | *(empty)* | Shared response cache; empty = in-process LRU |
| `DELOS_CACHE` | *(on)* | `off` disables response caching |
| `DELOS_CACHE_TTL` | `5m` | Cache entry lifetime |
| `DELOS_REQUEST_TIMEOUT` | `5m` | End-to-end budget including retries and fallbacks |
| `DELOS_ROUTES` | *(none)* | JSON alias → fallback chain; overrides `gateway.routes` |
| `DELOS_TAIL` | *(on)* | `off` disables `GET /v1/events` |
| `DELOS_TAIL_BUFFER` | `200` | Events retained for `delos tail --replay` |
| `DELOS_OTLP_ENDPOINT` | *(empty)* | Where spans go; empty = no export |
| `DELOS_OTLP_PROTOCOL` | `http` | `http` (URL endpoint) or `grpc` (host:port) |
| `DELOS_TRACE_STDOUT` | `false` | Print spans to stdout |
| `DELOS_TRACING_SAMPLING` | `1.0` | Head sampling fraction |
| `DELOS_TRACE_CONTENT` | `false` | Capture prompts/completions in spans |
| `DELOS_TRACE_REDACT` | *(empty)* | Comma-separated regexes applied before export |

Provider credentials, all **secrets, environment only** — one provider is
registered per credential found:

| Variable | Provider |
|----------|----------|
| `OPENAI_API_KEY` | OpenAI |
| `ANTHROPIC_API_KEY` | Anthropic |
| `GEMINI_API_KEY` / `GOOGLE_API_KEY` | Google Gemini |
| `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` (or `AWS_PROFILE`), `AWS_REGION`, `AWS_SESSION_TOKEN` | AWS Bedrock |
| `DELOS_RUNTIME_OLLAMA_ENABLED=true`, `DELOS_RUNTIME_OLLAMA_URL` | Ollama |
| `DELOS_COMPAT_BASE_URL`, `DELOS_COMPAT_NAME`, `DELOS_COMPAT_API_KEY` | Any OpenAI-compatible endpoint (vLLM, SGLang, Together, OpenRouter, …) |

The legacy `DELOS_RUNTIME_<PROVIDER>_KEY` names still work as fallbacks. See
[providers.md](providers.md) for the per-provider feature matrix.

### `delos serve` (control plane)

| Variable | Default | Purpose |
|----------|---------|---------|
| `DELOS_CONFIG` | `./delos.yaml` if present | Path to the config file |
| `DELOS_PORT` | `8081` | gRPC (h2c) + `/healthz` listen port |
| `DELOS_ENV` | `development` | Environment name |
| `DELOS_LOG_LEVEL` / `DELOS_LOG_FORMAT` | `info` / `json` | Logging |
| `DELOS_STORAGE_BACKEND` | `memory` | Use `postgres` in production — `memory` loses everything on restart |
| `DELOS_DB_HOST` / `DELOS_DB_PORT` | `localhost` / `5432` | Database |
| `DELOS_DB_USER` / `DELOS_DB_NAME` | `delos` / `delos` | Database |
| `DELOS_DB_PASSWORD` | *(empty)* | **Secret — environment only** |
| `DELOS_DB_SSLMODE` | `disable` | TLS mode |
| `DELOS_GATEWAY_URL` | `http://localhost:8080` | How the eval runner reaches the gateway |
| `DELOS_GATEWAY_API_KEY` | *(empty)* | **Secret** — virtual key used by the eval runner |

### CLI

| Variable | Default | Purpose |
|----------|---------|---------|
| `DELOS_CONTROL_PLANE_ADDR` | `localhost:8081` | gRPC target |
| `DELOS_GATEWAY_URL` | `http://localhost:8080` | Gateway base URL |
| `DELOS_API_KEY` | *(empty)* | Virtual key for `delos tail` and gateway calls |
| `DELOS_FORMAT` | `table` | Default `-o` value |

## Checklist

Before calling it production:

```bash
delos config validate delos.yaml   # the file is well-formed
delos config doctor                # the things it points at actually answer
```

- [ ] `storage: postgres` on both planes, with a password from a secret store
- [ ] Virtual keys created; dev mode is off (the gateway no longer logs
      `virtual keys are NOT enforced`)
- [ ] TLS terminated at the LB; the control plane's leg forwards HTTP/2
- [ ] Health checks on `/healthz`, alerting on `providers: 0`
- [ ] `telemetry.otlp_endpoint` set — see [otel.md](otel.md)
- [ ] `trace_content` left **off**, or `trace_redact` rules reviewed by whoever
      owns your privacy policy
- [ ] Termination grace period > 30 s, deregistration delay ≥ longest completion
- [ ] A retention plan for trace and eval history
