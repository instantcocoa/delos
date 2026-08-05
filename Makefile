.PHONY: all build build-delos build-gateway
.PHONY: test test-unit test-integration test-integration-full test-ollama test-coverage
.PHONY: test-deps-up test-deps-wait
.PHONY: conformance conformance-record bench soak compat vulncheck
.PHONY: lint proto proto-lint proto-breaking proto-format docs-test
.PHONY: up up-all down logs clean tools help
.PHONY: run-gateway run-control-plane run-all stop-all
.PHONY: up-ollama ollama-pull ollama-ready

# Variables
GO_MODULE := github.com/instantcocoa/delos
COMPOSE := docker compose -f deploy/local/docker-compose.yaml

# Default target
all: build

# Build both binaries
build: build-delos build-gateway

# Control plane + CLI (one binary; `delos serve` runs the control plane)
build-delos:
	@echo "Building delos..."
	@go build -o bin/delos ./cmd/delos

# Data plane (stateless LLM gateway)
build-gateway:
	@echo "Building delos-gateway..."
	@go build -o bin/delos-gateway ./cmd/delos-gateway

# Run all tests (starts test dependencies first)
test: test-deps-up test-deps-wait
	@echo "Running all tests..."
	@go test -race -cover ./... ; status=$$?; \
		$(COMPOSE) --profile test stop postgres localstack > /dev/null 2>&1 || true; \
		exit $$status

# Run unit tests only (no external dependencies)
test-unit:
	@go test -race -cover ./...

# Run tests with coverage report
test-coverage: test-deps-up test-deps-wait
	@go test -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@$(COMPOSE) --profile test stop postgres localstack > /dev/null 2>&1 || true
	@echo "Coverage report: coverage.html"

# Start test dependencies (PostgreSQL, LocalStack)
test-deps-up:
	@$(COMPOSE) --profile test up -d postgres localstack > /dev/null 2>&1

# Wait for test dependencies to be healthy (uses Docker health checks)
test-deps-wait:
	@echo "Waiting for test dependencies..."
	@for i in $$(seq 1 30); do \
		$(COMPOSE) ps postgres 2>/dev/null | grep -q "(healthy)" && break; \
		sleep 1; \
	done
	@for i in $$(seq 1 60); do \
		curl -sf http://localhost:4566/_localstack/health > /dev/null 2>&1 && break; \
		sleep 2; \
	done || (echo "Warning: LocalStack not ready, some tests may fail"; true)

# Run integration tests
test-integration:
	@./tests/integration/run.sh

# Run integration tests with auto-start
test-integration-full:
	@./tests/integration/run.sh --start-services

# Run Ollama integration tests only
test-ollama:
	@./tests/integration/run.sh ollama

# Provider conformance suite: replays recorded provider responses through the
# real gateway surfaces. Hermetic - no keys, no network beyond loopback.
conformance:
	@go test ./tests/conformance/ -count=1

# Re-record every cassette against the live provider APIs. Needs real keys
# (providers without one are skipped); `git diff tests/conformance/cassettes`
# afterwards is the provider-drift report.
conformance-record:
	@DELOS_CONFORMANCE_RECORD=1 go test ./tests/conformance/ -count=1 -v

# Gateway overhead benchmark: 2000 sequential requests against an instant
# upstream, gated on p99 < 5ms with no goroutine leaks.
bench:
	@go test ./tests/conformance/ -run 'TestGatewayOverheadP99' -count=1 -v

# 10s soak: constant traffic, then assert goroutine and FD counts are flat.
soak:
	@DELOS_SOAK=1 go test ./tests/conformance/ -run 'TestGatewaySoak' -count=1 -v -timeout 5m

# Client compatibility: the unmodified official openai (Python + Node) and
# anthropic (Python) clients against a real gateway over a mock upstream.
compat:
	@./tests/compat/run.sh

# Supply-chain hygiene: report known vulnerabilities in our dependency graph.
vulncheck:
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Execute the documentation. Every fenced bash block whose first line is
# "# docs-test" is extracted and run in a scratch directory with bin/ on PATH,
# so stale docs fail like any other test.
docs-test: build
	@go run ./tools/docstest

# Lint Go code
lint: proto-lint
	@golangci-lint run ./...

# Generate protobuf code using buf
proto:
	@echo "Generating protobuf code with buf..."
	@buf generate
	@echo "Protobuf generation complete"

# Lint proto files
proto-lint:
	@echo "Linting proto files..."
	@buf lint

# Check for breaking changes (currently expected to fail during the
# two-binary refactor; re-enable in CI once the topology change lands)
proto-breaking:
	@echo "Checking for breaking changes..."
	@buf breaking --against '.git#branch=main'

# Format proto files
proto-format:
	@buf format -w

# Start local infrastructure (PostgreSQL only - that's all Delos needs)
up:
	@$(COMPOSE) up -d postgres

# Start the whole stack via Docker Compose (postgres + gateway + delos)
up-all:
	@$(COMPOSE) up -d

# Stop all containers
down:
	@$(COMPOSE) --profile test --profile ollama --profile observability down

# View logs from containers
logs:
	@$(COMPOSE) logs -f

