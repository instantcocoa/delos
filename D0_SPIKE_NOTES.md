# D0 Spike notes — DL-S (Bifrost toolchain spike)

Branch: `team/dl-s/d0-spike` (cut from tag `d0-baseline` = main @ `6e73783`).
This file is branch-local scratch, not for merge. Raw command output and findings.

## TL;DR

- Bifrost is **not** a single module `github.com/maximhq/bifrost`. It is a **multi-module
  repo with no root go.mod**. The plan's `go get github.com/maximhq/bifrost@d913ed8` is wrong
  on two counts: (1) no such module exists, and (2) the whole-repo "module" zips to **742 MiB**
  and exceeds Go's hardcoded `MaxZipFile = 500 MiB`, so `go get` of the root path fails with
  "downloaded zip file too large".
- Correct consumption: the submodules, each pinned at commit `d913ed8`:
  - `github.com/maximhq/bifrost/core`           → `v1.15.3-0.20261123154047-d913ed88aee2`
  - `github.com/maximhq/bifrost/framework`      → `v1.12.3-0.20261123154047-d913ed88aee2`
  - `github.com/maximhq/bifrost/plugins/governance` → `v1.12.3-0.20261123154047-d913ed88aee2`
  - `github.com/maximhq/bifrost/plugins/logging`    → `v1.12.3-0.20261123154047-d913ed88aee2`
  - `github.com/maximhq/bifrost/plugins/otel`       → `v1.6.8-0.20261123154047-d913ed88aee2`
- Bifrost core depends on a **`replace` of `mark3labs/mcp-go` by a fork** that is NOT
  propagated to consumers. Delos's go.mod must carry the same replace or the build fails with
  `undefined: client.WithRequestClientCapabilities`.
- A real headless `claude -p` session completed through `delos-gateway -tags=bifrost` →
  Bifrost `PassthroughStream` → a mock Anthropic upstream (exit 0). No valid Anthropic key
  exists in the environment, so the real-provider path could not be exercised.

## 1. Environment

- `go version`: go1.26.10 (default); `GOTOOLCHAIN=go1.27.4 go version` → go1.27.4 (auto-download works).
- `claude --version`: 2.1.330 (Claude Code) at /home/emaland/.local/bin/claude.
- `ANTHROPIC_API_KEY` is set (len 35) but prefix is `sk-2a5f…` — NOT a valid Anthropic key
  (`sk-ant-…`). Real-provider headless run is blocked by this.
- `buf`: 1.73.0 (used `make proto` to generate `gen/go/`, which is gitignored).

## 2. Git/branch

```
git rev-parse d0-baseline       -> 698753ca... (annotated tag object)
git rev-parse 'd0-baseline^{}'  -> 6e73783f77ebbd5e7ec2dec90e0bc82ef8fd687f  (= main HEAD)
```
Created `team/dl-s/d0-spike` at `6e73783`.

## 3. Go bump

- `go.mod`: `go 1.25.5` → `go 1.27.4`.
- I first added `toolchain go1.27.4`, but `go get`/`go mod tidy` DROPS it: an explicit
  `toolchain go1.27.4` is redundant when `go 1.27.4` already implies toolchain >= 1.27.4.
  So the final go.mod has only `go 1.27.4` (sufficient; GOTOOLCHAIN=auto fetches 1.27.4).

## 4. Bifrost version resolution

- `go list -m github.com/maximhq/bifrost@latest` → `v0.0.0-20261123163700-659e17e2e4a1`
  (commit `659e17e2e4a1`, "feat(warp): virtual key for callers with no user identity",
  committed 2026-11-23T16:37:00Z) — this is **later** than d913ed8.
- `d913ed8` = commit `d913ed88aee2d7f8c74969e563f2bc30d7b550c3`, committed 2026-11-23T15:40:47Z,
  "fix(governance): declare policy_access_denied on the credential-required refusal (#10355)".
- So `@latest` is ~1h AFTER the pinned commit; we pinned `d913ed8` as the plan says.
- The whole repo at d913ed8: 7007 files, 742.5 MiB. Go's `codehost.MaxZipFile = 500<<20`
  (cmd/go/internal/modfetch/codehost/codehost.go:35) → `go get github.com/maximhq/bifrost@d913ed8`
  fails: `downloaded zip file too large`. The bulk is `docs/media/*` (gif/mp4/png) and
  `tests/e2e/api/collections/provider-harness.json` (22 MB).

## 5. go.mod/go.sum change shape

`go.mod` grew 69 → 196 lines; `go.sum` 172 → 551 lines; `go list -m all` = 599 modules.
Direct additions (bifrost) plus the mandatory replace:

