# Delos

**A self-hosted LLM gateway with batteries-included observability, in two Go binaries.**

Point your existing OpenAI or Anthropic client at Delos and get routing, failover,
caching, virtual keys, budgets and full `gen_ai.*` tracing — without deploying a
Python proxy, a ClickHouse cluster and a separate observability vendor.

```bash
docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... ghcr.io/instantcocoa/delos-gateway
```

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
print(client.chat.completions.create(
    model="gpt-4o",
    messages=[{"role": "user", "content": "Hello!"}],
).choices[0].message.content)
```

That is the whole setup. No config file, no signup, no database. With no config
the gateway runs in **dev mode**: any `api_key` value is accepted (`delos-dev` is
just a convention), model names route straight through to whichever providers
have keys in the environment, the cache is in-process, and nothing is metered.

Using the Anthropic SDK instead? Swap the same way:

```python
import anthropic

client = anthropic.Anthropic(base_url="http://localhost:8080", api_key="delos-dev")
```

## What you get

- **Routing and failover** — model aliases with ordered fallback chains
  (`gpt-4` → `openai/gpt-4o` → `anthropic/claude-sonnet-4-5`), jittered retry on
  429/5xx, a circuit breaker per provider, and one end-to-end timeout budget.
- **Response caching** — exact-match on (model, normalized messages, params).
  In-process by default; point several gateways at one Redis to share it.
- **Virtual keys and budgets** — argon2id-hashed keys, scoped to models, with
  monthly token and dollar budgets. Over-budget requests are refused with a
  structured 429 before any provider is contacted.
- **`gen_ai.*` tracing, OTLP out** — one span per request in the OpenTelemetry
  GenAI semantic conventions. Prompt/completion capture is off by default and
  redactable when on. Export to Jaeger, Grafana, Datadog, or the Delos control
  plane; the gateway never needs the control plane to run.
- **`delos tail`** — every request through the gateway, live in your terminal:
  model, provider, latency, tokens, cost, cache hit, key.
- **Honest errors** — the provider's own error text, a stable machine-readable
  code, and the request ID that finds the request in your traces.

```
$ delos tail
TIME      STATUS  MODEL       PROVIDER   LATENCY  TOKENS   COST      CACHE  KEY
14:22:03  200     gpt-4o      openai       1.2s   842->96  $0.00131  -      web-prod
14:22:04  200     gpt-4o      openai      188us   842->96  $0.00131  hit    web-prod
14:22:07  429     gpt-4o      -             2ms   -        -         -      batch  budget_exceeded: monthly dollar budget exhausted for key "batch"
```

## 60 seconds, locally

```bash
# Both binaries, no Docker
go install github.com/instantcocoa/delos/cmd/delos-gateway@latest
go install github.com/instantcocoa/delos/cmd/delos@latest

OPENAI_API_KEY=sk-... delos-gateway          # data plane  :8080
delos tail                                   # watch it, in another terminal
```

From a clone, with the control plane and Postgres too:

```bash
git clone https://github.com/instantcocoa/delos.git && cd delos
cp deploy/local/.env.example deploy/local/.env   # keys go in .env.local (gitignored)
make up          # PostgreSQL - the only dependency
make run-all     # delos-gateway :8080 and delos serve :8081 in the background
make stop-all    # ...and stop them
```

Or the whole stack in Docker — exactly three containers:

```bash
make up-all      # postgres + gateway + control plane
```

Check that it all works:

```bash
delos config doctor
```

## Architecture

Two binaries and PostgreSQL. That is the entire system.

```
                                    ┌─────────────────────────────────────┐
your app ──── base_url swap ───────►│  delos-gateway            :8080     │
(openai / anthropic SDK, curl)      │  data plane, stateless, no database │
                                    │  routing · failover · cache · keys  │
                                    └──────┬───────────────────┬──────────┘
                                           │                   │
                                           │ provider APIs     │ OTLP gen_ai.* spans
                                           ▼                   ▼
                          OpenAI · Anthropic · Gemini    any OTLP backend
                          Bedrock · vLLM/Ollama/...      (Jaeger, Grafana, Datadog,
                                                          or delos serve)
                                    ┌─────────────────────────────────────┐
delos CLI ──── gRPC ───────────────►│  delos serve              :8081     │
(prompt, eval, gate, datasets,      │  control plane: observe · prompt ·  │
 observe, key)                      │  datasets · eval · deploy gates     │
                                    └──────┬──────────────────────────────┘
                                           │
                                           ▼
                                      PostgreSQL
