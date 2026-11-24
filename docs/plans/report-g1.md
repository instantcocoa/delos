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

## 5. Open questions for Eric

1. **`dk_` migration sign-off (gate condition):** migrated keys become inactive (argon2id secret unrecoverable) and must be re-minted via the hub. Approve "migrate to inactive + ledger", or keep the legacy argon2id path for one release? (note 0002, Open question 2.)
2. **Live provider keys:** the `sk-ant-oat01-…` key returns 401 from api.anthropic.com, and the env `OPENAI_API_KEY` also 401s at OpenAI. Live Claude Code/Codex end-to-end + ≤1% cost checks are blocked on valid `ANTHROPIC_API_KEY` (`sk-ant-…`) and `OPENAI_API_KEY`. Everything else is proven keyless (cassettes + mock upstream).
3. **PR shape:** I opened one integration PR (#13, draft) covering gaps 1–6 rather than one PR per gap. Split, or review as-is?
4. **Open decisions from the brief still unresolved:** Bifrost version policy (recommend monthly upgrade-gate PR), semantic cache (off until agent suites green), spans via OTLP, transcript content = `(session_id, seq)` refs only, budget authority (Delos refuses model spend).

## Remaining after sign-off

- Live end-to-end + cost accuracy once keys land; full syft SBOM diff (DL-S follow-up); Bifrost upgrade-gate + nightly re-record CI (DL-T, D0 follow-ups). D3–D5 intentionally not started.
