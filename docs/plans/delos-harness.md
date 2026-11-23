# Delos update plan for the agent harness

Nov 23, 2026 · @Eric Maland

## Agent brief

**This whole document is your brief.** You are the lead agent for the Delos work described here, in `github.com/instantcocoa/delos`. Read every section before you start. Where this document and the code disagree, the code wins: record the difference in your report and keep going.

Your job, in one line: make Delos the model gateway and observability sink for a self-hosted coding-agent platform, **delegating to Bifrost's open-source Go core wherever Bifrost already does the job**. Build in Delos only what Bifrost does not do. If Bifrost has to be replaced later, it will be replaced later; do not build abstraction layers now to make that easier.

You run milestones D0 to D2 (see Workstreams). Stop for review after the D0 spike and again after D2. D3 to D5 are listed so you understand where the work is heading; do not start them.

## Platform context

The platform runs coding agents (Claude Code, Codex, pi and other ACP agents) remotely, one per Kata Containers VM. The pieces you need to know:

| Piece | What it is | What it means for Delos |
| --- | --- | --- |
| Hub | A Go service, ported from the design of `ahpio/ahpd`. Owns users, sessions, VMs and each session's event log in Postgres | The hub mints and revokes Delos virtual keys over gRPC and sends agent spans to Delos over OTLP |
| AHP and ACP | Agent Host Protocol (clients to hub, WebSocket) and Agent Client Protocol (hub to each agent, stdio) | Not used by Delos directly; they explain where sessions and turns come from |
| Agent VM | A Kata microVM holding the repo, tools and one agent process | It holds no provider key. The agent's `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` point at `delos-gateway` with a short-lived, session-scoped virtual key |
| Egress proxy | Outside each VM; injects credentials for non-model services (GitHub and so on) | Model traffic goes to Delos instead, so Delos is the model-credential boundary |
| Identity | Ory Hydra (OAuth2/OIDC tokens) and Ory Kratos (people) | From D3 the control plane verifies Hydra-issued JWTs |
| Authorization | SpiceDB (Zanzibar-style relationships: org, team, project, session) | From D3 control-plane reads and writes are checked against SpiceDB |
| Event log | The hub's Postgres table of ordered session actions; the source of truth for every session | Delos stores spans that point to `(session_id, seq)`, never a second copy of transcripts |

## Constraints

- **Two binaries plus PostgreSQL.** Bifrost is a library inside `delos-gateway`, never a second process. Bifrost's config store may use the same Postgres in its own schema. Add no required service: no NATS, ClickHouse, Redis-as-requirement or SaaS. Open-source dependencies only.
- **Compatibility contract.** The OpenAI- and Anthropic-compatible HTTP surfaces and the `delos.yaml` schema must not break; additive changes only.
- **Secrets are environment-only**; `delos.yaml` stays commit-safe.
- **The gateway serves traffic with the control plane down.**
- **Releases stay static:** `CGO_ENABLED=0` for linux and darwin on amd64 and arm64, cosign-signed, with an SBOM.
- **One gap per pull request**, each with tests and docs. Mark runnable doc blocks with `# docs-test`.
- **Prefer Bifrost.** Before writing Delos code for anything in the delegation table, show in the PR that Bifrost cannot do it, or does it wrongly for an agent client.

## How to work: agent teams

Use agent teams: you are the lead; spawn teammates, keep a shared task list, and message between teams. If your environment has no agent-teams feature, run each team as a subagent with the same boundaries. Each team owns its files, works on `team/<id>/<topic>` branches, and opens its own PRs. A change to another team's files goes through that team.

