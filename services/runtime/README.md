# Runtime Service

The Runtime service is the LLM Gateway for Delos - a unified abstraction layer for interacting with multiple Large Language Model providers. It handles provider routing, request management, cost tracking, and streaming responses.

## Overview

| Property | Value |
|----------|-------|
| **Port** | 9001 |
| **Protocol** | gRPC |
| **Package** | `delos.runtime.v1` |

### Key Features

- **Multi-provider support**: OpenAI, Anthropic, Google Gemini, AWS Bedrock, Vertex AI, Together AI, OpenRouter, and Ollama (local)
- **Intelligent routing**: Cost-optimized, latency-optimized, quality-optimized, or specific provider selection
- **Streaming support**: Real-time token streaming for all providers
- **Embeddings**: Generate embeddings from supported providers
- **Cost tracking**: Automatic cost calculation per request
- **Unified API**: Single interface regardless of underlying provider

## Supported LLM Providers

| Provider | Completions | Streaming | Embeddings | Notes |
|----------|-------------|-----------|------------|-------|
| **OpenAI** | Yes | Yes | Yes | GPT-4o, GPT-4, GPT-3.5 |
| **Anthropic** | Yes | Yes | No | Claude Sonnet 4, Opus 4, Claude 3.5 |
| **Google Gemini** | Yes | Yes | Yes | Gemini 1.5 Pro/Flash |
| **AWS Bedrock** | Yes | Yes | Yes | Claude, Llama, Titan, Mistral, Cohere |
| **Vertex AI** | Yes | Yes | Yes | Gemini models + Claude via Vertex |
| **Together AI** | Yes | Yes | Yes | Llama, Qwen, Mistral, DeepSeek |
| **OpenRouter** | Yes | Yes | No | Multi-provider gateway |
| **Ollama** | Yes | Yes | Yes | Local models (free) |

### Supported Models by Provider

<details>
<summary><strong>OpenAI</strong></summary>

- `gpt-4o`
- `gpt-4o-mini`
- `gpt-4-turbo`
- `gpt-4`
- `gpt-3.5-turbo`
</details>

<details>
<summary><strong>Anthropic</strong></summary>

- `claude-sonnet-4-20250514`
- `claude-opus-4-20250514`
- `claude-3-5-sonnet-20241022`
- `claude-3-5-haiku-20241022`
- `claude-3-opus-20240229`
</details>

<details>
<summary><strong>Google Gemini</strong></summary>

- `gemini-1.5-pro`
- `gemini-1.5-flash`
- `gemini-1.5-flash-8b`
- `gemini-pro`
</details>

<details>
<summary><strong>AWS Bedrock</strong></summary>

- `anthropic.claude-3-5-sonnet-20241022-v2:0`
- `anthropic.claude-3-5-haiku-20241022-v1:0`
- `anthropic.claude-3-opus-20240229-v1:0`
- `meta.llama3-1-405b-instruct-v1:0`
- `meta.llama3-1-70b-instruct-v1:0`
- `meta.llama3-1-8b-instruct-v1:0`
- `amazon.titan-text-premier-v1:0`
- `mistral.mistral-large-2407-v1:0`
- `cohere.command-r-plus-v1:0`
</details>

<details>
<summary><strong>Vertex AI</strong></summary>

- `gemini-1.5-pro-002`
- `gemini-1.5-flash-002`
- `claude-3-5-sonnet-v2@20241022`
- `claude-3-5-haiku@20241022`
- `claude-3-opus@20240229`
</details>

<details>
<summary><strong>Together AI</strong></summary>

- `meta-llama/Llama-3.3-70B-Instruct-Turbo`
- `meta-llama/Meta-Llama-3.1-405B-Instruct-Turbo`
- `Qwen/Qwen2.5-72B-Instruct-Turbo`
- `deepseek-ai/DeepSeek-V3`
- `deepseek-ai/DeepSeek-R1`
- `mistralai/Mixtral-8x22B-Instruct-v0.1`
</details>

<details>
<summary><strong>OpenRouter</strong></summary>

- `anthropic/claude-3.5-sonnet`
- `openai/gpt-4o`
- `google/gemini-pro-1.5`
- `meta-llama/llama-3.1-405b-instruct`
- `mistralai/mistral-large`
- `deepseek/deepseek-chat`
</details>

<details>
<summary><strong>Ollama (Local)</strong></summary>

Models are auto-discovered from your local Ollama installation. Default: `llama3.2`
</details>

## API Reference

### RPCs

