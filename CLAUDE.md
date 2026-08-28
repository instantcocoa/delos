# Delos Development Guide

## Project Overview

Delos is **two binaries + PostgreSQL**:

- **`delos-gateway`** (`./cmd/delos-gateway`) - the data plane. A stateless
  OpenAI/Anthropic-compatible LLM gateway on HTTP port **8080**. No database, no
  knowledge of the control plane. Configured purely by environment variables.
- **`delos`** (`./cmd/delos`) - the control plane *and* the CLI, one binary.
  `delos serve` runs the control-plane server on port **8081**; every other
  subcommand is a client.

There is no Python SDK and there are no separate microservice processes. The old
6-service topology on ports 9000-9005 is gone.

## Repository Structure

```
delos/
├── cmd/
│   ├── delos/                # Control plane + CLI binary
│   └── delos-gateway/        # Data plane binary
├── controlplane/             # Wires the control-plane modules into one process
│   └── controlplane.go
├── services/                 # Domain logic (libraries, not processes)
│   ├── runtime/              # LLM providers + gateway HTTP API
│   ├── prompt/               # Prompt versioning
│   ├── datasets/             # Test data management
│   ├── eval/                 # Quality assurance + eval runner
│   ├── deploy/               # CI/CD gates
│   └── observe/              # Tracing backend
├── cli/                      # Cobra commands (cli/cmd), CLI config
├── proto/                    # Protocol Buffer definitions
│   ├── prompt/v1/
│   ├── datasets/v1/
│   ├── eval/v1/
│   ├── deploy/v1/
│   └── observe/v1/
├── gen/go/                   # Generated Go code (from buf generate)
├── pkg/                      # Shared Go libraries
│   ├── config/               # Configuration loading
│   ├── database/             # Postgres connection + migrator
│   ├── grpcutil/             # gRPC helpers, interceptors
│   ├── telemetry/            # OpenTelemetry setup
│   └── testutil/             # Test helpers
├── deploy/local/             # Docker Compose for local dev
├── tests/                    # Integration tests
├── docs/
├── Dockerfile.delos          # Control plane + CLI image
├── Dockerfile.gateway        # Gateway image
├── buf.yaml                  # Buf module configuration
├── buf.gen.yaml              # Buf code generation config
└── Makefile
```

## Technology Stack

| Component | Technology |
|-----------|------------|
| Binaries | Go 1.25+ |
| Gateway API | HTTP (OpenAI- and Anthropic-compatible) |
| Control plane API | gRPC over h2c + Protocol Buffers |
| Proto Management | **Buf** (linting, generation, breaking change detection) |
| Database | PostgreSQL 15+ |
| Tracing | OpenTelemetry (OTLP) |
| Client libraries | Any OpenAI/Anthropic-compatible client (base URL swapped) |
| CLI | Go with Cobra |

## Service Architecture

### Data plane: `delos-gateway` (port 8080, `DELOS_GATEWAY_PORT`)

Stateless. Serves:

| Endpoint | Purpose |
|----------|---------|
| `POST /v1/chat/completions` | OpenAI-compatible completions (streaming supported) |
| `POST /v1/embeddings` | Embeddings |
| `GET /v1/models`, `GET /v1/models/{model}` | Model discovery |
| `POST /v1/messages` | Anthropic-compatible messages |
| `GET /healthz` | Health + configured provider count |

Providers are registered at startup, one per API key found in the environment
(see Configuration). The gateway never talks to the control plane or a database.

### Control plane: `delos serve` (port 8081, `DELOS_PORT`)

Hosts observe, prompt, datasets, eval, and deploy in a single process, serving
gRPC over h2c plus `GET /healthz` on the same port. Calls between modules are
direct Go function calls, not network hops. It applies its own migrations at
startup - there is no separate migrate step. It reaches the gateway over the
gateway's public OpenAI-compatible API via `DELOS_GATEWAY_URL`.

### CLI: everything else

`delos prompt|datasets|eval|deploy|observe|gateway|version`. Control-plane
subcommands dial `DELOS_CONTROL_PLANE_ADDR` (default `localhost:8081`) over gRPC;
`delos gateway` (alias `delos runtime`) drives the gateway's HTTP API at
`DELOS_GATEWAY_URL` (default `http://localhost:8080`).

### Package layout

`services/<name>/` are **libraries** consumed by `controlplane/` and `cmd/`. They
have no `cmd/server` entry points and no per-service Dockerfiles. Each follows
idiomatic Go structure (flat, minimal packages):

```
services/<name>/
├── handler.go            # gRPC handlers
├── store.go              # Storage interface + implementations
├── migrations/           # SQL migrations (embedded)
├── <name>.go             # Types + business logic
└── <name>_test.go        # Tests
```

**Design principles:**
- Use proto types at API boundaries, convert only for storage
- Single package per service (no internal/ subdirectories)
- Colocate tests with code
- Minimize abstraction layers

### Module Responsibilities

| Module | Lives in | Purpose |
|--------|----------|---------|
| **runtime** | `delos-gateway` | LLM provider abstraction, OpenAI/Anthropic HTTP surfaces |
| **observe** | control plane | Trace ingestion, metrics aggregation, query API |
| **prompt** | control plane | Prompt versioning, collaboration, semantic diffing |
| **datasets** | control plane | Test suite management, generation, versioning |
| **eval** | control plane | Quality scoring, regression testing, evaluators |
| **deploy** | control plane | Quality gates: named thresholds over eval results, `gate check` exits 0/1 for CI |

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
- `buf.gen.yaml` - Code generation configuration (Go + Go gRPC)

