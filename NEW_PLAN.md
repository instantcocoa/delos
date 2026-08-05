# Delos Refocus Plan

Directive for the coding agent. This supersedes PLAN.md. When this document conflicts
with existing code structure, this document wins. Work top to bottom; each phase has
acceptance criteria that must pass before starting the next.

## Positioning (context for every decision below)

Delos is a **self-hosted LLM gateway with batteries-included observability, in two Go
binaries**. The pitch to a civilian team: point your existing OpenAI/Anthropic client at
Delos, get routing, failover, caching, virtual keys, budgets, and full tracing — without
deploying a Python proxy, a ClickHouse cluster, and a separate observability vendor.

Non-goals (do not build, remove if present):
- 100-provider coverage. We support a curated set, well.
- A client SDK in any language. OpenAI-compatibility over HTTP *is* the SDK.
- A deployment orchestrator. We emit verdicts; CI/CD systems act on them.
- A proprietary trace format. OTLP `gen_ai.*` in, OTLP out.

## Phase 0 — Collapse the topology

Current state: six gRPC services (observe, runtime, prompt, datasets, eval, deploy) +
Postgres + Redis + NATS. Target state: **two binaries + Postgres**. Redis optional.

1. **`delos-gateway`** (data plane): the current `runtime` service. Stateless, hot path,
   holds provider credentials. HTTP-first: serves the OpenAI-compatible surface
   (`/v1/chat/completions`, `/v1/embeddings`, `/v1/models`) and the Anthropic Messages
   surface (`/v1/messages`) natively. gRPC becomes internal-only or is dropped from this
   binary entirely.
2. **`delos`** (control plane): observe + prompt + datasets + eval + deploy linked into
   one process behind one HTTP port. Keep the proto-generated types as the internal
   module boundaries — the packages stay separate under `services/` — but replace all
   inter-service gRPC calls with direct Go function calls behind the existing interfaces.
   The re-split path stays open; the network hops go away.

Mechanical steps:
- [ ] Extract each service's server into a mountable module: `func (s *PromptService) Routes() http.Handler` or equivalent.
- [ ] New `cmd/delos/main.go` mounts all control-plane modules; `cmd/delos-gateway/main.go` runs runtime alone.
- [ ] Replace gRPC clients between control-plane services with direct interface calls. Delete the client pools, retry wrappers, and connection config that existed only for internal hops.
- [ ] Remove NATS. Anything using it moves to Postgres (`FOR UPDATE SKIP LOCKED` queue table or LISTEN/NOTIFY). Delete it from docker-compose and docs.
- [ ] Make Redis optional: gateway falls back to in-process LRU when `DELOS_REDIS_URL` is unset. Single-replica deployments should need only Postgres.
- [ ] Delete `sdk/python/` and its CI/publishing. README shows `base_url` swap instead (see DevX).
- [ ] Ports collapse: gateway on 8080, control plane on 8081. Kill the 9000–9005 spread.

Acceptance:
- `docker compose up` starts exactly three containers: gateway, control plane, Postgres.
- An unmodified `openai` Python client with `base_url=http://localhost:8080/v1` completes a chat request through the gateway.
- `go build ./...` produces two binaries; no service imports another service's gRPC client.
- Integration tests pass with NATS and Redis absent.

## Phase 1 — Gateway is the product

Providers, curated: **OpenAI, Anthropic, Google (Gemini), AWS Bedrock, and any
OpenAI-compatible endpoint** (covers vLLM, SGLang, Ollama, and most gateways). Use
official Go SDKs where they exist; do not hand-roll request signing. A provider is a Go
interface with five methods, registered in one file. Adding a provider must never touch
routing, auth, or telemetry code.

Deliver, in order:
1. **Translation layer**: OpenAI-surface request → any backend provider → OpenAI-surface
   response, including streaming (SSE), tool calls, and vision blocks. Same for the
   Anthropic surface. This mapping is the highest-defect-risk code in the repo; it gets
   the conformance treatment described under Testing.
2. **Virtual keys & budgets**: keys are hashed at rest (argon2id), scoped to models and
   monthly token/dollar budgets, revocable, with a `delos key create/revoke/list` CLI.
   Never log or echo a full key after creation.
