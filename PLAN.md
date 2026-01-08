# Delos Implementation Plan

## Project Overview

Delos is a unified infrastructure platform for LLM applications, providing:
- **6 Go microservices** communicating via gRPC
- **Python SDK** for application developers
- **CLI** for operations and testing

## Architecture Decisions

| Decision | Choice |
|----------|--------|
| Repository Structure | Monorepo (all services + SDK + CLI) |
| Scaffold Approach | Full scaffold (all 6 services first) |
| Deployment | Docker Compose (local) + Kubernetes (production) |
| Proto Management | Buf (linting, generation, breaking change detection) |

## Implementation Phases

### Phase 1: Foundation ✅ COMPLETE

| Task | Status |
|------|--------|
| Monorepo directory structure | ✅ Done |
| go.mod, Makefile, .gitignore | ✅ Done |
| Proto definitions for all 6 services | ✅ Done |
| Buf configuration (buf.yaml, buf.gen.yaml) | ✅ Done |
| pkg/config - Configuration loading | ✅ Done |
| pkg/telemetry - OpenTelemetry setup | ✅ Done |
| pkg/grpcutil - gRPC server helpers | ✅ Done |
| pkg/testutil - Test helpers | ✅ Done |
| Docker Compose for local dev | ✅ Done |
| Generated Go code in gen/ | ✅ Done |

### Phase 2: Core Services ✅ COMPLETE

| Service | Port | Implementation Status |
|---------|------|----------------------|
| **observe** | 9000 | ✅ Full implementation (domain, repository, service, handler) |
| **runtime** | 9001 | ✅ Full implementation with OpenAI + Anthropic providers |
| **prompt** | 9002 | ✅ Full implementation with CRUD + versioning + slug references |

All services have:
- ✅ Health checks
- ✅ Graceful shutdown
- ✅ Structured logging
- ✅ Clean architecture (domain → repository → service → handler)

### Phase 3: Integration ✅ COMPLETE

| Service | Port | Implementation Status |
|---------|------|----------------------|
| **datasets** | 9003 | ✅ Full CRUD, examples management, prompt linking |
| **eval** | 9004 | ✅ Evaluation runs, 6 evaluator types, run comparison |
| **deploy** | 9005 | ✅ Deployment state machine, quality gates, rollback |

### Phase 4: SDK & CLI ✅ COMPLETE

| Task | Status |
|------|--------|
| Python SDK with async support | ✅ Done |
| CLI wrapping SDK functionality | ✅ Done |

#### Python SDK (`sdk/python/`)
- Pydantic models for all 6 services
- gRPC client wrappers with type safety
- Unified `DelosClient` for all services
- README with usage examples

#### CLI (`cli/`)
- Go CLI using Cobra framework
- Commands for all 6 services:
  - `observe`: traces, trace, metrics, health
  - `runtime`: complete, providers, embed, health
  - `prompt`: list, get, create, update, delete, history, compare
  - `datasets`: list, get, create, delete, examples
  - `eval`: run, list, get, cancel, results, compare, evaluators
  - `deploy`: create, list, get, approve, rollback, cancel, status, gates, gate-create
- Multiple output formats (table, json, yaml)
- Verbose mode for additional details

### Phase 5: Production Readiness 🔄 IN PROGRESS

| Task | Status |
|------|--------|
| Pluggable storage backends (memory/postgres) | ✅ Done |
| PostgreSQL repository (prompt service) | ✅ Done |
| PostgreSQL repositories (other services) | ⏳ Pending |
| Redis caching layer | ⏳ Pending |
| Kubernetes manifests + Helm | ⏳ Pending |
| CI/CD pipeline | ⏳ Pending |
| Unit tests (>80% coverage) | 🔄 In Progress |
| Integration tests | ⏳ Pending |

### Phase 6: Dataset Sources & Formats ⏳ NOT STARTED

| Task | Status |
|------|--------|
| CSV import/export | ⏳ Pending |
| JSONL format support | ⏳ Pending |
| Parquet format support | ⏳ Pending |
| S3 data source | ⏳ Pending |
| GCS data source | ⏳ Pending |
| Local filesystem source | ⏳ Pending |

---

## Current State

### What's Working

All 6 services compile and run with in-memory storage:

```bash
# Build all services
go build ./...

# Run any service
go run ./services/observe/cmd/server
go run ./services/runtime/cmd/server
go run ./services/prompt/cmd/server
go run ./services/datasets/cmd/server
go run ./services/eval/cmd/server
go run ./services/deploy/cmd/server
```

### Service Implementation Details

#### observe (9000)
- OTLP trace ingestion
- Span storage and querying
- Trace retrieval by ID
- Service-level filtering

#### runtime (9001)
- Provider abstraction (OpenAI, Anthropic)
- Routing strategies: cost, latency, quality
- Streaming support (CompleteStream)
- Model listing per provider

#### prompt (9002)
- Full CRUD operations
- Version history with auto-increment
- Slug-based references ("summarizer:v2", "summarizer:latest")
- Template variables and messages
- Semantic diffing between versions

#### datasets (9003)
- Dataset CRUD with prompt linking
- Schema definitions (input/output fields)
- Example management (add, get, remove)
- Filtering by prompt ID, tags, search
- Pagination and shuffle support

