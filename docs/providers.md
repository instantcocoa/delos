# Providers

Delos supports a curated provider set, well, instead of a hundred providers
badly. A provider is five Go methods registered in one file; adding one never
touches routing, auth, or telemetry code.

## Configuring providers

The gateway registers one provider per credential found in the environment:

| Provider | Environment | Notes |
|----------|-------------|-------|
| OpenAI | `OPENAI_API_KEY` | api.openai.com |
| Anthropic | `ANTHROPIC_API_KEY` | api.anthropic.com |
| Google Gemini | `GEMINI_API_KEY` (or `GOOGLE_API_KEY`) | Generative Language API |
| AWS Bedrock | `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` (or `AWS_PROFILE`), `AWS_REGION` | Uses the official AWS SDK (Converse API); the full default credential chain works |
| Ollama | `DELOS_RUNTIME_OLLAMA_ENABLED=true` (+ `DELOS_RUNTIME_OLLAMA_URL`) | Served through Ollama's OpenAI-compatible endpoint |
| Any OpenAI-compatible endpoint | `DELOS_COMPAT_BASE_URL` (+ `DELOS_COMPAT_NAME`, `DELOS_COMPAT_API_KEY`) | Covers vLLM, SGLang, Together, OpenRouter, LM Studio, most gateways |

Models are addressed by bare name (`gpt-4o`, `claude-sonnet-4-5`) when
unambiguous, or explicitly as `provider/model` (`ollama/gemma3:4b`,
`bedrock/amazon.nova-lite-v1:0`). `GET /v1/models` lists everything the
gateway can serve.

Credentials are read once, at startup: **restart the gateway after changing
them.** There is no hot reload and no runtime API for registering providers.
`GET /healthz` reports how many were picked up — `{"providers": 0}` means none
were. For local development keep keys in `deploy/local/.env.local` (gitignored;
Compose loads it), or export them before `make run-gateway`.

### AWS Bedrock

The credentials need at minimum:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"],
    "Resource": "arn:aws:bedrock:*:*:foundation-model/*"
  }]
}
```

Models must also be enabled for your account in the Bedrock console, per region.

### Ollama

```bash
make up-ollama     # 'ollama' Compose profile on :11434, pulls gemma3:4b
make ollama-ready  # verify Ollama and the model are up
```

Inside Compose the gateway reaches Ollama at `http://ollama:11434`; from the
host it is `http://localhost:11434`. With Ollama installed on the host instead,
`ollama serve` plus `ollama pull <model>` is all it needs. Models are discovered
dynamically, so whatever you have pulled shows up in `GET /v1/models`.

### Any OpenAI-compatible endpoint

```bash
DELOS_COMPAT_NAME=together
DELOS_COMPAT_BASE_URL=https://api.together.xyz/v1
DELOS_COMPAT_API_KEY=...
# then: "model": "together/meta-llama/Llama-3-70b-chat-hf"
```

`DELOS_COMPAT_NAME` becomes the `provider/model` prefix and the `owned_by`
value. Backends with slash-separated model IDs of their own (OpenRouter's
`anthropic/claude-3.5-sonnet`) are easiest to reach through a route alias — see
[api-reference/runtime.md](api-reference/runtime.md#routes-and-failover).

## Feature matrix

What works — and explicitly what does not:

| Feature | OpenAI | Anthropic | Gemini | Bedrock | OpenAI-compatible |
|---|---|---|---|---|---|
| Chat completions | ✅ | ✅ | ✅ | ✅ | ✅ |
| Streaming (SSE) | ✅ | ✅ | ✅ | ✅ | ✅ |
| Tool calls (incl. streaming deltas) | ✅ | ✅ | ✅ | ✅ | ✅* |
| Vision: image as data URI / base64 | ✅ | ✅ | ✅ | ✅ | ✅* |
| Vision: image as remote URL | ✅ | ✅ | ❌ fetch it client-side | ❌ fetch it client-side | ✅* |
| `response_format: json_object` | ✅ | ❌ returns a clear error | ✅ | ❌ returns a clear error | ✅* |
| `response_format: json_schema` | ✅ | ❌ | ✅ | ❌ | ✅* |
| Embeddings | ✅ | ❌ no embeddings API | ✅ (batch) | ✅ Titan only | ✅* |
| `tool_choice: none` | ✅ | ✅ | ✅ | ❌ returns a clear error | ✅* |
| `n > 1` choices | ❌ gateway-wide | ❌ | ❌ | ❌ | ❌ |

`*` for OpenAI-compatible endpoints, support depends on what the backend
actually implements; the gateway forwards the fields verbatim and surfaces
the backend's error if it rejects them.

Unsupported parameters are rejected with a structured 400 — never silently
dropped.

## Cost accounting

Each provider carries a per-model pricing table used to attribute
`usage.cost_usd` (a Delos extension field standard clients ignore) and the
`delos.cost_usd` span attribute. OpenAI-compatible endpoints report zero cost
unless priced.

## Both surfaces reach every provider

The OpenAI surface (`/v1/chat/completions`) and the Anthropic surface
(`/v1/messages`) both translate to any configured provider: you can point an
Anthropic SDK at `bedrock/...` or an OpenAI SDK at `anthropic/...` and tool
calls, streaming, and usage reporting translate in both directions.