| Team | Owns | Milestones | Files it owns |
| --- | --- | --- | --- |
| **LEAD** (you) | Plan, task list, gates, reports to Eric | All | `docs/plans/`, design notes index |
| **DL-S** spike and toolchain | Go bump, Bifrost module, build and SBOM | D0 spike, then upgrades | `go.mod`, `go.sum`, `.goreleaser.yaml`, `Dockerfile`, `cmd/delos-gateway/main.go` wiring |
| **DL-G** gateway | Client HTTP surfaces delegated to Bifrost: passthrough, translation, Responses, `count_tokens`, routing, cache, usage, spans | D1 | `services/runtime/` except keys, `pkg/semconv/` |
| **DL-K** keys and budgets | `KeyService` over Bifrost governance, key migration from `virtual_keys`, revoke propagation | D2 | `services/runtime/keys*`, new `proto/keys/v1`, `cli/cmd/key*`, key migrations |
| **DL-T** tests and proof | CI skip-fail, `make bench`, cassettes, agent end-to-end, swap parity, Bifrost upgrade gate | D0, D1, D2 | `tests/`, `Makefile` test and bench targets, `.github/workflows/` |
| **R-REVIEW** reviewer | Reviews every PR against this brief before merge; writes no feature code | All | none |

Order of work: DL-T and DL-S run D0 in parallel. **Gate G0:** LEAD reviews the spike report with Eric before D1. DL-G and DL-K then run D1 and D2 in parallel, with DL-T writing their suites alongside. **Gate G1:** stop after D2 and report.

Write a numbered design note in `docs/design/notes/` before any change that alters wire behavior, a database schema, or the config surface. R-REVIEW approves the note before code lands.

## Summary

Delos becomes two things for the harness: the **model gateway every agent VM calls** (so no VM holds a provider key) and the **observability and eval sink** the hub's session event log projects into. It is close on architecture (Go, two binaries, Postgres, OTLP in and out) and far on three fronts:

- **Agent-grade API fidelity.** The Anthropic surface translates requests through a narrow struct with no `thinking`, `cache_control`, `metadata` or `count_tokens`, and there is no OpenAI Responses API. Claude Code and Codex need those. **Closed by embedding Bifrost's Go core** (decision below) rather than rebuilt by hand.
- **Multi-tenancy and identity.** The control plane is single-tenant with one shared token; virtual keys are minted only by the CLI writing to Postgres and carry no owner, session or expiry.
- **Agent-shaped observability.** Spans are stored flat with no tenant or session columns, on GenAI semantic conventions 1.29, with no retention. Datasets, eval and deploy still run on in-memory stores even with the Postgres backend.

**Decision (2026-11-23): delegate to Bifrost wherever possible.** `delos-gateway` embeds Bifrost's open-source Go core and plugins (`github.com/maximhq/bifrost`, Apache-2.0) for provider calls, passthrough, translation, routing, caching, pricing, and virtual keys with budgets. Delos builds only what Bifrost lacks: the control plane (prompts, datasets, evals, gates, the observe store), the hub-facing key API, session attribution, and from D3 identity and tenancy. If Bifrost has to be replaced later, it will be replaced then.

## Current state

Read from `instantcocoa/delos` at `6e73783` (2026-08-27, the latest commit). Delos is now two binaries plus PostgreSQL; the earlier six gRPC services over Postgres, Redis and NATS were folded into libraries under `services/`. NATS is gone, and Redis is only an optional shared cache.

| Part | What it does today | Storage | Auth |
| --- | --- | --- | --- |
| `delos-gateway` (:8080) | OpenAI `/v1/chat/completions`, `/v1/embeddings`, `/v1/models`; Anthropic `/v1/messages`; `/v1/events` SSE for `delos tail`. Routing, fallback chains, retries, circuit breaker, exact-match cache | Stateless; Postgres only for keys | Virtual keys `dk_…`, argon2id, model-scoped, monthly token and USD budgets |
| Providers | OpenAI, Anthropic, Gemini, Bedrock, Ollama, any OpenAI-compatible | — | Provider keys from env only |
| `observe` | OTLP/HTTP ingest at `/v1/traces`, trace and metric queries, cost stats | Postgres `observe.spans`, one row per span, `gen_ai.*` essentials promoted to columns | Control-plane shared token |
| `prompt` | Git-like prompt versions, diff, templates | Postgres | Shared token |
| `datasets`, `eval`, `deploy` (gates) | Datasets, evaluators (exact, contains, regex, JSON schema), runs, CI pass/fail verdicts | **In memory**, even with the Postgres backend | Shared token |
| Telemetry | One span per request, semconv `1.29.0`, prompt/completion capture off by default and redactable | — | — |
| Tests | Unit, integration (tag), provider conformance cassettes (OpenAI, Anthropic, Gemini, Ollama), overhead test, docs-test | — | — |

