#!/usr/bin/env bash
# Agent end-to-end suite (D1): run unmodified Claude Code and Codex headless
# against a real delos-gateway in front of a mock provider, and record the
# traffic as cassettes for keyless replay.
#
# Live runs need real provider keys OR a mock upstream; cassette replay is
# keyless. This script is the live/record half; replay is `tests/agent/replay`
# (once the gateway's Bifrost path lands). At integration time it asserts:
# prompt caching, thinking blocks and tool calls survive the round trip.
#
# Usage:
#   tests/agent/run.sh                    record + run everything available
#   AGENT_REQUIRE_ALL=1 ...               a missing agent CLI is a failure (CI)
#   DELOS_AGENT_E2E=1 ...                 enable the suite (gate until DL-G lands)
#
# The suite is gated off by default: it depends on DL-G's gateway (Bifrost
# passthrough) and on a gateway-side Anthropic base-URL override so the
# Anthropic provider can be pointed at the mock upstream.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

MOCK_PORT="${MOCK_PORT:-9099}"
GATEWAY_PORT="${GATEWAY_PORT:-8099}"
REQUIRE_ALL="${AGENT_REQUIRE_ALL:-0}"
ENABLED="${DELOS_AGENT_E2E:-0}"

if [[ "$ENABLED" != "1" ]]; then
    echo "agent e2e suite disabled: set DELOS_AGENT_E2E=1 once DL-G's gateway lands"
    [[ "$REQUIRE_ALL" == "1" ]] && { echo "AGENT_REQUIRE_ALL=1 but DELOS_AGENT_E2E is not set" >&2; exit 1; }
    exit 0
fi

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
(
    cd "$WORKDIR"
    DELOS_GATEWAY_PORT="$GATEWAY_PORT" \
    DELOS_COMPAT_NAME=mock \
    DELOS_COMPAT_BASE_URL="$MOCK_URL/v1" \
    ANTHROPIC_API_KEY="${ANTHROPIC_API_KEY:-mock-key}" \
    ANTHROPIC_BASE_URL="${ANTHROPIC_BASE_URL:-$MOCK_URL}" \
    DELOS_CACHE=off \
    DELOS_STORAGE_BACKEND=memory \
    DELOS_ALLOW_UNAUTHENTICATED=true \
    DELOS_LOG_LEVEL=warn \
    "$WORKDIR/delos-gateway" > "$WORKDIR/gateway.log" 2>&1 &
    echo $! > "$WORKDIR/gateway.pid"
)
GATEWAY_PID="$(cat "$WORKDIR/gateway.pid")"
wait_for "$GATEWAY_URL/healthz" "delos-gateway"

status=0
ran_any=0

run_agent() {
    local name="$1" cli="$2"
    shift 2
    echo
    echo "==> $name"
    if ! command -v "$cli" > /dev/null 2>&1; then
        echo "$cli not installed - skipping $name"
        [[ "$REQUIRE_ALL" == "1" ]] && { echo "AGENT_REQUIRE_ALL=1 and $cli is missing" >&2; status=1; }
        return
    fi
    ran_any=1
    # Scripted, headless task: write a one-line file, so success is observable
    # in the working directory and the run is fully non-interactive.
    local task_dir="$WORKDIR/$name-task"
    mkdir -p "$task_dir"
    if ! "$cli" -p "create a file named done.txt containing the word ok" \
        --dangerously-skip-permissions \
        --output-format json > "$WORKDIR/$name.log" 2>&1; then
        echo "$name session FAILED" >&2
        tail -n 30 "$WORKDIR/$name.log" >&2 || true
        status=1
    fi
    # TODO(DL-T, D1): capture the raw upstream traffic as a cassette here, then
    # assert on replay that cache_control / thinking / tool-call blocks from the
    # recorded session survive byte-faithfully (note 0001: same-provider
    # passthrough is byte-faithful, beta headers and SSE event order included).
}

# Claude Code speaks the Anthropic surface (/v1/messages); Codex the OpenAI
# Responses surface (/v1/responses).
run_agent "Claude Code" claude
run_agent "Codex" codex

echo
if [[ "$ran_any" == "0" ]]; then
    echo "no agent CLIs available; nothing was verified"
    [[ "$REQUIRE_ALL" == "1" ]] && status=1
elif [[ "$status" == "0" ]]; then
    echo "agent end-to-end suite passed"
fi
exit $status
