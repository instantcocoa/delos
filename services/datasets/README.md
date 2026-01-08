# Datasets Service

The Datasets service manages test data collections for evaluating LLM applications. It provides versioned datasets containing input/output examples that are used by the Eval service to assess prompt quality and detect regressions.

## Overview

**Port:** 9003

The Datasets service is responsible for:

- Creating and managing collections of test examples
- Linking datasets to specific prompts for targeted testing
- Importing examples from external sources (CSV, JSONL, Parquet, S3)
- Exporting examples for offline analysis or sharing
- Auto-generating examples using LLM (planned feature)
- Tracking example provenance (manual, generated, production, imported)

## API Reference

The service exposes a gRPC API defined in `proto/datasets/v1/datasets.proto`.

### Dataset Management

| RPC | Description |
|-----|-------------|
| `CreateDataset` | Creates a new dataset with name, description, schema, and optional prompt link |
| `GetDataset` | Retrieves a dataset by ID |
| `UpdateDataset` | Updates dataset metadata (name, description, tags) |
| `ListDatasets` | Lists datasets with filtering by prompt ID, tags, and search text |
| `DeleteDataset` | Deletes a dataset and all its examples |

### Example Management

| RPC | Description |
|-----|-------------|
| `AddExamples` | Adds one or more examples to a dataset |
| `GetExamples` | Retrieves examples from a dataset with pagination and optional shuffling |
| `RemoveExamples` | Removes specific examples by ID |
| `GenerateExamples` | Auto-generates examples using LLM (not yet implemented) |

### Import/Export

| RPC | Description |
|-----|-------------|
| `ImportExamples` | Imports examples from external sources (CSV, JSONL, JSON, Parquet) |
| `ExportExamples` | Exports examples to various formats, optionally to S3 |

### Health

| RPC | Description |
|-----|-------------|
| `Health` | Returns service health status and version |

## Data Model

### Dataset

A dataset is a collection of test examples with the following properties:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier (UUID) |
| `name` | string | Human-readable name |
| `description` | string | Optional description |
| `prompt_id` | string | Link to associated prompt (optional) |
| `schema` | DatasetSchema | Defines structure of input/output fields |
| `example_count` | int | Number of examples in the dataset |
| `version` | int | Incremented on each update |
| `tags` | []string | Labels for categorization |
| `metadata` | map[string]string | Custom key-value pairs |
| `created_by` | string | Creator identifier |
| `created_at` | timestamp | Creation time |
| `last_updated` | timestamp | Last modification time |

### Example

An example represents a single test case:

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Unique identifier (UUID) |
| `dataset_id` | string | Parent dataset ID |
| `input` | Struct | Input variables for the prompt |
| `expected_output` | Struct | Expected output for evaluation |
| `metadata` | map[string]string | Custom metadata |
| `source` | ExampleSource | How the example was created |
| `created_at` | timestamp | Creation time |

### Example Sources

Examples track their provenance:

- `MANUAL` - Created manually by a user
- `GENERATED` - Auto-generated using LLM
- `PRODUCTION` - Captured from production traffic
- `IMPORTED` - Imported from external data

## Configuration

The service uses environment variables with the `DELOS_` prefix:

### Required

| Variable | Description | Default |
|----------|-------------|---------|
| `DELOS_STORAGE_BACKEND` | Storage backend: `memory` or `postgres` | `memory` |

### Database (when using PostgreSQL)

| Variable | Description | Default |
|----------|-------------|---------|
| `DELOS_DB_HOST` | PostgreSQL host | `localhost` |
| `DELOS_DB_PORT` | PostgreSQL port | `5432` |
| `DELOS_DB_USER` | Database user | `delos` |
| `DELOS_DB_PASSWORD` | Database password | (empty) |
| `DELOS_DB_NAME` | Database name | `delos` |
| `DELOS_DB_SSLMODE` | SSL mode | `disable` |

### Observability

| Variable | Description | Default |
|----------|-------------|---------|
| `DELOS_ENV` | Environment: `development`, `staging`, `production` | `development` |
| `DELOS_LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` | `info` |
| `DELOS_LOG_FORMAT` | Log format: `json` or `text` | `json` |
| `DELOS_OBSERVE_ENDPOINT` | OTLP endpoint for traces | `localhost:9000` |
| `DELOS_TRACING_ENABLED` | Enable distributed tracing | `true` |
| `DELOS_TRACING_SAMPLING` | Trace sampling rate (0.0-1.0) | `1.0` |

## Running Locally

### Using Go directly

```bash
# From repository root
go run ./services/datasets/cmd/server

# With PostgreSQL backend
DELOS_STORAGE_BACKEND=postgres \
DELOS_DB_HOST=localhost \
DELOS_DB_PASSWORD=secret \
go run ./services/datasets/cmd/server
```

### Using Docker

```bash
# Build the image
docker build -t delos-datasets -f services/datasets/Dockerfile .

# Run with in-memory storage
docker run -p 9003:9003 delos-datasets

# Run with PostgreSQL
docker run -p 9003:9003 \
  -e DELOS_STORAGE_BACKEND=postgres \
  -e DELOS_DB_HOST=host.docker.internal \
  -e DELOS_DB_PASSWORD=secret \
  delos-datasets
```

### Using Make

```bash
# Start all dependencies (PostgreSQL, Redis, etc.)
make up

# Run the datasets service
make run-datasets
```

## Architecture

### Service Dependencies

