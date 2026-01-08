# LLM Providers Guide

Delos supports multiple LLM providers through a unified gateway. This guide covers configuration and best practices for each provider.

## Supported Providers

| Provider | Type | Streaming | Embeddings | Cost |
|----------|------|-----------|------------|------|
| OpenAI | Cloud | Yes | Yes | Pay-per-token |
| Anthropic | Cloud | Yes | No | Pay-per-token |
| Gemini | Cloud | Yes | Yes | Pay-per-token |
| OpenRouter | Cloud | Yes | No | Pay-per-token |
| AWS Bedrock | Cloud | Yes | Yes | Pay-per-token |
| Vertex AI | Cloud | Yes | Yes | Pay-per-token |
| Together AI | Cloud | Yes | Yes | Pay-per-token |
| Ollama | Local | Yes | Yes | Free |

## OpenAI

### Configuration

```bash
export DELOS_RUNTIME_OPENAI_KEY=sk-...
```

### Available Models

| Model | Context | Cost (per 1K tokens) |
|-------|---------|----------------------|
| gpt-4o | 128K | $0.005 |
| gpt-4o-mini | 128K | $0.00015 |
| gpt-4-turbo | 128K | $0.01 |
| gpt-4 | 8K | $0.03 |
| gpt-3.5-turbo | 16K | $0.0005 |

### Embeddings

Default model: `text-embedding-3-small`

```python
embeddings = await client.runtime.embed(
    texts=["Hello world"],
    provider="openai",
    model="text-embedding-3-large"  # Optional: use larger model
)
```

## Anthropic

### Configuration

```bash
export DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...
```

### Available Models

| Model | Context | Cost (per 1K tokens) |
|-------|---------|----------------------|
| claude-opus-4-20250514 | 200K | $0.015 |
| claude-sonnet-4-20250514 | 200K | $0.003 |
| claude-3-5-sonnet-20241022 | 200K | $0.003 |
| claude-3-5-haiku-20241022 | 200K | $0.0008 |
| claude-3-opus-20240229 | 200K | $0.015 |

### Notes

