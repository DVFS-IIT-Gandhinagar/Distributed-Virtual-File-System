#!/usr/bin/env bash
set -e

# ==============================================================================
# DVFS Fileserver Startup Wrapper
# Automatically detects the primary Ethernet IPv4 address routing to the Metaserver
# ==============================================================================

# Default configurations (override via environment variables or flags)
GIST_MACHINES_URL="https://gist.githubusercontent.com/dvfs-iitgn/6eb8da397735b83f76b54af4cca64c83/raw/machines.json"
CACHE_FILE="/tmp/dvfs_meta_ip"

# 1. Resolve Fileserver ID (FS_ID)
# Respect user-specified FS_ID if already provided, otherwise auto-detect from hostname or user (dvfs1 -> fs1, etc.)
if [ -n "$FS_ID" ]; then
    echo "[STARTUP] Using user-specified FS_ID: ${FS_ID}"
else
    DETECTED_NAME="${USER:-$(id -un 2>/dev/null || whoami)}"
    if [[ "$DETECTED_NAME" =~ dvfs([0-9]+) ]]; then
        FS_ID="fs${BASH_REMATCH[1]}"
    elif [[ "$HOSTNAME" =~ dvfs([0-9]+) ]]; then
        FS_ID="fs${BASH_REMATCH[1]}"
    else
        FS_ID="fs1"
    fi
    echo "[STARTUP] Auto-detected FS_ID: ${FS_ID} (from host/user '${DETECTED_NAME}')"
fi

# 2. Resolve Metaserver Address (META_ADDR)
# Priority:
#   a) User-specified META_ADDR (e.g. META_ADDR=10.0.171.38:50051 or static IP)
#   b) User-specified META_IP (e.g. META_IP=10.0.171.38)
#   c) Dynamic query from Gist (finds 'dvfs1' IP)
#   d) Cached IP from previous query (/tmp/dvfs_meta_ip)
#   e) Fallback to 127.0.0.1:50051
if [ -n "$META_ADDR" ]; then
    echo "[STARTUP] Using user-specified META_ADDR: ${META_ADDR}"
elif [ -n "$META_IP" ]; then
    META_ADDR="${META_IP}:50051"
    echo "[STARTUP] Using user-specified META_IP: ${META_ADDR}"
else
    echo "[STARTUP] Querying Metaserver (dvfs1) IP from Gist..."
    META_IP=""

    # Attempt fetch via curl and python3 parser
    if command -v python3 >/dev/null 2>&1; then
        META_IP=$(curl -sSL --connect-timeout 5 "$GIST_MACHINES_URL" 2>/dev/null | python3 -c '
import sys, json
try:
    data = json.load(sys.stdin)
    for m in data:
        if m.get("username") == "dvfs1" and m.get("ip"):
            print(m["ip"].strip())
            sys.exit(0)
except Exception:
    pass
' 2>/dev/null || true)
    fi

    # Fallback parser using grep/sed if python3 is unavailable
    if [ -z "$META_IP" ]; then
        META_IP=$(curl -sSL --connect-timeout 5 "$GIST_MACHINES_URL" 2>/dev/null | \
            grep -A 3 '"username":\s*"dvfs1"' | grep '"ip":' | head -n 1 | sed -E 's/.*"ip":\s*"([^"]+)".*/\1/' || true)
    fi

    if [ -n "$META_IP" ]; then
        echo "$META_IP" > "$CACHE_FILE" 2>/dev/null || true
        META_ADDR="${META_IP}:50051"
        echo "[STARTUP] Resolved Metaserver (dvfs1) from Gist: ${META_ADDR}"
    elif [ -f "$CACHE_FILE" ] && [ -s "$CACHE_FILE" ]; then
        META_IP=$(cat "$CACHE_FILE" | tr -d ' \r\n')
        META_ADDR="${META_IP}:50051"
        echo "[STARTUP WARNING] Gist unreachable. Using cached Metaserver IP: ${META_ADDR}"
    else
        META_ADDR="127.0.0.1:50051"
        echo "[STARTUP WARNING] Could not resolve Metaserver IP. Defaulting to: ${META_ADDR}"
    fi
fi

META_HOST=$(echo "$META_ADDR" | cut -d: -f1)

# 3. Dynamically extract the local IP that routes to the metaserver.
# Falls back to the first global IPv4 address from `ip addr show` or `hostname -I`.
OWN_IP=""
if command -v ip >/dev/null 2>&1; then
    OWN_IP=$(ip -4 route get "$META_HOST" 2>/dev/null | awk '{for(i=1;i<=NF;i++) if($i=="src") print $(i+1)}')
    if [ -z "$OWN_IP" ]; then
        OWN_IP=$(ip -4 addr show scope global 2>/dev/null | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | head -n 1)
    fi
fi
if [ -z "$OWN_IP" ]; then
    OWN_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
fi

if [ -z "$OWN_IP" ]; then
    echo "[STARTUP ERROR] Could not determine local IP address. Please specify OWN_IP manually." >&2
    exit 1
fi

FS_PORT="${FS_PORT:-50052}"
DATA_DIR="${DATA_DIR:-./fileserver_data}"

echo "[STARTUP] Starting DVFS Fileserver..."
echo "[STARTUP] ID:        ${FS_ID}"
echo "[STARTUP] Port:      ${FS_PORT}"
echo "[STARTUP] Data Dir:  ${DATA_DIR}"
echo "[STARTUP] Meta Addr: ${META_ADDR}"
echo "[STARTUP] Own IP:    ${OWN_IP}"

# Ensure data directory exists
mkdir -p "${DATA_DIR}"

exec ./bin/fileserver \
  -id="${FS_ID}" \
  -port="${FS_PORT}" \
  -data="${DATA_DIR}" \
  -meta_addr="${META_ADDR}" \
  -own_ip="${OWN_IP}" \
  "$@"
