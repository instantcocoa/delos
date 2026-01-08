# Prompt Service

The Prompt Service manages versioned prompt templates for LLM applications. It provides prompt versioning, collaboration, and semantic comparison capabilities.

## Overview

The Prompt Service is a core component of the Delos platform, responsible for:

- **Prompt Versioning**: Every update creates a new version, preserving complete history
- **Template Management**: Store multi-message prompt templates with variable placeholders
- **Semantic Comparison**: Compare different versions to understand what changed
- **Flexible Retrieval**: Fetch prompts by ID, slug, or slug:version reference

## Port

The service runs on port **9002** by default.

## API Reference

The service exposes a gRPC API defined in `proto/prompt/v1/prompt.proto`.

### RPCs

| RPC | Description |
|-----|-------------|
| `CreatePrompt` | Creates a new prompt with an initial version |
| `GetPrompt` | Retrieves a prompt by ID or reference (e.g., `summarizer:v2`) |
| `UpdatePrompt` | Creates a new version of an existing prompt |
| `ListPrompts` | Lists prompts with filtering by search, tags, and status |
| `DeletePrompt` | Soft deletes a prompt (sets `deleted_at` timestamp) |
| `GetPromptHistory` | Returns version history for a prompt |
| `CompareVersions` | Performs semantic diff between two versions |
| `Health` | Health check endpoint |

### Key Types

#### Prompt

```protobuf
message Prompt {
  string id = 1;
  string name = 2;
  string slug = 3;              // URL-friendly name (e.g., "summarizer")
  int32 version = 4;
  string description = 5;
  repeated PromptMessage messages = 6;
  repeated PromptVariable variables = 7;
  GenerationConfig default_config = 8;
  repeated string tags = 9;
  map<string, string> metadata = 10;
  PromptStatus status = 15;
  // ... audit fields
}
```

#### PromptMessage

Messages support roles (`system`, `user`, `assistant`) and can contain `{{variable}}` placeholders:

```protobuf
message PromptMessage {
  string role = 1;     // system, user, assistant
  string content = 2;  // can contain {{variable}} placeholders
}
```

#### PromptVariable

Define variables that can be interpolated into prompt templates:

```protobuf
message PromptVariable {
  string name = 1;
  string description = 2;
  string type = 3;          // string, number, boolean, json
  bool required = 4;
  string default_value = 5;
}
```

#### PromptStatus

```protobuf
enum PromptStatus {
  PROMPT_STATUS_UNSPECIFIED = 0;
  PROMPT_STATUS_DRAFT = 1;
  PROMPT_STATUS_ACTIVE = 2;
  PROMPT_STATUS_DEPRECATED = 3;
  PROMPT_STATUS_ARCHIVED = 4;
}
```

## Configuration

The service uses environment variables for configuration.

### Common Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_ENV` | `development` | Environment (development, staging, production) |
| `DELOS_VERSION` | `dev` | Service version |
| `DELOS_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `DELOS_LOG_FORMAT` | `json` | Log format (json, text) |

### Storage Backend

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_STORAGE_BACKEND` | `memory` | Storage backend (`memory` or `postgres`) |

### Database (when using PostgreSQL)

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_DB_HOST` | `localhost` | PostgreSQL host |
| `DELOS_DB_PORT` | `5432` | PostgreSQL port |
| `DELOS_DB_USER` | `delos` | Database user |
| `DELOS_DB_PASSWORD` | (empty) | Database password |
| `DELOS_DB_NAME` | `delos` | Database name |
| `DELOS_DB_SSLMODE` | `disable` | SSL mode |

### Observability

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_OBSERVE_ENDPOINT` | `localhost:9000` | OTLP endpoint for traces |
| `DELOS_TRACING_ENABLED` | `true` | Enable tracing |
| `DELOS_TRACING_SAMPLING` | `1.0` | Trace sampling rate (0.0-1.0) |

## Running Locally

### With In-Memory Storage (Development)

```bash
# From repository root
go run ./services/prompt/cmd/server
```

### With PostgreSQL

```bash
# Start PostgreSQL (via Docker Compose)
make up

# Run migrations
psql -h localhost -U delos -d delos -f services/prompt/migrations/001_initial.up.sql

# Start the service
DELOS_STORAGE_BACKEND=postgres \
DELOS_DB_PASSWORD=yourpassword \
go run ./services/prompt/cmd/server
```

### Using Docker

```bash
docker build -t delos-prompt -f services/prompt/Dockerfile .
docker run -p 9002:9002 delos-prompt
```

## Architecture

### Versioning Strategy