The README's own "in progress" list: the conformance suite's nightly re-record, the published p99 overhead number (target under 5 ms), and observe's UI.

## Target role in the harness

The Go hub owns sessions and the authoritative event log in Postgres. Delos sits beside it with four interfaces, all on protocols Delos already speaks.

| Caller → Delos | Protocol | Purpose |
| --- | --- | --- |
| Agent in a Kata VM (Claude Code, Codex, pi) → gateway | HTTP: `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` pointed at Delos | Every model call. The VM holds only a short-lived, session-scoped virtual key; provider keys stay in Delos |
| Hub → control plane | gRPC, authenticated with a Hydra client-credentials token | Mint a virtual key when a session starts, revoke it when the session ends, read usage for budgets |
| Hub event-log projector → observe | OTLP/HTTP `/v1/traces` | Agent spans (session, turn, tool call) built from AHP actions, joined to the gateway's `gen_ai` spans by session id |
| People and CI → control plane | gRPC, Hydra-issued user tokens, SpiceDB checks | Traces, prompts, datasets built from sessions, evals and gates, scoped per org and team |

Two rules follow. First, the event log stays in the hub: observe stores spans that point to `(session_id, seq)` rather than a second copy of transcripts. Second, Delos never needs the hub to serve model traffic, matching its existing rule that the gateway runs without the control plane.

## Delegation to Bifrost

Bifrost owns every row it can; Delos owns the rest. Read from `maximhq/bifrost` at `d913ed8` (2026-11-23). Bifrost entry points: `core/bifrost.go` (`Init`, `Passthrough`, `PassthroughStream`, `ChatCompletion*`, `Responses*`, `CountTokensRequest`, `SetTracer`), `core/schemas/account.go` (`Account`), `core/schemas/plugin.go` (`LLMPlugin`, `HTTPTransportPlugin`), `plugins/` (governance, semanticcache, otel, logging, routing, telemetry) and `framework/configstore/` (virtual keys, budgets, rate limits, with `expires_at`). Each "verify in spike" row is confirmed or overturned in the D0 spike report.

| Capability | Owner | How |
| --- | --- | --- |
| Provider calls, passthrough, translation, Responses API, `count_tokens` | **Bifrost** core | Same-provider calls through `Passthrough` / `PassthroughStream` with the body and `anthropic-*` headers as sent; cross-provider through `ChatCompletion*` / `Responses*` |
| Client HTTP routes | **Bifrost** handlers if they mount inside `delos-gateway` (verify in spike), else thin Delos handlers over core | Keep Delos's URL paths: `/v1/chat/completions`, `/v1/messages`, `/v1/messages/count_tokens`, `/v1/responses`, `/v1/embeddings`, `/v1/models`. Bifrost's routes live in the `transports/v2` module, which serves fasthttp |
| Routing, retries, fallbacks, load balancing, circuit breaking | **Bifrost** | Delos route aliases become Bifrost fallback lists; Delos's own retry loop and breaker are removed |
| Response cache | **Bifrost** (exact-match; semantic cache off at first) | Delos's in-process and Redis cache is removed once parity holds |
| Model catalog and pricing | **Bifrost** model catalog, if its prices match provider usage within 1% (verify in spike) | Delos pricing table kept only for models Bifrost lacks |
| Virtual keys, budgets, rate limits, expiry | **Bifrost** governance plugin and config store, in Postgres (verify it runs in SDK mode) | Delos `dk_` keys migrate into Bifrost virtual keys; the `delos key` CLI calls the new API |
| Hub-facing key API (`KeyService`) | **Delos** | A thin gRPC API over Bifrost governance: mint with session, org, subject, labels, expiry and budgets; revoke; usage |
| Provider credentials | **Delos** implements Bifrost's `Account` | Keys stay environment-only |
| Session attribution | **Delos** | Pass session id, org and subject from the key into Bifrost's context; stamp them on spans and tail events |
| Tracing | **Bifrost** otel plugin, pointed at Delos observe | Delos adds only `gen_ai.conversation.id`, `delos.org_id`, `delos.subject`, and checks attribute names against current GenAI semconv |
| `delos tail` live feed | **Delos** feed fed from Bifrost's logging plugin or event broadcaster | Same output and tenant scoping as today |
| Control plane: prompts, datasets, evals, gates, observe store | **Delos** | Unchanged in D0 to D2; Bifrost has none of it |
| Identity and tenancy (D3) | **Delos** | Hydra JWTs and SpiceDB checks on the control plane |