- Anthropic does not support embeddings
- System messages are handled separately (Anthropic's `system` parameter)
- Streaming uses SSE with event types

## Gemini

### Configuration

```bash
export DELOS_RUNTIME_GEMINI_KEY=AIza...
```

Get your API key from [Google AI Studio](https://aistudio.google.com/).

### Available Models

| Model | Context | Cost (per 1K tokens) |
|-------|---------|----------------------|
| gemini-1.5-pro | 2M | $0.00125 |
| gemini-1.5-flash | 1M | $0.000075 |
| gemini-1.5-flash-8b | 1M | $0.0000375 |
| gemini-pro | 32K | $0.0005 |

### Embeddings

Default model: `text-embedding-004`

```python
embeddings = await client.runtime.embed(
    texts=["Hello world"],
    provider="gemini"
)
```

### Notes

- Role mapping: `assistant` -> `model`
- System instructions use a separate parameter
- Very large context windows (up to 2M tokens)

## OpenRouter

OpenRouter provides access to 100+ models from multiple providers through a single API.

### Configuration

```bash
export DELOS_RUNTIME_OPENROUTER_KEY=sk-or-...

# Optional: for rankings/analytics
export DELOS_RUNTIME_OPENROUTER_SITE_URL=https://your-app.com
export DELOS_RUNTIME_OPENROUTER_SITE_NAME="Your App"
```

Get your API key from [OpenRouter](https://openrouter.ai/keys).

### Available Models

OpenRouter provides access to models from many providers. Model names include the provider prefix:

| Model | Provider | Context |
|-------|----------|---------|
| `anthropic/claude-3.5-sonnet` | Anthropic | 200K |
| `anthropic/claude-3-opus` | Anthropic | 200K |
| `openai/gpt-4o` | OpenAI | 128K |
| `openai/gpt-4o-mini` | OpenAI | 128K |
| `google/gemini-pro-1.5` | Google | 2M |
| `meta-llama/llama-3.1-405b-instruct` | Meta | 128K |
| `meta-llama/llama-3.1-70b-instruct` | Meta | 128K |
| `mistralai/mistral-large` | Mistral | 128K |
| `deepseek/deepseek-chat` | DeepSeek | 64K |
| `qwen/qwen-2.5-72b-instruct` | Alibaba | 128K |

See [OpenRouter Models](https://openrouter.ai/models) for the full list.

### Usage

```python
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="openrouter",
    model="anthropic/claude-3.5-sonnet"
)
```

### Notes

- Model names must include the provider prefix (e.g., `anthropic/claude-3.5-sonnet`)
- Pricing varies by model - check OpenRouter's pricing page
- No embeddings support (use OpenAI or Gemini for embeddings)
- Great for accessing models not directly available (Llama, Mistral, etc.)

---

## AWS Bedrock

AWS Bedrock provides access to foundation models within your AWS account.

### Configuration

```bash
# Standard AWS credentials
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
export AWS_REGION=us-east-1

# Optional: for temporary credentials (STS)
export AWS_SESSION_TOKEN=...
```

### IAM Permissions

Your AWS credentials need the following permissions:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "bedrock:InvokeModel",
        "bedrock:InvokeModelWithResponseStream"
      ],
      "Resource": "arn:aws:bedrock:*:*:foundation-model/*"
    }
  ]
}
```

### Available Models

Model IDs include the provider prefix and version:

| Model | Provider | Context |
|-------|----------|---------|
| `anthropic.claude-3-5-sonnet-20241022-v2:0` | Anthropic | 200K |
| `anthropic.claude-3-5-haiku-20241022-v1:0` | Anthropic | 200K |
| `anthropic.claude-3-opus-20240229-v1:0` | Anthropic | 200K |
| `meta.llama3-1-405b-instruct-v1:0` | Meta | 128K |
| `meta.llama3-1-70b-instruct-v1:0` | Meta | 128K |
| `amazon.titan-text-premier-v1:0` | Amazon | 32K |
| `mistral.mistral-large-2407-v1:0` | Mistral | 128K |
| `cohere.command-r-plus-v1:0` | Cohere | 128K |

### Usage

```python
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="bedrock",
    model="anthropic.claude-3-5-sonnet-20241022-v2:0"
)
```

### Embeddings

Default model: `amazon.titan-embed-text-v2:0`

```python
embeddings = await client.runtime.embed(
    texts=["Hello world"],
    provider="bedrock"
)
```

### Notes

- Uses AWS Signature V4 authentication (no separate API key needed)
- Model access must be enabled in the AWS Bedrock console
- Pricing varies by region - check AWS pricing page
- Supports temporary credentials via STS for enhanced security

---

## Vertex AI

Google Cloud Vertex AI provides enterprise access to Gemini and Claude models within your GCP project.

### Configuration

```bash
# Google Cloud project settings
export GOOGLE_CLOUD_PROJECT=your-project-id
export GOOGLE_CLOUD_LOCATION=us-central1  # Optional, defaults to us-central1
export GOOGLE_CLOUD_ACCESS_TOKEN=ya29...  # OAuth2 access token
```

### Getting an Access Token

```bash
# Using gcloud CLI
gcloud auth print-access-token
```

For production, use a service account with Workload Identity or Application Default Credentials.

### Available Models

Vertex AI supports both Google Gemini and Anthropic Claude models:

**Gemini Models:**

| Model | Context | Cost (per 1K tokens) |
|-------|---------|----------------------|
| gemini-1.5-pro-002 | 2M | $0.00125 |
| gemini-1.5-flash-002 | 1M | $0.000075 |
| gemini-1.5-pro | 2M | $0.00125 |
| gemini-1.5-flash | 1M | $0.000075 |
| gemini-1.0-pro | 32K | $0.0005 |

**Claude Models (via Vertex):**

| Model | Context | Cost (per 1K tokens) |
|-------|---------|----------------------|
| claude-3-5-sonnet-v2@20241022 | 200K | $0.003 |
| claude-3-5-haiku@20241022 | 200K | $0.0008 |
| claude-3-opus@20240229 | 200K | $0.015 |
| claude-3-sonnet@20240229 | 200K | $0.003 |

### Usage

```python
# Gemini model
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="vertexai",
    model="gemini-1.5-flash-002"
)

