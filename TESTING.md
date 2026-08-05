# Delos Testing Guide

This document describes the integration testing strategy for Delos, including test coverage, known gaps, and how to run tests.

Delos is two binaries plus Postgres, so the integration tests exercise exactly two endpoints:

| Process | Address | Env override |
|---------|---------|--------------|
| `delos-gateway` (data plane, HTTP) | `http://localhost:8080` | `DELOS_GATEWAY_URL` |
| `delos serve` (control plane, gRPC) | `localhost:8081` | `DELOS_CONTROL_PLANE_ADDR` |

The control plane serves observe, prompt, datasets, eval and deploy from that single gRPC port. The gateway has no gRPC surface at all; it is exercised over plain HTTP.

## Quick Start

```bash
# Unit tests - no external dependencies
make test-unit

# All tests, with Postgres + LocalStack started automatically
make test

# Bring up the stack, then run integration tests against it
make up-all           # postgres + gateway (8080) + delos (8081) in Docker
make test-integration

# Optional: local LLM for the Ollama tests (~3.3GB model download)
make up-ollama
make test-ollama
```

Prefer running the binaries locally? Use Postgres in Docker and the binaries on the host:

```bash
make up          # Postgres only
make run-all     # gateway + control plane in the background
make test-integration
make stop-all
```

## Test Organization

```
tests/integration/
├── all_services_test.go     # Smoke tests: gateway HTTP surfaces + control-plane modules
├── clients_test.go          # Shared gRPC/HTTP client helpers and wire types
├── cli_test.go              # CLI integration tests
├── datasets_test.go         # Datasets tests
├── error_handling_test.go   # Error handling tests (NOT_FOUND, bad input, etc.)
├── evaluators_test.go       # Eval CRUD and evaluator tests
├── ollama_test.go           # LLM completion tests via the gateway + Ollama
├── prompt_test.go           # Prompt versioning tests
├── workflow_test.go         # End-to-end workflow tests
└── run.sh                   # Test runner script
```

## Test Categories

### 1. Unit Tests (`make test-unit`)

Fast tests that don't require external services. Run these for quick feedback during development. `make test` runs the same suite but starts Postgres and LocalStack first, so tests that need them don't self-skip.

### 2. Integration Tests (`make test-integration`)

Tests that require the running stack. Built with the `integration` build tag; they hit the gateway over HTTP and the control plane over gRPC. Run `./tests/integration/run.sh --help` for the runner's options.

### 3. Ollama Tests (`make test-ollama`)

Tests that require a local Ollama with the gemma3:4b model, reached through the gateway's `ollama/` provider prefix. They skip themselves when the gateway does not list any `ollama`-owned model.

## Test Coverage

### Gateway (HTTP, port 8080)

| Test | File | Description |
|------|------|-------------|
| `TestGateway_Healthz` | all_services_test.go | Health check + provider count |
| `TestGateway_ListModels` | all_services_test.go | `GET /v1/models` |
| `TestGateway_GetModel` | all_services_test.go | `GET /v1/models/{model}` |
| `TestGateway_ChatCompletions` | all_services_test.go | `POST /v1/chat/completions` |
| `TestGateway_ChatCompletions_Stream` | all_services_test.go | SSE streaming |
| `TestGateway_Embeddings` | all_services_test.go | `POST /v1/embeddings` |
| `TestOllama_Available` | ollama_test.go | Ollama provider registered |
| `TestOllama_Complete_*` | ollama_test.go | Various completion scenarios |
| `TestOllama_CompleteStream` | ollama_test.go | Streaming with Ollama |
| `TestPromptWithOllamaCompletion` | workflow_test.go | Stored prompt + gateway completion |

**Error Handling Tests:**
| Test | File | Description |
|------|------|-------------|
| `TestGateway_Complete_UnknownModel` | all_services_test.go | `model_not_found` envelope |
| `TestGateway_Complete_MalformedJSON` | all_services_test.go | `invalid_json` envelope |
| `TestGateway_Complete_MissingFields` | all_services_test.go | Missing `model`/`messages` |

**Gaps:**
- [ ] Anthropic `POST /v1/messages` surface (non-streaming and streaming)
- [ ] `encoding_format: "base64"` embeddings
- [ ] `stream_options.include_usage` usage chunk
- [ ] Mid-stream provider failure (`provider_stream_error`)
- [ ] Rejection of unsupported parameters (`tools`, `n` > 1, image content)
- [ ] Provider auth failures surfacing as `502 provider_error`
- [ ] Token counting and `cost_usd` accuracy

### Observe (control plane)

