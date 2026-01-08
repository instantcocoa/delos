.PHONY: all build test test-unit test-integration lint proto proto-lint proto-breaking up down clean help
.PHONY: test-deps-up test-deps-wait
.PHONY: run-runtime run-prompt run-datasets run-eval run-deploy run-observe run-all stop-all

# Variables
SERVICES := runtime prompt datasets eval deploy observe
GO_MODULE := github.com/instantcocoa/delos

# Default target
all: build

# Build all services
build:
	@for svc in $(SERVICES); do \
		echo "Building $$svc..."; \
		go build -o bin/$$svc ./services/$$svc/cmd/server; \
	done

# Build a specific service
build-%:
	@echo "Building $*..."
	@go build -o bin/$* ./services/$*/cmd/server

# Build CLI
build-cli:
	@echo "Building CLI..."
	@go build -o bin/delos ./cli

# Run all tests (starts test dependencies first)
test: test-deps-up test-deps-wait
	@echo "Running all tests..."
	@go test -race -cover ./... ; status=$$?; \
		docker-compose -f deploy/local/docker-compose.yaml --profile test stop postgres localstack redis > /dev/null 2>&1 || true; \
		exit $$status

# Run unit tests only (no external dependencies)
test-unit:
	@go test -race -cover ./...

# Run tests with coverage report
test-coverage: test-deps-up test-deps-wait
	@go test -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@docker-compose -f deploy/local/docker-compose.yaml --profile test stop localstack redis > /dev/null 2>&1 || true
	@echo "Coverage report: coverage.html"

# Start test dependencies (PostgreSQL, Redis, LocalStack)
test-deps-up:
	@docker-compose -f deploy/local/docker-compose.yaml --profile test up -d postgres redis localstack > /dev/null 2>&1

# Wait for test dependencies to be healthy (uses Docker health checks)
test-deps-wait:
	@echo "Waiting for test dependencies..."
	@for i in $$(seq 1 30); do \
		docker-compose -f deploy/local/docker-compose.yaml ps postgres 2>/dev/null | grep -q "(healthy)" && break; \
		sleep 1; \
	done
	@for i in $$(seq 1 30); do \
		docker-compose -f deploy/local/docker-compose.yaml ps redis 2>/dev/null | grep -q "(healthy)" && break; \
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

# Check for breaking changes
proto-breaking:
	@echo "Checking for breaking changes..."
	@buf breaking --against '.git#branch=main'

# Format proto files
proto-format:
	@buf format -w

# Start local infrastructure (PostgreSQL, Redis, NATS)
up:
	@docker-compose -f deploy/local/docker-compose.yaml up -d postgres redis nats

# Start all services via Docker Compose
up-all:
	@docker-compose -f deploy/local/docker-compose.yaml up -d

# Stop all containers
down:
	@docker-compose -f deploy/local/docker-compose.yaml --profile test down

# View logs from containers
logs:
	@docker-compose -f deploy/local/docker-compose.yaml logs -f

# Run individual services (foreground)
run-runtime:
	@go run ./services/runtime/cmd/server

run-prompt:
	@go run ./services/prompt/cmd/server

run-datasets:
	@go run ./services/datasets/cmd/server

run-eval:
	@go run ./services/eval/cmd/server

run-deploy:
	@go run ./services/deploy/cmd/server

run-observe:
	@go run ./services/observe/cmd/server

# Run all services in background using built binaries
# Use 'make stop-all' to stop them
run-all: build
	@echo "Starting all services in background..."
	@echo "Use 'make stop-all' to stop them"
	@./bin/observe &
	@sleep 1
	@./bin/runtime &
	@sleep 1
	@./bin/prompt &
	@sleep 1
	@./bin/datasets &
	@sleep 1
	@./bin/eval &
	@sleep 1
	@./bin/deploy &
	@echo "All services started. PIDs:"
	@pgrep -f 'bin/(observe|runtime|prompt|datasets|eval|deploy)' || true

# Stop all background services started by run-all
stop-all:
	@echo "Stopping all services..."
	@pkill -f 'bin/observe' 2>/dev/null || true
	@pkill -f 'bin/runtime' 2>/dev/null || true
	@pkill -f 'bin/prompt' 2>/dev/null || true
	@pkill -f 'bin/datasets' 2>/dev/null || true
	@pkill -f 'bin/eval' 2>/dev/null || true
	@pkill -f 'bin/deploy' 2>/dev/null || true
	@echo "All services stopped"

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

# Database migrations (requires golang-migrate)
migrate-up:
	@for svc in $(SERVICES); do \
		if [ -d "services/$$svc/migrations" ]; then \
			echo "Running migrations for $$svc..."; \
			migrate -path services/$$svc/migrations -database "$$DELOS_DB_URL" up; \
		fi \
	done

migrate-down:
	@for svc in $(SERVICES); do \
		if [ -d "services/$$svc/migrations" ]; then \
			echo "Rolling back migrations for $$svc..."; \
			migrate -path services/$$svc/migrations -database "$$DELOS_DB_URL" down 1; \
		fi \
	done

# Help
help:
	@echo "Delos Makefile"
	@echo ""
	@echo "Quick Start:"
	@echo "  make up && make build && make run-all    Start everything locally"
	@echo "  make up-all                              Start everything via Docker"
	@echo ""
	@echo "Building:"
	@echo "  make build              Build all service binaries to bin/"
	@echo "  make build-<svc>        Build specific service (runtime, prompt, etc.)"
	@echo "  make build-cli          Build CLI to bin/delos"
	@echo ""
	@echo "Running Services:"
	@echo "  make up                 Start infrastructure (postgres, redis, nats)"
	@echo "  make up-all             Start all services via Docker Compose"
	@echo "  make run-all            Run all services locally (background)"
	@echo "  make stop-all           Stop services started by run-all"
	@echo "  make run-<svc>          Run specific service (foreground)"
	@echo "  make down               Stop all containers"
	@echo "  make logs               View container logs"
	@echo ""
	@echo "Testing:"
	@echo "  make test               Run tests (starts postgres/redis/localstack)"
	@echo "  make test-unit          Run tests without starting deps"
	@echo "  make test-integration   Run integration tests (requires services)"
	@echo "  make test-coverage      Generate HTML coverage report"
	@echo ""
	@echo "Proto/Lint:"
	@echo "  make proto              Generate code from proto files"
	@echo "  make proto-lint         Lint proto files"
	@echo "  make lint               Run Go and proto linters"
	@echo ""
	@echo "Other:"
	@echo "  make tools              Install dev tools (buf, golangci-lint)"
	@echo "  make clean              Remove build artifacts"
	@echo "  make help               Show this help"
