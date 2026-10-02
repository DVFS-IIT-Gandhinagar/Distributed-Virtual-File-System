#!/usr/bin/env bash
set -e

# ==============================================================================
# DVFS Metaserver Startup Wrapper
# ==============================================================================

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

META_PORT="${META_PORT:-50051}"
MONGO_URI="${MONGO_URI:-mongodb://127.0.0.1:27017/dvfs}"
MONGO_DB="${MONGO_DB:-dvfs}"
HEARTBEAT_TIMEOUT="${HEARTBEAT_TIMEOUT:-30s}"
HEARTBEAT_INTERVAL="${HEARTBEAT_INTERVAL:-5s}"
TLS_CERT="${TLS_CERT:-certs/server.crt}"
TLS_KEY="${TLS_KEY:-certs/server.key}"
TLS_CA="${TLS_CA:-certs/ca.crt}"

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
echo "[STARTUP] Mongo URI:          $(printf %s "${MONGO_URI}" | sed -E 's#(//[^/@:]+):[^@]*@#\1:***@#')"
echo "[STARTUP] Mongo DB:           ${MONGO_DB}"
echo "[STARTUP] Heartbeat Timeout:  ${HEARTBEAT_TIMEOUT}"
echo "[STARTUP] Heartbeat Interval: ${HEARTBEAT_INTERVAL}"
echo "[STARTUP] TLS Cert:           ${TLS_CERT}"
echo "[STARTUP] TLS Key:            ${TLS_KEY}"
echo "[STARTUP] TLS CA:             ${TLS_CA}"

exec ./bin/metaserver \
  -port="${META_PORT}" \
  -mongo_uri="${MONGO_URI}" \
  -mongo_db="${MONGO_DB}" \
  -heartbeat_timeout="${HEARTBEAT_TIMEOUT}" \
  -heartbeat_check_interval="${HEARTBEAT_INTERVAL}" \
  -tls_cert="${TLS_CERT}" \
  -tls_key="${TLS_KEY}" \
  -ca_cert="${TLS_CA}" \
  "$@"