3. **Routing & failover**: ordered fallback chains per model alias (`gpt-4` → Azure
   deployment → OpenAI direct), retry with jittered backoff on 429/5xx, circuit breaker
   per provider, request timeout budget end-to-end.
4. **Caching**: exact-match response cache keyed on (model, normalized messages, params),
   TTL configurable per key. Semantic caching is out of scope for now — note it in
   ROADMAP.md and move on.
5. **Telemetry emission**: every request produces an OTLP span using `gen_ai.*` semantic
   conventions (`gen_ai.request.model`, `gen_ai.usage.input_tokens`, etc.). Attribute
   names live in ONE Go package (`pkg/semconv`) so a spec rename is a one-file change —
   the conventions are pre-stable, pin the version. Content capture (prompts/completions
   in spans) is **off by default**, on via config, redactable via regex/allowlist rules
   applied before export. Exporter endpoint is configurable; default points at the
   control plane's OTLP port. **The gateway must run with zero knowledge of the control
   plane's existence.**

Acceptance:
- Conformance suite (below) green for all five provider types, streaming and non-streaming.
- Kill the primary provider mid-stream in a test; the request fails over and the client sees a valid completed stream from the fallback.
- A request with an over-budget key is rejected with a structured 429 and never reaches a provider.
- Traces from the gateway render correctly in vanilla Jaeger with no Delos control plane running.

## Phase 2 — Control plane earns its keep

- **observe** becomes an OTLP consumer: ingest via OTLP/HTTP (protobuf + JSON), store in
  Postgres, serve a trace/cost/latency UI. It must accept spans from *any* source
  emitting `gen_ai.*` — instrumented apps, other gateways — not just delos-gateway.
  No Delos component may depend on observe's query API. If Postgres ingest becomes the
  bottleneck, the answer is documented ClickHouse support later, not a custom store now.
- **prompt** goes git-native: prompts are YAML/JSON files with a published JSON Schema.
  `delos prompt push/pull/diff/render` syncs a directory with the control plane; Postgres
  is an index and serving cache, the repo is the source of truth. Semantic diffing
  operates on the file format.
- **eval** runs evaluator suites against datasets and prompt versions, records scores as
  spans/metrics, and exposes one verdict endpoint: `GET /v1/gates/{gate}/verdict` →
  `{pass: bool, reasons: []}`.
- **deploy** shrinks to gates: `delos gate check <name>` exits 0/1 for CI. Remove any
  code that mutates external systems (rollouts, k8s calls). If it deploys things, delete it.

Acceptance:
- A Langfuse-instrumented or OTel-SDK-instrumented sample app's traces appear in observe with token counts and cost attributed.
- Round-trip: `prompt pull` → edit file → `diff` shows semantic change → `push` → gateway serves new version.
- A failing eval flips a gate verdict, and a sample GitHub Actions workflow blocks on it.

## Testing & validation

1. **Provider conformance suite** (the crown jewel): a record/replay harness
   (`tests/conformance/`) with golden request/response fixtures per provider per feature
   (chat, streaming, tools, vision, errors, token counting). Runs hermetically in CI
   against recorded cassettes; a nightly job re-records against live APIs with real keys
   and diffs the cassettes, so provider drift shows up as a failing nightly, not a user
   bug report. Every translation-layer bug fix adds a cassette.
2. **Semconv golden traces**: fixtures asserting the exact attribute set emitted per
   request type, validated against the pinned OTel semconv version. Bumping the semconv
   dependency must fail these tests until fixtures are consciously regenerated.
3. **Failure injection**: table-driven tests for provider 429/500/timeout/malformed-SSE
   mid-stream; assert failover, breaker state transitions, and that partial streams are
   never delivered as complete.
4. **Load/soak**: a `make bench` target (vegeta or k6) hitting a mock provider; CI gate
   on p99 gateway overhead < 5ms and zero goroutine/FD leaks over a 10-minute soak.
   Track the number in the README — it's a headline claim, so it must be reproducible.
5. **Compatibility tests**: unmodified official `openai` (Python + TS) and `anthropic`
   clients run against the gateway in CI. If a client library update breaks us, we find
   out before users do.
6. **Migration discipline**: schema changes via versioned migrations (goose/atlas);
   CI job upgrades a seeded previous-version database and runs the integration suite
   against it.