# Run the gateway (data plane, :8080) in the foreground
run-gateway:
	@go run ./cmd/delos-gateway

# Run the control plane (:8081) in the foreground
run-control-plane:
	@go run ./cmd/delos serve

# Run both binaries in the background using the built binaries
# Use 'make stop-all' to stop them
run-all: build
	@echo "Starting delos-gateway (:8080) and delos serve (:8081) in background..."
	@echo "Use 'make stop-all' to stop them"
	@./bin/delos-gateway &
	@sleep 1
	@./bin/delos serve &
	@echo "Started. PIDs:"
	@pgrep -f 'bin/delos' || true

# Stop background processes started by run-all
stop-all:
	@echo "Stopping delos processes..."
	@pkill -f 'bin/delos-gateway' 2>/dev/null || true
	@pkill -f 'bin/delos serve' 2>/dev/null || true
	@echo "Stopped"

# Start Ollama (optional profile) and pull the default model
up-ollama:
	@echo "Starting Ollama..."
	@$(COMPOSE) --profile ollama up -d ollama
	@echo "Waiting for Ollama to be healthy..."
	@for i in $$(seq 1 30); do \
		curl -sf http://localhost:11434/api/tags > /dev/null 2>&1 && break; \
		sleep 2; \
	done || (echo "Ollama not ready after 60s"; exit 1)
	@echo "Ollama is ready. Pulling gemma3:4b model..."
	@$(COMPOSE) --profile ollama up ollama-init
	@echo "Ollama setup complete!"

# Pull gemma3 model (assumes Ollama is running)
ollama-pull:
	@echo "Pulling gemma3:4b model..."
	@docker exec delos-ollama ollama pull gemma3:4b

# Check if Ollama and model are ready
ollama-ready:
	@curl -sf http://localhost:11434/api/tags | grep -q "gemma3" && echo "Ollama ready with gemma3" || echo "Ollama or gemma3 not available"

# Clean build artifacts
clean:
	@rm -rf bin/
	@rm -rf gen/
	@rm -f coverage.out coverage.html

# Install development tools
tools:
	@echo "Installing buf..."
	@go install github.com/bufbuild/buf/cmd/buf@latest
	@echo "Installing golangci-lint..."
	@go install github.com/golangci-lint/golangci-lint/cmd/golangci-lint@latest
	@echo "Development tools installed"

# Help
help:
	@echo "Delos Makefile"
	@echo ""
	@echo "Delos is two binaries + Postgres:"
	@echo "  delos-gateway   data plane, HTTP :8080, stateless, no database"
	@echo "  delos           control plane + CLI; 'delos serve' listens on :8081"
	@echo ""
	@echo "Quick Start:"
	@echo "  make up && make run-all                  Postgres in Docker, binaries locally"
	@echo "  make up-all                              Everything via Docker (3 containers)"
	@echo ""
	@echo "Building:"
	@echo "  make build              Build bin/delos and bin/delos-gateway"
	@echo "  make build-delos        Build the control plane + CLI binary"
	@echo "  make build-gateway      Build the gateway binary"
	@echo ""
	@echo "Running:"
	@echo "  make up                 Start Postgres only"
	@echo "  make up-all             Start postgres + gateway + control plane"
	@echo "  make run-gateway        Run the gateway (:8080) in the foreground"
	@echo "  make run-control-plane  Run the control plane (:8081) in the foreground"
	@echo "  make run-all            Run both binaries in the background"
	@echo "  make stop-all           Stop binaries started by run-all"
	@echo "  make down               Stop all containers"
	@echo "  make logs               View container logs"
	@echo ""
	@echo "Testing:"
	@echo "  make test               Run tests (starts postgres/localstack)"
	@echo "  make test-unit          Run tests without starting deps"
	@echo "  make test-integration   Run integration tests (requires the stack)"
	@echo "  make test-ollama        Run Ollama integration tests only"
	@echo "  make test-coverage      Generate HTML coverage report"
	@echo "  make docs-test          Run the marked code blocks in the docs"
	@echo ""
	@echo "Gateway conformance and compatibility:"
	@echo "  make conformance        Replay the provider cassettes (hermetic)"
	@echo "  make conformance-record Re-record cassettes against live APIs (needs keys)"
	@echo "  make bench              Gateway overhead benchmark (p99 < 5ms gate)"
	@echo "  make soak               10s soak: goroutine and FD leak check"
	@echo "  make compat             Official openai/anthropic clients vs the gateway"
	@echo "  make vulncheck          govulncheck over the module"
	@echo ""
	@echo "Proto/Lint:"
	@echo "  make proto              Generate code from proto files"
	@echo "  make proto-lint         Lint proto files"
	@echo "  make lint               Run Go and proto linters"
	@echo ""
	@echo "Ollama (optional 'ollama' compose profile):"
	@echo "  make up-ollama          Start Ollama and pull gemma3:4b"
	@echo "  make ollama-ready       Check if Ollama and model are ready"
	@echo "  make ollama-pull        Pull gemma3:4b (if Ollama running)"
	@echo ""
	@echo "Other:"
	@echo "  make tools              Install dev tools (buf, golangci-lint)"
	@echo "  make clean              Remove build artifacts"
	@echo "  make help               Show this help"
