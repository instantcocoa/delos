# Getting Started with Delos Development

This guide walks you through setting up a local development environment for Delos.

## Prerequisites

- **Go 1.25+** - [Install Go](https://go.dev/doc/install)
- **Docker & Docker Compose** - [Install Docker](https://docs.docker.com/get-docker/)
- **Buf** (optional, for proto changes) - `go install github.com/bufbuild/buf/cmd/buf@latest`

Verify your setup:
```bash
go version      # Should show 1.25+
docker --version
docker-compose --version
```

## Service Architecture

### Service Ports

| Service | Port | Description |
|---------|------|-------------|
| observe | 9000 | Tracing and metrics (foundation) |
| runtime | 9001 | LLM provider gateway |
| prompt | 9002 | Prompt versioning |
| datasets | 9003 | Test data management |
| eval | 9004 | Quality evaluation |
| deploy | 9005 | Deployment orchestration |

### Service Dependencies & Startup Order

Services have the following dependency graph:

```
observe (no dependencies - start first)
    ↑
runtime ←→ prompt ←→ datasets
    ↓         ↓         ↓
         eval (depends on runtime, prompt, datasets)
           ↓
        deploy (depends on eval)
```

**Recommended startup order:**
1. `observe` - Foundation service, no dependencies
2. `runtime`, `prompt`, `datasets` - Core services (can start in parallel)
3. `eval` - Depends on runtime for completions
4. `deploy` - Depends on eval for quality gates

**What happens if services start out of order?**
- Services use gRPC with automatic reconnection
- If a dependency isn't available, the service logs a warning but continues starting
- Operations requiring missing dependencies will fail with `UNAVAILABLE` until the dependency comes up
- No manual restart needed - connections auto-recover

## Option 1: Full Docker Setup (Recommended)

This runs everything in containers - no local Go builds needed.

```bash
# Clone the repo
git clone https://github.com/instantcocoa/delos.git
cd delos

# Copy environment file
cp deploy/local/.env.example deploy/local/.env

# Start everything
docker-compose -f deploy/local/docker-compose.yaml up -d --build

# Verify services are running
docker-compose -f deploy/local/docker-compose.yaml ps
```

All services should show as "Up" with healthy status for infrastructure.

### Build the CLI to interact with services

```bash
make build-cli
./bin/delos --help
./bin/delos prompt list
```

## Option 2: Local Development Setup

This builds and runs services locally (faster iteration).

### Step 1: Start Infrastructure

```bash
# Start PostgreSQL, Redis, and NATS
make up

# Verify they're running
docker ps
```

You should see `delos-postgres`, `delos-redis`, and `delos-nats` containers.

### Step 2: Build Everything

```bash
# Build all services
make build

# Build CLI
make build-cli
```

Binaries are output to `./bin/`.

### Step 3: Run Services

**Option A: Run all services (background)**
```bash
make run-all
```

Note: This runs services as background processes. To stop them:
```bash
pkill -f 'bin/(observe|runtime|prompt|datasets|eval|deploy)'
```

**Option B: Run services individually (foreground, for debugging)**

Open 6 terminal windows and run one service in each:
```bash
# Terminal 1
./bin/observe

# Terminal 2
./bin/runtime

# Terminal 3
./bin/prompt

# Terminal 4
./bin/datasets

# Terminal 5
./bin/eval

# Terminal 6
./bin/deploy
```

### Step 4: Verify Services

```bash
# Check all services respond
./bin/delos prompt list
./bin/delos datasets list
./bin/delos eval evaluators
./bin/delos deploy list
```

## Database Migrations

When using PostgreSQL storage backend, you need to run database migrations.

### With Docker Compose (Automatic)

If using Docker Compose, migrations run automatically on container startup.

### Manual Migration (Local Development)

```bash
# Install golang-migrate CLI
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# Set database URL
export DELOS_DB_URL="postgres://delos:delos@localhost:5432/delos?sslmode=disable"

# Run all migrations (all services share one database)
migrate -path services/observe/migrations -database "$DELOS_DB_URL" up
migrate -path services/prompt/migrations -database "$DELOS_DB_URL" up
migrate -path services/datasets/migrations -database "$DELOS_DB_URL" up
migrate -path services/eval/migrations -database "$DELOS_DB_URL" up
migrate -path services/deploy/migrations -database "$DELOS_DB_URL" up
```

### Check Migration Status

```bash
migrate -path services/prompt/migrations -database "$DELOS_DB_URL" version
```

### Rollback Migrations

```bash
# Rollback last migration for a service
migrate -path services/prompt/migrations -database "$DELOS_DB_URL" down 1

# Rollback all migrations (dangerous!)
migrate -path services/prompt/migrations -database "$DELOS_DB_URL" down
```

## Running Tests

### Unit Tests

```bash
# Run all unit tests (no external dependencies needed)
go test ./...
```

### Integration Tests

Integration tests require services to be running:

```bash
# Option 1: With Docker Compose (recommended)
docker-compose -f deploy/local/docker-compose.yaml up -d
./tests/integration/run.sh

# Option 2: With local services
make up
make run-all
./tests/integration/run.sh

# Run specific test suites
./tests/integration/run.sh cli      # CLI tests only
./tests/integration/run.sh prompt   # Prompt service tests only
./tests/integration/run.sh -v       # Verbose output
```

## Configuration

### Environment Variables

Services read configuration from environment variables. For local development:

1. **Docker Compose**: Edit `deploy/local/.env`
2. **Local services**: Export variables or create a `.env` file

Key variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `DELOS_OBSERVE_ADDR` | `localhost:9000` | Observe service address |
| `DELOS_RUNTIME_ADDR` | `localhost:9001` | Runtime service address |
| `DELOS_PROMPT_ADDR` | `localhost:9002` | Prompt service address |
| `DELOS_DATASETS_ADDR` | `localhost:9003` | Datasets service address |
| `DELOS_EVAL_ADDR` | `localhost:9004` | Eval service address |
| `DELOS_DEPLOY_ADDR` | `localhost:9005` | Deploy service address |
| `DELOS_RUNTIME_OPENAI_KEY` | - | OpenAI API key (for completions) |
| `DELOS_RUNTIME_ANTHROPIC_KEY` | - | Anthropic API key (for completions) |

### LLM Provider Keys

To use the runtime service for completions, configure at least one provider:

```bash
# In deploy/local/.env
DELOS_RUNTIME_OPENAI_KEY=sk-...
DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...
```

Without keys, the runtime service starts but completions will fail.

## Making Changes

### Modifying Proto Files

```bash
# Edit files in proto/
vim proto/prompt/v1/prompt.proto

# Regenerate Go and Python code
make proto

# Rebuild affected services
make build
```

### Adding a New Endpoint

1. Define the RPC in the proto file
2. Run `make proto`
3. Implement the handler in the service
4. Add tests
5. Update CLI if needed
6. Update SDK if needed

### Code Style

```bash
# Run linters
make lint

# Format code
go fmt ./...
goimports -w .
```

## Troubleshooting

See [TROUBLESHOOTING.md](TROUBLESHOOTING.md) for common issues and solutions.

### Quick Fixes

**Services won't start - port already in use**
```bash
# Kill existing processes
pkill -f 'bin/(observe|runtime|prompt|datasets|eval|deploy)'
# Or find and kill specific port
lsof -ti:9001 | xargs kill -9
```

**Docker containers won't start**
```bash
# Clean up and restart
docker-compose -f deploy/local/docker-compose.yaml down -v
docker-compose -f deploy/local/docker-compose.yaml up -d
```

**Tests fail with "service unavailable"**
```bash
# Ensure services are running
docker-compose -f deploy/local/docker-compose.yaml ps
# Or
curl -s localhost:9002/health || echo "Prompt service not running"
```

## Next Steps

- Read [CLAUDE.md](CLAUDE.md) for architecture details and coding standards
- Check out the [CLI source](cli/) to understand command structure
- Look at [sdk/python/](sdk/python/) for SDK implementation
- Browse [tests/integration/](tests/integration/) for test examples
