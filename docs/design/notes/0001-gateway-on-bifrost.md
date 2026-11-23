# 0001 — Gateway HTTP surfaces on Bifrost (D1)

Status: draft — awaiting R-REVIEW approval. Teams: DL-G (implementation), DL-T (tests), LEAD (this note). Surface: **wire behavior**. Gaps closed: 1, 2, 3, 5, 6.

## Decision

Per the 2026-11-23 decision, `delos-gateway` embeds Bifrost's Go core. D0 confirmed Bifrost's own HTTP routes are fasthttp and do **not** mount inside Delos's net/http server, so the gateway keeps its URL paths and serves them with **thin net/http handlers over Bifrost core** (`Passthrough`/`PassthroughStream`, `ChatCompletion*`, `Responses*`, `CountTokensRequest`).

## Wire behavior changes

| Route | Today (D0 baseline) | After D1 |
| --- | --- | --- |
| `POST /v1/messages` | Narrow `anthMessagesRequest`; silently drops `thinking`, `cache_control`, `metadata`, documents, `anthropic-beta` | Same-provider (Anthropic→Anthropic) via `PassthroughStream`, body + `anthropic-*` headers as sent; those fields pass through. Cross-provider via `ChatCompletion*` translation |
| `POST /v1/messages/count_tokens` | **Absent** | New, via `CountTokensRequest` |
| `POST /v1/responses` | **Absent** | New, via `ResponsesRequest`/`ResponsesStreamRequest`; passthrough for OpenAI |
| `POST /v1/chat/completions`, `/v1/embeddings`, `/v1/models` | Delos translators | Same paths; same-provider passthrough / cross-provider translation via Bifrost |
| Routing, retries, fallbacks, circuit breaker, cache | Delos loop + breaker + cache | Moved to Bifrost (fallback lists, routing, cache). Delos's versions removed **once swap-parity holds** |
| Usage | `input_tokens`/`output_tokens` only; cache tokens parsed but dropped | Bifrost usage incl. `cache_read`/`cache_creation` tokens, costed from catalog |
| Tracing | Delos telemetry, semconv 1.29, `delos.virtual_key` | Bifrost otel plugin → Delos observe; stamp `gen_ai.conversation.id` = session id, `delos.org_id`, `delos.subject` from the key; attribute names checked against current GenAI semconv |
| `delos tail` | Delos feed | Fed from Bifrost logging plugin / event broadcaster; same output + tenant scoping |

## What must NOT change (compatibility contract)

- URL paths above are fixed; additive only (`count_tokens`, `responses` are additions, not renames).
- OpenAI/Anthropic-compatible response shapes for existing endpoints.
- `delos.yaml` schema unchanged; secrets remain environment-only (Delos implements Bifrost `Account` with env keys).
- Gateway stays stateless and serves traffic with the control plane down.

## Behavior differences to expect (call out in tests/docs)

- Error shapes and usage fields may differ between Delos's translators and Bifrost's where they disagree; the swap-parity suite records each and the **documented Delos behavior is kept** unless the brief says otherwise.
- Streaming event order on translation routes may differ; same-provider passthrough is byte-faithful (including beta headers and SSE event order).
- Usage now surfaces cache read/write tokens (this changes `gen_ai.usage.*` on spans and `delos tail` output).

## Rollout

Delos translators stay behind a flag for one release, then are deleted. Semantic cache stays **off** until agent suites are green.

## Exit criteria (D1)

Existing conformance cassettes pass unchanged; one Claude Code and one Codex session finish through Delos with prompt caching hitting; costs within 1% of provider usage; p99 overhead < 5 ms with Bifrost inside.

## Open decisions this note depends on

- Budget authority (D2): Delos refuses model spend via Bifrost governance.
- Spans reach observe via OTLP (hub projector + Bifrost otel plugin).
