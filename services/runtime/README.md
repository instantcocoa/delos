# services/runtime

The Go package behind **`delos-gateway`**, the Delos data plane. It is a
library, not a process: `cmd/delos-gateway/main.go` reads the environment,
registers providers, and mounts `runtime.NewHTTPServer` on port **8080**. The
gateway is stateless and knows nothing about the control plane.

## Public surface

`runtime.NewHTTPServer` serves plain HTTP (there is no proto/gRPC API here):

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible completions, SSE streaming |
| `POST /v1/embeddings` | Embeddings |
| `GET /v1/models`, `GET /v1/models/{model}` | Model discovery |
| `POST /v1/messages` | Anthropic-compatible messages |
| `GET /v1/events` | Live request stream (SSE) — what `delos tail` reads |
| `GET /healthz` | Health and configured provider count |

Both surfaces reach every provider: an Anthropic SDK can call `bedrock/...`
and an OpenAI SDK can call `anthropic/...`.

## What lives here

| Area | Files |
|------|-------|
| Provider abstraction + registry | `provider.go`, `provider_openai.go`, `provider_anthropic.go`, `provider_gemini.go`, `provider_bedrock.go` (OpenAI-compatible endpoints reuse the OpenAI client) |
| HTTP surfaces | `httpapi.go`, `openai_api.go`, `anthropic_api.go` |
| Routing, retry, circuit breaker | `failover.go` |
| Response cache (in-process LRU or Redis) | `cache.go` |
| Virtual keys, budgets, auth | `keys.go`, `keys_postgres.go`, `auth.go` |
| `gen_ai.*` spans and redaction | `telemetry.go` |
| Live request stream | `tail.go` |
| Structured errors | `errors.go` |

Providers are the curated set — OpenAI, Anthropic, Gemini, Bedrock, plus any
OpenAI-compatible endpoint (Ollama, vLLM, SGLang, Together, OpenRouter, LM
Studio). See [docs/providers.md](../../docs/providers.md) for the feature
matrix and what explicitly does *not* work.

## Storage

Almost nothing. The gateway holds no domain state; the one exception is
**virtual keys**, which are Postgres-backed via `NewPostgresKeyStore` with the
schema in `migrations/` (applied by the gateway at startup under the `gateway`
schema). With no database configured the gateway runs in dev mode: keys are
not enforced and any `api_key` is accepted. The response cache is an
in-process LRU unless `DELOS_REDIS_URL` points at Redis, and it is a cache,
not storage — losing it costs latency, not data.

## Tests

```bash
go test ./services/runtime/...
```

Everything here is unit-testable without external dependencies; provider tests
use recorded/mocked HTTP. Live-provider and Ollama tests live in `tests/` and
skip when the dependency is unavailable — see [TESTING.md](../../TESTING.md).

## See also

- [README.md](../../README.md) — what the gateway is and the 60-second start
- [docs/providers.md](../../docs/providers.md) — provider setup + feature matrix
- [docs/api-reference/runtime.md](../../docs/api-reference/runtime.md) — HTTP API reference
- [docs/otel.md](../../docs/otel.md) — `gen_ai.*` spans and OTLP export
- [docs/getting-started/configuration.md](../../docs/getting-started/configuration.md) — every environment variable
