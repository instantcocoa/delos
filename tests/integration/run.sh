#!/bin/bash
# Integration test runner for Delos
#
# Usage:
#   ./run.sh                    Run all integration tests
#   ./run.sh cli                Run only CLI integration tests
#   ./run.sh prompt             Run only prompt service tests
#   ./run.sh -v                 Verbose output
#   ./run.sh --start-services   Start services before running tests
#   ./run.sh --skip-if-down     Skip tests (exit 0) if services unavailable
#
# Environment:
#   DELOS_CONTROL_PLANE_ADDR   Control plane gRPC address (default localhost:8081)
#   DELOS_GATEWAY_URL          Gateway base URL (default http://localhost:8080)
#   DELOS_AUTH_TOKEN           Bearer token, when the stack requires one
#   DELOS_CLI_BINARY           Prebuilt delos binary for the CLI tests
#                              (default: ../../bin/delos, else built on demand)
#   OPENAI_API_KEY             Required for cloud LLM completion tests
#
# Tests that need a provider (gateway completions, embeddings, Ollama) SKIP with
# a visible reason when none is configured; they never pass silently.

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# The whole system is two endpoints: the control plane (gRPC) and the gateway (HTTP).
export DELOS_CONTROL_PLANE_ADDR=${DELOS_CONTROL_PLANE_ADDR:-"localhost:8081"}
export DELOS_GATEWAY_URL=${DELOS_GATEWAY_URL:-"http://localhost:8080"}

# Options
START_SERVICES=false
SKIP_IF_DOWN=false
VERBOSE=""
TEST_FILTER=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE="-v"
            shift
            ;;
        --start-services)
            START_SERVICES=true
            shift
            ;;
        --skip-if-down)
            SKIP_IF_DOWN=true
            shift
            ;;
        cli)
            TEST_FILTER="-run TestCLI"
            shift
            ;;
        ollama)
            TEST_FILTER="-run TestOllama"
            shift
            ;;
        gateway)
            TEST_FILTER="-run TestGateway"
            shift
            ;;
        prompt|datasets|eval|deploy|observe)
            TEST_FILTER="-run Test$(echo $1 | sed 's/.*/\u&/')Service"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options] [test-filter]"
            echo ""
            echo "Options:"
            echo "  -v, --verbose        Verbose test output"
            echo "  --start-services     Start Docker Compose services before tests"
            echo "  --skip-if-down       Exit 0 (skip) if services unavailable"
            echo "  -h, --help           Show this help"
            echo ""
            echo "Test filters:"
            echo "  cli                  Run CLI tests only"
            echo "  ollama               Run Ollama integration tests only"
            echo "  gateway              Run gateway HTTP surface tests only"
            echo "  prompt               Run prompt service tests only"
            echo "  datasets             Run datasets service tests only"
            echo "  eval                 Run eval service tests only"
            echo "  deploy               Run deploy service tests only"
            echo "  observe              Run observe service tests only"
            exit 0
            ;;
        *)
            echo "Unknown option: $1 (use --help for usage)"
            exit 1
            ;;
    esac
done

echo -e "${YELLOW}Delos Integration Tests${NC}"
echo "========================"
echo ""

# Check that a TCP endpoint accepts connections (portable - works without nc)
check_tcp() {
    local name=$1
    local addr=$2
    local host=${addr%:*}
    local port=${addr#*:}

    if command -v nc &>/dev/null; then
        nc -z "$host" "$port" 2>/dev/null
    else
        # Fallback to bash /dev/tcp (works on most systems)
        (echo >/dev/tcp/"$host"/"$port") 2>/dev/null
    fi

    if [ $? -eq 0 ]; then
        echo -e "  ${GREEN}✓${NC} $name ($addr)"
        return 0
    else
        echo -e "  ${RED}✗${NC} $name ($addr)"
        return 1
    fi
}

# Check the gateway health endpoint over HTTP
check_gateway() {
    if curl -sf --connect-timeout 2 "$DELOS_GATEWAY_URL/healthz" >/dev/null 2>&1; then
        echo -e "  ${GREEN}✓${NC} gateway ($DELOS_GATEWAY_URL)"
        return 0
    else
        echo -e "  ${RED}✗${NC} gateway ($DELOS_GATEWAY_URL)"
        return 1
    fi
}

# Start services if requested
if [ "$START_SERVICES" = true ]; then
    echo "Starting services via Docker Compose..."
    cd "$PROJECT_ROOT"
    docker compose -f deploy/local/docker-compose.yaml up -d
    echo ""
    echo "Waiting for services to be ready..."
    sleep 5
fi

# Check availability of the two endpoints
echo "Checking service availability..."
SERVICES_OK=true
check_tcp "control plane" "$DELOS_CONTROL_PLANE_ADDR" || SERVICES_OK=false
check_gateway || SERVICES_OK=false
echo ""

if [ "$SERVICES_OK" = false ]; then
    if [ "$SKIP_IF_DOWN" = true ]; then
        echo -e "${YELLOW}Services unavailable - skipping integration tests${NC}"
        echo "To start services: docker compose -f deploy/local/docker-compose.yaml up -d"
        exit 0
    else
        echo -e "${RED}The control plane or gateway is not available.${NC}"
        echo ""
        echo "Options:"
        echo "  1. Start services: docker compose -f deploy/local/docker-compose.yaml up -d"
        echo "  2. Auto-start:     $0 --start-services"
        echo "  3. Skip tests:     $0 --skip-if-down"
        exit 1
    fi
fi

# Check for LLM API keys
if [ -n "$OPENAI_API_KEY" ] || [ -n "$ANTHROPIC_API_KEY" ] || [ -n "$DELOS_RUNTIME_OPENAI_KEY" ] || [ -n "$DELOS_RUNTIME_ANTHROPIC_KEY" ]; then
    echo -e "${GREEN}LLM API keys detected - cloud completion tests will run${NC}"
else
    echo -e "${YELLOW}No LLM API keys - cloud completion tests will be skipped${NC}"
fi

# Check for Ollama availability (port 11434)
OLLAMA_HOST=${DELOS_RUNTIME_OLLAMA_URL:-"http://localhost:11434"}
OLLAMA_PORT=${OLLAMA_HOST##*:}
OLLAMA_PORT=${OLLAMA_PORT%%/*}
if check_tcp "ollama" "localhost:$OLLAMA_PORT" 2>/dev/null; then
    echo -e "${GREEN}Ollama detected - local LLM tests will run${NC}"
else
    echo -e "${YELLOW}Ollama not detected - local LLM tests will be skipped${NC}"
fi
echo ""

# Ensure CLI binary exists
CLI_BINARY="$PROJECT_ROOT/bin/delos"
if [ ! -f "$CLI_BINARY" ]; then
    echo "Building delos binary..."
    cd "$PROJECT_ROOT"
    make build
    echo ""
fi

# Run tests
echo "Running integration tests..."
echo ""

cd "$PROJECT_ROOT"
go test -tags=integration $VERBOSE $TEST_FILTER ./tests/integration/...

echo ""
echo -e "${GREEN}Integration tests completed!${NC}"