```
require (
    github.com/maximhq/bifrost/core v1.15.3-0.20261123154047-d913ed88aee2
    github.com/maximhq/bifrost/plugins/governance v1.12.3-0.20261123154047-d913ed88aee2
    github.com/maximhq/bifrost/plugins/logging v1.12.3-0.20261123154047-d913ed88aee2
    github.com/maximhq/bifrost/plugins/otel v1.6.8-0.20261123154047-d913ed88aee2
    // indirect
    github.com/maximhq/bifrost/framework v1.12.3-0.20261123154047-d913ed88aee2
)

replace github.com/mark3labs/mcp-go => github.com/maximhq/mcp-go v0.0.0-20261102181017-b7da3bc145a1
```

All five submodules are at commit `d913ed8` (pseudo-version timestamp 20261123154047).
`go get` also emitted: `github.com/pierrec/lz4/v4@v4.1.27: retracted by module author`
(pre-existing Delos dep from parquet-go, not introduced by Bifrost).

## 6. Build matrix (CGO_ENABLED=0)

All four targets built for each binary. linux/amd64 sizes in bytes (cross-compiled):

| binary | linux/amd64 | linux/arm64 | darwin/amd64 | darwin/arm64 |
|---|---|---|---|---|
| delos (plain) | 47,694,634 | 44,113,629 | 49,192,304 | 45,936,562 |
| delos-gateway (plain) | 38,828,649 | 35,869,813 | 40,042,544 | 37,434,290 |
| delos-gateway (-tags=bifrost) | 139,994,403 | 128,238,893 | 146,349,760 | 135,507,394 |

All OK. `go vet ./...` → exit 0. `go vet -tags=bifrost ./...` → exit 0.
`go vet -tags=integration ./...` → exit 0. `go test ./...` → all `ok`, no failures
(last line: `ok github.com/instantcocoa/delos/tests/conformance 3.562s`).

### Baseline sizes (before, linux/amd64, built from d0-baseline)

- delos: 46,386,270
- delos-gateway: 37,740,236

### Size delta

- delos: 46,386,270 → 47,694,634 (+1.31 MB, +2.8%) — Go 1.27.4 toolchain only (no Bifrost in delos).
- delos-gateway plain: 37,740,236 → 38,828,649 (+1.09 MB, +2.9%) — Go toolchain only.
- delos-gateway -tags=bifrost: 37,740,236 → 139,994,403 (+102.25 MB, +271%) — Bifrost + plugins
  + transitive deps linked in.

## 7. Headless Claude Code through PassthroughStream (task 4)

Wiring: new files in `cmd/delos-gateway/`:
- `bifrost_wiring.go` (`//go:build bifrost`) — `bifrostHandler()` builds `bifrost.Init` with an
  env-backed `schemas.Account`, and a thin net/http handler that calls
  `Passthrough` / `PassthroughStream` and streams raw provider bytes back.
- `bifrost_plugins.go` (`//go:build bifrost`) — blank imports of governance/otel/logging plugins.
- `bifrost_stub.go` (`//go:build !bifrost`) — returns nil.
- `main.go` — mounts the bifrost handler for `/v1/messages`, `/v1/chat/completions`,
  `/v1/responses` when present, with `/` falling through to the Delos runtime.

Base URLs are env-overridable: `DELOS_BIFROST_ANTHROPIC_BASE_URL`,
`DELOS_BIFROST_OPENAI_BASE_URL`.

Path taken: **mock upstream** (no valid Anthropic key). `command -v claude` present; key invalid.

Proof 1 — OpenAI SSE passthrough through the GIVEN `tests/compat/mockupstream` (127.0.0.1:9099):
`curl -sN -X POST http://127.0.0.1:8080/v1/chat/completions -d '{"model":"delos-mock","stream":true,...}'`
streamed the full mock SSE back (word-by-word deltas + usage + `data: [DONE]`). Exit 0.

Proof 2 — Claude-shaped `/v1/messages` through a scratch mock (127.0.0.1:9199, /tmp only):
sent body with `thinking`, `metadata`, `cache_control` and `anthropic-beta` headers; the mock
received them intact and streamed a valid Anthropic SSE message back. The mock's captured JSON
shows `anthropic-beta: prompt-caching-2024-07-31,interleaved-thinking-2025-05-14,...` and the
full body with thinking/cache_control/metadata preserved.

