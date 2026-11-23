# D1/D2 suites notes (DL-T) — branch-local scratch, NOT for merge

Branch: `team/dl-t/d1-d2-suites` (from `team/integration/d1-base` = `7081274`).
These are raw notes backing the DL-T report to LEAD. Delete before any merge.

## What was written (all DL-T-owned)

- `tests/conformance/swap_parity_test.go` — replays every cassette through the old
  Delos translators and the Bifrost path and asserts parity (same status + both paths
  satisfy the cassette's expectation set, which pins usage fields). Gated on the
  Bifrost switch; skips until it is enabled. `bifrostGateway()` is the one hook DL-G
  must provide.
- `tests/conformance/messages_fidelity_test.go` — the D1 contract for `/v1/messages`,
  replacing the D0 baseline pinning (which lives on `team/dl-t/d0-baseline`): thinking /
  cache_control / metadata / anthropic-beta pass through; `document` still 400s. Gated
  on the Bifrost switch.
- `tests/integration/key_lifecycle_test.go` — D2 key-lifecycle table (expired, revoked,
  wrong model, over daily budget, over total budget) + revoke-propagation ≤1 s across
  replicas. Scaffold: gated on `DELOS_KEYS_ENABLED=1`; the three wire helpers are
  placeholders until DL-K lands `proto/keys/v1`.
- `tests/agent/run.sh` (+ Makefile `test-agent` target) — headless Claude Code + Codex
  live/record harness over the mock upstream; skips until `DELOS_AGENT_E2E=1` and the
  agent CLIs are present.
- `tests/compat/mockupstream/main.go` — extended the mock provider with Anthropic
  `/v1/messages` (+ `/v1/messages/count_tokens`, streaming and not) and OpenAI
  `/v1/responses` (streaming and not), so agent sessions have a target.

## Coordination points / blockers for LEAD

1. **The Bifrost switch flag name is provisional.** I used `DELOS_GATEWAY_BIFROST=1`
   (const `bifrostEnv` in `swap_parity_test.go`). Confirm the real flag with DL-G and
   update the const.
2. **`bifrostGateway()` is a placeholder.** It `t.Fatal`s if the switch is enabled before
   DL-G exposes the in-process hook (HTTPServerOption / RuntimeService mode) that builds
   the Bifrost path. DL-G owns `services/runtime`; they should implement this helper or
   give DL-T the hook to call.
3. **Agent e2e needs a gateway-side Anthropic base-URL override.** `cmd/delos-gateway/
   main.go` registers `runtime.NewAnthropicProvider(key)` with no base-URL override, so
   the gateway cannot point its Anthropic provider at the mock upstream. `run.sh` sets
   `ANTHROPIC_BASE_URL` assuming such an override lands (DL-S/DL-G own main.go wiring).
4. **KeyService (proto/keys/v1) is not landed.** `key_lifecycle_test.go` is a scaffold;
   DL-K/DL-T wire `mintKeyForTest` / `revokeKeyForTest` / `seedUsageForTest` /
   `startGatewayReplicas` at D2 integration.
5. **`document` contract ambiguity.** Note 0001 §"Wire behavior changes" says documents
   pass through on same-provider passthrough ("those fields pass through"). The
   coordinator's instruction says `document` still 400s. I followed the coordinator in
   `messages_fidelity_test.go`. Confirm with DL-G/LEAD which is right before integration.

## Verification run (all green)

    buf generate
    gofmt -l (new files)                 -> clean
    go vet ./tests/conformance/ ./tests/compat/mockupstream/   -> exit 0
    go vet -tags=integration ./tests/integration/               -> exit 0
    go vet -tags=integration ./...                              -> exit 0
    go build ./...                                              -> exit 0
    go test ./tests/conformance/ -count=1 -v                    -> ok; new tests SKIP
        --- SKIP: TestAnthropicMessagesFidelityBifrost (Bifrost path disabled)
        --- SKIP: TestSwapParity (Bifrost path disabled)
    mockupstream smoke: /v1/messages (stream+not), /v1/messages/count_tokens,
        /v1/responses (stream+not) all answer correctly.

## Still to run at integration (impl not landed yet)

- `DELOS_GATEWAY_BIFROST=1 go test ./tests/conformance/ -run 'TestSwapParity|TestAnthropicMessagesFidelityBifrost' -v`
- `DELOS_KEYS_ENABLED=1 go test -tags=integration ./tests/integration/ -run 'TestKeyLifecycle|TestKeyRevokePropagation' -v`
- `DELOS_AGENT_E2E=1 make test-agent` (Claude Code + Codex headless; needs live keys or
  the mock path + the Anthropic base-URL override above)
- `make bench` once DL-G's gateway lands, to record the with-Bifrost p99.
