# DL-G D1 integration seam notes (branch-local scratch)

Branch: `team/dl-g/d1-integration` (from `team/integration/d1-d2` @ 296e71a).

## What changed

1. **Per-request key re-check (revoke/expiry <1s).**
   - `services/runtime/bifrost_auth.go`: `bifrostKeyAuthenticator.Authenticate` calls
     `configstore.ConfigStore.GetVirtualKeyByValue` on every request (no cache),
     checks `TableVirtualKey.IsActiveValue()` + `ExpiresAt`, and maps to
     `KeyRecord`/`VirtualKey{ID,Name,Models}` for model scope.
   - `authenticate` (auth.go) uses it when `s.bifrostKeys != nil && useBifrostPath()`;
     `recordUsage` skips the legacy ledger for governance keys.
   - Latency = one Postgres round-trip per request (ms). No in-process cache, so
     revoke/expiry lands on the next request on every replica.

2. **Key metadata -> span stamping.**
   - `delosAttributionPlugin` (LLMPlugin in bifrost_auth.go) stamps, on the root
     span via the framework tracer handle: `gen_ai.conversation.id` (session),
     `delos.org_id`, `delos.subject`, `delos.labels` from `MetadataSessionID/OrgID/
     Subject/Labels` resolved at auth time.

3. **Cross-provider translation in the default build.**
   - Removed `//go:build delos_translators` from the translator files and test
     files; `registerLegacyRoutes`/`serveLegacyRoute` are always compiled.
   - `useBifrostPath()` is now env-only (`DELOS_GATEWAY_BIFROST=0` -> legacy).
   - Added `runtime.NewLegacyHTTPServer` (forced legacy) alongside
     `runtime.NewBifrostHTTPServer` (forced Bifrost) for tests/swap-parity.
   - Test helpers pinned to `NewLegacyHTTPServer` so the translator tests pass in
     the default build.

4. **main.go** (DL-S's file, minimal edit): opens `runtime.OpenBifrostKeyStore`
   and passes `runtime.WithBifrostKeyStore` alongside the legacy
   `WithKeyStore`.

## Proof

- `CGO_ENABLED=0 go build ./...` OK; `CGO_ENABLED=0 go vet ./...` OK.
- `CGO_ENABLED=0` cross-build OK: linux/amd64 + darwin/arm64 (gateway ~149 MB).
- `DELOS_GATEWAY_BIFROST=0 go test ./...` — ALL OK (16 pkgs incl. conformance).
- `go test ./...` (default/Bifrost) — 2 failures in services/runtime
  (TestHTTPAuthRequired, TestHTTPUsageRecorded, both DL-K) + TestCassettes
  (DL-T). Cross-provider conformance (TestStreamFailures/failover/overhead)
  now passes in the default build.

## Precise fix for DL-K (report only — did not edit keys_test.go)

`services/runtime/keys_test.go` `authTestServer` (line ~117) and
`TestHTTPBudgetExceeded` (line ~180) call `NewHTTPServer` (now Bifrost-default),
so they no longer exercise the legacy mock provider. Fix: replace those two
`NewHTTPServer` calls with `NewLegacyHTTPServer` (exported by DL-G), or run the
package with `DELOS_GATEWAY_BIFROST=0`.

## go.mod

None added by DL-G. `framework/configstore`, `framework/tracing`,
`framework/postgresconn` were already in the module graph from DL-K's D2 work
(framework is marked `// indirect`; becomes direct at the next `go mod tidy`).