| Test | File | Description |
|------|------|-------------|
| `TestObserveService_Health` | all_services_test.go | Health check |
| `TestObserveService_IngestTraces` | all_services_test.go | Span ingestion |
| `TestObserveService_QueryTraces` | all_services_test.go | Query traces |
| `TestObserveService_GetTrace` | all_services_test.go | Get trace by ID |
| `TestObserveService_QueryMetrics` | all_services_test.go | Query metrics |
| `TestOllamaCallsAreTraced` | workflow_test.go | Verify LLM calls create traces |

**Gaps:**
- [ ] Complex trace assembly with parent-child relationships
- [ ] Duration and time range filtering
- [ ] Attribute/tag filtering
- [ ] Span event handling
- [ ] Metrics aggregation

### Prompt (control plane)

| Test | File | Description |
|------|------|-------------|
| `TestPromptService_Health` | all_services_test.go | Health check |
| `TestPromptService_CreateAndGet` | prompt_test.go | Basic CRUD |
| `TestPromptService_UpdateCreatesNewVersion` | prompt_test.go | Version increment on update |
| `TestPromptService_List` | prompt_test.go | List with tag filters |
| `TestPromptService_GetPromptHistory` | prompt_test.go | Version history |
| `TestPromptService_CompareVersions` | prompt_test.go | Semantic diff |
| `TestPromptService_Delete` | prompt_test.go | Delete |
| `TestPromptVersioningWorkflow` | workflow_test.go | Full versioning workflow |

**Coverage:**
- Version creation and retrieval
- `slug:version` references (`summarizer:v2`, `summarizer:latest`)
- Version history tracking
- Semantic comparison between versions
- Tag-based filtering

**Error Handling Tests:**
| Test | File | Description |
|------|------|-------------|
| `TestPromptService_GetPrompt_NotFound` | error_handling_test.go | Nonexistent prompt ID |
| `TestPromptService_DeletePrompt_NotFound` | error_handling_test.go | Delete nonexistent |
| `TestPromptService_CreatePrompt_DuplicateSlug` | error_handling_test.go | Duplicate slug error |

**Gaps:**
- [ ] Reference-only lookup (`GetPromptRequest.reference` with an empty `id`)
- [ ] Variable type validation
- [ ] Generation config edge cases
- [ ] Concurrent version creation (race conditions)
- [ ] Large prompt content handling
- [ ] Search by name/description

### Datasets (control plane)

| Test | File | Description |
|------|------|-------------|
| `TestDatasetsService_Health` | all_services_test.go | Health check |
| `TestDatasetsService_CreateAndGet` | datasets_test.go | Basic CRUD |
| `TestDatasetsService_AddExamples` | datasets_test.go | Add examples |
| `TestDatasetsService_LinkToPrompt` | datasets_test.go | Dataset-prompt association |
| `TestDatasetsService_List` | datasets_test.go | List with tag filters |
| `TestDatasetsService_FullCRUD` | all_services_test.go | Full CRUD workflow |

**Error Handling Tests:**
| Test | File | Description |
|------|------|-------------|
| `TestDatasetsService_GetDataset_NotFound` | error_handling_test.go | NOT_FOUND for invalid ID |
| `TestDatasetsService_AddExamples_InvalidDatasetID` | error_handling_test.go | Invalid dataset reference |
| `TestDatasetsService_ImportExamples_NoData` | error_handling_test.go | Import validation |
| `TestDatasetsService_ExportExamples_NonexistentDataset` | error_handling_test.go | Export error handling |

**Import/Export: IMPLEMENTED**

The `ImportExamples` and `ExportExamples` RPCs are now fully implemented:
- **ImportExamples** supports JSON, JSONL, and CSV formats with inline data
- **ExportExamples** supports JSON, JSONL, and CSV output formats
- Column mappings for custom field mapping during import
- Auto-detection of input/output fields using naming conventions

**Remaining Gaps:**
- [ ] S3/GCS/URL data source support for import
- [ ] External destination support for export
- [ ] Schema validation against examples
- [ ] Pagination edge cases
- [ ] Large dataset handling

### Eval (control plane)

