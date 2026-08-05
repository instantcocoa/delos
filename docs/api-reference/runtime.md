# Gateway API Reference

`delos-gateway` is the Delos data plane: a stateless HTTP LLM gateway that fronts a curated provider set (OpenAI, Anthropic, Gemini, AWS Bedrock, plus any OpenAI-compatible endpoint — Ollama, vLLM, SGLang, Together, OpenRouter, LM Studio) behind OpenAI- and Anthropic-compatible APIs.

**Port**: 8080 (override with `DELOS_GATEWAY_PORT`)
**Binary**: `bin/delos-gateway` (built from `./cmd/delos-gateway`)
**Base URL**: `http://localhost:8080`

The gateway needs no control plane. Providers are configured entirely through environment variables — see [docs/providers.md](../providers.md). A database is optional and used for one thing only: enforcing virtual keys.

## Endpoints

| Method | Path | Description |
|--------|------|-------------|
| POST | [`/v1/chat/completions`](#post-v1chatcompletions) | OpenAI-compatible chat completion (streaming supported) |
| POST | [`/v1/embeddings`](#post-v1embeddings) | OpenAI-compatible embeddings |
| GET | [`/v1/models`](#get-v1models) | List models across configured providers |
| GET | [`/v1/models/{model}`](#get-v1modelsmodel) | Look up a single model |
| POST | [`/v1/messages`](#post-v1messages) | Anthropic-compatible Messages API (streaming supported) |
| GET | [`/v1/events`](#get-v1events) | Live request stream (SSE) — what `delos tail` reads |
| GET | [`/healthz`](#get-healthz) | Health check + configured provider count |

## Authentication

Requests carry a **virtual key** in the standard header for the surface: `Authorization: Bearer dk_...` on the OpenAI surface, `x-api-key: dk_...` (or `Authorization: Bearer`) on the Anthropic surface. Virtual keys are hashed with argon2id, scoped to models, and carry monthly token and dollar budgets. `delos key` manages them directly in Postgres using the `DELOS_DB_*` settings — the plaintext secret is printed exactly once, at creation.

```bash
delos key create web-prod --models "openai/*,claude-sonnet-4-5" --usd-budget 250
# prints the secret once: dk_...
delos key list
delos key revoke <id>
```

**Enforcement requires a database.** With no database configured the gateway logs `dev mode: virtual keys are NOT enforced` and accepts any `api_key` value — `delos-dev` is just a convention:

```python
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
```

Provider credentials never come from the client; they come from the gateway's own environment. Do not expose a dev-mode gateway to untrusted networks.

### Key errors

| Status | `code` | Cause |
|--------|--------|-------|
| 401 | `invalid_api_key` | Missing key, unknown key, or a revoked key |
| 403 | `model_not_allowed` | The key is scoped to models that do not include the requested one |
| 429 | `budget_exceeded` | The key's monthly token or dollar budget is exhausted — refused before any provider is contacted |
| 503 | `budget_unavailable` | Budget lookup failed, so the request is refused rather than billed blind |

The Anthropic surface returns the same conditions with `authentication_error` / `invalid_request_error` types in the Anthropic envelope.

## Request IDs

Every response carries an `X-Request-Id` header. If the client sends one, it is echoed; otherwise the gateway generates `req_<uuid>`. The request ID is also appended to error messages, so it can be quoted when reporting problems.

## Model resolution

There is no `provider` parameter. The gateway picks the provider from the `model` string, in this order:

1. **Route alias** — the model matches a configured alias (see [routes and failover](#routes-and-failover)), which expands to an ordered list of `provider/model` targets.
2. **Explicit prefix** — `provider/model`, e.g. `openai/gpt-4o`, `ollama/gemma3:4b`, `bedrock/amazon.nova-lite-v1:0`. Everything after the first `/` is passed to the provider as the model name.
3. **Exact match** — the model appears in a configured provider's model list (`GET /v1/models`).
4. **Well-known prefixes** — `gpt-`, `o1`, `o3`, `o4`, `text-embedding-` map to `openai`; `claude-` maps to `anthropic`; `gemini-` maps to `gemini`.

If none match, the request fails with `404` / `model_not_found`.

A provider registered from an OpenAI-compatible endpoint uses the name you give it (`DELOS_COMPAT_NAME`, or `ollama` for the Ollama shortcut), so its models are addressed as `<name>/<model>`. Backends whose model IDs are themselves slash-separated (OpenRouter's `anthropic/claude-3.5-sonnet`, for example) are best addressed through a route alias.

## Routes and failover

A **route** maps an alias to an ordered list of `provider/model` targets. The gateway tries each in turn, moving on when one returns 429/5xx or trips its per-provider circuit breaker, with jittered retry in between. The whole chain shares one end-to-end deadline (`DELOS_REQUEST_TIMEOUT`).

Configure routes in `delos.yaml` under `gateway.routes`, or with the equivalent JSON environment variable, which takes precedence:

```bash
DELOS_ROUTES='{"gpt-4":["openai/gpt-4o","anthropic/claude-sonnet-4-5"],"cheap":["openai/gpt-4o-mini","ollama/gemma3:4b"]}'
```

Clients then just ask for `"model": "gpt-4"`. The `model` field in the response reports the target that actually served the request.

## Response caching

Responses are cached on an exact match of (model, normalized messages, params). The cache is an in-process LRU by default; set `DELOS_REDIS_URL` to share one cache across several gateways, or `DELOS_CACHE=off` to disable caching entirely. `DELOS_CACHE_TTL` sets the entry lifetime (default `5m`).

A response served from cache carries:

```
X-Delos-Cache: hit
```

The header is absent on a miss. Cached responses never contact a provider, and appear in `delos tail` with `cache_hit: true`.

---

## POST /v1/chat/completions

OpenAI-compatible chat completion.

**Request body**:

| Field | Type | Notes |
|-------|------|-------|
| `model` | string | Required |
| `messages` | array | Required. `content` may be a string or an array of `{"type": "text", ...}` / `{"type": "image_url", ...}` parts |
| `temperature` | number | Optional |
| `top_p` | number | Optional |
| `max_tokens` | integer | Optional |
| `max_completion_tokens` | integer | Optional; takes precedence over `max_tokens` |
| `stop` | string or array of strings | Optional |
| `stream` | boolean | Optional; see [streaming](#streaming) |
| `stream_options.include_usage` | boolean | Optional; emit a final usage-only chunk |
| `tools` / `tool_choice` | array / string or object | Optional; tool calls translate to every provider, including streaming deltas |
| `response_format` | object | Optional; `json_object` and `json_schema`, on providers that support them |
| `n` | integer | Only `1` is supported |
| `user` | string | Accepted, unused |

Parameters a provider cannot honour are rejected with a structured `400`, never silently dropped — [docs/providers.md](../providers.md) has the per-provider matrix.

**Example**:

```bash
curl http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gpt-4o-mini",
    "messages": [
      {"role": "system", "content": "You are helpful."},
      {"role": "user", "content": "Hello!"}
    ],
    "max_tokens": 128
  }'
```

**Response**:

```json
{
  "id": "chatcmpl-req_1f0c...",
  "object": "chat.completion",
  "created": 1754179200,
  "model": "gpt-4o-mini",
  "choices": [
    {
      "index": 0,
      "message": {"role": "assistant", "content": "Hi! How can I help?"},
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 18,
    "completion_tokens": 7,
    "total_tokens": 25,
    "cost_usd": 0.0000041
  }
}
```

`usage.cost_usd` is a Delos extension; standard OpenAI clients ignore it.

**CLI**:

```bash
delos gateway complete "Hello!" --model gpt-4o-mini --system "You are helpful."
```

### Streaming

Set `"stream": true` to receive Server-Sent Events (`Content-Type: text/event-stream`). Each frame is a `data:` line holding a `chat.completion.chunk` object; the stream ends with `data: [DONE]`.

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model": "gpt-4o-mini", "messages": [{"role": "user", "content": "Write a haiku"}], "stream": true}'
```

```
data: {"id":"chatcmpl-req_1f0c...","object":"chat.completion.chunk","created":1754179200,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"role":"assistant","content":"Silent"},"finish_reason":null}]}

data: {"id":"chatcmpl-req_1f0c...","object":"chat.completion.chunk","created":1754179200,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":" servers"},"finish_reason":null}]}

data: {"id":"chatcmpl-req_1f0c...","object":"chat.completion.chunk","created":1754179200,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]
```

The `role` field appears only on the first delta. With `"stream_options": {"include_usage": true}`, a final chunk with an empty `choices` array and a populated `usage` object is sent before `[DONE]` (when the provider reports usage).

If the provider stream breaks mid-flight, the gateway emits a `data:` frame containing an error envelope with `code: "provider_stream_error"` and closes **without** sending `[DONE]`, so a truncated response is not mistaken for a complete one.

**CLI**:

```bash
delos gateway complete "Write a haiku" --model gpt-4o-mini --stream
```

---

## POST /v1/embeddings

OpenAI-compatible embeddings.

**Request body**:

| Field | Type | Notes |
|-------|------|-------|
| `model` | string | Required |
| `input` | string or array of strings | Required. Pre-tokenized integer arrays are not supported |
| `encoding_format` | string | `"base64"` returns little-endian float32 bytes; anything else returns a float array |
| `user` | string | Accepted, unused |

**Example**:

```bash
curl http://localhost:8080/v1/embeddings \
  -H "Content-Type: application/json" \
  -d '{"model": "text-embedding-3-small", "input": ["Hello world", "Goodbye world"]}'
```

**Response**:

```json
{
  "object": "list",
  "data": [
    {"object": "embedding", "index": 0, "embedding": [0.0023, -0.0091, "..."]},
    {"object": "embedding", "index": 1, "embedding": [0.0117, -0.0042, "..."]}
  ],
  "model": "text-embedding-3-small",
  "usage": {"prompt_tokens": 6, "completion_tokens": 0, "total_tokens": 6}
}
```

**CLI**:

```bash
delos gateway embed "Hello world" "Goodbye world" --model text-embedding-3-small
```

---

## GET /v1/models

List every model advertised by the configured providers. Ollama models are discovered dynamically from the running Ollama instance; the other providers report a static list.

```bash
curl http://localhost:8080/v1/models
```

```json
{
  "object": "list",
  "data": [
    {"id": "gpt-4o", "object": "model", "created": 0, "owned_by": "openai"},
    {"id": "claude-sonnet-4-20250514", "object": "model", "created": 0, "owned_by": "anthropic"}
  ]
}
```

`owned_by` is the Delos provider name, which is also the prefix accepted in `provider/model` references.

**CLI**:

```bash
delos gateway models
```

---

## GET /v1/models/{model}

Look up a single model by its exact ID.

```bash
curl http://localhost:8080/v1/models/gpt-4o
```

```json
{"id": "gpt-4o", "object": "model", "created": 0, "owned_by": "openai"}
```

Returns `404` with an OpenAI error envelope (`code: "model_not_found"`) if no configured provider lists the model.

---

## POST /v1/messages

Anthropic-compatible Messages API. Point the Anthropic SDK at the gateway by overriding its base URL.

**Request body**:

| Field | Type | Notes |
|-------|------|-------|
| `model` | string | Required |
| `messages` | array | Required. `content` may be a string or an array of `{"type": "text", "text": ...}` blocks |
| `max_tokens` | integer | Required, must be positive |
| `system` | string or array of text blocks | Optional; prepended as a `system` message |
| `temperature` | number | Optional |
| `top_p` | number | Optional |
| `stop_sequences` | array of strings | Optional |
| `stream` | boolean | Optional; see [streaming](#streaming-1) |
| `tools` / `tool_choice` | array / object | Optional; translated to whichever provider serves the request |

`image` content blocks are supported (base64 source everywhere, `url` source on providers that fetch remote images). Unsupported blocks are rejected with `400`.

Note that the `model` is resolved the same way as on the OpenAI surface, so this endpoint can front a non-Anthropic provider (e.g. `"model": "openai/gpt-4o"`).

**Example**:

```bash
curl http://localhost:8080/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-haiku-20241022",
    "max_tokens": 256,
    "system": "You are helpful.",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

**Response**:

```json
{
  "id": "msg_9f2b...",
  "type": "message",
  "role": "assistant",
  "model": "claude-3-5-haiku-20241022",
  "content": [{"type": "text", "text": "Hi! How can I help?"}],
  "stop_reason": "end_turn",
  "stop_sequence": null,
  "usage": {"input_tokens": 14, "output_tokens": 8}
}
```

### Streaming

With `"stream": true` the gateway emits named SSE events in Anthropic's order:

```
event: message_start
data: {"type":"message_start","message":{"id":"msg_9f2b...","type":"message","role":"assistant","model":"claude-3-5-haiku-20241022","content":[],"stop_reason":"","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"input_tokens":14,"output_tokens":8}}

event: message_stop
data: {"type":"message_stop"}
```

The `usage` field on `message_delta` is present only when the provider reports usage. A mid-stream failure emits an `event: error` frame carrying the Anthropic error envelope.

---

## GET /v1/events

Live request stream, as Server-Sent Events. One `data:` frame per request that finished flowing through the gateway. This is what `delos tail` renders.

```bash
curl -N http://localhost:8080/v1/events?replay=5 \
  -H "Authorization: Bearer dk_..."
```

```
data: {"time":"2026-08-05T14:22:03Z","request_id":"req_1f0c...","surface":"chat","model":"gpt-4o","provider":"openai","status":200,"latency_ms":1204.5,"prompt_tokens":842,"completion_tokens":96,"cost_usd":0.00131,"cache_hit":false,"key_name":"web-prod"}
```

| Field | Notes |
|-------|-------|
| `surface` | `chat`, `embeddings`, `messages`, or `models` |
| `provider` | The provider that actually served it, after route/failover resolution |
| `cache_hit` | `true` when the response came from the cache |
| `key_name` | The virtual key's name; absent in dev mode |
| `error` | `code: message` for failed requests; absent on success |

| Query param | Notes |
|-------------|-------|
| `replay` | Send the N most recent buffered events before going live |

The endpoint authenticates like any other. `DELOS_TAIL=off` disables it (the endpoint then returns `404` / `tail_disabled`); `DELOS_TAIL_BUFFER` sizes the replay ring (default 200).

**CLI**:

```bash
delos tail                  # live table
delos tail --replay 20      # recent history first
delos tail --json           # raw events, one JSON object per line
```

---

## GET /healthz

Liveness check. Also reports how many providers were registered at startup — `0` means no provider credentials were found in the environment.

```bash
curl http://localhost:8080/healthz
```

```json
{"status": "ok", "providers": 3}
```

**CLI**:

```bash
delos gateway health
```

---

## Errors

The OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/embeddings`, `/v1/models*`) return:

```json
{
  "error": {
    "message": "model \"gpt-5\" is not served by any configured provider (request id: req_1f0c...)",
    "type": "invalid_request_error",
    "code": "model_not_found"
  }
}
```

`type` is `invalid_request_error` for 4xx and `api_error` for 5xx. The request ID is appended to `message`.

`/v1/messages` returns the Anthropic envelope instead:

```json
{
  "type": "error",
  "error": {
    "type": "not_found_error",
    "message": "model \"claude-9\" is not served by any configured provider (request id: req_1f0c...)"
  }
}
```

### Common codes

| Status | `code` | Cause |
|--------|--------|-------|
| 400 | `invalid_json` | Body is not valid JSON |
| 400 | `missing_model` | No `model` field |
| 400 | `missing_messages` | No `messages` field |
| 400 | `invalid_messages` | Unsupported role/content shape |
| 400 | `invalid_input` | `input` for embeddings is empty or not string/array-of-strings |
| 400 | `unsupported_parameter` | `n` > 1, or a parameter the resolved provider cannot honour |
| 401 | `invalid_api_key` | Missing, unknown, or revoked virtual key |
| 403 | `model_not_allowed` | Virtual key is not scoped to the requested model |
| 404 | `model_not_found` | No configured provider serves the model |
| 404 | `tail_disabled` | `GET /v1/events` while `DELOS_TAIL=off` |
| 429 | `budget_exceeded` | Virtual key's monthly token or dollar budget is exhausted |
| 500 | `streaming_unsupported` | Response writer cannot stream |
| 502 | `provider_error` | Upstream provider call failed |
| 503 | `budget_unavailable` | Budget lookup failed; the request is refused rather than billed blind |
| — | `provider_stream_error` | Upstream stream failed after headers were sent (delivered in-band) |

---

## Using the gateway from clients

There is no Delos SDK. Any OpenAI- or Anthropic-compatible client works with the base URL swapped:

```python
# OpenAI SDK
from openai import OpenAI

client = OpenAI(base_url="http://localhost:8080/v1", api_key="delos-dev")
resp = client.chat.completions.create(
    model="claude-3-5-haiku-20241022",   # any configured provider's model
    messages=[{"role": "user", "content": "Hello!"}],
)
print(resp.choices[0].message.content)
```

```python
# Anthropic SDK
from anthropic import Anthropic

client = Anthropic(base_url="http://localhost:8080", api_key="delos-dev")
msg = client.messages.create(
    model="claude-3-5-haiku-20241022",
    max_tokens=256,
    messages=[{"role": "user", "content": "Hello!"}],
)
print(msg.content[0].text)
```

## Supported providers

| Provider | Name (`owned_by` / prefix) | Enabled by | Embeddings | Streaming |
|----------|---------------------------|------------|------------|-----------|
| OpenAI | `openai` | `OPENAI_API_KEY` | Yes | Yes |
| Anthropic | `anthropic` | `ANTHROPIC_API_KEY` | No | Yes |
| Gemini | `gemini` | `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) | Yes | Yes |
| AWS Bedrock | `bedrock` | AWS credentials (or `AWS_PROFILE`) + `AWS_REGION` | Titan only | Yes |
| Ollama | `ollama` | `DELOS_RUNTIME_OLLAMA_ENABLED=true` (+ `DELOS_RUNTIME_OLLAMA_URL`) | Depends on backend | Yes |
| Any OpenAI-compatible endpoint | `DELOS_COMPAT_NAME` (default `compat`) | `DELOS_COMPAT_BASE_URL` (+ `DELOS_COMPAT_API_KEY`) | Depends on backend | Yes |

The OpenAI-compatible slot covers vLLM, SGLang, Together, OpenRouter, LM Studio and most gateways; the fields are forwarded verbatim and the backend's own error is surfaced if it rejects them.

See [docs/providers.md](../providers.md) for the full feature matrix — including what explicitly does *not* work — and the per-provider setup.
