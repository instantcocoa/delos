# Installation

## Prerequisites

- **Go 1.22+** - For running services locally
- **Docker** and **Docker Compose** - For local development environment
- **Python 3.11+** - For the SDK (optional)
- **Protocol Buffers compiler** - For regenerating proto code (optional)

## Clone the Repository

```bash
git clone https://github.com/your-org/delos.git
cd delos
```

## Start Infrastructure

The easiest way to get started is with Docker Compose:

```bash
# Start all dependencies (PostgreSQL, Redis, NATS)
make up

# Or directly with docker-compose
docker-compose -f deploy/local/docker-compose.yaml up -d postgres redis nats
```

## Build the Services

```bash
# Build all services
make build

# Or build individually
go build -o bin/runtime ./services/runtime/cmd/server
go build -o bin/prompt ./services/prompt/cmd/server
go build -o bin/datasets ./services/datasets/cmd/server
go build -o bin/eval ./services/eval/cmd/server
go build -o bin/deploy ./services/deploy/cmd/server
go build -o bin/observe ./services/observe/cmd/server
```

## Run the Services

```bash
# Start all services (requires infrastructure running)
make run-all

# Or start individual services
./bin/observe &   # Port 9000
./bin/runtime &   # Port 9001
./bin/prompt &    # Port 9002
./bin/datasets &  # Port 9003
./bin/eval &      # Port 9004
./bin/deploy &    # Port 9005
```

## Install the CLI

```bash
# Build the CLI
go build -o bin/delos ./cli

# Add to PATH (optional)
sudo mv bin/delos /usr/local/bin/

# Verify installation
delos --help
```

## Install the Python SDK

```bash
pip install delos-sdk

# Or from source
pip install -e sdk/python
```

## Verify Installation

```bash
# Check service health
delos runtime health
delos prompt health
delos observe health

# Or use curl
curl -s localhost:9001/health
```

## LLM Provider Setup

To use the runtime service, configure at least one LLM provider:

```bash
# OpenAI
export DELOS_RUNTIME_OPENAI_KEY=sk-...

# Anthropic
export DELOS_RUNTIME_ANTHROPIC_KEY=sk-ant-...

# Google Gemini
export DELOS_RUNTIME_GEMINI_KEY=AIza...

# Ollama (local)
export DELOS_RUNTIME_OLLAMA_ENABLED=true
export DELOS_RUNTIME_OLLAMA_URL=http://localhost:11434
```

See [Configuration](configuration.md) for all available options.
