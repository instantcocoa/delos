# Delos Testing Guide

This document describes the integration testing strategy for Delos, including test coverage, known gaps, and how to run tests.

## Quick Start

```bash
# Start infrastructure (postgres, redis, ollama)
make up

# Wait for Ollama to download gemma3:4b model (~3.3GB)
make up-ollama

# Run unit tests
make test-unit

# Run all integration tests (requires services running)
make test-integration

# Run Ollama-specific tests only
make test-ollama
```

## Test Organization

```
tests/integration/
├── all_services_test.go     # Basic CRUD smoke tests for all 6 services
├── cli_test.go              # CLI integration tests
├── datasets_test.go         # Datasets service tests
├── error_handling_test.go   # Error handling tests (NOT_FOUND, etc.)
├── evaluators_test.go       # Eval service CRUD and evaluator tests
├── ollama_test.go           # LLM completion tests with Ollama
├── prompt_test.go           # Prompt versioning tests
├── workflow_test.go         # End-to-end workflow tests
└── run.sh                   # Test runner script
```

## Test Categories

### 1. Unit Tests (`make test-unit`)

Fast tests that don't require external services. Run these for quick feedback during development.

### 2. Integration Tests (`make test-integration`)

Tests that require running services. These test real gRPC calls between services.

### 3. Ollama Tests (`make test-ollama`)

Tests that require the Ollama LLM. These test real prompt completions with the gemma3:4b model.

## Test Coverage by Service

### Observe Service (Port 9000)

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

### Runtime Service (Port 9001)

| Test | File | Description |
|------|------|-------------|
| `TestRuntimeService_Health` | all_services_test.go | Health check |
| `TestRuntimeService_ListProviders` | all_services_test.go | List available providers |
| `TestRuntimeService_Complete` | all_services_test.go | Basic completion |
| `TestRuntimeService_Embed` | all_services_test.go | Embedding generation |
| `TestRuntimeService_CompleteStream` | all_services_test.go | Streaming completion |
| `TestOllama_Available` | ollama_test.go | Verify Ollama provider |
| `TestOllama_Complete_*` | ollama_test.go | Various completion scenarios |
| `TestOllama_CompleteStream` | ollama_test.go | Streaming with Ollama |
| `TestPromptWithOllamaCompletion` | workflow_test.go | Prompt + Ollama integration |

**Error Handling Tests:**
| Test | File | Description |
|------|------|-------------|
| `TestRuntimeService_Complete_InvalidProvider` | error_handling_test.go | Unknown provider error |
| `TestRuntimeService_Complete_EmptyMessages` | error_handling_test.go | Empty messages handling |

**Gaps:**
- [ ] Provider failover behavior
- [ ] Caching behavior verification
- [ ] Rate limiting / throttling
- [ ] Error handling for provider failures
- [ ] Token counting accuracy

### Prompt Service (Port 9002)

| Test | File | Description |
|------|------|-------------|
| `TestPromptService_Health` | all_services_test.go | Health check |
| `TestPromptService_CreateAndGet` | prompt_test.go | Basic CRUD |
| `TestPromptService_UpdateCreatesNewVersion` | prompt_test.go | Version increment on update |
| `TestPromptService_List` | prompt_test.go | List with tag filters |
| `TestPromptService_GetPromptHistory` | prompt_test.go | Version history |
| `TestPromptService_CompareVersions` | prompt_test.go | Semantic diff |
| `TestPromptService_Delete` | prompt_test.go | Soft delete |
| `TestPromptVersioningWorkflow` | workflow_test.go | Full versioning workflow |

**Coverage:**
- Version creation and retrieval
- Slug:version references (`summarizer:v2`, `summarizer:latest`)
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
- [ ] GetPromptBySlug API (not currently in proto)
- [ ] Variable type validation
- [ ] Generation config edge cases
- [ ] Concurrent version creation (race conditions)
- [ ] Large prompt content handling
- [ ] Search by name/description

### Datasets Service (Port 9003)

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

### Eval Service (Port 9004)

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
| `TestEvalService_GetEvalRun_NotFound` | error_handling_test.go | NOT_FOUND error |
| `TestEvalService_CancelEvalRun_NotFound` | error_handling_test.go | Cancel nonexistent |
| `TestEvalService_CompareRuns_NotFound` | error_handling_test.go | Compare nonexistent |

**Evaluators Available:**
- `exact_match` - Exact string comparison
- `contains` - Substring matching (case_sensitive param)
- `semantic_similarity` - Embedding-based comparison
- `llm_judge` - AI-based quality assessment
- `regex` - Pattern matching (pattern param)
- `json_schema` - Structured output validation (schema param)

