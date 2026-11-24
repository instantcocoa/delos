# G1 report — D1 (gateway on Bifrost) + D2 (keys on Bifrost governance)

Date: 2026-11-23 · Lead: Claude Code (LEAD) · Status: **awaiting Eric's review** (gate G1)

All work is integrated and green on `team/integration/d1-d2`; nothing merged to `main` (`main` is still `d0-baseline` @ `6e73783`).

## 1. What changed, with evidence

| Milestone | Branch(es) | PR | State |
| --- | --- | --- | --- |
| D0 baseline (tests/CI/bench) | `team/dl-t/d0-baseline` | #7 | ready |
| D0 spike (Go 1.27.4 + Bifrost modules) | `team/dl-s/d0-spike` | #8 | ready |
| Planning docs | `team/lead/d0` | #9 | ready |
| D1 gateway | `team/dl-g/d1-gateway` (+ integration) | #10 (draft) | superseded by #13 |
| D2 keys | `team/dl-k/d2-keys` (+ integration) | #11 (draft) | superseded by #13 |
| D1/D2 test suites | `team/dl-t/d1-d2-suites` (+ integration) | #12 (draft) | superseded by #13 |
| **D1+D2 integrated** | `team/integration/d1-d2` | **#13 (draft)** | the review target |

Test evidence (default = Bifrost path, `CGO_ENABLED=0`):

- `go test ./... -count=1` → **16/16 packages `ok`** (incl. `services/runtime`, `tests/conformance`).
- `go test ./tests/conformance/ -run TestSwapParity -count=1` → **38/38** (old vs Bifrost path across the cassette set).
- `go test ./tests/conformance/ -run TestAnthropicMessagesFidelityBifrost -count=1` → **5/5** (thinking / cache_control / metadata / document / anthropic-beta pass through).
- `go vet ./...` → exit 0. `go build ./...` and `go build -tags=delos_translators ./...` → exit 0.
- `make bench` (Bifrost inside): p99 overhead **1.105 ms / 1.418 ms** (gate 5 ms) — below the 2.36 ms pre-Bifrost baseline.
- Keyless agent e2e (`DELOS_AGENT_E2E=1 tests/agent/run.sh`, mock upstream): **both Claude Code and Codex complete end-to-end** through the integrated gateway.
- **Live Claude Code session through Delos** (real api.anthropic.com, user-scoped key + `DELOS_BIFROST_ANTHROPIC_WORKSPACE_ID`): completes (`AGENT-LIVE-OK`, exit 0), and **prompt caching hits** — turn 2 reports `cache_read_input_tokens: 2862` after turn 1's `cache_creation_input_tokens: 2862`.
- Revoke/expiry: per-request Postgres re-check of `is_active`/`expires_at` (no cache), `services/runtime/bifrost_auth.go` → well under the 1 s target.

## 2. Delegation table — D1/D2 rows

| Capability | Owner | Verdict |
| --- | --- | --- |
| Provider calls, passthrough, translation | Bifrost core | **Confirmed** — same-provider passthrough via `Passthrough`/`PassthroughStream`; cross-provider still on Delos's legacy translators (compiled into the default build) because the SDK does not expose an Anthropic→Responses converter |
| Client HTTP routes | thin Delos handlers over core | **Confirmed** — Bifrost's routes are fasthttp (spike verdict), so Delos keeps its paths with thin net/http handlers |
| Routing/retries/fallbacks/load balancing/circuit breaker/cache | Bifrost | **Confirmed** — moved to Bifrost; legacy path kept behind `DELOS_GATEWAY_BIFROST=0` for swap parity |
| Model catalog + pricing | Bifrost catalog | **Confirmed** — `/v1/models` from `ListAllModels`/`ModelRetrieveRequest`; live ≤1% cost check still pending a valid key |
| Virtual keys, budgets, rate limits, expiry | Bifrost governance + config store (Postgres) | **Confirmed** — SDK mode, no separate process |
| Hub-facing `KeyService` | Delos | **Confirmed** — `proto/keys/v1` Mint/Revoke/GetKeyUsage/List |
| Provider credentials (`Account`) | Delos | **Confirmed** — env-only |
| Session attribution | Delos | **Confirmed** — `gen_ai.conversation.id`/`delos.org_id`/`delos.subject`/`delos.labels` stamped from key metadata |
| Tracing | Bifrost otel plugin → observe | **Confirmed** — attribution plugin stamps the added attributes |
| `delos tail` | Delos | **Kept on thin handlers** (not Bifrost logging plugin) — a stateless gateway cannot require Bifrost's log store |