| Test | File | Description |
|------|------|-------------|
| `TestEvalService_Health` | all_services_test.go | Health check |
| `TestEvalService_ListEvaluators` | all_services_test.go | Available evaluators |
| `TestEvalService_FullWorkflow` | all_services_test.go | Create run, get results, compare |
| `TestEvaluator_ListAvailable` | evaluators_test.go | Verify all 6 evaluators exist |
| `TestEvalRun_Create` | evaluators_test.go | Create eval run with config |
| `TestEvalRun_Get` | evaluators_test.go | Get eval run by ID |
| `TestEvalRun_List` | evaluators_test.go | List runs with filters |
| `TestEvalRun_Cancel` | evaluators_test.go | Cancel pending run |
| `TestEvalRun_MultipleEvaluatorConfigs` | evaluators_test.go | Multi-evaluator setup |
| `TestEvalWithOllama` | workflow_test.go | Eval run executed against Ollama |
| `TestEvalService_GetEvalRun_NotFound` | error_handling_test.go | NOT_FOUND error |
| `TestEvalService_CancelEvalRun_NotFound` | error_handling_test.go | Cancel nonexistent |
| `TestEvalService_CompareRuns_NotFound` | error_handling_test.go | Compare nonexistent |
| `TestEvalService_CreateEvalRun_InvalidPromptID` | error_handling_test.go | Invalid prompt reference |

**Evaluators Available:**
- `exact_match` - Exact string comparison
- `contains` - Substring matching (`case_sensitive` param)
- `semantic_similarity` - Embedding-based comparison
- `llm_judge` - AI-based quality assessment
- `regex` - Pattern matching (`pattern` param)
- `json_schema` - Structured output validation (`schema` param)

**Eval Execution Engine: IMPLEMENTED**

The eval runner runs inside the control plane process and:
- Polls for pending runs and executes them
- Fetches prompts and datasets via direct in-process calls (no network hop)
- Calls `delos-gateway` over its OpenAI-compatible HTTP surface for completions and embeddings (`DELOS_GATEWAY_URL`, optional `DELOS_GATEWAY_API_KEY`)
- Runs evaluators against actual vs expected outputs
- Stores results and computes summary statistics

**Implemented Evaluators:**
- `exact_match` - Exact string comparison (with whitespace normalization)
- `contains` - Substring matching (supports `case_sensitive` param)
- `regex` - Regular expression matching (requires `pattern` param)
- `json_schema` - JSON Schema validation (requires `schema` param)
- `llm_judge` - AI-powered quality assessment (uses `gpt-4o` by default)
- `semantic_similarity` - Embedding-based similarity (threshold: 0.8 default)

Both `llm_judge` and `semantic_similarity` need a gateway with a provider that serves the judge/embedding model, so they fail or skip when the gateway has no matching provider.

**Remaining Gaps:**
- [ ] Large dataset evaluation performance
- [ ] Concurrent evaluation runs
- [ ] Custom evaluator plugins

### Deploy (control plane)

| Test | File | Description |
|------|------|-------------|
| `TestDeployService_Health` | all_services_test.go | Health check |
| `TestDeployService_FullWorkflow` | all_services_test.go | Create, approve, rollback |
| `TestEndToEndWorkflow` | workflow_test.go | Deployment after eval |

**Deployment Strategies:**
- `IMMEDIATE` - All traffic at once
- `GRADUAL` - Incremental traffic shift
- `CANARY` - Small percentage first
- `BLUE_GREEN` - Parallel environments

**Gaps:**
- [ ] Quality gate evaluation with real eval scores
- [ ] Gradual rollout progression
- [ ] Auto-rollback on quality drop
- [ ] State machine transition validation
- [ ] Concurrent deployment handling

### CLI

`cli_test.go` drives the built `bin/delos` binary against both endpoints:

| Area | Tests |
|------|-------|
| Basics | `TestCLI_Help`, `TestCLI_Version`, `TestCLI_InvalidCommand`, `TestCLI_OutputFormats` |
| Control plane | `TestCLI_Prompt_*`, `TestCLI_Datasets_*`, `TestCLI_Eval_*`, `TestCLI_Deploy_*`, `TestCLI_Observe_*` |
| Gateway | `TestCLI_Gateway_Health`, `TestCLI_Gateway_Models`, `TestCLI_Gateway_Complete`, `TestCLI_Gateway_RuntimeAlias` |

Build the binaries with `make build` before running these.

## End-to-End Workflow Tests

### `TestEndToEndWorkflow` (workflow_test.go)

Tests the complete Delos workflow:

1. **Create Prompt v1** - Initial prompt with variables
2. **Create Dataset** - Test cases linked to prompt
3. **Run Evaluation** - Eval through the gateway with Ollama + `contains` evaluator
4. **Create Quality Gate** - Score threshold requirement
5. **Create Deployment** - Deploy v1 to staging
6. **Update Prompt to v2** - New version
7. **Eval v2 and Compare** - Compare v1 vs v2 performance

**Duration:** ~15-30 seconds (with local Ollama and gemma3:4b)

## Known Gaps & Future Work

### Critical (Blocking Features)

1. **Gateway Tracing** - Tracing infrastructure exists but the gateway HTTP handlers create no spans

### Medium Priority

