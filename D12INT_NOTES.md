# D1/D2 integration notes (DL-T) — branch-local scratch, NOT for merge

Branch: `team/dl-t/d1-integration` (from `team/integration/d1-d2` = `9ddf240`).
These are raw notes backing the DL-T integration report to LEAD. Delete before merge.

## What changed (all DL-T-owned, tests/conformance)

- `harness.go`: `serveService` now builds `runtime.NewLegacyHTTPServer` so the
  cassettes (`TestCassettes`) and the failure-injection tests pin the exact
  pre-D1 behavior. Added `serveBifrostService` (`runtime.NewBifrostHTTPServer`)
  for the Bifrost-path suites.
- `swap_parity_test.go`: rewritten to drive the two exported constructors.
  Legacy side = `serveGateway` (legacy server); Bifrost side =
  `serveBifrostService` + `t.Setenv` of `DELOS_BIFROST_<PROVIDER>_BASE_URL`
  and the provider key env, pointed at the same fake upstream. Cross-provider
  cassettes delegate to the legacy translator on both paths.
- `messages_fidelity_test.go`: now drives the Bifrost path directly
  (`serveBifrostService` + `DELOS_BIFROST_ANTHROPIC_BASE_URL` -> fake upstream)
  and asserts thinking / cache_control / metadata / document / anthropic-beta
  all pass through (note 0001 / gap 1). No env-switch gate.
- `overhead_test.go`: `make bench` now measures the Bifrost path
  (`serveBifrostService`, model `gpt-4o` so `providerForModel` takes OpenAI
  passthrough, `OPENAI_API_KEY` + `DELOS_BIFROST_OPENAI_BASE_URL` -> upstream).

## Test-side corrections made along the way

- Streaming cassettes: `body_contains` pins the legacy translator's
  alphabetically-sorted SSE keys (`{"text":"","type":"text"}`), while Bifrost
  passthrough is byte-faithful to the provider (`{"type":"text","text":""}`).
  note 0001 allows SSE byte order to differ for passthrough, so
  `assertBodyBifrost` skips `body_contains` for streaming cassettes and relies
  on the cassette's `sse` expectation (deltas/done/events).
- 429 retry count: the cassette records 2 upstream responses for the legacy
  retry loop; Bifrost owns retries now (note 0001) and made 1 call.
  `assertUpstreamBifrost` keeps the overflow check but drops the exact
  consumed==queued equality for the Bifrost side (client-visible status/body
  is the parity signal).

## Commands + key output

    git checkout -b team/dl-t/d1-integration team/integration/d1-d2
    buf generate
    CGO_ENABLED=0 go test ./tests/conformance/ -run TestAnthropicMessagesFidelityBifrost -count=1 -v
        -> PASS (5/5 pass through)
    CGO_ENABLED=0 go test ./tests/conformance/ -run TestSwapParity -count=1 -v
        -> 33/38 subtests PASS; 5 FAIL (below)
    CGO_ENABLED=0 make bench
        -> over 2000 requests: gateway p50=715.124µs p95=999.534µs p99=1.592228ms |
           direct-to-upstream p50=261.802µs p99=487.572µs | p99 gateway overhead=1.104656ms
        -> second run: p99 gateway overhead=1.418227ms
        -> PASS (gate 5ms). Pre-Bifrost baseline was ~2.36ms.
    CGO_ENABLED=0 go test ./... -count=1
        -> all packages ok except tests/conformance (5 swap-parity failures).
    go vet ./tests/conformance/ ; go vet -tags=integration ./...  -> both exit 0.

## Remaining failures (all DL-G-owned, services/runtime — not edited by DL-T)

1. `anthropic/error_400`, `anthropic/error_429`, `openai/error_400`,
   `openai/error_429` — Bifrost passthrough forwards the provider's raw error
   body instead of the Delos error envelope. Legacy maps provider errors to
   `code: "provider_error"` + `"provider <name>: "` message prefix (see
   services/runtime/openai_api.go `writeProviderError`,
   anthropic_api.go `writeAnthropicProviderError`). note 0001 says error
   shapes keep the documented Delos behavior. Fix: `writeBifrostError`
   (services/runtime/bifrost_gateway.go ~line 792) should apply the same
   mapping. Observed: `error.code` = null / `rate_limit_exceeded` / raw body
   with no `provider <name>:` prefix.
2. `gemini/embeddings` — model `text-embedding-004` is classified as OpenAI by
   `wellKnownProvider` (the `text-embedding-` prefix matches before
   `text-embedding-004` -> gemini; see services/runtime/service.go
   `wellKnownProvider`, evaluated first-match). The Bifrost
   `providerForModel` (bifrost_gateway.go ~line 686) therefore routes the
   Gemini embeddings request to OpenAI passthrough and the call fails with
   status 401 (wrong key). Fix in DL-G: consult the provider registry /
   reorder the prefix match.

## Bench

Bifrost p99 gateway overhead: ~1.10–1.42 ms (two runs), PASS vs 5 ms gate and
faster than the 2.36 ms pre-Bifrost baseline.
