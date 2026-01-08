# Eval Service

The eval service provides quality assessment and regression testing for LLM outputs. It runs evaluations against prompts and datasets, computing scores using configurable evaluators, and enables comparison between evaluation runs to detect regressions.

## Overview

The eval service is responsible for:

- Running evaluation suites against prompts using test datasets
- Computing quality scores with multiple evaluator types
- Tracking evaluation run status and progress
- Comparing runs to detect regressions and improvements
- Aggregating cost, latency, and performance metrics

**Port**: 9004

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Eval Service                             │
├─────────────────────────────────────────────────────────────────┤
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐      │
│  │   Handler    │───▶│   Service    │───▶│    Store     │      │
│  │   (gRPC)     │    │   (Logic)    │    │  (Storage)   │      │
│  └──────────────┘    └──────────────┘    └──────────────┘      │
│                              │                                  │
│                              ▼                                  │
│                      ┌──────────────┐                          │
│                      │  Evaluators  │                          │
│                      │  - exact_match                          │
│                      │  - contains                             │
│                      │  - semantic_similarity                  │
│                      │  - llm_judge                            │
│                      │  - regex                                │
│                      │  - json_schema                          │
│                      └──────────────┘                          │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┼───────────────┐
              ▼               ▼               ▼
        ┌──────────┐   ┌──────────┐   ┌──────────┐
        │ Runtime  │   │  Prompt  │   │ Datasets │
        │ Service  │   │ Service  │   │ Service  │
        └──────────┘   └──────────┘   └──────────┘
```

## API Reference

The eval service exposes a gRPC API defined in `proto/eval/v1/eval.proto`.

### RPCs

| RPC | Description |
|-----|-------------|
| `CreateEvalRun` | Creates and starts a new evaluation run against a prompt and dataset |
| `GetEvalRun` | Retrieves an evaluation run by ID with status and summary |
| `ListEvalRuns` | Lists evaluation runs with filtering by prompt, dataset, or status |
| `CancelEvalRun` | Cancels a pending or running evaluation |
| `GetEvalResults` | Retrieves detailed per-example results for an evaluation run |
| `CompareRuns` | Compares two evaluation runs to identify regressions and improvements |
| `ListEvaluators` | Returns available evaluator types and their parameters |
| `Health` | Health check endpoint |

### Key Messages

**EvalRun**: Represents an evaluation execution with status, progress, and summary.

```protobuf
message EvalRun {
  string id = 1;
  string name = 2;
  string prompt_id = 4;
  int32 prompt_version = 5;
  string dataset_id = 6;
  EvalConfig config = 7;
  EvalRunStatus status = 8;
  int32 total_examples = 10;
  int32 completed_examples = 11;
  EvalSummary summary = 12;
  // ... timestamps and metadata
}
```

**EvalRunStatus**: Tracks evaluation lifecycle.
- `PENDING` - Created, waiting to start
- `RUNNING` - Actively evaluating examples
- `COMPLETED` - Successfully finished
- `FAILED` - Terminated due to error
- `CANCELLED` - Cancelled by user

**EvalSummary**: Aggregated results from a completed evaluation.

```protobuf
message EvalSummary {
  double overall_score = 1;           // 0-1 weighted score
  map<string, double> scores_by_evaluator = 2;
  int32 passed_count = 3;
  int32 failed_count = 4;
  double pass_rate = 5;
  double total_cost_usd = 6;
  int32 total_tokens = 7;
  double avg_latency_ms = 8;
}
```

## Evaluators

The eval service supports multiple evaluator types, each providing different quality assessment strategies.

### exact_match

Checks if the actual output exactly matches the expected output.

**Parameters**: None

**Use case**: Deterministic outputs, structured data, exact text matching.

### contains

Checks if the actual output contains expected strings.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `case_sensitive` | boolean | No | `false` | Whether to use case-sensitive matching |

**Use case**: Checking for required keywords, phrases, or patterns.

### semantic_similarity

Compares outputs using embedding similarity scores.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `threshold` | number | No | `0.8` | Minimum similarity score (0-1) |
| `model` | string | No | `text-embedding-3-small` | Embedding model to use |

**Use case**: Paraphrased content, semantic equivalence, meaning-focused evaluation.

### llm_judge

Uses an LLM to evaluate output quality based on custom criteria.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `criteria` | string | Yes | - | Evaluation criteria for the judge |
| `model` | string | No | `gpt-4o` | Model to use as judge |

**Use case**: Subjective quality, helpfulness, tone, complex evaluation criteria.

### regex

Matches output against a regular expression pattern.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `pattern` | string | Yes | - | Regular expression pattern |

**Use case**: Format validation, structured output verification.

### json_schema

Validates output against a JSON schema.

| Parameter | Type | Required | Default | Description |
|-----------|------|----------|---------|-------------|
| `schema` | json | Yes | - | JSON schema to validate against |

**Use case**: Structured JSON output, API response validation.

## Evaluation Pipeline

1. **Create Run**: A new evaluation run is created with a prompt, dataset, and evaluator configuration
2. **Load Examples**: Examples are loaded from the specified dataset
3. **Execute**: For each example:
   - Render the prompt with example inputs
   - Call the runtime service for LLM completion
   - Run each configured evaluator on the output
   - Record individual results with scores and metadata
4. **Aggregate**: Compute summary statistics (overall score, pass rate, costs)
5. **Complete**: Mark the run as completed with final summary

### Scoring

- Each evaluator produces a score from 0 to 1
- Evaluators can have weights (default 1.0) that affect the overall score
- The overall score is the weighted average of all evaluator scores
- An example "passes" if its overall score exceeds the threshold (default 0.5)

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_ENV` | `development` | Environment (development, staging, production) |
| `DELOS_VERSION` | `dev` | Service version |
| `DELOS_STORAGE_BACKEND` | `memory` | Storage backend (`memory` or `postgres`) |
| `DELOS_DB_HOST` | `localhost` | PostgreSQL host |
| `DELOS_DB_PORT` | `5432` | PostgreSQL port |
| `DELOS_DB_USER` | `delos` | PostgreSQL user |
| `DELOS_DB_PASSWORD` | - | PostgreSQL password |
| `DELOS_DB_NAME` | `delos` | PostgreSQL database name |
| `DELOS_DB_SSLMODE` | `disable` | PostgreSQL SSL mode |
| `DELOS_REDIS_URL` | `redis://localhost:6379` | Redis connection URL |
| `DELOS_OBSERVE_ENDPOINT` | `localhost:9000` | Observe service endpoint for tracing |
| `DELOS_LOG_LEVEL` | `info` | Log level (debug, info, warn, error) |
| `DELOS_LOG_FORMAT` | `json` | Log format (json, text) |
| `DELOS_TRACING_ENABLED` | `true` | Enable OpenTelemetry tracing |
| `DELOS_TRACING_SAMPLING` | `1.0` | Trace sampling rate (0.0-1.0) |

