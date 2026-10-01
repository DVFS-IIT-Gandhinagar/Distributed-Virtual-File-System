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
TLS_CERT="${TLS_CERT:-certs/server.crt}"
TLS_KEY="${TLS_KEY:-certs/server.key}"
TLS_CA="${TLS_CA:-certs/ca.crt}"
HTTP_PORT="${HTTP_PORT:-8090}"

# Raft Configuration
RAFT_ENABLED="${RAFT_ENABLED:-false}"
RAFT_NODE_ID="${RAFT_NODE_ID:-}"
RAFT_BIND_ADDR="${RAFT_BIND_ADDR:-}"
RAFT_PEERS="${RAFT_PEERS:-}"
RAFT_DATA_DIR="${RAFT_DATA_DIR:-./bin/raft/}"
RAFT_BOOTSTRAP="${RAFT_BOOTSTRAP:-false}"
GIST_MACHINES_URL="${GIST_MACHINES_URL:-https://gist.githubusercontent.com/dvfs-iitgn/6eb8da397735b83f76b54af4cca64c83/raw/machines.json}"

if [ "$RAFT_ENABLED" = "true" ]; then
    # Auto-detect node ID if empty
    if [ -z "$RAFT_NODE_ID" ]; then
        DETECTED_NAME="${USER:-$(id -un 2>/dev/null || whoami)}"
        if [[ "$DETECTED_NAME" =~ dvfs([0-9]+) ]]; then
            RAFT_NODE_ID="dvfs${BASH_REMATCH[1]}"
        elif [[ "$HOSTNAME" =~ dvfs([0-9]+) ]]; then
            RAFT_NODE_ID="dvfs${BASH_REMATCH[1]}"
        else
            RAFT_NODE_ID="dvfs1"
        fi
    fi

    # Auto-detect own LAN IP if RAFT_BIND_ADDR is not specified
    if [ -z "$RAFT_BIND_ADDR" ]; then
        OWN_IP=""
        if command -v ip >/dev/null 2>&1; then
            OWN_IP=$(ip -4 route get 8.8.8.8 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}')
            if [ -z "$OWN_IP" ]; then
                OWN_IP=$(ip -4 addr show scope global 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -n 1)
            fi
        fi
        if [ -z "$OWN_IP" ]; then
            OWN_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
        fi
        if [ -n "$OWN_IP" ]; then
            RAFT_BIND_ADDR="${OWN_IP}:50053"
        fi
    fi

    # Auto-resolve RAFT_PEERS from Gist if empty
    if [ -z "$RAFT_PEERS" ] && command -v python3 >/dev/null 2>&1; then
        RAFT_PEERS=$(curl -sSL --connect-timeout 5 "$GIST_MACHINES_URL" 2>/dev/null | python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    peers = []
    for m in data:
        uname = m.get('username', '')
        ip = m.get('ip', '')
        services = m.get('services', [])
        if 'metaserver' in services and uname != '$RAFT_NODE_ID' and ip:
            peers.append(uname + '=' + ip.strip() + ':50053')
    print(','.join(peers))
except Exception:
    pass
" 2>/dev/null || true)
    fi
fi

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
echo "[STARTUP] TLS Cert:           ${TLS_CERT}"
echo "[STARTUP] TLS Key:            ${TLS_KEY}"
echo "[STARTUP] TLS CA:             ${TLS_CA}"
echo "[STARTUP] HTTP Status Port:   ${HTTP_PORT}"
if [ "$RAFT_ENABLED" = "true" ]; then
    echo "[STARTUP] Raft Enabled:       true (Node: ${RAFT_NODE_ID}, Bind: ${RAFT_BIND_ADDR})"
fi

exec ./bin/metaserver \
  -port="${META_PORT}" \
  -state_file="${STATE_FILE}" \
  -heartbeat_timeout="${HEARTBEAT_TIMEOUT}" \
  -heartbeat_check_interval="${HEARTBEAT_INTERVAL}" \
  -tls_cert="${TLS_CERT}" \
  -tls_key="${TLS_KEY}" \
  -ca_cert="${TLS_CA}" \
  -http_port="${HTTP_PORT}" \
  -raft_enabled="${RAFT_ENABLED}" \
  -raft_node_id="${RAFT_NODE_ID}" \
  -raft_bind_addr="${RAFT_BIND_ADDR}" \
  -raft_peers="${RAFT_PEERS}" \
  -raft_data_dir="${RAFT_DATA_DIR}" \
  -raft_bootstrap="${RAFT_BOOTSTRAP}" \
  "$@"
