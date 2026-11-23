# DL-G D1 scratch notes (branch-local, not for merge)

## What landed (team/dl-g/d1-gateway, commit d37498a)

- Bifrost is the default gateway path: `services/runtime/bifrost_gateway.go`
  (thin net/http handlers over Bifrost core) + `bifrost_account.go`
  (`schemas.Account`, env-only keys).
- Legacy Delos translators behind `-tags=delos_translators` + the
  `DELOS_GATEWAY_BIFROST` runtime switch. Default (unset) = Bifrost;
  `DELOS_GATEWAY_BIFROST=0` = legacy (only meaningful in a delos_translators
  build).
- In-process hook for DL-T: `runtime.NewBifrostHTTPServer(service, logger,
  opts...)` (always Bifrost). `runtime.NewHTTPServer` is the switch-aware
  entry main.go already calls.
- Base-URL overrides kept: `DELOS_BIFROST_ANTHROPIC_BASE_URL`,
  `DELOS_BIFROST_OPENAI_BASE_URL` (plus Gemini/Ollama equivalents).

## Deviations from note 0001 (route behavior)

- `/v1/messages/count_tokens` uses Bifrost `Passthrough` to Anthropic
  (byte-faithful) instead of `CountTokensRequest`: that entry point takes a
  Responses-shaped request and needs an Anthropic->Responses converter the SDK
  does not expose. Same wire result for same-provider; cross-provider
  count_tokens is unsupported in D1.
- Cross-provider translation (Anthropic surface -> another provider, and
  vice-versa) is delegated to the legacy translator where compiled
  (delos_translators build); in a Bifrost-only build it is a clean 501.
- Models are served from Bifrost `ListAllModels`/`ModelRetrieveRequest`
  (live poll with catalog fallback inside Bifrost), filtered by Delos key scope.
- `delos tail` is fed from the thin handlers publishing into the shared
  `TailBroadcaster` (same output + tenant scoping), not yet from Bifrost's
  logging plugin: that plugin requires a log-store backend which a stateless
  gateway must not require.

## D1/D2 seams (route via LEAD)

1. **Attribution**: semconv constants added (`gen_ai.conversation.id`,
   `delos.org_id`, `delos.subject`, `delos.labels`, cache-usage attrs). Session
   id is stamped into the Bifrost context (-> `session.id` on spans). The key
   metadata -> span-attribute stamping is stubbed: DL-K's
   `runtime.MetadataSessionID/OrgID/Subject/Labels` constants and the Bifrost
   `TableVirtualKey.Metadata` map are not on this branch yet. When D2 lands,
   populate the metadata in `newBifrostContext`/handlers.
2. **Revoke/expiry <1s**: D1 keys are still the Delos `KeyAuthenticator`
   (cache TTL 1 minute, baseline behavior). The Bifrost-governance per-request
   re-check is D2 wiring on top of `authenticate`; the gate is in
   `services/runtime/bifrost_gateway.go` (`authenticate` per request).

## Proof state

- `go build ./...` OK; `go build -tags=delos_translators ./...` OK.
- `go vet ./...` OK; `go vet -tags=delos_translators ./...` OK.
- `CGO_ENABLED=0` cross-build OK: linux/amd64 + darwin/arm64 for both binaries
  (gateway ~143 MB linux/amd64, vs ~38 MB baseline).
- `DELOS_GATEWAY_BIFROST=0 go test -tags=delos_translators ./...` ALL OK
  (legacy path fully intact, incl. tests/conformance).
- `go test ./...` (default/Bifrost) fails in:
  - `services/runtime` keys_test.go HTTP tests needing a mock provider:
    TestHTTPAuthRequired, TestHTTPUsageRecorded, TestDevModeNoAuth (DL-K).
  - `tests/conformance` (DL-T) — needs `DELOS_GATEWAY_BIFROST=0` + tag, or a
    Bifrost-path harness via `NewBifrostHTTPServer`.

## go.mod needs

None added by DL-G (Bifrost core/plugins + framework/tracing were already in
the module graph from DL-S). framework/tracing is marked `// indirect`; it
becomes direct at the next `go mod tidy` (DL-S).