#### eval (9004)
- Evaluation run lifecycle management
- 6 built-in evaluator types:
  - exact_match, contains, semantic_similarity
  - llm_judge, regex, json_schema
- Run comparison with regression detection
- Status tracking (pending → running → completed/failed/cancelled)

#### deploy (9005)
- Deployment state machine (8 states):
  - pending_approval → pending_gates → in_progress → completed
  - gates_failed, rolled_back, cancelled, failed
- Quality gate configuration and evaluation
- 4 deployment strategies:
  - immediate, gradual, canary, blue-green
- Rollback creates reverse deployment
- Auto-rollback configuration

### File Structure

```
delos/
├── proto/                          # Proto definitions
│   ├── runtime/v1/runtime.proto
│   ├── prompt/v1/prompt.proto
│   ├── datasets/v1/datasets.proto
│   ├── eval/v1/eval.proto
│   ├── deploy/v1/deploy.proto
│   └── observe/v1/observe.proto
├── gen/go/                         # Generated Go code
├── pkg/                            # Shared libraries
│   ├── config/
│   ├── grpcutil/
│   ├── telemetry/
│   ├── database/
│   └── cache/
├── services/
│   ├── observe/
│   │   ├── cmd/server/main.go
│   │   ├── handler.go              # gRPC handlers
│   │   ├── store.go                # Storage interface + implementations
│   │   └── observe.go              # Types and business logic
│   ├── runtime/
│   │   ├── cmd/server/main.go
│   │   ├── handler.go
│   │   ├── store.go
│   │   ├── provider.go             # LLM provider abstraction
│   │   └── runtime.go
│   ├── prompt/
│   │   ├── cmd/server/main.go
│   │   ├── handler.go
│   │   ├── store.go
│   │   └── prompt.go
│   ├── datasets/
│   │   ├── cmd/server/main.go
│   │   ├── handler.go
│   │   ├── store.go
│   │   └── datasets.go
│   ├── eval/
│   │   ├── cmd/server/main.go
│   │   ├── handler.go
│   │   ├── store.go
│   │   └── eval.go
│   └── deploy/
│       ├── cmd/server/main.go
│       ├── handler.go
│       ├── store.go
│       └── deploy.go
├── sdk/python/                     # Python SDK
├── cli/                            # Go CLI
├── deploy/local/docker-compose.yml
├── buf.yaml
├── buf.gen.yaml
├── go.mod
├── Makefile
├── CLAUDE.md
└── PLAN.md
```

---

## Architecture Simplification (Go-Idiomatic Refactor)

### Motivation

The original architecture used Clean Architecture patterns (domain/repository/service/api layers) which is common in Java/C# but not idiomatic Go. This added unnecessary:
- Indirection (4 packages per service)
- Type duplication (domain types mirroring proto types)
- Conversion boilerplate (proto ↔ domain mappers)

### New Structure

Each service is now a single package with 3-4 files:

```
services/prompt/
├── cmd/server/main.go    # Entry point
├── prompt.go             # Types + business logic
├── handler.go            # gRPC handlers
├── store.go              # Storage interface + implementations
├── handler_test.go       # Handler tests
└── store_test.go         # Store tests (unit + integration)
```

### Refactoring Progress

| Service | Status |
|---------|--------|
| **prompt** | ✅ Refactored to flat structure |
| **observe** | ⏳ Pending |
| **runtime** | ⏳ Pending |
| **datasets** | ⏳ Pending |
| **eval** | ⏳ Pending |
| **deploy** | ⏳ Pending |

### Key Changes

1. **Removed `internal/` directory** - Unnecessary for internal services
2. **Removed `domain/` package** - Types defined where used
3. **Merged `service/` into handlers** - Most "service" logic was just delegation
4. **Simplified `repository/` to `store.go`** - Single file with interface + implementations
5. **Use proto types at edges** - Minimize conversion, convert only when needed for storage

### Benefits

- Fewer packages to navigate
- Less boilerplate
- Easier to understand data flow
- Tests colocated with code
- Matches Go standard library patterns

---

## What's Next

### Immediate Priority: Phase 5 - Production Readiness

1. **PostgreSQL Repositories**
   - Replace in-memory with SQL
   - Database migrations
   - Connection pooling

2. **Testing**
   - Unit tests for domain logic
   - Integration tests with testcontainers
   - >80% coverage target

3. **Kubernetes**
   - Helm charts for each service
   - ConfigMaps and Secrets
   - Horizontal Pod Autoscaling
   - Ingress configuration

4. **CI/CD**
   - GitHub Actions workflows
   - Buf breaking change detection
   - Automated testing and linting
   - Container image builds

### Future Enhancements

**Python SDK - DataFrame Integration**
- Polars DataFrame support (`Dataset.from_polars()`, `Dataset.to_polars()`)
- Pandas DataFrame support (`Dataset.from_pandas()`, `Dataset.to_pandas()`)
- Direct S3/GCS loading in SDK (`Dataset.from_s3()`)
- Schema inference from DataFrame columns
- Batch upload with progress tracking

**Infrastructure**
- Real LLM provider integrations (actual API calls)
- Async evaluation execution
- Real-time metrics during deployments
- WebSocket support for streaming updates
- Multi-tenancy support
- RBAC and authentication
