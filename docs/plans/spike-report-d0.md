# D0 spike report — baseline and Bifrost spike

Date: 2026-11-23 · Lead: Claude Code (LEAD) · Status: **complete — awaiting gate G0 approval**

Baseline tag `d0-baseline` = main @ `6e73783` (pushed to origin). No PRs opened yet: both team branches are held local pending G0 approval (see open questions).

## 1. What changed, with evidence

### DL-S (spike and toolchain) — commit `24c7844` on `team/dl-s/d0-spike`
Files: `go.mod`, `go.sum`, `Dockerfile.delos`, `Dockerfile.gateway`, `cmd/delos-gateway/{main.go,bifrost_wiring.go,bifrost_plugins.go,bifrost_stub.go}`, `D0_SPIKE_NOTES.md` (branch-local scratch, not for merge). Nothing pushed.

- **Go bump**: `go.mod` `go 1.25.5` → `go 1.27.4` (no `toolchain` directive — Go drops it as redundant). `GOTOOLCHAIN=go1.27.4 go version` auto-downloads on this host.
- **Bifrost added** (pinned to `d913ed8`, 2026-11-23 15:40): `core`, `framework`, `plugins/governance`, `plugins/logging`, `plugins/otel` as submodule pseudo-versions. Requires consumer-side `replace github.com/mark3labs/mcp-go => github.com/maximhq/mcp-go v0.0.0-20261102181017-b7da3bc145a1`. `go.mod` grew 69→196 lines; `go.sum` 172→551.
- **Spike wiring behind `-tags=bifrost`**: `bifrost_stub.go` (`!bifrost`) returns a nil handler so the default build is byte-identical in behavior; `bifrost_wiring.go` (`bifrost`) implements a thin net/http handler over Bifrost `PassthroughStream` with an `envAccount` (`schemas.Account`, keys env-only). `main.go` mounts it for `/v1/messages`, `/v1/chat/completions`, `/v1/responses`; everything else falls through to the Delos runtime handler.
- **Build matrix** (`CGO_ENABLED=0`, all four targets): all pass. `go vet ./...`, `go vet -tags=bifrost ./...`, `go vet -tags=integration ./...` all exit 0; `go test ./...` all `ok`.

### DL-T (tests and proof) — commit `677650a` on `team/dl-t/d0-baseline`
Files: `.github/workflows/ci.yaml`, `Makefile`, `TESTING.md`, `tests/skipguard/main.go` (new), `tests/conformance/messages_fidelity_test.go` (new), `D0_BASELINE_NOTES.md` (branch-local scratch). Nothing pushed.

- **Skip-fail gate**: `tests/skipguard` wraps `go test -json`, fails on any skip in `pkg/database`, `services/prompt`, `services/runtime` when `DELOS_TEST_REQUIRE_DB=1`. CI `test` job (Postgres already provisioned) now uses it; local dev keeps skip-friendly `make test`/`make test-unit`; new `make test-require-db`. Proven both ways (8 skips → exit 1 without DB; exit 0 with DB). No cross-team file edits.
- **`/v1/messages` baseline** pinned by `tests/conformance/messages_fidelity_test.go` (mock upstream): see section 3.
- **Pre-Bifrost bench baseline**: p99 gateway overhead **2.371 ms / 2.343 ms** (two runs), gate 5 ms → **PASS**.

## 2. Delegation table — "verify in spike" rows

All eight **confirmed** (DL-S; evidence from Bifrost source at `d913ed8`):

| Row | Claim | Verdict | Evidence |
| --- | --- | --- | --- |
| Client HTTP routes | Bifrost handlers mount inside `delos-gateway` | **Overturned — fasthttp, not mountable** | Bifrost HTTP server is fasthttp (`transports/bifrost-http/server/server.go:22` `router.Router`; handlers take `*fasthttp.RequestCtx`). Thin net/http handlers over core are required — exactly what the spike wiring did. |
| Governance in SDK mode | Governance plugin + config store run in Postgres, SDK mode | **Confirmed** | `plugins/governance/main.go:243 Init / :324 InitFromStore` (library, no server); `framework/configstore/store.go:1323 newPostgresConfigStore`; `tables/virtualkey.go:261 ExpiresAt ("expires_at")`, `:39 Budgets`, `:38 RateLimit`. |
| Catalog prices ≤1% | Bifrost prices match provider usage within 1% | **Mechanism confirmed; live check not run** | `tables/modelpricing.go` per-model input/output/cache rates; `datasheet/cost.go computeTextCost` prices prompt/cache-read/cache-write separately. Dollar ≤1% match needs a provider key (blocked — no valid `sk-ant-` key). |
| Static builds | `CGO_ENABLED=0` on linux/darwin × amd64/arm64 | **Confirmed** | All four targets built for both binaries; bifrost-tagged gateway also builds on all four. |
| Binary size | Before/after | **Confirmed** | See section 6. |
| SBOM diff | New deps + licenses; Apache-2.0 attribution | **Confirmed (approximate)** | Bifrost LICENSE = Apache-2.0 at each module root; no NOTICE in module zips; MPL-2.0 components documented in Bifrost's `THIRD_PARTY_NOTICES.md`. Full syft SBOM not generated (tooling not installed) — approximated via `go list -m all` + module-cache license scan. |
| Transitive deps | fasthttp, sonic, GORM, … | **Confirmed** | fasthttp v1.74.0 (MIT), sonic v1.15.3 (Apache-2.0), GORM+pgx (MIT), starlark (BSD-3), tiktoken (MIT), zerolog (MIT), Azure + Google Cloud SDKs (MIT), a2a-go (Apache-2.0). Non-Apache/MIT/BSD: only MPL-2.0 (`hashicorp/go-version`, `philippgille/chromem-go`) — file-level, permissive-compatible. No GPL/AGPL/SSPL/BUSL. |
| Cache-read tokens | Bifrost surfaces + costs cache tokens | **Confirmed** | `BifrostLLMUsage.PromptTokensDetails.CachedReadTokens/CachedWriteTokens` (`core/schemas/chatcompletions.go:2265-2266`); Anthropic maps `cache_read_input_tokens`/`cache_creation_input_tokens` (`providers/anthropic/chat.go:1480-1490`); costed in `datasheet/cost.go:1150-1192`; spans emit `gen_ai.usage.cache_read.input_tokens` / `gen_ai.usage.cache_creation.input_tokens` (`schemas/trace.go:1077-1078`). |