Costs accepted with this decision:

- **Toolchain.** Bifrost core declares Go 1.27.4; Delos is on 1.25.5.
- **Dependencies.** Bifrost brings `valyala/fasthttp`, `bytedance/sonic`, GORM and more. Static release builds and the SBOM must be re-checked.
- **Churn.** Bifrost's latest commit was the day this was written. Pin a release, upgrade on a schedule, and gate each bump on the cassette and agent suites.
- **Open-core pull.** SSO, RBAC, clustering, guardrails and vault-backed secrets are in Bifrost's paid tier. Delos does not need them here: identity and tenancy live in Delos's control plane and the hub.

## Gaps

Thirteen gaps, P0 first. Gaps 1 to 3 are now closed by wiring in Bifrost, not by writing new translators. P0 blocks the harness's first milestone (one user, one agent in a VM, calling models through Delos). Each row names the evidence in the repo.

| # | Pri | Area | Gap | Evidence | Fix |
| --- | --- | --- | --- | --- | --- |
| 1 | P0 | Gateway: Anthropic | The request struct has no `thinking`, `cache_control`, `metadata`, documents, server tools or `anthropic-beta` headers, and there is no `/v1/messages/count_tokens`. Claude Code loses prompt caching (cost) and extended thinking, and may fail on token counting | `anthMessagesRequest` in `services/runtime/anthropic_api.go`; endpoint list in the README | Route Anthropic→Anthropic through Bifrost `PassthroughStream` with the body and `anthropic-*` headers as sent. Serve `count_tokens` from `CountTokensRequest`. Keep the current translator only for cross-provider routes until Bifrost's replaces it |
| 2 | P0 | Gateway: usage and cost | The Anthropic surface reports only `input_tokens` / `output_tokens`. Cache read and write tokens are parsed from the provider but not surfaced or priced separately | `anthUsage` vs `CacheReadInputTokens` in `provider_anthropic.go` | Take usage, cache tokens included, and cost from Bifrost's model catalog and governance accounting; check it against provider usage; carry it into spans and tail |
| 3 | P0 | Gateway: OpenAI | No `/v1/responses`. Codex uses the Responses API | Endpoint list | A Delos `/v1/responses` handler over Bifrost `ResponsesRequest` / `ResponsesStreamRequest`, passthrough for OpenAI |
| 4 | P0 | Keys | Keys are minted only by `delos key` writing straight to Postgres. No API, no expiry, no owner, team, session or metadata, monthly budgets only | `virtual_keys` and `key_usage` migrations; architecture doc, CLI section | Move keys into Bifrost governance (virtual keys, budgets with reset intervals, rate limits, `expires_at`) and migrate existing `dk_` keys. Add a thin control-plane `KeyService` for the hub: mint with session, org, subject and labels; revoke on session end; usage |
| 5 | P0 | Gateway: attribution | Spans carry `delos.virtual_key` but no session, user or org | `pkg/semconv/semconv.go` | Stamp `gen_ai.conversation.id` = session id plus `delos.org_id` and `delos.subject` from the key, on spans from Bifrost's otel plugin, so gateway spans join hub spans |
| 6 | P0 | Toolchain and dependencies | Delos is on Go 1.25.5; Bifrost core needs Go 1.27.4 and brings `fasthttp`, `sonic` and more | `go.mod` in both repos | Bump Go; prove `CGO_ENABLED=0` builds on all four release targets; diff the SBOM; add Bifrost's Apache-2.0 notice |
| 7 | P1 | Identity | Control plane is "a single-tenant operator surface" with one shared `DELOS_AUTH_TOKEN` | `pkg/grpcutil/auth.go` | Verify Hydra-issued JWTs (JWKS, audience, expiry); keep the shared token as the break-glass root. Map `sub` and `groups` to a principal |
| 8 | P1 | Authorization | Nothing is scoped by org, team or project | No tenant columns in any migration | Add `org_id` to every table; check SpiceDB (`view`, `edit`, `admin` on org and project) in an interceptor; filter queries by org |
| 9 | P1 | Observe: schema | No `org_id`, `session_id`, `user`, `turn` or `tool` columns; semconv pinned at `1.29.0`, which predates the agent spans and renames | `observe` migration; `semconv.Version` | Promote `org_id`, `conversation_id`, `agent_name`, `tool_name`, `operation_name`. Support agent operations (`invoke_agent`, `execute_tool`) and read old and new attribute names. Confirm names against the current semconv release before coding |
| 10 | P1 | Observe: scale | One flat table, no partitioning, no retention. Agent sessions emit far more spans than chat traffic | Same migration | Monthly partitions on `start_time`, a retention setting, and roll-off to Parquet on S3 (both libraries are already in `go.mod`) |
| 11 | P1 | Durability | Datasets, eval and deploy use in-memory stores even when `DELOS_STORAGE_BACKEND=postgres`; a restart loses runs and gates | Architecture doc, "Storage" | Wire the Postgres stores; migrations already exist for all three |
| 12 | P2 | Evals for agents | Evaluators score one completion against an expected string. Nothing scores a session (tests passed, diff applied, turns, cost) | `services/eval/evaluators.go` | Session-level evaluators fed by the hub; datasets built from session references |
| 13 | P2 | Tests and proof | p99 overhead number unpublished; nightly re-record in progress; DB tests skip silently without `delos_test` | README "Performance"; `TESTING.md` | Publish `make bench`, measured with Bifrost inside. Make CI fail on skipped DB tests. Add agent end-to-end, tenant-isolation and Bifrost-upgrade suites (see Test plan) |

