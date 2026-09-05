#!/usr/bin/env bash
set -e

# ==============================================================================
# DVFS Metaserver Startup Wrapper
# ==============================================================================

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

META_PORT="${META_PORT:-50051}"
STATE_FILE="${STATE_FILE:-./bin/metaserver_state.json}"
HEARTBEAT_TIMEOUT="${HEARTBEAT_TIMEOUT:-30s}"
HEARTBEAT_INTERVAL="${HEARTBEAT_INTERVAL:-5s}"

# Ensure state directory exists
STATE_DIR="$(dirname "$STATE_FILE")"
if [ -n "$STATE_DIR" ] && [ "$STATE_DIR" != "." ]; then
    mkdir -p "$STATE_DIR"
fi

# Ensure binary is built
if [ ! -f "./bin/metaserver" ]; then
    echo "[STARTUP] ./bin/metaserver not found. Building..."
    if command -v go >/dev/null 2>&1; then
        go build -o ./bin/metaserver cmd/metaserver/main.go
    elif command -v make >/dev/null 2>&1; then
        make build
    else
        echo "[STARTUP ERROR] Binary ./bin/metaserver not found and neither go nor make is available." >&2
        exit 1
    fi
fi

echo "[STARTUP] Starting DVFS Metaserver..."
echo "[STARTUP] Port:               ${META_PORT}"
echo "[STARTUP] State File:         ${STATE_FILE}"
echo "[STARTUP] Heartbeat Timeout:  ${HEARTBEAT_TIMEOUT}"
echo "[STARTUP] Heartbeat Interval: ${HEARTBEAT_INTERVAL}"

exec ./bin/metaserver \
  -port="${META_PORT}" \
  -state_file="${STATE_FILE}" \
  -heartbeat_timeout="${HEARTBEAT_TIMEOUT}" \
  -heartbeat_check_interval="${HEARTBEAT_INTERVAL}" \
  "$@"