**Eval Execution Engine: IMPLEMENTED**

The eval service includes a full execution engine that:
- Polls for pending runs and executes them
- Fetches prompts and datasets from their respective services
- Calls the runtime service for LLM completions
- Runs evaluators against actual vs expected outputs
- Stores results and computes summary statistics

**Implemented Evaluators:**
- `exact_match` - Exact string comparison (with whitespace normalization)
- `contains` - Substring matching (supports `case_sensitive` param)
- `regex` - Regular expression matching (requires `pattern` param)
- `json_schema` - JSON Schema validation (requires `schema` param)
- `llm_judge` - AI-powered quality assessment (uses GPT-4 by default)
- `semantic_similarity` - Embedding-based similarity (threshold: 0.8 default)

**Remaining Gaps:**
- [ ] Large dataset evaluation performance
- [ ] Concurrent evaluation runs
- [ ] Custom evaluator plugins

### Deploy Service (Port 9005)

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

## End-to-End Workflow Tests

### `TestEndToEndWorkflow` (workflow_test.go)

Tests the complete Delos workflow:

1. **Create Prompt v1** - Initial prompt with variables
2. **Create Dataset** - Test cases linked to prompt
3. **Run Evaluation** - Eval with Ollama + contains evaluator
4. **Create Quality Gate** - Score threshold requirement
5. **Create Deployment** - Deploy v1 to staging
6. **Update Prompt to v2** - New version
7. **Eval v2 and Compare** - Compare v1 vs v2 performance

**Duration:** ~15-30 seconds (with local Ollama and gemma3:4b)

## Known Gaps & Future Work

### Critical (Blocking Features)

1. **Runtime Tracing** - Tracing infrastructure exists but no spans created in handlers

### Medium Priority

1. **Deploy Quality Gates** - Integration with eval service for automated gates
2. **Prompt GetBySlug** - API to get prompt by slug:version reference
3. **Concurrency** - Race condition testing
4. **Performance** - Large dataset handling, many versions
5. **Custom Evaluator Plugins** - Allow users to register custom evaluators

### Lower Priority (Nice to Have)

1. **CLI Tests** - cli_test.go exists but coverage is limited
2. **Metrics** - Observe service metrics aggregation
3. **PostgreSQL-specific** - Currently tests use memory stores
4. **Pagination edge cases** - Offset/limit boundary testing

## Running Specific Tests

```bash
# Run only Ollama tests
./tests/integration/run.sh ollama

# Run only prompt tests
./tests/integration/run.sh prompt

# Run with verbose output
./tests/integration/run.sh -v

# Auto-start services before tests
./tests/integration/run.sh --start-services

# Skip if services unavailable (useful for CI)
./tests/integration/run.sh --skip-if-down
```

## Environment Variables

```bash
# Service addresses (default: localhost:PORT)
DELOS_OBSERVE_ADDR=localhost:9000
DELOS_RUNTIME_ADDR=localhost:9001
DELOS_PROMPT_ADDR=localhost:9002
DELOS_DATASETS_ADDR=localhost:9003
DELOS_EVAL_ADDR=localhost:9004
DELOS_DEPLOY_ADDR=localhost:9005

# LLM API keys (optional - for cloud providers)
DELOS_OPENAI_API_KEY=sk-...
DELOS_ANTHROPIC_API_KEY=sk-ant-...

# Ollama (local LLM)
DELOS_RUNTIME_OLLAMA_ENABLED=true
DELOS_RUNTIME_OLLAMA_URL=http://localhost:11434
```

## Test Infrastructure

### Docker Compose Services

```bash
# Start all infrastructure
docker-compose -f deploy/local/docker-compose.yaml up -d

# Check service health
docker-compose -f deploy/local/docker-compose.yaml ps

# View logs
docker-compose -f deploy/local/docker-compose.yaml logs -f
```

### Ollama Setup

```bash
# Start Ollama and pull model
make up-ollama

# Check model availability
make ollama-ready

# Manual model pull
docker exec delos-ollama ollama pull gemma3:4b
```

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

# Check service logs
docker logs delos-runtime --tail 50

# Check Ollama status
curl http://localhost:11434/api/tags

# Verify service connectivity
nc -zv localhost 9001
```

## Contributing New Tests

1. Add tests to appropriate file in `tests/integration/`
2. Use `//go:build integration` tag
3. Use helper functions (`getPromptClient`, `toStruct`, etc.)
4. Clean up created resources in `defer` blocks
5. Use meaningful test names: `Test{Service}_{Feature}_{Scenario}`
6. Add timeouts for LLM operations (120s default)
7. Use `t.Skip()` if required services unavailable