## Workstreams and order

Six milestones, D0 to D5, each shippable on its own. D1 and D2 gate the harness's first milestone; D3 to D5 can run while the hub is built.

1. **D0: Baseline and Bifrost spike (about a week; DL-T and DL-S in parallel).**
   - DL-T: fail CI when database tests skip; record with a test what `/v1/messages` does today with `thinking`, `cache_control` on blocks, `metadata`, a `document` block and an `anthropic-beta` header (a 400 as `docs/providers.md` promises, or dropped); land `make bench` and measure the pre-Bifrost baseline.
   - DL-S: bump Go to the version Bifrost requires; add Bifrost core and the governance, otel and logging plugins; run one headless Claude Code session through `PassthroughStream` behind a build flag.
   - DL-S spike report answers every "verify in spike" row of the delegation table: do Bifrost's HTTP handlers mount inside `delos-gateway`; does governance run in SDK mode with Postgres; do catalog prices match provider usage; static builds on all four targets; binary size; SBOM diff; new transitive dependencies; cache-read tokens on later turns.
   - Exit (gate G0): Eric approves the spike report.
2. **D1: Gateway on Bifrost (gaps 1, 2, 3, 5, 6; DL-G, with DL-T).** Delos `Account`; same-provider passthrough; Bifrost translation, `/v1/responses` and `count_tokens`; routing, fallbacks and cache moved to Bifrost and Delos's versions removed; Bifrost otel plugin pointed at observe with session attribution; `delos tail` fed from Bifrost logging. Delos translators stay behind a flag for one release, then are deleted.
   - Exit: the existing conformance cassettes pass unchanged; one Claude Code and one Codex session finish through Delos with prompt caching hitting; costs within 1% of provider usage; p99 overhead under 5 ms.
3. **D2: Keys on Bifrost governance (gap 4; DL-K, with DL-T).** Migrate `dk_` keys and budgets into Bifrost virtual keys; thin `KeyService` gRPC for the hub (mint with session, org, subject, labels, expiry, budgets; revoke; usage); `delos key` CLI calls it.
   - Exit: a session key stops working within 1 s of revoke and at `expires_at` on every gateway replica; budget refusals stay a structured 429 naming the key and the budget (gate G1: stop and report).
4. **D3: Identity, tenancy and durability (gaps 7, 8, 11).** Hydra JWT verification, SpiceDB interceptor, `org_id` on every table, Postgres stores for datasets, eval and deploy. Not started in this round.
5. **D4: Agent observability (gaps 9, 10).** Schema promotion, agent operations, partitions, retention, Parquet roll-off. Not started in this round.
6. **D5: Session evals (gap 12).** Session evaluators and datasets of session references. Not started in this round.