The service implements immutable versioning:

1. **Initial Creation**: A new prompt starts at version 1
2. **Updates Create New Versions**: Each `UpdatePrompt` call increments the version
3. **All Versions Preserved**: Previous versions remain accessible via `GetVersion` or slug reference
4. **Soft Deletes**: Deleting a prompt sets `deleted_at` rather than removing data

### Reference Format

Prompts can be retrieved using flexible reference formats:

- By ID: `pmt_1234567890`
- By slug (latest version): `summarizer`
- By slug with version: `summarizer:v2` or `summarizer:2`
- By slug with latest keyword: `summarizer:latest`

### Semantic Comparison

The `CompareVersions` RPC compares two versions and returns:

- **Diffs**: List of changed fields with old/new values and diff type (added, removed, modified)
- **Semantic Similarity Score**: 0-1 score indicating how similar the versions are

Current implementation compares:
- Description text
- Message count and content
- Variables

The similarity score is calculated based on the number of differences detected.

### Storage Backends

The service supports two storage backends:

1. **Memory Store**: In-memory storage for development and testing. Data is lost on restart.

2. **PostgreSQL Store**: Persistent storage for production. Uses the following tables:
   - `prompts`: Core prompt metadata
   - `prompt_versions`: Version history with change descriptions
   - `prompt_messages`: Messages for each version
   - `prompt_variables`: Variable definitions per version
   - `prompt_generation_configs`: Default generation parameters per version
   - `prompt_tags`: Tag associations
   - `prompt_metadata`: Key-value metadata

### Database Schema

Key tables and relationships:

```
prompts (1) ──< prompt_versions (1) ──< prompt_messages
                     │
                     ├──< prompt_variables
                     │
                     └──< prompt_generation_configs

prompts (1) ──< prompt_tags
prompts (1) ──< prompt_metadata
```

## Example Usage

### Create a Prompt

```bash
grpcurl -plaintext -d '{
  "name": "Email Summarizer",
  "slug": "email-summarizer",
  "description": "Summarizes email threads",
  "messages": [
    {"role": "system", "content": "You are a helpful assistant that summarizes emails."},
    {"role": "user", "content": "Please summarize this email thread:\n\n{{email_content}}"}
  ],
  "variables": [
    {"name": "email_content", "type": "string", "required": true, "description": "The email thread to summarize"}
  ],
  "default_config": {
    "temperature": 0.3,
    "max_tokens": 500
  },
  "tags": ["email", "summarization"]
}' localhost:9002 delos.prompt.v1.PromptService/CreatePrompt
```

### Get a Prompt by Reference

```bash
# Get latest version
grpcurl -plaintext -d '{"reference": "email-summarizer"}' \
  localhost:9002 delos.prompt.v1.PromptService/GetPrompt

# Get specific version
grpcurl -plaintext -d '{"reference": "email-summarizer:v2"}' \
  localhost:9002 delos.prompt.v1.PromptService/GetPrompt
```

### Update a Prompt

```bash
grpcurl -plaintext -d '{
  "id": "pmt_1234567890",
  "messages": [
    {"role": "system", "content": "You are an expert assistant that creates concise email summaries."},
    {"role": "user", "content": "Summarize the following email thread in 3 bullet points:\n\n{{email_content}}"}
  ],
  "change_description": "Improved system prompt and added bullet point format"
}' localhost:9002 delos.prompt.v1.PromptService/UpdatePrompt
```

### List Prompts with Filters

```bash
grpcurl -plaintext -d '{
  "search": "email",
  "tags": ["summarization"],
  "status": "PROMPT_STATUS_ACTIVE",
  "limit": 10,
  "order_by": "updated_at",
  "descending": true
}' localhost:9002 delos.prompt.v1.PromptService/ListPrompts
```

### Compare Versions

```bash
grpcurl -plaintext -d '{
  "prompt_id": "pmt_1234567890",
  "version_a": 1,
  "version_b": 2
}' localhost:9002 delos.prompt.v1.PromptService/CompareVersions
```

### Get Version History

```bash
grpcurl -plaintext -d '{
  "id": "pmt_1234567890",
  "limit": 10
}' localhost:9002 delos.prompt.v1.PromptService/GetPromptHistory
```

## Dependencies

- **PostgreSQL 15+**: Required for production storage
- **Observe Service**: Optional, for trace collection (port 9000)

## Testing

```bash
# Run unit tests
go test ./services/prompt/...

# Run with verbose output
go test -v ./services/prompt/...

# Run with coverage
go test -cover ./services/prompt/...
```