7. **Supply chain hygiene** (self-interest, not compliance): `govulncheck` in CI,
   dependabot, pinned action SHAs in workflows, `go.sum` verified. Keep the direct
   dependency count in the README badge — minimal deps is part of the pitch.

## DevX

**Time-to-first-token is the metric.** A developer with Docker and one provider key must
get a completion through Delos with a single `docker run` and a `base_url` swap — no
config file, no signup, no reading past the first screen of the README. The README's
first code block must be the thing that does it:

```bash
docker run -p 8080:8080 -e OPENAI_API_KEY=sk-... ghcr.io/instantcocoa/delos-gateway
```

```python
client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
```

Requirements:
- **Zero-config dev mode**: with no config file, the gateway boots with a dev virtual key
  printed once to stdout, routes model names straight through to whichever providers have
  env keys set, in-memory cache, traces to stdout. Every knob has a sane default; config
  file is for production, not for hello-world.
- **One config file** (`delos.yaml`), fully documented, with `delos config validate` and
  `delos config doctor` (checks provider connectivity and key validity, prints what's
  misconfigured in plain language). Env vars override file values with a predictable
  `DELOS_` mapping.
- **Errors are structured and honest**: every gateway error response carries a stable
  machine-readable code, the provider's underlying error verbatim, and the request ID
  that finds it in traces. Never swallow provider errors into a generic 500.
- **`delos tail`**: live-stream requests through the gateway in the terminal (model,
  latency, tokens, cost, cache hit, key). This is the demo feature and the debugging
  feature; treat it as first-class.
- **Single-binary story**: `go install` works for both binaries; releases ship
  cosign-signed binaries for linux/darwin, amd64/arm64, plus SBOM. CGO_ENABLED=0.
- **Docs structure**: README (shortest path to first token), `docs/deploy.md` (production: TLS, Postgres
  sizing, HA gateway behind an LB), `docs/providers.md` (per-provider setup + supported
  feature matrix — be explicit about what does NOT work), `docs/otel.md` (pointing
  exporters at Datadog/Grafana/Jaeger instead of observe). Every doc code block is
  extracted and executed in CI; stale docs are failing tests.
- **Versioning**: SemVer; the OpenAI/Anthropic-compatible surfaces and `delos.yaml`
  schema are the compatibility contract. Breaking either requires a major version and a
  written migration note. Internal Go packages carry no stability promise — say so in
  the README to keep freedom to refactor.

## Goals: from here to a complete working system

Each goal is a stable state of the repo, defined by what is true, not by when it happens.
A goal is reached only when all of its conditions hold simultaneously on `main` with CI
green. Do not begin work that only serves a later goal while an earlier goal has unmet
conditions.

**G1 — Honest topology.** Two binaries and Postgres are the entire system. NATS, the
Python SDK, and all internal gRPC hops are gone from the tree, not just unused. An
unmodified OpenAI client completes a request through the gateway via `base_url`. All
Phase 0 acceptance criteria hold.

**G2 — Usable gateway.** The translation layer passes the conformance suite for all five
provider types, streaming included, and virtual keys with budgets gate every request.
A team could adopt Delos as a drop-in proxy for a single provider and lose nothing they
had before.

**G3 — Preferable gateway.** Failover, circuit breaking, caching, and OTLP `gen_ai.*`
emission all hold their acceptance criteria, including the mid-stream failover test and
rendering traces in vanilla Jaeger with no control plane running. `delos tail` works.
At this state the gateway is a better answer than LiteLLM for its supported providers,
and that claim is backed by the benchmark number published in the README.

**G4 — Complete working system.** The control plane meets all Phase 2 acceptance
criteria: observe ingests OTLP from any `gen_ai.*` source, prompts round-trip through
git-native files, evals produce gate verdicts, and a sample CI workflow blocks on one.
The full loop is demonstrable end to end: edit a prompt file → push → gateway serves it →
traffic traced in observe → eval scores it → gate verdict gates a merge. Docs cover
deploy, providers, and external OTel backends, and every doc code block executes in CI.

**Standing conditions at every goal:** the compatibility tests against official client
libraries pass, migrations upgrade from the previous goal's schema, `govulncheck` is
clean, and the direct dependency count has not grown without a written justification in
the PR that grew it. When in doubt between adding a feature and deleting a dependency,
delete the dependency.