| RPC | Description |
|-----|-------------|
| `Complete` | Perform a completion request (non-streaming) |
| `CompleteStream` | Perform a streaming completion request |
| `Embed` | Generate embeddings for text |
| `ListProviders` | List available LLM providers and their status |
| `Health` | Health check with provider status |

### Complete / CompleteStream

Request parameters (`CompletionParams`):

| Field | Type | Description |
|-------|------|-------------|
| `prompt_ref` | string | Reference to a stored prompt (e.g., `"summarizer:v2.1"`) |
| `messages` | Message[] | Chat messages (role: system/user/assistant) |
| `variables` | map | Template variables when using prompt_ref |
| `routing` | RoutingStrategy | How to select provider |
| `provider` | string | Specific provider name (required if `SPECIFIC_PROVIDER`) |
| `model` | string | Model override |
| `temperature` | double | Sampling temperature (0-2) |
| `max_tokens` | int32 | Maximum tokens to generate |
| `top_p` | double | Nucleus sampling parameter |
| `stop` | string[] | Stop sequences |
| `output_schema` | string | JSON schema for structured output |
| `use_cache` | bool | Enable response caching |
| `cache_ttl_seconds` | int32 | Cache TTL |
| `metadata` | map | Custom metadata for tracking |

### Routing Strategies

| Strategy | Description |
|----------|-------------|
| `ROUTING_STRATEGY_UNSPECIFIED` | Default (cost-optimized) |
| `ROUTING_STRATEGY_COST_OPTIMIZED` | Select cheapest available provider |
| `ROUTING_STRATEGY_LATENCY_OPTIMIZED` | Select fastest provider |
| `ROUTING_STRATEGY_QUALITY_OPTIMIZED` | Prefer Anthropic, then OpenAI |
| `ROUTING_STRATEGY_SPECIFIC_PROVIDER` | Use specified provider only |

### Embed

Request parameters (`EmbedRequest`):

| Field | Type | Description |
|-------|------|-------------|
| `texts` | string[] | Texts to embed |
| `model` | string | Embedding model (optional) |
| `provider` | string | Provider to use (optional, defaults to OpenAI) |

## Configuration

### Environment Variables

#### Core Service Configuration

| Variable | Description | Default |
|----------|-------------|---------|
| `DELOS_LOG_LEVEL` | Logging level | `info` |
| `DELOS_ENV` | Environment name | `development` |
| `DELOS_OBSERVE_ENDPOINT` | OpenTelemetry endpoint | - |

#### Provider API Keys

| Variable | Provider | Required |
|----------|----------|----------|
| `DELOS_RUNTIME_OPENAI_KEY` | OpenAI | No |
| `DELOS_RUNTIME_ANTHROPIC_KEY` | Anthropic | No |
| `DELOS_RUNTIME_GEMINI_KEY` | Google Gemini | No |
| `DELOS_RUNTIME_TOGETHER_KEY` | Together AI | No |
| `DELOS_RUNTIME_OPENROUTER_KEY` | OpenRouter | No |

#### AWS Bedrock

| Variable | Description |
|----------|-------------|
| `AWS_ACCESS_KEY_ID` | AWS access key |
| `AWS_SECRET_ACCESS_KEY` | AWS secret key |
| `AWS_REGION` | AWS region (default: `us-east-1`) |
| `AWS_SESSION_TOKEN` | Session token (for temporary credentials) |

#### Google Cloud / Vertex AI

| Variable | Description |
|----------|-------------|
| `GOOGLE_CLOUD_PROJECT` | GCP project ID |
| `GOOGLE_CLOUD_LOCATION` | Region (default: `us-central1`) |
| `GOOGLE_CLOUD_ACCESS_TOKEN` | OAuth2 access token |

#### Ollama (Local)

| Variable | Description |
|----------|-------------|
| `DELOS_RUNTIME_OLLAMA_ENABLED` | Set to `true` to enable |
| `DELOS_RUNTIME_OLLAMA_URL` | Ollama URL (default: `http://localhost:11434`) |

#### OpenRouter (Optional)

| Variable | Description |
|----------|-------------|
| `DELOS_RUNTIME_OPENROUTER_SITE_URL` | Your site URL for rankings |
| `DELOS_RUNTIME_OPENROUTER_SITE_NAME` | Your site name |

## Running Locally

### Prerequisites

- Go 1.22+
- At least one LLM provider API key
- (Optional) Ollama installed for local models

### Start the service