## 3. Where the plan (or Delos's docs) turned out wrong

- **Bifrost is multi-module** — no root `go.mod`. The plan's `go get github.com/maximhq/bifrost@d913ed8` cannot work: the whole repo zips to 742 MiB > Go's 500 MiB `MaxZipFile`. Must add the five submodules individually. (`delos-harness.md` "Delegation to Bifrost" reads from `maximhq/bifrost` as if a single module.)
- **Consumer must replicate Bifrost's `replace`** of `mark3labs/mcp-go` by the `maximhq/mcp-go` fork — all five submodules declare it, and Go ignores `replace` in dependency `go.mod`s. (First `go build -tags=bifrost` failed on `undefined: client.WithRequestClientCapabilities` until this replace was added.)
- **`transports/v2` is a module path, not a directory** — the code lives in `transports/bifrost-http/`.
- **`toolchain go1.27.4` is redundant** and auto-dropped by `go get`/`tidy`; `go 1.27.4` alone is sufficient.
- **Delos doc inaccuracy (answered by DL-T)**: `/v1/messages` silently drops `thinking`, `cache_control` on blocks, top-level `metadata`, and `anthropic-beta` (HTTP 200) — contradicting `docs/providers.md:95` ("structured 400 — never silently dropped"). A `document` block does 400 (`invalid_request_error`, unsupported block type, no provider call). Pinned in `tests/conformance/messages_fidelity_test.go`.
- **Gap #13 names two DB-skip sites; there is a third** with the same convention: `pkg/database/database_integration_test.go` (skipguard covers all three).
- **Dockerfiles pinned `golang:1.25-alpine`** — DL-S bumped both to `golang:1.27-alpine`. The release/CI workflows' `GO_VERSION` (DL-T's file) still says `1.25` and needs a 1.27.4 bump (follow-up).

## 4. Bifrost behavior vs Delos documented behavior

- **Transport**: Bifrost's client routes are fasthttp, so they cannot be mounted under Delos's net/http server. **Behavior kept**: Delos keeps its URL paths (`/v1/messages`, `/v1/chat/completions`, `/v1/responses`, …) served by thin net/http handlers over Bifrost core — the spike proved this exact shape works, including SSE.
- Errors / usage / streaming-order parity is D1 surface; first signal (DL-S §4): a Claude Code headless turn completes end-to-end through Bifrost `PassthroughStream`, with Anthropic beta headers and `cache_control` captured intact by the mock upstream.

## 5. Open questions for Eric

1. **Live-provider runs need a valid `ANTHROPIC_API_KEY`** (`sk-ant-…`; the env var present is `sk-2a5f…`, which Claude Code treats as OpenAI-style). With one, DL-S can finish the ≤1% cost check and a real headless turn. Should we provision one, or accept the mock-upstream proof for G0?
2. **Branch push/PR policy**: nothing is pushed. Approve pushing `team/dl-s/d0-spike` + `team/dl-t/d0-baseline` and opening PRs now, or after you read the report?
3. **`GO_VERSION` follow-up**: DL-T should bump `1.25` → `1.27.4` in `.github/workflows/ci.yaml` + `release.yaml` (DL-S can't touch DL-T's files). OK to route?
4. **Scratch notes**: `D0_SPIKE_NOTES.md` / `D0_BASELINE_NOTES.md` are branch-local raw logs — drop them from the merge, or keep one condensed version?
5. **Binary size**: Bifrost-tagged `delos-gateway` grows ~102 MB (→ ~140 MB, +271%). Expected per the plan's "costs accepted", but worth your sign-off given the static-release posture.

## 6. Sizes (linux/amd64, `CGO_ENABLED=0`)

| Binary | Before (d0-baseline) | After | Δ |
| --- | --- | --- | --- |
| delos | 46,386,270 | 47,694,634 | +1.31 MB (+2.8%) — Go 1.27.4 only |
| delos-gateway (plain) | 37,740,236 | 38,828,649 | +1.09 MB — Go 1.27.4 only |
| delos-gateway `-tags=bifrost` | 37,740,236 | 139,994,403 | +102 MB (+271%) — Bifrost core + governance/otel/logging + deps |

## LEAD-verified environment facts (pre-team)

- Tag `d0-baseline` created and pushed; resolves to `6e73783f77ebbd5e7ec2dec90e0bc82ef8fd687f`.
- Host `go version` = `go1.26.10 linux/amd64`; `go.mod` declared `go 1.25.5`.
- `GOTOOLCHAIN=go1.27.4 go version` succeeds (auto-downloads).
- `proxy.golang.org` reachable; Bifrost `@latest` = `659e17e` (16:37) which is **later** than the pinned `d913ed8` (15:40) — pinning to `d913ed8` was the right call per the plan.
- `make bench` already existed (`TestGatewayOverheadP99`); `.goreleaser.yaml` already builds 4-target CGO-free with syft SBOMs + cosign.
