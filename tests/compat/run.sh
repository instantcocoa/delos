#!/usr/bin/env bash
# Client-compatibility suite.
#
# Starts a mock OpenAI-compatible upstream and a real delos-gateway in front of
# it, then runs the unmodified official client libraries against the gateway:
#
#   - Python: openai (chat, streaming, models, embeddings) + anthropic (messages,
#     streaming) via `uv run --with ...`
#   - Node:   openai (chat, streaming, models, embeddings) via npm + node
#
# Nothing but base_url changes on the client side. If a client release breaks
# the gateway's surfaces, this fails before users find out.
#
# Usage:
#   tests/compat/run.sh                 run everything available
#   COMPAT_REQUIRE_ALL=1 ...            missing uv/node is a failure (CI)
#   MOCK_PORT=9099 GATEWAY_PORT=8099    override ports
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

MOCK_PORT="${MOCK_PORT:-9099}"
GATEWAY_PORT="${GATEWAY_PORT:-8099}"
COMPAT_MODEL="${DELOS_COMPAT_MODEL:-delos-mock}"
REQUIRE_ALL="${COMPAT_REQUIRE_ALL:-0}"

GATEWAY_URL="http://127.0.0.1:${GATEWAY_PORT}"
MOCK_URL="http://127.0.0.1:${MOCK_PORT}"

WORKDIR="$(mktemp -d)"
MOCK_PID=""
GATEWAY_PID=""

cleanup() {
    local status=$?
    [[ -n "$GATEWAY_PID" ]] && kill "$GATEWAY_PID" 2>/dev/null || true
    [[ -n "$MOCK_PID" ]] && kill "$MOCK_PID" 2>/dev/null || true
    wait 2>/dev/null || true
    if [[ $status -ne 0 ]]; then
        echo
        echo "--- gateway log ---"
        tail -n 40 "$WORKDIR/gateway.log" 2>/dev/null || true
        echo "--- mock upstream log ---"
        tail -n 20 "$WORKDIR/mock.log" 2>/dev/null || true
    fi
    rm -rf "$WORKDIR"
    exit $status
}
trap cleanup EXIT INT TERM

wait_for() {
    local url="$1" name="$2"
    for _ in $(seq 1 60); do
        if curl -sf "$url" > /dev/null 2>&1; then
            return 0
        fi
        sleep 0.5
    done
    echo "error: $name did not become ready at $url" >&2
    return 1
}

echo "==> Building delos-gateway and the mock upstream"
export CGO_ENABLED="${CGO_ENABLED:-0}"
go build -o "$WORKDIR/delos-gateway" "$PROJECT_ROOT/cmd/delos-gateway"
go build -o "$WORKDIR/mockupstream" "$PROJECT_ROOT/tests/compat/mockupstream"

echo "==> Starting the mock upstream on :$MOCK_PORT"
"$WORKDIR/mockupstream" -addr "127.0.0.1:${MOCK_PORT}" > "$WORKDIR/mock.log" 2>&1 &
MOCK_PID=$!
wait_for "$MOCK_URL/healthz" "mock upstream"

echo "==> Starting delos-gateway on :$GATEWAY_PORT"
# The gateway runs from a scratch directory so no repo-local config file leaks
# into the test, and with the response cache off so every client call is real.
(
    cd "$WORKDIR"
    DELOS_GATEWAY_PORT="$GATEWAY_PORT" \
    DELOS_COMPAT_NAME=mock \
    DELOS_COMPAT_BASE_URL="$MOCK_URL/v1" \
    DELOS_CACHE=off \
    DELOS_STORAGE_BACKEND=memory \
    DELOS_LOG_LEVEL=warn \
    "$WORKDIR/delos-gateway" > "$WORKDIR/gateway.log" 2>&1 &
    echo $! > "$WORKDIR/gateway.pid"
)
GATEWAY_PID="$(cat "$WORKDIR/gateway.pid")"
wait_for "$GATEWAY_URL/healthz" "delos-gateway"

export DELOS_GATEWAY_URL="$GATEWAY_URL"
export DELOS_COMPAT_MODEL="$COMPAT_MODEL"
export DELOS_API_KEY="delos-dev"

status=0
ran_any=0

echo
echo "==> Python clients (openai + anthropic)"
if command -v uv > /dev/null 2>&1; then
    ran_any=1
    if ! uv run --quiet --with openai --with anthropic \
        "$PROJECT_ROOT/tests/compat/python/compat_test.py"; then
        echo "python compatibility checks FAILED" >&2
        status=1
    fi
else
    echo "uv not installed - skipping the Python clients"
    [[ "$REQUIRE_ALL" == "1" ]] && { echo "COMPAT_REQUIRE_ALL=1 and uv is missing" >&2; status=1; }
fi

echo
echo "==> Node client (openai)"
if command -v npm > /dev/null 2>&1 && command -v node > /dev/null 2>&1; then
    ran_any=1
    # Install into a scratch copy so the repo stays free of node_modules.
    cp "$PROJECT_ROOT/tests/compat/node/package.json" \
       "$PROJECT_ROOT/tests/compat/node/compat_test.mjs" "$WORKDIR/"
    if (cd "$WORKDIR" && npm install --silent --no-audit --no-fund > npm.log 2>&1); then
        if ! (cd "$WORKDIR" && node compat_test.mjs); then
            echo "node compatibility checks FAILED" >&2
            status=1
        fi
    else
        echo "npm install failed:" >&2
        tail -n 20 "$WORKDIR/npm.log" >&2 || true
        [[ "$REQUIRE_ALL" == "1" ]] && status=1
    fi
else
    echo "node/npm not installed - skipping the Node client"
    [[ "$REQUIRE_ALL" == "1" ]] && { echo "COMPAT_REQUIRE_ALL=1 and node/npm are missing" >&2; status=1; }
fi

echo
if [[ "$ran_any" == "0" ]]; then
    echo "no client runtimes available; nothing was verified"
    [[ "$REQUIRE_ALL" == "1" ]] && status=1
elif [[ "$status" == "0" ]]; then
    echo "compatibility suite passed"
fi
exit $status