```bash
# Set at least one provider API key
export DELOS_RUNTIME_OPENAI_KEY=sk-...
# or
export DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...

# Run the service
make run-runtime
# or
go run ./services/runtime/cmd/server
```

### With Docker Compose

```bash
# Start all dependencies and services
make up

# Or start just the runtime service
docker-compose up runtime
```

### Verify it's running

```bash
# Using grpcurl
grpcurl -plaintext localhost:9001 delos.runtime.v1.RuntimeService/Health

# List providers
grpcurl -plaintext localhost:9001 delos.runtime.v1.RuntimeService/ListProviders
```

## Architecture

### Provider Abstraction

The service uses a `Provider` interface that all LLM providers implement:

```go
type Provider interface {
    Name() string
    Models() []string
    Available(ctx context.Context) bool
    Complete(ctx context.Context, params CompletionParams) (*CompletionResult, error)
    CompleteStream(ctx context.Context, params CompletionParams) (<-chan StreamChunk, error)
    Embed(ctx context.Context, params EmbedParams) (*EmbedResult, error)
    CostPer1KTokens() map[string]float64
}
```

### Provider Registry

Providers are registered at startup based on which API keys are configured. The registry tracks:
- Available providers
- Supported models per provider
- Cost per 1K tokens per model

### Request Flow

1. Client sends `Complete` or `CompleteStream` request
2. Service selects provider based on routing strategy
3. Request is transformed to provider-specific format
4. Response is normalized and returned with usage/cost info

### Streaming

All providers support SSE (Server-Sent Events) streaming:
- OpenAI, Anthropic, Gemini, OpenRouter, Together: SSE format
- Ollama: Newline-delimited JSON (NDJSON)
- Bedrock: AWS event stream format

Responses are sent as `StreamChunk` messages with incremental deltas.

## Example Usage

### Using grpcurl

```bash
# Simple completion
grpcurl -plaintext -d '{
  "params": {
    "messages": [
      {"role": "user", "content": "What is 2+2?"}
    ],
    "provider": "openai",
    "model": "gpt-4o-mini"
  }
}' localhost:9001 delos.runtime.v1.RuntimeService/Complete

# Cost-optimized routing
grpcurl -plaintext -d '{
  "params": {
    "messages": [
      {"role": "system", "content": "You are a helpful assistant."},
      {"role": "user", "content": "Explain quantum computing in one sentence."}
    ],
    "routing": "ROUTING_STRATEGY_COST_OPTIMIZED",
    "max_tokens": 100
  }
}' localhost:9001 delos.runtime.v1.RuntimeService/Complete

# Generate embeddings
grpcurl -plaintext -d '{
  "texts": ["Hello world", "Goodbye world"],
  "provider": "openai"
}' localhost:9001 delos.runtime.v1.RuntimeService/Embed
```

### Using the Python SDK

```python
from delos import RuntimeClient

async with RuntimeClient("localhost:9001") as client:
    # Simple completion
    response = await client.complete(
        messages=[{"role": "user", "content": "Hello!"}],
        provider="anthropic",
        model="claude-3-5-sonnet-20241022"
    )
    print(response.content)

    # Streaming
    async for chunk in client.complete_stream(
        messages=[{"role": "user", "content": "Tell me a story"}],
        routing="cost_optimized"
    ):
        print(chunk.delta, end="", flush=True)
```

## Testing

```bash
# Run unit tests
go test ./services/runtime/...

# Run with verbose output
go test -v ./services/runtime/...

# Run specific test
go test -v ./services/runtime/... -run TestComplete_Success
```

## Related Services

- **Prompt Service** (port 9002): Manages prompt templates that can be referenced via `prompt_ref`
- **Observe Service** (port 9000): Receives traces from runtime operations
- **Eval Service** (port 9004): Uses runtime for evaluation runs

## Troubleshooting

### No providers available

Ensure at least one API key is set:
```bash
echo $DELOS_RUNTIME_OPENAI_KEY
echo $DELOS_RUNTIME_ANTHROPIC_KEY
```

### Provider not available

Check the health endpoint for provider status:
```bash
grpcurl -plaintext localhost:9001 delos.runtime.v1.RuntimeService/Health
```

### Ollama not connecting

1. Ensure Ollama is running: `ollama serve`
2. Check the URL: `curl http://localhost:11434/api/tags`
3. Set `DELOS_RUNTIME_OLLAMA_ENABLED=true`

### Rate limit errors

The service does not currently implement automatic retries. Consider:
- Using a different provider via routing
- Implementing client-side retry logic
- Using OpenRouter which handles rate limits across providers
