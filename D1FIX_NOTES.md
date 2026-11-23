# DL-G D1 bugfix notes (branch-local scratch)

Branch: `team/dl-g/d1-bugfix` (from `team/integration/d1-d2` @ 8ff422f).

## Fixes

1. **Error envelope parity** (`services/runtime/bifrost_gateway.go`).
   - Bifrost `Passthrough` returns provider 4xx/5xx as a *response* (not a
     `BifrostError`), so the passthrough handler was forwarding the raw provider
     body (`error.code=null`, `rate_limit_exceeded`). Now:
     - `writeProviderErrorEnvelope` centralises the legacy mapping (note 0001):
       OpenAI `code: "provider_error"`, message `provider <name>: <msg>`; Anthropic
       429 -> `rate_limit_error`, 4xx -> `invalid_request_error`, same prefix.
     - passthrough routes `resp.StatusCode >= 400` (non-streaming and first stream
       chunk) through it, extracting the message from `error.message`.
     - `writeBifrostError` (for a true `*BifrostError`) delegates to the same
       envelope with the provider from `ExtraFields.Provider`.

2. **Gemini embedding routing** (`services/runtime/service.go`).
   - `wellKnownProvider` now checks `text-embedding-004` -> gemini before the
     generic OpenAI `text-embedding-` prefix, so `gemini/embeddings` routes to
     Gemini instead of OpenAI (and 401-ing).

## Proof

- `go test ./tests/conformance/ -run TestSwapParity -count=1` -> 38/38 pass.
- `CGO_ENABLED=0 go test ./... -count=1` -> all green (16 packages).
- `CGO_ENABLED=0 go vet ./...` -> OK.