Proof 3 — real headless CLI against the gateway+mock:
```
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 ANTHROPIC_API_KEY=sk-ant-mock claude -p "reply with exactly: mock-ok" --model claude-sonnet-4-5
→ "mock"  (exit 0)
```
The mock captured Claude Code's real request: `anthropic-beta:
claude-code-20250219,interleaved-thinking-2025-05-14,context-management-2025-06-27,...`,
full system-reminder blocks, and `cache_control` on the final user block. This proves a
headless Claude Code turn completes through delos-gateway → Bifrost PassthroughStream.

Blocked: real-provider run (no valid `sk-ant-` key in env). Real path is "blocked: missing valid key",
not "mock only by choice".

## 8. Verify-in-spike rows (evidence)

### (a) Do Bifrost HTTP handlers mount inside delos-gateway (net/http), or fasthttp?
**CONFIRMED fasthttp.** `transports/go.mod` declares module
`github.com/maximhq/bifrost/transports/v2`; the server code lives in
`transports/bifrost-http/` (the plan's "transports/v2" is the module path, not a dir).
`transports/bifrost-http/server/server.go:22` imports `github.com/fasthttp/router`,
`:284` `Router *router.Router`; handlers take `*fasthttp.RequestCtx`. They do NOT mount in a
net/http server; Delos needs thin net/http handlers over core (the spike did exactly this) or a
fasthttp→net/http adaptor.

### (b) Governance in SDK mode with Postgres?
**CONFIRMED.** `plugins/governance/main.go:243` `Init(...)` and `:324` `InitFromStore(ctx,
config, logger, governanceStore, configStore, modelCatalog, mcpCatalog, inMemoryStore)` construct
the plugin as a library (SDK), no server process. `framework/configstore/store.go:1323-1326`
`newPostgresConfigStore` provides a Postgres backend (`PostgresConfig`). Virtual keys/budgets/
rate limits/expiry confirmed at `framework/configstore/tables/virtualkey.go`: `ExpiresAt
*time.Time` (`expires_at`, :261), `Budgets []TableBudget` (:39), `RateLimit *TableRateLimit`
(:38). GORM + `gorm.io/driver/postgres` + `jackc/pgx/v5` are the backing store.

### (c) Catalog prices match provider usage within 1%?
**MECHANISM CONFIRMED; live 1% check NOT VERIFIABLE without a provider key.**
`framework/configstore/tables/modelpricing.go` `TableModelPricing` carries per-model
input/output/cache rates (incl. `cache_read_input_token_cost`,
`cache_creation_input_token_cost`, long-context tiers). `framework/modelcatalog/datasheet/cost.go`
`computeTextCost` prices prompt/completion/cache-read/cache-write separately, clamping cached
counts to prompt tokens. Could not compare dollar values against live provider `usage` (no key).

### (d) Static CGO_ENABLED=0 builds on all four targets?
**CONFIRMED** — see build matrix table in §6; all 12 binary-target combinations built clean.

### (e) Binary size before vs after?
**CONFIRMED (measured)** — see §6. Gateway +102 MB with `-tags=bifrost` (139,994,403 bytes).

### (f) SBOM diff + Bifrost license/NOTICE?
**CONFIRMED.** Bifrost LICENSE at every module root is Apache-2.0. No `NOTICE` file ships in the
per-module zips; the repo-root `THIRD_PARTY_NOTICES.md` documents MPL-2.0 third-party components
(e.g. gormigrate-derived `framework/migrator/migrator.go`) and embedded source. Delos must ship
the Apache-2.0 text and the MPL-2.0 attribution. 168 new module paths in go.sum.

### (g) New transitive deps + licenses?
**CONFIRMED.** Notable: valyala/fasthttp v1.74.0 (MIT), fasthttp/websocket (BSD-3),
bytedance/sonic v1.15.3 (Apache-2.0) + cloudwego/base64x, gorm.io/gorm v1.31.1 +
driver/postgres + driver/clickhouse + driver/sqlite (MIT), jackc/pgx/v5 (MIT),
canonical/starlark (BSD-3), maxmimhq/mcp-go fork (MIT), tiktoken-go + weaviate/tiktoken-go
(MIT), rs/zerolog (MIT), a2aproject/a2a-go/v2 (Apache-2.0), Azure SDK + Google Cloud SDK
(MIT), keybase/go-keychain (MIT), hajimehoshi/go-mp3 (Apache-2.0), golang-jwt/jwt/v5 (MIT).
Non-Apache/MIT/BSD outliers in the full scan: **MPL-2.0** only — `hashicorp/go-version` and
`philippgille/chromem-go` (both file-level copyleft, permissive-compatible). No GPL/AGPL/SSPL/BUSL.

### (h) Cache-read tokens on later turns?
**CONFIRMED.** `schemas.BifrostLLMUsage.PromptTokensDetails` is `ChatPromptTokensDetails`
(`core/schemas/chatcompletions.go:2259`) with `CachedReadTokens` (`cached_read_tokens`, :2265)
and `CachedWriteTokens` (`cached_write_tokens`, :2266). Anthropic `cache_creation_input_tokens`/
`cache_read_input_tokens` map into these (`core/providers/anthropic/chat.go:1480-1490`,
`passthrough_usage.go:82-96`). `computeTextCost` bills them at `cacheReadInputRate` and
`cacheCreationInputRate` (`framework/modelcatalog/datasheet/cost.go:1150-1192`). Spans emit
`gen_ai.usage.cache_read.input_tokens` / `gen_ai.usage.cache_creation.input_tokens`
(`core/schemas/trace.go:1077-1078`).

## 9. Plan inaccuracies / things that turned out wrong

1. **Bifrost is multi-module, not a single module.** Plan ("Bifrost module:
   `github.com/maximhq/bifrost`", `core/bifrost.go`, `core/schemas/...`, `plugins/...`,
   `framework/configstore/...` as subpackages) is wrong. Real paths:
   `github.com/maximhq/bifrost/{core,framework,plugins/governance,plugins/logging,plugins/otel,transports/v2}`,
   each its own module with independent tags. `go get github.com/maximhq/bifrost@d913ed8` fails.
2. **The whole-repo tree zips to 742 MiB > Go's 500 MiB MaxZipFile** (docs/media + e2e fixtures),
   so the root path can never be fetched as a module. Submodules zip only their subdir and are fine.
3. **Bifrost core needs a consumer-side `replace` of `mark3labs/mcp-go` → `maximhq/mcp-go` fork**
   (present in all 5 submodule go.mods). Without it, build fails
   `undefined: client.WithRequestClientCapabilities`.
4. **`transports/v2` is the module path, not a directory.** The code is under
   `transports/bifrost-http/`.
5. **Explicit `toolchain go1.27.4` is auto-dropped** (redundant with `go 1.27.4`); the plan's
   "raise go/toolchain" is satisfied by `go 1.27.4` alone.
6. **`go mod tidy` does not take `-tags`** in Go 1.27 (it considers all build tags by default),
   so `go mod tidy -tags=bifrost` errors; plain `go mod tidy` is correct.
7. **Delos Dockerfiles pin `golang:1.25-alpine`** — must become 1.27 (I bumped both to
   `golang:1.27-alpine`; I own them). The release workflow's Go version (DL-T's file) also needs
   a 1.27.4 bump — flagged, not edited.

## 10. Files changed on this branch

- `go.mod`, `go.sum`
- `cmd/delos-gateway/main.go` (mount bifrost handler)
- `cmd/delos-gateway/bifrost_wiring.go` (new, `//go:build bifrost`)
- `cmd/delos-gateway/bifrost_plugins.go` (new, `//go:build bifrost`)
- `cmd/delos-gateway/bifrost_stub.go` (new, `//go:build !bifrost`)
- `Dockerfile.delos`, `Dockerfile.gateway` (golang:1.25-alpine → golang:1.27-alpine)
- `D0_SPIKE_NOTES.md` (this file, branch-local)