## Running Locally

### Prerequisites

- Go 1.22+
- PostgreSQL 15+ (optional, for persistent storage)
- Redis 7+ (optional)

### Using Make

```bash
# From repository root
make run-eval
```

### Direct Run

```bash
# From repository root
go run ./services/eval/cmd/server

# Or build and run
go build -o bin/eval ./services/eval/cmd/server
./bin/eval
```

### Docker

```bash
# Build
docker build -t delos-eval -f services/eval/Dockerfile .

# Run
docker run -p 9004:9004 delos-eval
```

### Docker Compose

```bash
# Start all services including eval
make up

# Or just dependencies
docker compose -f deploy/local/docker-compose.yml up -d postgres redis
go run ./services/eval/cmd/server
```

## Database Schema

The eval service uses the following PostgreSQL tables (see `migrations/001_initial.up.sql`):

- `eval_runs` - Evaluation run metadata and status
- `eval_run_evaluators` - Evaluator configurations per run
- `eval_run_summaries` - Aggregated results per run
- `eval_results` - Individual example results
- `eval_result_scores` - Per-evaluator scores for each result

## Example Usage

### Create an Evaluation Run

```bash
grpcurl -plaintext -d '{
  "name": "v2-quality-check",
  "prompt_id": "prompt-123",
  "prompt_version": 2,
  "dataset_id": "dataset-456",
  "config": {
    "evaluators": [
      {"type": "exact_match", "weight": 0.3},
      {"type": "semantic_similarity", "weight": 0.7, "params": {"threshold": "0.85"}}
    ],
    "provider": "openai",
    "model": "gpt-4o",
    "concurrency": 5
  }
}' localhost:9004 delos.eval.v1.EvalService/CreateEvalRun
```

### Get Evaluation Run Status

```bash
grpcurl -plaintext -d '{"id": "run-789"}' \
  localhost:9004 delos.eval.v1.EvalService/GetEvalRun
```

### List Evaluation Runs

```bash
grpcurl -plaintext -d '{"prompt_id": "prompt-123", "limit": 10}' \
  localhost:9004 delos.eval.v1.EvalService/ListEvalRuns
```

### Compare Two Runs

```bash
grpcurl -plaintext -d '{"run_id_a": "run-old", "run_id_b": "run-new"}' \
  localhost:9004 delos.eval.v1.EvalService/CompareRuns
```

### List Available Evaluators

```bash
grpcurl -plaintext localhost:9004 delos.eval.v1.EvalService/ListEvaluators
```

### Get Detailed Results (Failed Only)

```bash
grpcurl -plaintext -d '{"eval_run_id": "run-789", "failed_only": true}' \
  localhost:9004 delos.eval.v1.EvalService/GetEvalResults
```

## Dependencies

The eval service depends on:

| Service | Purpose |
|---------|---------|
| **runtime** | Executes LLM completions for evaluation |
| **prompt** | Retrieves prompt templates and versions |
| **datasets** | Loads test examples for evaluation |
| **observe** | Receives traces and metrics |

## Testing

```bash
# Run unit tests
go test ./services/eval/...

# Run with verbose output
go test -v ./services/eval/...

# Run with coverage
go test -cover ./services/eval/...
```

## Health Check

```bash
grpcurl -plaintext localhost:9004 delos.eval.v1.EvalService/Health
```

Response:
```json
{
  "status": "healthy",
  "version": "0.1.0"
}
```