## Test plan

Every suite below runs in CI on Linux with Postgres; provider-backed suites replay cassettes and re-record nightly.

| Suite | Proves | How |
| --- | --- | --- |
| Agent end-to-end | Claude Code and Codex work unmodified through Delos | Run each CLI headless against the gateway with a scripted task; record cassettes; assert caching, thinking blocks and tool calls survive |
| Passthrough fidelity | Same-provider calls through Bifrost are byte-faithful except auth | Diff request and response bodies with and without Delos over the cassette set, including beta headers and SSE event order |
| Swap parity | Moving to Bifrost changes nothing a client sees | Run the existing conformance cassettes against the old translators and the Bifrost path; responses, error codes and usage must match |
| Bifrost upgrade gate | A Bifrost version bump is safe | Every bump is its own PR that runs the cassette, agent end-to-end and bench suites; no merge on a regression |
| Build and supply chain | Releases stay static and auditable | `CGO_ENABLED=0` builds for linux and darwin on amd64 and arm64; SBOM diff and license check, Bifrost's Apache-2.0 notice included |
| Cost accuracy | Usage and dollars match the provider | Compare per-request cost, cache tokens included, against recorded provider `usage`; tolerance 1% |
| Key lifecycle | Mint, scope, expire, revoke, budget | Table-driven tests: expired, revoked, wrong model, over daily budget, over total budget; revoke propagates to every gateway replica within 1 s |
| Tenant isolation | No org sees another's spans, keys, prompts, datasets or tail events | Two orgs, every read RPC and `/v1/events`, asserting empty or denied results across the boundary; run with SpiceDB in the loop |
| Identity | JWT checks are strict | Wrong audience, expired, unknown key ID, `alg: none` and a signature from an untrusted JWKS are all refused |
| Observe load | Agent-scale ingest and query | Ingest 100 sessions × 10,000 spans; p95 trace load under 1 s; retention drops a partition while ingest continues |
| Gateway overhead | p99 overhead under 5 ms with Bifrost inside, no leaks | `make bench` against the mock provider, plus a 10-minute soak checking goroutines and file descriptors |
| Restart durability | Nothing in the control plane lives only in memory | Kill and restart `delos serve` mid-eval; runs, datasets and gates survive |

## Open decisions

Decided 2026-11-23: delegate to Bifrost wherever possible (see Delegation to Bifrost), including routing, retries, fallbacks, cache, pricing, and virtual keys with budgets. No swap-out abstraction is built now. Still open:

- [ ] **Bifrost version policy.** Recommended: pin a release, upgrade monthly through the upgrade-gate PR, patch forward only for security fixes.
- [ ] **Semantic cache.** Recommended: off until the agent suites are green, then trial it on non-agent traffic only. Agent prompts rarely repeat exactly, and a wrong cache hit breaks a coding session.
- [ ] **How spans reach observe.** Recommended: OTLP from the hub's projector and from Bifrost's otel plugin, which keeps Delos free of NATS.
- [ ] **Transcript content in observe.** Recommended: references to `(session_id, seq)` only, with content capture off for agent spans; the hub's log is the source of truth.
- [ ] **Budget authority.** Recommended: Delos, through Bifrost governance, refuses model spend; the hub refuses compute (VM hours, sessions).
- [ ] **Postgres for spans at agent scale.** Recommended: stay on partitioned Postgres until the observe load test fails.

## Reporting and stop points

LEAD reports to Eric at gate G0 (after the spike) and gate G1 (after D2), and waits for approval before going past either. Each report holds:

1. What changed, with PR links and test evidence: the commands run and their output.
2. The delegation table with every "verify in spike" row marked confirmed or overturned, and why.
3. Anything in this document that turned out wrong about Delos or Bifrost, with the file and line.
4. Every place where Bifrost behaves differently from Delos's documented behavior (errors, usage, streaming order), and which behavior was kept.
5. Open questions for Eric, one line each.

Stop and ask before any of these: breaking the compatibility contract, adding a required service, dropping data in a migration, or starting D3.