Protos cover the control-plane services only. The gateway's API is plain HTTP
(OpenAI/Anthropic shapes) and has no proto definitions.

```protobuf
// Use versioned packages
package delos.prompt.v1;

// All RPCs return specific response types (not Empty)
rpc GetPrompt(GetPromptRequest) returns (GetPromptResponse);

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
# (temporarily disabled in CI while the two-binary refactor lands)
buf breaking --against '.git#branch=main'

# Generate Go code
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
- All modules must have >80% coverage on domain logic
- Tests skip gracefully when PostgreSQL/LocalStack/Ollama unavailable

**Testing workflow:**
```bash
# Quick tests (skips tests when deps unavailable)
go test ./...

# Full tests with dependencies (starts postgres, localstack)
make test

# Integration tests against a running stack
make test-integration

# Generate coverage report
make test-coverage
```

## Configuration

Everything is environment variables. Local defaults live in
`deploy/local/.env.example`; secrets go in `deploy/local/.env.local` (gitignored).

**Gateway (`delos-gateway`)** - one provider is registered per key found:

```bash
DELOS_GATEWAY_PORT=8080           # default 8080
OPENAI_API_KEY=sk-...
ANTHROPIC_API_KEY=sk-ant-...
GEMINI_API_KEY=...                # or GOOGLE_API_KEY
AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...   # Bedrock (or AWS_PROFILE; + AWS_REGION, AWS_SESSION_TOKEN)
DELOS_RUNTIME_OLLAMA_ENABLED=true DELOS_RUNTIME_OLLAMA_URL=http://localhost:11434
DELOS_COMPAT_BASE_URL=... DELOS_COMPAT_NAME=... DELOS_COMPAT_API_KEY=...  # any OpenAI-compatible endpoint
```

That is the whole provider set: OpenAI, Anthropic, Gemini, Bedrock, plus the
OpenAI-compatible slot (Ollama, vLLM, SGLang, Together, OpenRouter, LM Studio).
There are no native OpenRouter, Together or Vertex AI providers.

The older `DELOS_RUNTIME_<PROVIDER>_KEY` names still work as fallbacks.

**Control plane (`delos serve`)**:

```bash
DELOS_PORT=8081                   # default 8081
DELOS_STORAGE_BACKEND=postgres    # memory (default, ephemeral) or postgres
DELOS_DB_HOST=localhost
DELOS_DB_PORT=5432
DELOS_DB_USER=delos
DELOS_DB_PASSWORD=delos
DELOS_DB_NAME=delos
DELOS_DB_SSLMODE=disable
DELOS_GATEWAY_URL=http://localhost:8080   # http://gateway:8080 in Docker
DELOS_LOG_LEVEL=info
```

**CLI**:

```bash
DELOS_CONTROL_PLANE_ADDR=localhost:8081   # gRPC target, default localhost:8081
DELOS_GATEWAY_URL=http://localhost:8080
```

## Development Workflow

### Local Development
```bash
# Start PostgreSQL (the only dependency)
make up

# Run either binary in the foreground
make run-gateway         # data plane on :8080
make run-control-plane   # control plane on :8081

# Or both in the background (make stop-all to stop)
make run-all

# Run tests
make test

# Generate proto code
make proto

# Lint
make lint
```

### Adding a New Feature
1. Update proto definitions if the control-plane API changes
2. Run `make proto` to regenerate
3. Implement in the relevant `services/<name>` package
4. Wire it up in `controlplane/controlplane.go` if it is a new module
5. Add tests
6. Update the CLI (`cli/cmd`) if client-facing

## Important Constraints

- **Never commit API keys** - Use `.env.local` (gitignored)
- **Proto-first development** - Define control-plane APIs in proto before implementing
- **Gateway stays stateless** - No database, no control-plane dependency
- **Control plane is stateless** - State lives in PostgreSQL
- **Graceful degradation** - Handle dependency failures; the gateway serves traffic without the control plane
- **Observability by default** - All operations emit traces

## Quick Reference: Make Targets

```
# Quick Start
make up && make build && make run-all   # Postgres in Docker, binaries locally
make up-all                             # Everything via Docker (3 containers)

# Building
make build          # Build bin/delos and bin/delos-gateway
make build-delos    # Control plane + CLI binary
make build-gateway  # Gateway binary

# Running
make up             # Start PostgreSQL only
make up-all         # Start postgres + gateway + control plane
make run-gateway        # Run the gateway (:8080) in the foreground
make run-control-plane  # Run the control plane (:8081) in the foreground
make run-all        # Run both binaries in the background
make stop-all       # Stop binaries started by run-all
make down           # Stop all containers
make logs           # Tail container logs

# Testing
make test           # Run tests with dependencies
make test-unit      # Run tests without starting deps
make test-integration  # Run integration tests
make test-ollama    # Ollama integration tests only
make test-coverage  # HTML coverage report

# Proto/Lint
make proto          # Generate code from protos
make proto-lint     # Lint proto files
make proto-format   # Format proto files
make proto-breaking # Breaking change check (disabled in CI during the refactor)
make lint           # Run all linters

# Ollama (optional 'ollama' compose profile)
make up-ollama      # Start Ollama and pull gemma3:4b
make ollama-pull    # Pull gemma3:4b
make ollama-ready   # Check Ollama + model availability

# Other
make tools          # Install dev tools
make clean          # Remove build artifacts
make help           # Show all targets
```