## 3. Where this document (or Delos's docs) turned out wrong

- Bifrost is **multi-module** (no root `go.mod`); the plan's single `go get github.com/maximhq/bifrost@d913ed8` cannot work (repo zips to 742 MiB > Go's 500 MiB cap). Five submodules pinned individually.
- `transports/v2` is a **module path**, not a directory (code is in `transports/bifrost-http/`).
- Consumers must replicate Bifrost's `replace` of `mark3labs/mcp-go` → `maximhq/mcp-go` fork.
- `docs/providers.md:95` ("unsupported params → structured 400") was **false** for `thinking`/`cache_control`/`metadata`/`anthropic-beta` — they were silently dropped (now pass through).
- Gap #13 names two DB-skip sites; there is a third: `pkg/database/database_integration_test.go`.

## 4. Bifrost behaves differently from Delos (and what was kept)

- **Error envelope**: Bifrost passthrough returns raw provider error bodies; the swap-parity suite caught this and DL-G restored the documented Delos envelope (`provider_error` + `provider <name>:` prefix, Anthropic 429→`rate_limit_error`). **Kept: Delos behavior.**
- **`count_tokens`**: served by passthrough to Anthropic, not Bifrost's `CountTokensRequest` (SDK does not expose the converter). **Kept: Delos URL path, additive.**
- **Cross-provider translation**: stays on Delos's legacy translators (compiled into every build) until Bifrost exposes a converter. **Kept: Delos behavior, one release.**
- **Streaming byte order**: Bifrost passthrough does not re-sort SSE keys the way the legacy translator did; note 0001 allows this on passthrough. **Kept: passthrough is byte-faithful.**
- **Retry count**: Bifrost owns retries; the 429 retry-count expectation is dropped on the Bifrost side. **Kept: Bifrost behavior.**

## 5. Decisions and remaining input

Decided (Eric delegated to LEAD, 2026-11-24):

1. **`dk_` migration** — migrate to inactive + ledger (option a); re-mint via the hub. Non-destructive; rollback clean.
2. **PR shape** — single integrated PR #13; #10–#12 closed.
3. **OpenAI key** — skipped; Codex is proven keyless (live Codex session deferred).
4. **Policy defaults** — Bifrost upgraded monthly via an upgrade-gate PR; semantic cache off until agent suites are green; spans via OTLP; transcript content = `(session_id, seq)` refs only; Delos refuses model spend, the hub refuses compute.

Remaining input (only this):

- **Anthropic workspace ID** (or a workspace-scoped key). The `sk-ant-usr-…` key is **valid but user-scoped**, so api.anthropic.com requires an `anthropic-workspace-id` header; the org/account UUIDs in the local Claude config are not valid workspace IDs. Need either the workspace ID, or a workspace-scoped `sk-ant-api03-…` key (which needs no header).

**Resolved (2026-11-24):** workspace ID `wrkspc_011iNZJvEBKYj32usSyoBbCn` works; the gateway now sends it via a new env var `DELOS_BIFROST_ANTHROPIC_WORKSPACE_ID` (commit `ade200e`). Live Claude Code session + prompt caching verified. Live Codex remains deferred (OpenAI key skipped).

## Remaining after sign-off

- Full syft SBOM diff (DL-S follow-up); Bifrost upgrade-gate + nightly re-record CI (DL-T, D0 follow-ups); live Codex session (optional — OpenAI key skipped); exact dollar-cost-to-provider-invoice comparison (usage mapping already asserted by the cassettes and live usage incl. cache tokens). D3–D5 intentionally not started.