# Claude model on Vertex
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="vertexai",
    model="claude-3-5-sonnet-v2@20241022"
)
```

### Embeddings

Default model: `text-embedding-004`

```python
embeddings = await client.runtime.embed(
    texts=["Hello world"],
    provider="vertexai"
)
```

### Notes

- Uses OAuth2 authentication (Google Cloud access token)
- Claude models use a different API endpoint (`rawPredict` vs `generateContent`)
- Model access may need to be enabled in the Vertex AI Model Garden
- Enterprise features: VPC Service Controls, CMEK, audit logging

---

## Together AI

Together AI provides affordable access to open-source models including Llama, Mistral, Qwen, and DeepSeek.

### Configuration

```bash
export DELOS_RUNTIME_TOGETHER_KEY=...
```

Get your API key from [Together AI](https://api.together.xyz/).

### Available Models

| Model | Provider | Context | Cost (per 1K tokens) |
|-------|----------|---------|----------------------|
| meta-llama/Llama-3.3-70B-Instruct-Turbo | Meta | 128K | $0.00088 |
| meta-llama/Meta-Llama-3.1-405B-Instruct-Turbo | Meta | 128K | $0.0035 |
| meta-llama/Meta-Llama-3.1-70B-Instruct-Turbo | Meta | 128K | $0.00088 |
| meta-llama/Meta-Llama-3.1-8B-Instruct-Turbo | Meta | 128K | $0.00018 |
| Qwen/Qwen2.5-72B-Instruct-Turbo | Qwen | 128K | $0.0012 |
| Qwen/QwQ-32B-Preview | Qwen | 32K | $0.0012 |
| mistralai/Mixtral-8x22B-Instruct-v0.1 | Mistral | 64K | $0.0012 |
| deepseek-ai/DeepSeek-V3 | DeepSeek | 64K | $0.0009 |
| deepseek-ai/DeepSeek-R1 | DeepSeek | 64K | $0.003 |
| google/gemma-2-27b-it | Google | 8K | $0.0008 |

See [Together Models](https://docs.together.ai/docs/inference-models) for the full list.

### Usage

```python
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="together",
    model="meta-llama/Llama-3.3-70B-Instruct-Turbo"
)
```

### Embeddings

Default model: `togethercomputer/m2-bert-80M-8k-retrieval`

```python
embeddings = await client.runtime.embed(
    texts=["Hello world"],
    provider="together"
)
```

### Notes

- OpenAI-compatible API
- Great for cost-effective access to open-source models
- Supports latest Llama, DeepSeek, Qwen, and Mistral models
- Fast inference with optimized Turbo variants

---

## Ollama (Local)

### Configuration

```bash
# Enable Ollama
export DELOS_RUNTIME_OLLAMA_ENABLED=true

# Custom URL (optional, defaults to localhost:11434)
export DELOS_RUNTIME_OLLAMA_URL=http://localhost:11434
```

### Setup

1. Install Ollama: https://ollama.ai/download
2. Pull a model: `ollama pull llama3.2`
3. Start Ollama: `ollama serve`

### Available Models

Models are dynamically discovered from your local Ollama installation:

```bash
ollama list  # Shows available models
```

Common models:
- `llama3.2` - Meta's Llama 3.2
- `mistral` - Mistral 7B
- `codellama` - Code-focused Llama
- `nomic-embed-text` - Embeddings

### Embeddings

Default model: `nomic-embed-text`

```bash
# Pull the embedding model first
ollama pull nomic-embed-text
```

```python
embeddings = await client.runtime.embed(
    texts=["Hello world"],
    provider="ollama"
)
```

### Notes

- No API key required
- Cost is always $0
- Models must be pulled before use
- Longer timeouts (300s) for local inference

## Routing Strategies

### Cost Optimized (Default)

Selects the cheapest provider that supports the requested model:

```python
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    routing="COST_OPTIMIZED"  # Default
)
```

### Quality Optimized

Prefers higher-quality models:
1. Claude (Anthropic)
2. GPT-4 (OpenAI)
3. Others

```python
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    routing="QUALITY_OPTIMIZED"
)
```

### Specific Provider

Force a specific provider:

```python
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="anthropic",
    model="claude-3-5-sonnet-20241022"
)
```

## Failover

When a provider fails, Delos automatically tries the next available provider:

1. If specific provider requested, try it first
2. Fall back to other available providers
3. Return error only if all providers fail

```python
# This will try Anthropic first, then fall back to others
response = await client.runtime.complete(
    messages=[{"role": "user", "content": "Hello"}],
    provider="anthropic"  # Preferred, but will failover
)
```

## Cost Tracking

All completions include cost information:

```python
response = await client.runtime.complete(...)

print(f"Provider: {response.provider}")
print(f"Model: {response.model}")
print(f"Tokens: {response.usage.total_tokens}")
print(f"Cost: ${response.usage.cost_usd:.6f}")
```

## Best Practices

1. **Use prompt references** for versioned prompts instead of inline messages
2. **Enable caching** for repeated queries
3. **Use local models** (Ollama) for development to save costs
4. **Monitor costs** through the observe service
5. **Set appropriate timeouts** for your use case
