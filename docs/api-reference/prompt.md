# Prompt Service API Reference

The Prompt Service manages versioned prompts with full history tracking and semantic diffing.

**Port**: 9002
**Package**: `delos.prompt.v1`

## Endpoints

| RPC | Description |
|-----|-------------|
| [Health](#health) | Service health check |
| [CreatePrompt](#createprompt) | Create a new prompt |
| [GetPrompt](#getprompt) | Get a prompt by ID or slug |
| [UpdatePrompt](#updateprompt) | Update a prompt (creates new version) |
| [ListPrompts](#listprompts) | List prompts with filtering |
| [DeletePrompt](#deleteprompt) | Archive a prompt |
| [GetPromptHistory](#getprompthistory) | Get version history |
| [CompareVersions](#compareversions) | Compare two versions |

---

## Health

Check service health status.

**Response**:
```protobuf
message HealthResponse {
  string status = 1;  // "healthy"
}
```

---

## CreatePrompt

Create a new versioned prompt.

**Request**:
```protobuf
message CreatePromptRequest {
  string name = 1;                    // Display name
  string slug = 2;                    // URL-safe identifier (unique)
  string description = 3;             // Optional description
  repeated PromptMessage messages = 4; // Prompt template
  map<string, string> metadata = 5;   // Custom metadata
  repeated string tags = 6;           // Tags for filtering
}

message PromptMessage {
  string role = 1;     // system, user, assistant
  string content = 2;  // Message content (supports {{variables}})
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
  int32 version = 4;                   // Always 1 for new prompts
  string description = 5;
  repeated PromptMessage messages = 6;
  map<string, string> metadata = 7;
  repeated string tags = 8;
  PromptStatus status = 9;
  google.protobuf.Timestamp created_at = 10;
  google.protobuf.Timestamp updated_at = 11;
}

enum PromptStatus {
  PROMPT_STATUS_UNSPECIFIED = 0;
  PROMPT_STATUS_ACTIVE = 1;
  PROMPT_STATUS_ARCHIVED = 2;
}
```

**CLI**:
```bash
delos prompt create \
  --name "Summarizer" \
  --slug "summarizer" \
  --message "system:Summarize the following text." \
  --tag "production"
```

**Python SDK**:
```python
prompt = await client.prompts.create(
    name="Summarizer",
    slug="summarizer",
    messages=[
        {"role": "system", "content": "Summarize the following text: {{text}}"}
    ],
    tags=["production"]
)
print(f"Created: {prompt.id} v{prompt.version}")
```

---

## GetPrompt

Get a prompt by ID, slug, or slug with version.

**Request**:
```protobuf
message GetPromptRequest {
  string id = 1;       // Prompt ID (pmt_...)
  string slug = 2;     // Prompt slug
  int32 version = 3;   // Optional version (default: latest)
}
```

**CLI**:
```bash
# By ID
delos prompt get --id pmt_123

# By slug (latest version)
delos prompt get --slug summarizer

# By slug with version
delos prompt get --slug summarizer --version 2
```

**Python SDK**:
```python
# By ID
prompt = await client.prompts.get(id="pmt_123")

# By slug
prompt = await client.prompts.get(slug="summarizer")

# Specific version
prompt = await client.prompts.get(slug="summarizer", version=2)
```

---

## UpdatePrompt

Update a prompt, creating a new version.

**Request**:
```protobuf
message UpdatePromptRequest {
  string id = 1;
  string name = 2;                     // Optional
  string description = 3;              // Optional
  repeated PromptMessage messages = 4;  // New messages
  map<string, string> metadata = 5;
  repeated string tags = 6;
  string change_description = 7;       // Describe the change
}
```

**Response**: Returns the updated `Prompt` with incremented version.

**CLI**:
```bash
delos prompt update \
  --id pmt_123 \
  --message "system:Summarize in 3 bullet points." \
  --change "Made output more structured"
```

**Python SDK**:
```python
updated = await client.prompts.update(
    id="pmt_123",
    messages=[
        {"role": "system", "content": "Summarize in 3 bullet points: {{text}}"}
    ],
    change_description="Made output more structured"
)
print(f"Now at v{updated.version}")
```

---

## ListPrompts

List prompts with filtering and pagination.

**Request**:
```protobuf
message ListPromptsRequest {
  repeated string tags = 1;       // Filter by tags
  PromptStatus status = 2;        // Filter by status
  string search = 3;              // Search name/description
  int32 limit = 4;                // Page size (default 20)
  string cursor = 5;              // Pagination cursor
}
```

**Response**:
```protobuf
message ListPromptsResponse {
  repeated Prompt prompts = 1;
  string next_cursor = 2;
}
```

**CLI**:
```bash
delos prompt list --tag production --limit 10
```

**Python SDK**:
```python
prompts = await client.prompts.list(tags=["production"], limit=10)
for p in prompts:
    print(f"{p.slug} v{p.version}: {p.name}")
```

---

## DeletePrompt

Archive a prompt (soft delete).

**Request**:
```protobuf
message DeletePromptRequest {
  string id = 1;
}
```

**Response**: Returns the archived `Prompt` with `status = ARCHIVED`.

**CLI**:
```bash
delos prompt delete --id pmt_123
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
  repeated PromptVersion versions = 1;
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
delos prompt history --id pmt_123
```

**Python SDK**:
```python
history = await client.prompts.get_history(id="pmt_123")
for v in history.versions:
    print(f"v{v.version}: {v.change_description}")
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
  repeated MessageDiff diffs = 1;
  double semantic_similarity = 2;  // 0.0 to 1.0
}

message MessageDiff {
  string role = 1;
  string content_a = 2;
  string content_b = 3;
  DiffType diff_type = 4;
}

enum DiffType {
  DIFF_TYPE_UNSPECIFIED = 0;
  DIFF_TYPE_ADDED = 1;
  DIFF_TYPE_REMOVED = 2;
  DIFF_TYPE_MODIFIED = 3;
  DIFF_TYPE_UNCHANGED = 4;
}
```

**CLI**:
```bash
delos prompt compare --id pmt_123 --version-a 1 --version-b 3
```

**Python SDK**:
```python
diff = await client.prompts.compare_versions(
    prompt_id="pmt_123",
    version_a=1,
    version_b=3
)
print(f"Similarity: {diff.semantic_similarity:.2%}")
for d in diff.diffs:
    print(f"{d.role}: {d.diff_type}")
```

---

## Prompt References

Prompts can be referenced in the Runtime service using the format:

```
{slug}:v{version}
```

Examples:
- `summarizer:v1` - Version 1 of summarizer
- `summarizer:v2` - Version 2 of summarizer

```python
response = await client.runtime.complete(
    prompt_ref="summarizer:v2",
    variables={"text": "Long article..."}
)
```