1. **Deploy Quality Gates** - Integration with eval for automated gates
2. **Anthropic surface coverage** - `POST /v1/messages` has no integration tests
3. **Concurrency** - Race condition testing
4. **Performance** - Large dataset handling, many versions
5. **Custom Evaluator Plugins** - Allow users to register custom evaluators

### Lower Priority (Nice to Have)

1. **Metrics** - Observe metrics aggregation
2. **PostgreSQL-specific** - Only the prompt module has a Postgres store; observe, datasets, eval and deploy are memory-only, so `DELOS_STORAGE_BACKEND=postgres` changes prompt behaviour alone
3. **Pagination edge cases** - Offset/limit boundary testing

## Running Specific Tests

```bash
# Via the runner (see --help for the current filters)
./tests/integration/run.sh --help
./tests/integration/run.sh ollama

# Or straight through go test
go test -tags=integration -v -run TestGateway ./tests/integration/...
go test -tags=integration -v -run TestPromptService_CreateAndGet ./tests/integration/...
```

## Environment Variables

```bash
# Endpoints under test
DELOS_GATEWAY_URL=http://localhost:8080          # delos-gateway (HTTP, with scheme)
DELOS_CONTROL_PLANE_ADDR=localhost:8081          # delos serve (gRPC, no scheme)

# LLM provider keys read by the gateway process (optional)
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
GEMINI_API_KEY=AIza...

# Ollama (local LLM) - also read by the gateway process
DELOS_RUNTIME_OLLAMA_ENABLED=true
DELOS_RUNTIME_OLLAMA_URL=http://localhost:11434

# Control plane storage (default: memory)
DELOS_STORAGE_BACKEND=postgres
DELOS_DB_HOST=localhost
DELOS_DB_PORT=5432
DELOS_DB_USER=delos
DELOS_DB_PASSWORD=delos
DELOS_DB_NAME=delos
DELOS_DB_SSLMODE=disable
```

Provider keys belong to the **gateway process**, not the test process — set them before starting the gateway (or put them in `deploy/local/.env.local` for Docker Compose) and restart it.

## Test Infrastructure

### Docker Compose

`deploy/local/docker-compose.yaml` starts three containers by default: `postgres`, `gateway` (8080) and `delos` (8081). Everything else is behind an opt-in profile.

```bash
make up-all     # start the default three
make logs       # follow container logs
make down       # stop everything, including profiles
```

Optional profiles (off by default):

| Profile | Provides | Port |
|---------|----------|------|
| `ollama` | Ollama + gemma3:4b model pull | 11434 |
| `observability` | Jaeger UI + OTLP collector | 16686, 4317, 4318 |
| `test` | LocalStack (S3) | 4566 |

`make test` and `make test-coverage` start `postgres` and `localstack` themselves and stop them afterwards.

### Ollama Setup

```bash
# Start Ollama and pull the model
make up-ollama

# Check model availability
make ollama-ready

# Manual model pull
docker exec delos-ollama ollama pull gemma3:4b
```

The gateway probes Ollama at startup, so start Ollama **before** the gateway (or restart the gateway afterwards) for the provider to register.

## Test Execution Times

| Test Suite | Approximate Time |
|------------|------------------|
| Unit tests | 5-10 seconds |
| Basic integration | 30-60 seconds |
| Ollama tests | 30-60 seconds |
| Workflow tests | 2-5 minutes |
| Full suite | 5-10 minutes |

## Debugging Failed Tests

```bash
# Run with maximum verbosity
go test -tags=integration -v -run TestName ./tests/integration/...

# Check process logs
docker compose -f deploy/local/docker-compose.yaml logs gateway
docker compose -f deploy/local/docker-compose.yaml logs delos

# Verify the two endpoints by hand
curl -s localhost:8080/healthz     # {"status":"ok","providers":N}
curl -s localhost:8081/healthz     # {"status":"ok"}
curl -s localhost:8080/v1/models

# Check Ollama status
curl -s http://localhost:11434/api/tags
```

`"providers": 0` from the gateway means no provider credentials were found in its environment — every completion test will fail with `model_not_found`.

## Contributing New Tests

1. Add tests to appropriate file in `tests/integration/`
2. Use `//go:build integration` tag
3. Use the shared helpers in `clients_test.go` (`getPromptClient`, `newGatewayClient`, ...) rather than dialling addresses directly
4. Clean up created resources in `defer` blocks
5. Use meaningful test names: `Test{Module}_{Feature}_{Scenario}` (`TestGateway_*` for the data plane)
6. Add timeouts for LLM operations (120s default)
7. Use `t.Skip()` when a required endpoint or provider is unavailable
