# Delos Development Guide

## Project Overview

Delos is a unified infrastructure platform for LLM applications, consisting of:
- **6 Go microservices** communicating via gRPC
- **Python SDK** for application developers
- **CLI** for operations and testing

## Repository Structure

```
delos/
├── proto/                    # Protocol Buffer definitions
│   ├── runtime/v1/
│   ├── prompt/v1/
│   ├── datasets/v1/
│   ├── eval/v1/
│   ├── deploy/v1/
│   └── observe/v1/
├── gen/                      # Generated code (from buf generate)
│   ├── go/                   # Generated Go code
│   └── python/               # Generated Python code
├── pkg/                      # Shared Go libraries
│   ├── grpcutil/             # gRPC helpers, interceptors
│   ├── config/               # Configuration loading
│   ├── telemetry/            # OpenTelemetry setup
│   └── testutil/             # Test helpers
├── services/                 # Go microservices
│   ├── runtime/              # LLM Gateway
│   ├── prompt/               # Prompt versioning
│   ├── datasets/             # Test data management
│   ├── eval/                 # Quality assurance
│   ├── deploy/               # CI/CD gates
│   └── observe/              # Tracing backend
├── sdk/                      # Python SDK
│   └── python/
├── cli/                      # Go CLI tool
├── deploy/
│   ├── local/                # Docker Compose for local dev
│   └── k8s/                  # Kubernetes manifests + Helm
├── docs/
├── buf.yaml                  # Buf module configuration
├── buf.gen.yaml              # Buf code generation config
└── Makefile
```

## Technology Stack

| Component | Technology |
|-----------|------------|
| Services | Go 1.25+ |
| Service Communication | gRPC + Protocol Buffers |
| Proto Management | **Buf** (linting, generation, breaking change detection) |
| Database | PostgreSQL 15+ |
| Cache | Redis 7+ |
| Message Queue | NATS (for async events) |
| Tracing | OpenTelemetry (OTLP) |
| SDK | Python 3.11+ with Pydantic |
| CLI | Go with Cobra |

## Service Architecture

Each service follows idiomatic Go structure (flat, minimal packages):

```
services/<name>/
├── cmd/server/main.go    # Entry point
├── handler.go            # gRPC handlers
├── store.go              # Storage interface + implementations
├── <name>.go             # Types + business logic
└── <name>_test.go        # Tests
```

**Design principles:**
- Use proto types at API boundaries, convert only for storage
- Single package per service (no internal/ subdirectories)
- Colocate tests with code
- Minimize abstraction layers

### Service Responsibilities

| Service | Port | Purpose |
|---------|------|---------|
| **observe** | 9000 | OTLP trace ingestion, metrics aggregation, query API |
| **runtime** | 9001 | LLM provider abstraction, caching, failover, routing |
| **prompt** | 9002 | Prompt versioning, collaboration, semantic diffing |
| **datasets** | 9003 | Test suite management, auto-generation, versioning |
| **eval** | 9004 | Quality scoring, regression testing, evaluators |
| **deploy** | 9005 | Rollout orchestration, A/B testing, auto-rollback |

### Service Dependencies

```
observe (foundation - no dependencies)
    ↑
runtime ←→ prompt ←→ datasets
    ↓         ↓         ↓
         eval (depends on runtime, prompt, datasets)
           ↓
        deploy (depends on eval)
```

## Coding Standards

### Go Services

```go
// Use context for all operations
func (s *Service) GetPrompt(ctx context.Context, req *pb.GetPromptRequest) (*pb.Prompt, error)

// Return wrapped errors with context
return nil, fmt.Errorf("failed to fetch prompt %s: %w", req.Id, err)

// Use structured logging
slog.InfoContext(ctx, "prompt retrieved", "id", req.Id, "version", prompt.Version)
```

### Proto Definitions (Buf)

We use **Buf** for Protocol Buffer management. Buf provides:
- Linting with `buf lint`
- Breaking change detection with `buf breaking`
- Code generation with `buf generate`
- Dependency management via `buf.yaml`

**Configuration files:**
- `buf.yaml` - Module configuration, linting rules, breaking change policy
- `buf.gen.yaml` - Code generation configuration (Go, Python)

```protobuf
// Use versioned packages
package delos.runtime.v1;

// All RPCs return specific response types (not Empty)
rpc Complete(CompleteRequest) returns (CompleteResponse);

// Use field numbers strategically (1-15 for frequent fields)
message Prompt {
  string id = 1;
  string name = 2;
  int32 version = 3;
  // ...
}
```

**Buf workflow:**
```bash
# Lint protos
buf lint

# Check for breaking changes against main branch
buf breaking --against '.git#branch=main'

# Generate Go and Python code
buf generate
```

### Error Handling

Use gRPC status codes consistently:
- `NOT_FOUND` - Resource doesn't exist
- `INVALID_ARGUMENT` - Bad input
- `FAILED_PRECONDITION` - Operation not allowed in current state
- `INTERNAL` - Unexpected server error
- `UNAVAILABLE` - Transient failure, client should retry

### Testing Requirements

- Unit tests: `*_test.go` alongside implementation
- Integration tests: Use `t.Skip()` when dependencies unavailable
- All services must have >80% coverage on domain logic
- Tests skip gracefully when PostgreSQL/Redis/LocalStack unavailable

**Testing workflow:**
```bash
# Quick tests (skips tests when deps unavailable)
go test ./...

# Full tests with dependencies (starts postgres, redis, localstack)
make test

# Generate coverage report
make test-coverage
```

## Configuration

All services use environment variables with a common prefix:

```bash
DELOS_DB_HOST=localhost
DELOS_DB_PORT=5432
DELOS_REDIS_URL=redis://localhost:6379
DELOS_OBSERVE_ENDPOINT=localhost:9000
DELOS_LOG_LEVEL=info
```

Service-specific config:
```bash
DELOS_RUNTIME_OPENAI_KEY=sk-...
DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...
```

## Development Workflow

### Local Development
```bash
# Start all dependencies
make up

# Run a specific service
make run-runtime

# Run tests
make test

# Generate proto code
make proto

# Lint
make lint
```

### Adding a New Feature
1. Update proto definitions if API changes
2. Run `make proto` to regenerate
3. Implement in the service
4. Add tests
5. Update SDK if client-facing

## Important Constraints

- **Never commit API keys** - Use `.env.local` (gitignored)
- **Proto-first development** - Define API in proto before implementing
- **All services must be stateless** - State lives in PostgreSQL/Redis
- **Graceful degradation** - Services should handle dependency failures
- **Observability by default** - All operations emit traces

## Quick Reference: Make Targets

```
# Quick Start
make up && make build && make run-all   # Start everything locally
make up-all                              # Start everything via Docker

# Building
make build          # Build all services to bin/
make build-cli      # Build CLI to bin/delos

# Running
make up             # Start infrastructure (postgres, redis, nats)
make up-all         # Start all services via Docker Compose
make run-all        # Run all services locally (background)
make stop-all       # Stop background services
make down           # Stop all containers

# Testing
make test           # Run tests with dependencies
make test-unit      # Run tests without dependencies
make test-integration  # Run integration tests

# Proto/Lint
make proto          # Generate code from protos
make lint           # Run all linters

# Other
make tools          # Install dev tools
make help           # Show all targets
```
