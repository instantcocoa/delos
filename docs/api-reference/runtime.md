# Runtime Service API Reference

The Runtime Service is the unified LLM gateway that abstracts multiple providers (OpenAI, Anthropic, Gemini, Ollama) behind a single API.

**Port**: 9001
**Package**: `delos.runtime.v1`

## Endpoints

| RPC | Description |
|-----|-------------|
| [Health](#health) | Service health check |
| [ListProviders](#listproviders) | List available LLM providers |
| [Complete](#complete) | Non-streaming completion |
| [CompleteStream](#completestream) | Streaming completion |
| [Embed](#embed) | Generate embeddings |

---

## Health

Check service health status.

**Request**: `HealthRequest` (empty)

**Response**:
```protobuf
message HealthResponse {
  string status = 1;   // "healthy"
  string version = 2;  // Service version
}
```

**CLI**:
```bash
delos runtime health
```

**Python SDK**:
```python
health = await client.runtime.health()
print(health.status)
```

---

## ListProviders

List all available LLM providers and their models.

**Request**: `ListProvidersRequest` (empty)

**Response**:
```protobuf
message ListProvidersResponse {
  repeated Provider providers = 1;
}

message Provider {
  string name = 1;           // e.g., "openai", "anthropic", "gemini", "ollama"
  repeated string models = 2; // Available models
  bool available = 3;         // Currently available
}
```

**CLI**:
```bash
delos runtime providers
```

**Python SDK**:
```python
providers = await client.runtime.list_providers()
for p in providers:
    print(f"{p.name}: {p.models}")
```

---

## Complete

Perform a non-streaming LLM completion.

**Request**:
```protobuf
message CompleteRequest {
  CompletionParams params = 1;
}

message CompletionParams {
  string prompt_ref = 1;              // e.g., "summarizer:v2"
  repeated Message messages = 2;       // Direct messages (if no prompt_ref)
  map<string, string> variables = 3;   // Template variables

  // Routing
  RoutingStrategy routing = 4;         // Cost, latency, quality optimized
  string provider = 5;                 // Specific provider (optional)
  string model = 6;                    // Specific model (optional)

  // Generation parameters
  double temperature = 7;
  int32 max_tokens = 8;
  double top_p = 9;
  repeated string stop = 10;

  // Caching
  bool cache_enabled = 11;
  string cache_key = 12;

  // Metadata
  map<string, string> metadata = 13;
}

enum RoutingStrategy {
  ROUTING_STRATEGY_UNSPECIFIED = 0;
  ROUTING_STRATEGY_COST_OPTIMIZED = 1;
  ROUTING_STRATEGY_LATENCY_OPTIMIZED = 2;
  ROUTING_STRATEGY_QUALITY_OPTIMIZED = 3;
  ROUTING_STRATEGY_SPECIFIC_PROVIDER = 4;
}
```

**Response**:
```protobuf
message CompleteResponse {
  string id = 1;
  string content = 2;
  Message message = 3;
  string provider = 4;
  string model = 5;
  Usage usage = 6;
  string trace_id = 7;
}

message Usage {
  int32 prompt_tokens = 1;
  int32 completion_tokens = 2;
  int32 total_tokens = 3;
  double cost_usd = 4;
}
```

**CLI**:
```bash
# Using prompt reference
delos runtime complete --prompt-ref "summarizer:v1" --variable "text=Hello world"

# Using direct messages
delos runtime complete --message "system:You are helpful" --message "user:Hello"

# Specify provider
delos runtime complete --provider openai --model gpt-4o --message "user:Hello"
```

**Python SDK**:
```python
# Using prompt reference
response = await client.runtime.complete(
    prompt_ref="summarizer:v1",
    variables={"text": "Long article..."}
)

# Using direct messages
response = await client.runtime.complete(
    messages=[
        {"role": "system", "content": "You are helpful."},
        {"role": "user", "content": "Hello!"}
    ],
    provider="anthropic",
    model="claude-3-5-sonnet-20241022"
)

print(f"Response: {response.content}")
print(f"Cost: ${response.usage.cost_usd:.6f}")
```

---

## CompleteStream

Perform a streaming LLM completion.

**Request**: Same as `CompleteRequest`

**Response** (stream):
```protobuf
message CompleteStreamResponse {
  string id = 1;
  string delta = 2;          // Incremental content
  bool done = 3;             // True for final chunk
  Message message = 4;       // Full message (only on done=true)
  string provider = 5;
  string model = 6;
  Usage usage = 7;           // Only on done=true
  string trace_id = 8;
}
```

**CLI**:
```bash
delos runtime complete --stream --message "user:Write a poem about AI"
```

**Python SDK**:
```python
async for chunk in client.runtime.complete_stream(
    messages=[{"role": "user", "content": "Write a poem"}]
):
    if chunk.delta:
        print(chunk.delta, end="", flush=True)
    if chunk.done:
        print(f"\n\nTokens: {chunk.usage.total_tokens}")
```

---

## Embed

Generate embeddings for text.

**Request**:
```protobuf
message EmbedRequest {
  repeated string texts = 1;  // Texts to embed
  string model = 2;           // Optional model override
  string provider = 3;        // Optional provider override
}
```

**Response**:
```protobuf
message EmbedResponse {
  repeated Embedding embeddings = 1;
  string model = 2;
  string provider = 3;
  Usage usage = 4;
}

message Embedding {
  repeated float values = 1;
  int32 dimensions = 2;
}
```

**CLI**:
```bash
delos runtime embed --text "Hello world" --text "Goodbye world"
```

**Python SDK**:
```python
embeddings = await client.runtime.embed(
    texts=["Hello world", "Goodbye world"]
)
for emb in embeddings.embeddings:
    print(f"Dimensions: {emb.dimensions}")
```

---

## Routing Strategies

| Strategy | Behavior |
|----------|----------|
| `COST_OPTIMIZED` | Selects cheapest provider for the model |
| `LATENCY_OPTIMIZED` | Selects fastest available provider |
| `QUALITY_OPTIMIZED` | Prefers Claude > GPT-4 > others |
| `SPECIFIC_PROVIDER` | Uses the specified provider |

## Supported Providers

| Provider | Models | Embeddings | Streaming |
|----------|--------|------------|-----------|
| OpenAI | gpt-4o, gpt-4o-mini, gpt-4-turbo, gpt-3.5-turbo | text-embedding-3-small | Yes |
| Anthropic | claude-opus-4, claude-sonnet-4, claude-3-5-sonnet, claude-3-5-haiku | No | Yes |
| Gemini | gemini-1.5-pro, gemini-1.5-flash, gemini-pro | text-embedding-004 | Yes |
| Ollama | Any installed model | nomic-embed-text | Yes |