```

The two planes are independent. The gateway serves traffic whether or not the
control plane is running, and knows nothing about it. The control plane reaches
the gateway only through the gateway's public OpenAI-compatible API
(`DELOS_GATEWAY_URL`), the same way your application does.

| Binary | Role | Port | State |
|--------|------|------|-------|
| `delos-gateway` | Data plane: OpenAI/Anthropic-compatible LLM gateway | 8080 | Stateless, no database |
| `delos` | Control plane (`delos serve`) **and** the CLI | 8081 | PostgreSQL (or in-memory) |

Gateway endpoints:

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible completions (streaming supported) |
| `POST /v1/embeddings` | Embeddings |
| `GET /v1/models`, `GET /v1/models/{model}` | Model discovery |
| `POST /v1/messages` | Anthropic-compatible messages |
| `GET /v1/events` | Live request stream (SSE) — what `delos tail` reads |
| `GET /healthz` | Health and provider count |

## Configuration

Every knob has a default; the config file is for production, not for hello-world.
Precedence is **environment variable > `delos.yaml` > default**. The file is found
at `$DELOS_CONFIG` or `./delos.yaml`.

```bash
# docs-test
cp delos.yaml.example delos.yaml
delos config validate delos.yaml
```

Secrets never go in the file: provider API keys and `DELOS_DB_PASSWORD` are read
from the environment only, so `delos.yaml` is safe to commit.
[delos.yaml.example](delos.yaml.example) documents every option;
[docs/getting-started/configuration.md](docs/getting-started/configuration.md)
has the full environment-variable reference.

## Docs

| | |
|---|---|
| [GETTING_STARTED.md](GETTING_STARTED.md) | First request, virtual keys, prompts, evals, gates |
| [docs/providers.md](docs/providers.md) | Per-provider setup and the feature matrix — including what does *not* work |
| [docs/otel.md](docs/otel.md) | Pointing traces at Jaeger, Grafana Tempo, or Datadog |
| [docs/deploy.md](docs/deploy.md) | Production: TLS, Postgres sizing, HA behind a load balancer |
| [TROUBLESHOOTING.md](TROUBLESHOOTING.md) | When something is wrong |
| [docs/architecture/overview.md](docs/architecture/overview.md) | How the pieces fit |

Documentation code blocks marked `# docs-test` are extracted and executed in CI
(`make docs-test`), so stale docs fail the build.

## Performance

The claim we intend to stand behind: **p99 gateway overhead under 5 ms** against a
mock provider, with no goroutine or file-descriptor leaks over a 10-minute soak.

That number is **not published yet**, and this section stays a placeholder until
it is. The gate will live in `make bench` — one command, a mock provider, run in
CI on every change — so that whatever appears here is reproducible on hardware
named next to it. A benchmark you cannot re-run is marketing; we would rather
ship a blank.

## Project status

Pre-1.0 and honest about it:

- **Working**: both binaries, the OpenAI and Anthropic surfaces (streaming, tool
  calls, vision), OpenAI/Anthropic/Gemini/Bedrock/OpenAI-compatible providers,
  routing and failover, caching, virtual keys with budgets, `gen_ai.*` spans,
  `delos tail`, prompts, datasets, evals and CI gates.
- **In progress**: the provider conformance suite and its nightly re-record, the
  published benchmark number, observe's UI.
- **Not planned**: 100-provider coverage, a client SDK in any language
  (OpenAI-compatibility over HTTP *is* the SDK), and anything that deploys your
  application for you — Delos emits verdicts, your CI acts on them.

Expect rough edges outside the paths above. Bug reports with a request ID are
worth their weight.

## Versioning

SemVer. The **compatibility contract** is:

1. the OpenAI-compatible and Anthropic-compatible HTTP surfaces, and
2. the `delos.yaml` schema.

Breaking either requires a major version and a written migration note. Everything
else — the internal Go packages under `services/`, `pkg/` and `controlplane/` —
carries **no stability promise**; import them at your own risk. That freedom to
refactor is deliberate.

## Development

```bash
make help          # every target
make build         # bin/delos and bin/delos-gateway
make test          # tests (starts postgres/localstack)
make docs-test     # execute the marked documentation blocks
make lint          # Go and proto linters
make proto         # regenerate from proto/
```

Releases ship cosign-signed binaries plus an SBOM for linux and darwin on amd64
and arm64, built with `CGO_ENABLED=0`. See [.goreleaser.yaml](.goreleaser.yaml).

## License

MIT
