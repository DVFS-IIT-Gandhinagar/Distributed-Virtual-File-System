#!/usr/bin/env bash
set -e

# ==============================================================================
# DVFS Fileserver Startup Wrapper
# Automatically detects the primary Ethernet IPv4 address routing to the Metaserver
# ==============================================================================

# Default configurations (override via environment variables or flags)
META_ADDR="${META_ADDR:-127.0.0.1:50051}"
META_HOST=$(echo "$META_ADDR" | cut -d: -f1)

# Dynamically extract the local IP that routes to the metaserver.
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

FS_ID="${FS_ID:-fs1}"
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
  -own_ip="${OWN_IP}"