Not touched (other teams'): `tests/`, `Makefile`, `.github/workflows/`, `services/runtime/*`,
`.goreleaser.yaml` (no Go pin to change).

## 11. Raw command output highlights

```
go list -m github.com/maximhq/bifrost@latest
  github.com/maximhq/bifrost v0.0.0-20261123163700-659e17e2e4a1
go list -m github.com/maximhq/bifrost@d913ed8
  github.com/maximhq/bifrost v0.0.0-20261123154047-d913ed88aee2
go get github.com/maximhq/bifrost@d913ed8
  go: downloaded zip file too large
go get github.com/maximhq/bifrost/core@d913ed8
  go: added github.com/maximhq/bifrost/core v1.15.3-0.20261123154047-d913ed88aee2
go get github.com/maximhq/bifrost/plugins/governance@d913ed8 github.com/maximhq/bifrost/plugins/otel@d913ed8 github.com/maximhq/bifrost/plugins/logging@d913ed8
  go: added github.com/maximhq/bifrost/plugins/governance v1.12.3-0.20261123154047-d913ed88aee2
  go: added github.com/maximhq/bifrost/plugins/logging v1.12.3-0.20261123154047-d913ed88aee2
  go: added github.com/maximhq/bifrost/plugins/otel v1.6.8-0.20261123154047-d913ed88aee2
go get github.com/maximhq/bifrost/framework@d913ed8
  go: upgraded github.com/maximhq/bifrost/framework v1.12.2 => v1.12.3-0.20261123154047-d913ed88aee2
CGO_ENABLED=0 go build -tags=bifrost ./cmd/delos-gateway   # fails first: undefined: client.WithRequestClientCapabilities
go mod edit -replace github.com/mark3labs/mcp-go=github.com/maximhq/mcp-go@v0.0.0-20261102181017-b7da3bc145a1
CGO_ENABLED=0 go build -tags=bifrost ./cmd/delos-gateway   # OK
```
