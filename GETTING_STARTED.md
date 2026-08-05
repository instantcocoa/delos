# Getting Started with Delos Development

This guide walks you through setting up a local development environment for Delos.

## Prerequisites

- **Go 1.25+** - [Install Go](https://go.dev/doc/install)
- **Docker & Docker Compose** - [Install Docker](https://docs.docker.com/get-docker/)
- **Buf** (optional, for proto changes) - `go install github.com/bufbuild/buf/cmd/buf@latest`

Verify your setup:
```bash
go version      # Should show 1.25+
docker --version
docker compose version
```

## Architecture

Delos is **two binaries plus PostgreSQL**:

| Binary | Role | Port | State |
|--------|------|------|-------|
| `delos-gateway` | Data plane - OpenAI/Anthropic-compatible LLM gateway | 8080 | Stateless, no database |
| `delos` | Control plane + CLI - prompts, datasets, evals, quality gates, traces | 8081 (`delos serve`) | PostgreSQL (or in-memory) |

```
your app ──► delos-gateway :8080 ──► OpenAI / Anthropic / Gemini / Bedrock /
                  ▲                  Ollama / any OpenAI-compatible endpoint
                  │ DELOS_GATEWAY_URL
                  │
delos CLI ──► delos serve :8081 ──► PostgreSQL
              (observe, prompt, datasets, eval, gates)
```

There is no startup ordering to worry about. The gateway is completely
standalone - it needs no database and no control plane. The control plane only
calls the gateway when it needs completions (for example while running an
evaluation), and it applies its own database migrations at startup, so there is
no separate migration step.

## Clone and Configure

```bash
git clone https://github.com/instantcocoa/delos.git
cd delos

# Base config for Docker Compose (safe to check in)
cp deploy/local/.env.example deploy/local/.env
```

Put your provider API keys in `deploy/local/.env.local` (gitignored, never
commit keys):

```bash
# deploy/local/.env.local
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
```

The gateway registers one provider for each key it finds. Without any key it
still starts, logs a warning, and serves health checks - completions will fail
until a provider is configured.

## Option 1: Everything in Docker

```bash
# postgres + gateway (8080) + control plane (8081) - three containers
make up-all

# Watch them come up
docker compose -f deploy/local/docker-compose.yaml ps
make logs
```

## Option 2: Postgres in Docker, binaries locally

Faster iteration - rebuild and restart in seconds.

```bash
# PostgreSQL only - that's all Delos needs
make up

# Build bin/delos and bin/delos-gateway
make build

# Run both in the background
make run-all

# ... and stop them when you're done
make stop-all
```

Or run either binary in the foreground, one per terminal, for debugging:

```bash
make run-gateway        # go run ./cmd/delos-gateway   (:8080)
make run-control-plane  # go run ./cmd/delos serve     (:8081)
```

When running locally, point the control plane at the local gateway and database:

```bash
export DELOS_GATEWAY_URL=http://localhost:8080
export DELOS_STORAGE_BACKEND=postgres
export DELOS_DB_HOST=localhost
```

## Verify

```bash
curl http://localhost:8080/healthz    # gateway: {"status":"ok","providers":N}
curl http://localhost:8081/healthz    # control plane: {"status":"ok"}
```

## Dev mode: what you get with no configuration

With no config file and no database, the gateway starts in **dev mode**:

- **Any API key is accepted.** `delos-dev` is only a convention; the gateway
  logs `virtual keys are NOT enforced` at startup so it is never a surprise.
  Enforcement switches on the moment `DELOS_STORAGE_BACKEND=postgres` is set —
  then every request needs a real key from `delos key create`.
- **Model names pass straight through** to whichever providers have keys in the
  environment. Aliases and fallback chains are opt-in (`gateway.routes`).
- **The cache is in-process** (an LRU), so restarting clears it.
- **Nothing is metered** — no budgets, no usage rows.
- **Tracing is off** until you point `telemetry.otlp_endpoint` somewhere or set
  `telemetry.stdout: true`.

Dev mode is for hello-world and local iteration. Everything it turns off is one
config line away; see [docs/deploy.md](docs/deploy.md) for the production shape.

## Your First Completion

The gateway speaks the OpenAI and Anthropic HTTP APIs, so anything that talks to
those providers works with the base URL swapped. There is no Delos SDK to
install.

With curl:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello!"}]}'
```

With the OpenAI Python client:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
response = client.chat.completions.create(
    model="gpt-4o-mini",
    messages=[{"role": "user", "content": "Hello!"}],
)
print(response.choices[0].message.content)
```

With the Anthropic client:

```python
import anthropic

client = anthropic.Anthropic(base_url="http://localhost:8080", api_key="delos-dev")
```

Gateway endpoints:

| Endpoint | Description |
|----------|-------------|
| `POST /v1/chat/completions` | OpenAI-compatible completions (streaming supported) |
| `POST /v1/embeddings` | Embeddings |
| `GET /v1/models`, `GET /v1/models/{model}` | Model discovery |
| `POST /v1/messages` | Anthropic-compatible messages |
| `GET /v1/events` | Live request stream (SSE) — what `delos tail` reads |
| `GET /healthz` | Health and provider count |

Models are resolved by name (`gpt-4o-mini`, `claude-sonnet-4-20250514`,
`gemini-1.5-flash`) or with an explicit provider prefix (`openai/gpt-4o`,
`ollama/gemma3:4b`).

## Your First CLI Commands

The `delos` binary is both the control-plane server and the CLI. Every
subcommand other than `serve` is a client.

```bash
# Against the gateway (DELOS_GATEWAY_URL, default http://localhost:8080)
./bin/delos gateway health
./bin/delos gateway models
./bin/delos gateway complete "Summarize the Odyssey in one line." --model gpt-4o-mini
./bin/delos gateway complete "Tell me a story." --model gpt-4o-mini --stream

# Against the control plane (DELOS_CONTROL_PLANE_ADDR, default localhost:8081)
./bin/delos prompt create "Summarizer" --slug summarizer \
  --system "Summarize the following text concisely."
./bin/delos prompt list
./bin/delos datasets list
./bin/delos eval evaluators
./bin/delos gate list
./bin/delos gate check <name>   # exit 0 = pass, 1 = fail
```

Global flags: `-o/--output` (`table`, `json`, `yaml`) and `-v/--verbose`.
`delos runtime` is an alias for `delos gateway`, and `delos deploy` is an alias
for `delos gate`.

Every command has help text:

```bash
# docs-test
delos --help
delos tail --help
delos config validate --help
```

## Watching traffic: `delos tail`

`delos tail` prints one line per request as it happens — the fastest way to see
whether your app is really going through Delos, which provider answered, and
what it cost.

```bash
delos tail                 # live
delos tail --replay 20     # the last 20 requests first, then live
delos tail --json | jq .   # raw events for scripting
```

```
TIME      STATUS  MODEL       PROVIDER   LATENCY  TOKENS   COST      CACHE  KEY
14:22:03  200     gpt-4o      openai       1.2s   842->96  $0.00131  -      web-prod
14:22:04  200     gpt-4o      openai      188us   842->96  $0.00131  hit    web-prod
14:22:07  404     ghost       -             2ms   -        -         -      -      model_not_found: ...
```

It reads `GET /v1/events` on the gateway (`DELOS_GATEWAY_URL`), a plain SSE
stream — `curl -N http://localhost:8080/v1/events` shows the same JSON without
the CLI. The stream is lossy by design: a slow reader drops events rather than
slowing the gateway down. Turn it off entirely with `DELOS_TAIL=off`.

When virtual keys are enforced, `delos tail` needs one: export
`DELOS_API_KEY=<key>`.

## Configuration: `delos.yaml`

Delos needs no config file. When you want one — a fixed port, routes, Postgres,
an OTLP endpoint — there is exactly one:

```bash
# docs-test
cp delos.yaml.example delos.yaml
delos config validate delos.yaml
```

Precedence is **environment variable > `delos.yaml` > default**. The file is
found at `$DELOS_CONFIG`, else `./delos.yaml`. Secrets are environment-only
(provider keys, `DELOS_DB_PASSWORD`), so the file is safe to commit.

`validate` is offline and structural. `doctor` actually probes what the config
points at — control plane, gateway, Postgres, and provider connectivity through
`GET /v1/models`:

```bash
delos config doctor
```

```
ok    config file    /srv/delos/delos.yaml parses cleanly
ok    control plane  localhost:8081 healthy ({"status":"ok"})
ok    gateway        http://localhost:8080 healthy, 2 provider(s) registered
FAIL  postgres       cannot connect to localhost:5432/delos: dial tcp ...
      -> start it with `make up`, or fix DELOS_DB_* / database: in delos.yaml
ok    providers      37 model(s) reachable via openai, anthropic
```

It exits 1 if any check fails, so it works as a CI smoke test. `-o json` gives
machine-readable output.

## Virtual keys and budgets

Keys are enforced as soon as the gateway has Postgres configured. They are
stored as argon2id hashes, and the plaintext is shown exactly once:

```bash
delos key create web-prod --usd-budget 250 --models 'openai/*,gpt-4o'
delos key list
delos key revoke <id>          # the id from `delos key list`
```

Then use the key like any provider key:

```python
client = OpenAI(base_url="http://localhost:8080/v1", api_key="dk_...")
```

An over-budget or out-of-scope request is refused with a structured 429/403
before any provider is contacted — you can see those refusals in `delos tail`.

## Prompts as files

Prompts live in your repo as `.prompt.yaml` files; the control plane is an index
and a serving cache, not the source of truth.

```bash
delos prompt pull ./prompts            # write every prompt to a directory
$EDITOR ./prompts/summarizer.prompt.yaml
delos prompt diff ./prompts            # semantic diff against the control plane
delos prompt push ./prompts --dry-run  # show what would change
delos prompt push ./prompts            # sync (repo wins)
delos prompt render ./prompts/summarizer.prompt.yaml --var topic=Odyssey
```

## Gates in CI

`delos gate check` turns eval results into an exit code, so a pipeline can block
on quality:

```bash
delos gate create nightly --prompt summarizer --condition 'overall_score>=0.8'
delos gate list
delos gate check nightly   # exit 0 = pass, 1 = fail
```

Put that last line in a CI step and the merge blocks when the gate fails. Delos
emits the verdict; your CI system decides what to do with it.

## Optional Compose Profiles

None of these start by default.

```bash
# Local models via Ollama (:11434) - starts Ollama and pulls gemma3:4b
make up-ollama
make ollama-ready

# Then enable it for the gateway:
#   DELOS_RUNTIME_OLLAMA_ENABLED=true
#   DELOS_RUNTIME_OLLAMA_URL=http://ollama:11434   (http://localhost:11434 locally)

# Jaeger tracing UI on http://localhost:16686
docker compose -f deploy/local/docker-compose.yaml --profile observability up -d

# LocalStack S3 on :4566 (used by some tests)
docker compose -f deploy/local/docker-compose.yaml --profile test up -d
```

## Running Tests

```bash
# Unit tests, no external dependencies
make test-unit

# Full test run (starts postgres and localstack first)
make test

# Coverage report -> coverage.html
make test-coverage
```

Integration tests need the stack running:

```bash
make up-all
make test-integration

# Or drive the runner directly
./tests/integration/run.sh cli      # CLI tests only
./tests/integration/run.sh ollama   # Ollama tests only
./tests/integration/run.sh -v       # Verbose output
```

`make test-ollama` runs the Ollama suite only.

## Making Changes

### Modifying Proto Files

```bash
# Edit files in proto/
vim proto/prompt/v1/prompt.proto

# Regenerate Go code
make proto

# Rebuild
make build
```

### Adding a New Endpoint

1. Define the RPC in the proto file
2. Run `make proto`
3. Implement the handler in the service package under `services/`
4. Wire it up in `controlplane/controlplane.go` if it is a new module
5. Add tests
6. Update the CLI if needed

Gateway HTTP endpoints are not proto-driven - add them in
`services/runtime/httpapi.go` (with `openai_api.go` / `anthropic_api.go`).

### Code Style

```bash
# Run linters (Go + proto)
make lint

# Format code
go fmt ./...
goimports -w .
```

## Troubleshooting

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for common issues and solutions.

### Quick Fixes

**Port already in use**
```bash
make stop-all
# Or find and kill whatever holds the port
lsof -ti:8080 | xargs kill -9
lsof -ti:8081 | xargs kill -9
```

**Docker containers won't start**
```bash
docker compose -f deploy/local/docker-compose.yaml down -v
make up-all
```

**Completions fail with "no providers available"**
```bash
# Check what the gateway actually registered
curl http://localhost:8080/healthz
# Then confirm your keys are in deploy/local/.env.local and restart the gateway
```

**Tests fail with "service unavailable"**
```bash
docker compose -f deploy/local/docker-compose.yaml ps
curl -sf http://localhost:8081/healthz || echo "Control plane not running"
```

## Next Steps

- Read [CLAUDE.md](CLAUDE.md) for architecture details and coding standards
- Check out the [CLI source](cli/cmd/) to understand command structure
- Read [deploy/local/.env.example](deploy/local/.env.example) for every configuration option
- Browse [tests/integration/](tests/integration/) for test examples
