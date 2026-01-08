# Delos

Delos is an open-source infrastructure platform for LLM applications. It provides prompt management, evaluation, deployment gates, and observability as a set of Go microservices.

## What's Included

| Service | Port | Description |
|---------|------|-------------|
| **observe** | 9000 | Tracing backend - OTLP ingestion, metrics, query API |
| **runtime** | 9001 | LLM gateway - provider abstraction, caching, failover |
| **prompt** | 9002 | Prompt versioning - CRUD, history, semantic diffing |
| **datasets** | 9003 | Test data management - dataset CRUD, versioning |
| **eval** | 9004 | Quality assurance - evaluators, regression testing |
| **deploy** | 9005 | Deployment gates - rollouts, quality gates, rollback |

Plus:
- **CLI** (`delos`) - Command-line interface for all operations
- **Python SDK** - Client library for Python applications

## Quick Start

```bash
# Clone the repo
git clone https://github.com/instantcocoa/delos.git
cd delos

# Start infrastructure (PostgreSQL, Redis, NATS)
make up

# Build all services and CLI
make build
make build-cli

# Run all services
make run-all
```

The services will be available at `localhost:9000-9005`.

### Using Docker Compose (Full Stack)

```bash
# Start everything including services
docker-compose -f deploy/local/docker-compose.yaml up -d

# Check status
docker-compose -f deploy/local/docker-compose.yaml ps
```

### Using the CLI

```bash
# List prompts
./bin/delos prompt list

# Create a prompt
./bin/delos prompt create "My Prompt" --slug my-prompt --system "You are helpful."

# List datasets
./bin/delos datasets list

# Check available evaluators
./bin/delos eval evaluators
```

### Using the Python SDK

```python
from delos import DelosClient

async with DelosClient() as client:
    # Create a prompt
    prompt = await client.prompts.create(
        name="summarizer",
        slug="summarizer",
        messages=[{"role": "system", "content": "Summarize the following text."}]
    )

    # Run a completion (requires LLM API keys configured)
    response = await client.runtime.complete(
        messages=[{"role": "user", "content": "Hello!"}],
        model="gpt-4"
    )
```

## Development

See [GETTING_STARTED.md](GETTING_STARTED.md) for detailed development setup.

### Common Commands

```bash
make help              # Show all available commands
make build             # Build all services
make build-cli         # Build CLI
make test              # Run tests (starts dependencies)
make test-integration  # Run integration tests (requires services running)
make proto             # Generate protobuf code
make lint              # Run linters
make up                # Start infrastructure containers
make down              # Stop all containers
```

### Project Structure

```
delos/
├── proto/           # Protocol Buffer definitions
├── pkg/             # Shared Go libraries
├── services/        # Go microservices (one per directory)
├── cli/             # CLI tool
├── sdk/python/      # Python SDK
├── deploy/local/    # Docker Compose for local development
└── tests/           # Integration tests
```

## Configuration

Services are configured via environment variables. Copy the example and add your API keys:

```bash
cp deploy/local/.env.example deploy/local/.env

# Edit .env with your LLM provider keys:
# DELOS_RUNTIME_OPENAI_KEY=sk-...
# DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...
```

See [deploy/local/.env.example](deploy/local/.env.example) for all configuration options.

## Architecture

Services communicate via gRPC. The dependency graph:

```
observe (foundation - no dependencies)
    ↑
runtime ←→ prompt ←→ datasets
    ↓         ↓         ↓
         eval (depends on runtime, prompt, datasets)
           ↓
        deploy (depends on eval)
```

All services emit OpenTelemetry traces to the observe service.

## License

MIT