```
              +-----------+
              |  prompt   |
              +-----------+
                    ^
                    | (prompt_id reference)
                    |
              +-----------+
              | datasets  |
              +-----------+
                    ^
                    | (provides test data)
                    |
              +-----------+
              |   eval    |
              +-----------+
```

The Datasets service:
- References prompts by ID (does not call Prompt service directly)
- Is consumed by the Eval service for running evaluations
- Has no hard runtime dependencies on other services

### Internal Structure

```
services/datasets/
├── cmd/server/main.go    # Entry point, wires dependencies
├── datasets.go           # Domain types and DTOs
├── service.go            # Business logic layer
├── handler.go            # gRPC handlers
├── store.go              # Storage interface and memory implementation
├── dataformat.go         # CSV, JSON, JSONL, Parquet parsers/writers
├── datasource.go         # Data source abstractions (local, S3, URL)
├── migrations/           # PostgreSQL schema migrations
└── Dockerfile
```

### Storage

The service supports two storage backends:

1. **Memory** (default): In-memory storage suitable for development and testing
2. **PostgreSQL**: Persistent storage for production use

The PostgreSQL schema includes:
- `datasets` - Dataset metadata
- `dataset_tags` - Many-to-many dataset-tag relationships
- `dataset_schema_fields` - Schema field definitions
- `examples` - Test case data with JSONB for flexible input/output

## Example Usage

### Creating a Dataset

```bash
grpcurl -plaintext -d '{
  "name": "Customer Support Q&A",
  "description": "Test cases for customer support responses",
  "prompt_id": "prompt-123",
  "schema": {
    "input_fields": [
      {"name": "question", "type": "string", "required": true},
      {"name": "context", "type": "string", "required": false}
    ],
    "expected_output_fields": [
      {"name": "answer", "type": "string", "required": true},
      {"name": "confidence", "type": "number", "required": false}
    ]
  },
  "tags": ["support", "qa", "v1"]
}' localhost:9003 delos.datasets.v1.DatasetsService/CreateDataset
```

### Adding Examples

```bash
grpcurl -plaintext -d '{
  "dataset_id": "dataset-uuid",
  "examples": [
    {
      "input": {"question": "How do I reset my password?", "context": "user account"},
      "expected_output": {"answer": "Go to Settings > Security > Reset Password", "confidence": 0.95},
      "source": "EXAMPLE_SOURCE_MANUAL"
    },
    {
      "input": {"question": "What are your business hours?"},
      "expected_output": {"answer": "We are open Monday-Friday, 9 AM to 5 PM EST"},
      "source": "EXAMPLE_SOURCE_MANUAL"
    }
  ]
}' localhost:9003 delos.datasets.v1.DatasetsService/AddExamples
```

### Importing from CSV

```bash
grpcurl -plaintext -d '{
  "dataset_id": "dataset-uuid",
  "source": {
    "s3": {
      "bucket": "my-data-bucket",
      "key": "datasets/support-qa.csv",
      "region": "us-east-1"
    }
  },
  "format": "DATA_FORMAT_CSV",
  "column_mappings": [
    {"source_column": "question", "target_field": "question", "is_input": true},
    {"source_column": "context", "target_field": "context", "is_input": true},
    {"source_column": "expected_answer", "target_field": "answer", "is_input": false}
  ],
  "csv_options": {
    "has_header": true,
    "delimiter": ","
  },
  "skip_invalid": true
}' localhost:9003 delos.datasets.v1.DatasetsService/ImportExamples
```

### Exporting to JSONL

```bash
grpcurl -plaintext -d '{
  "dataset_id": "dataset-uuid",
  "format": "DATA_FORMAT_JSONL"
}' localhost:9003 delos.datasets.v1.DatasetsService/ExportExamples
```

### Listing Datasets

```bash
# List all datasets
grpcurl -plaintext localhost:9003 delos.datasets.v1.DatasetsService/ListDatasets

# Filter by prompt
grpcurl -plaintext -d '{"prompt_id": "prompt-123"}' \
  localhost:9003 delos.datasets.v1.DatasetsService/ListDatasets

# Search by name/description
grpcurl -plaintext -d '{"search": "customer support", "limit": 10}' \
  localhost:9003 delos.datasets.v1.DatasetsService/ListDatasets
```

## Supported Data Formats

| Format | Import | Export | Description |
|--------|--------|--------|-------------|
| CSV | Yes | Yes | Comma-separated values with configurable delimiter |
| JSONL | Yes | Yes | JSON Lines (one JSON object per line) |
| JSON | Yes | Yes | JSON array of objects |
| Parquet | Yes | Yes | Apache Parquet columnar format |

## Supported Data Sources

| Source | Import | Export | Description |
|--------|--------|--------|-------------|
| Local File | Yes | No | Read from local filesystem |
| S3 | Yes | Yes | Amazon S3 or S3-compatible storage (MinIO) |
| URL | Yes | No | HTTP/HTTPS endpoints |
| Inline | Yes | No | Data provided directly in request |
| GCS | Planned | Planned | Google Cloud Storage |

## Development

### Running Tests

```bash
# Unit tests
go test ./services/datasets/...

# With coverage
go test -coverprofile=coverage.out ./services/datasets/...
go tool cover -html=coverage.out
```

### Database Migrations

```bash
# Apply migrations (requires migrate CLI)
migrate -path services/datasets/migrations \
  -database "postgres://delos:password@localhost:5432/delos?sslmode=disable" \
  up
```
