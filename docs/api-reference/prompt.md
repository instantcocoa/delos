# Prompt Service API Reference

The Prompt Service manages versioned prompts with full history tracking and semantic diffing.

It is a module of the `delos` control plane, not a standalone process: `delos serve` hosts it alongside observe, datasets, eval and deploy in a single binary.

**Port**: 8081 (the control plane port; override with `DELOS_PORT`)
**Transport**: gRPC over h2c (plaintext HTTP/2) on that same port
**Package**: `delos.prompt.v1`

## Connecting

```bash
# CLI (default: localhost:8081)
delos prompt list

# Point at another control plane
DELOS_CONTROL_PLANE_ADDR=delos.internal:8081 delos prompt list
```

```bash
# Raw gRPC — the control plane enables server reflection
grpcurl -plaintext localhost:8081 list delos.prompt.v1.PromptService
grpcurl -plaintext -d '{"id": "pmt_123"}' localhost:8081 delos.prompt.v1.PromptService/GetPrompt
```

Storage is in-memory by default. Set `DELOS_STORAGE_BACKEND=postgres` (plus `DELOS_DB_*`) to persist prompts; the control plane applies its own migrations at startup.

## Endpoints

| RPC | Description |
|-----|-------------|
| [Health](#health) | Service health check |
| [CreatePrompt](#createprompt) | Create a new prompt |
| [GetPrompt](#getprompt) | Get a prompt by ID or reference |
| [UpdatePrompt](#updateprompt) | Update a prompt (creates new version) |
| [ListPrompts](#listprompts) | List prompts with filtering |
| [DeletePrompt](#deleteprompt) | Delete a prompt |
| [GetPromptHistory](#getprompthistory) | Get version history |
| [CompareVersions](#compareversions) | Compare two versions |

---

## Health

Check service health status.

**Request**: `HealthRequest` (empty)

**Response**:
```protobuf
message HealthResponse {
  string status = 1;
  string version = 2;
}
```

The control plane process as a whole also answers `GET http://localhost:8081/healthz` and the standard gRPC health service (`grpc.health.v1.Health`, with sub-services `observe`, `prompt`, `datasets`, `eval`, `deploy`).

---

## CreatePrompt

Create a new versioned prompt.

**Request**:
```protobuf
message CreatePromptRequest {
  string name = 1;                      // Display name
  string slug = 2;                      // URL-friendly identifier (unique)
  string description = 3;               // Optional description
  repeated PromptMessage messages = 4;  // Prompt template
  repeated PromptVariable variables = 5;
  GenerationConfig default_config = 6;
  repeated string tags = 7;             // Tags for filtering
  map<string, string> metadata = 8;     // Custom metadata
}

message PromptMessage {
  string role = 1;     // system, user, assistant
  string content = 2;  // Message content (supports {{variables}})
}

message PromptVariable {
  string name = 1;
  string description = 2;
  string type = 3;           // string, number, boolean, json
  bool required = 4;
  string default_value = 5;
}

message GenerationConfig {
  double temperature = 1;
  int32 max_tokens = 2;
  double top_p = 3;
  repeated string stop = 4;
  string output_schema = 5;  // JSON schema for structured output
}
```

**Response**:
```protobuf
message CreatePromptResponse {
  Prompt prompt = 1;
}

message Prompt {
  string id = 1;
  string name = 2;
  string slug = 3;
  int32 version = 4;                    // Always 1 for new prompts
  string description = 5;
  repeated PromptMessage messages = 6;
  repeated PromptVariable variables = 7;
  GenerationConfig default_config = 8;
  repeated string tags = 9;
  map<string, string> metadata = 10;
  string created_by = 11;
  google.protobuf.Timestamp created_at = 12;
  string updated_by = 13;
  google.protobuf.Timestamp updated_at = 14;
  PromptStatus status = 15;
}

enum PromptStatus {
  PROMPT_STATUS_UNSPECIFIED = 0;
  PROMPT_STATUS_DRAFT = 1;
  PROMPT_STATUS_ACTIVE = 2;
  PROMPT_STATUS_DEPRECATED = 3;
  PROMPT_STATUS_ARCHIVED = 4;
}
```

**CLI**:
```bash
delos prompt create "Summarizer" \
  --slug summarizer \
  --system "Summarize the following text: {{text}}" \
  --tags production
```

---

## GetPrompt

Get a prompt by ID, or by a `slug:version` reference.

**Request**:
```protobuf
message GetPromptRequest {
  // Either id or reference can be used
  string id = 1;
  string reference = 2;  // e.g., "summarizer:v2" or "summarizer:latest"
}

message GetPromptResponse {
  Prompt prompt = 1;
}
```

`id` takes precedence: the handler only resolves `reference` when `id` is empty.

**CLI**:
```bash
# By ID
delos prompt get pmt_123

# By reference — the positional argument is sent as `id`, so pass it empty
delos prompt get "" --reference summarizer:v2
```

---

## UpdatePrompt

Update a prompt, creating a new version.

**Request**:
```protobuf
message UpdatePromptRequest {
  string id = 1;
  string description = 2;
  repeated PromptMessage messages = 3;
  repeated PromptVariable variables = 4;
  GenerationConfig default_config = 5;
  repeated string tags = 6;
  map<string, string> metadata = 7;
  string change_description = 8;  // Describe the change
}
```

**Response**:
```protobuf
message UpdatePromptResponse {
  Prompt prompt = 1;          // Updated prompt with incremented version
  int32 previous_version = 2;
}
```

**CLI**:
```bash
delos prompt update pmt_123 \
  --system "Summarize in 3 bullet points: {{text}}" \
  --change-description "Made output more structured"
```

---

## ListPrompts

List prompts with filtering and pagination.

**Request**:
```protobuf
message ListPromptsRequest {
  string search = 1;          // Search in name/description
  repeated string tags = 2;   // Filter by tags
  PromptStatus status = 3;    // Filter by status
  int32 limit = 4;
  int32 offset = 5;
  string order_by = 6;        // created_at, updated_at, name
  bool descending = 7;
}
```

**Response**:
```protobuf
message ListPromptsResponse {
  repeated Prompt prompts = 1;
  int32 total_count = 2;
}
```

**CLI**:
```bash
delos prompt list --tags production --limit 10
delos prompt list --search summar
```

---

## DeletePrompt

Delete a prompt.

**Request**:
```protobuf
message DeletePromptRequest {
  string id = 1;
}
```

**Response**:
```protobuf
message DeletePromptResponse {
  bool success = 1;
}
```

**CLI**:
```bash
delos prompt delete pmt_123
```

---

## GetPromptHistory

Get the version history of a prompt.

**Request**:
```protobuf
message GetPromptHistoryRequest {
  string id = 1;     // Prompt ID
  int32 limit = 2;   // Max versions to return
}
```

**Response**:
```protobuf
message GetPromptHistoryResponse {
  string prompt_id = 1;
  repeated PromptVersion versions = 2;
}

message PromptVersion {
  int32 version = 1;
  string change_description = 2;
  string updated_by = 3;
  google.protobuf.Timestamp updated_at = 4;
}
```

**CLI**:
```bash
delos prompt history pmt_123 --limit 10
```

---

## CompareVersions

Compare two versions of a prompt.

**Request**:
```protobuf
message CompareVersionsRequest {
  string prompt_id = 1;
  int32 version_a = 2;
  int32 version_b = 3;
}
```

**Response**:
```protobuf
message CompareVersionsResponse {
  repeated VersionDiff diffs = 1;
  double semantic_similarity = 2;  // 0-1 score of semantic similarity
}

message VersionDiff {
  string field = 1;      // e.g. "description", "messages[0].content"
  string old_value = 2;
  string new_value = 3;
  string diff_type = 4;  // added, removed, modified
}
```

**CLI**:
```bash
delos prompt compare pmt_123 1 3
```

---

## Prompt References

Prompts can be referenced by slug and version using the format:

```
{slug}:v{version}
{slug}:latest
```

Examples:
- `summarizer:v1` - Version 1 of summarizer
- `summarizer:v2` - Version 2 of summarizer
- `summarizer:latest` - Newest version of summarizer

Pass a reference to `GetPrompt` to resolve it to a concrete `Prompt`:

```bash
delos prompt get "" --reference summarizer:v2 --output json
```

The gateway (`delos-gateway`) has no knowledge of prompts — it only speaks the OpenAI/Anthropic HTTP surfaces. To run a stored prompt, resolve it here and send the resulting messages to the gateway, or let an eval run do it for you (the control plane's eval runner fetches prompts in-process and calls the gateway over `DELOS_GATEWAY_URL`).
