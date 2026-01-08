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
#   DELOS_*_ADDR               Override service addresses
#   DELOS_OPENAI_API_KEY       Required for LLM completion tests

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default service addresses
export DELOS_OBSERVE_ADDR=${DELOS_OBSERVE_ADDR:-"localhost:9000"}
export DELOS_RUNTIME_ADDR=${DELOS_RUNTIME_ADDR:-"localhost:9001"}
export DELOS_PROMPT_ADDR=${DELOS_PROMPT_ADDR:-"localhost:9002"}
export DELOS_DATASETS_ADDR=${DELOS_DATASETS_ADDR:-"localhost:9003"}
export DELOS_EVAL_ADDR=${DELOS_EVAL_ADDR:-"localhost:9004"}
export DELOS_DEPLOY_ADDR=${DELOS_DEPLOY_ADDR:-"localhost:9005"}

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
        prompt|datasets|eval|deploy|runtime|observe)
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
            echo "  prompt               Run prompt service tests only"
            echo "  datasets             Run datasets service tests only"
            echo "  eval                 Run eval service tests only"
            echo "  deploy               Run deploy service tests only"
            echo "  runtime              Run runtime service tests only"
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

# Check if a service is available (portable - works without nc)
check_service() {
    local name=$1
    local addr=$2
    local host=${addr%:*}
    local port=${addr#*:}

    # Try multiple methods for portability
    if command -v nc &>/dev/null; then
        nc -z "$host" "$port" 2>/dev/null
    elif command -v curl &>/dev/null; then
        curl -s --connect-timeout 1 "http://$addr" &>/dev/null || curl -s --connect-timeout 1 "$addr" &>/dev/null
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

# Start services if requested
if [ "$START_SERVICES" = true ]; then
    echo "Starting services via Docker Compose..."
    cd "$PROJECT_ROOT"
    docker-compose -f deploy/local/docker-compose.yaml up -d
    echo ""
    echo "Waiting for services to be ready..."
    sleep 5
fi

# Check service availability
echo "Checking service availability..."
SERVICES_OK=true
check_service "observe" "$DELOS_OBSERVE_ADDR" || SERVICES_OK=false
check_service "runtime" "$DELOS_RUNTIME_ADDR" || SERVICES_OK=false
check_service "prompt" "$DELOS_PROMPT_ADDR" || SERVICES_OK=false
check_service "datasets" "$DELOS_DATASETS_ADDR" || SERVICES_OK=false
check_service "eval" "$DELOS_EVAL_ADDR" || SERVICES_OK=false
check_service "deploy" "$DELOS_DEPLOY_ADDR" || SERVICES_OK=false
echo ""

if [ "$SERVICES_OK" = false ]; then
    if [ "$SKIP_IF_DOWN" = true ]; then
        echo -e "${YELLOW}Services unavailable - skipping integration tests${NC}"
        echo "To start services: docker-compose -f deploy/local/docker-compose.yaml up -d"
        exit 0
    else
        echo -e "${RED}Some services are not available.${NC}"
        echo ""
        echo "Options:"
        echo "  1. Start services: docker-compose -f deploy/local/docker-compose.yaml up -d"
        echo "  2. Auto-start:     $0 --start-services"
        echo "  3. Skip tests:     $0 --skip-if-down"
        exit 1
    fi
fi

# Check for LLM API keys
if [ -n "$DELOS_OPENAI_API_KEY" ] || [ -n "$DELOS_ANTHROPIC_API_KEY" ]; then
    echo -e "${GREEN}LLM API keys detected - completion tests will run${NC}"
else
    echo -e "${YELLOW}No LLM API keys - completion tests will be skipped${NC}"
fi
echo ""

# Ensure CLI binary exists
CLI_BINARY="$PROJECT_ROOT/bin/delos"
if [ ! -f "$CLI_BINARY" ]; then
    echo "Building CLI binary..."
    cd "$PROJECT_ROOT"
    make build-cli
    echo ""
fi

# Run tests
echo "Running integration tests..."
echo ""

cd "$PROJECT_ROOT"
go test -tags=integration $VERBOSE $TEST_FILTER ./tests/integration/...

echo ""
echo -e "${GREEN}Integration tests completed!${NC}"
