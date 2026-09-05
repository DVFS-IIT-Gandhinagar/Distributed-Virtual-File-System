#!/usr/bin/env bash
set -e

# ==============================================================================
# DVFS Admin Console Startup Wrapper
# ==============================================================================

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_DIR"

ADMIN_PORT="${ADMIN_PORT:-8080}"
STATE_FILE="${STATE_FILE:-./bin/metaserver_state.json}"
STATIC_DIR="${STATIC_DIR:-./cmd/admin/static}"
SSH_USER="${SSH_USER:-$(id -un 2>/dev/null || whoami)}"
REPO_PATH="${REPO_PATH:-$HOME/Distributed-Virtual-File-System}"
SSH_PORT="${SSH_PORT:-22}"

# Auto-detect SSH private key if not explicitly set
if [ -z "$SSH_KEY" ]; then
    if [ -f "$HOME/.ssh/id_ed25519" ]; then
        SSH_KEY="$HOME/.ssh/id_ed25519"
    elif [ -f "$HOME/.ssh/id_rsa" ]; then
        SSH_KEY="$HOME/.ssh/id_rsa"
    else
        SSH_KEY="$HOME/.ssh/id_ed25519"
    fi
fi


cd ./cmd/admin/ui
npm run build
cd ../../..


# Ensure binary is built
if [ ! -f "./bin/admin" ]; then
    echo "[STARTUP] ./bin/admin not found. Building..."
    if command -v go >/dev/null 2>&1; then
        go build -o ./bin/admin cmd/admin/main.go
    elif command -v make >/dev/null 2>&1; then
        make build
    else
        echo "[STARTUP ERROR] Binary ./bin/admin not found and neither go nor make is available." >&2
        exit 1
    fi
fi

echo "[STARTUP] Starting DVFS Admin Console..."
echo "[STARTUP] Port:       ${ADMIN_PORT}"
echo "[STARTUP] State File: ${STATE_FILE}"
echo "[STARTUP] Static Dir: ${STATIC_DIR}"
echo "[STARTUP] SSH User:   ${SSH_USER}"
echo "[STARTUP] SSH Key:    ${SSH_KEY}"
echo "[STARTUP] SSH Port:   ${SSH_PORT}"
echo "[STARTUP] Repo Path:  ${REPO_PATH}"

exec ./bin/admin \
  -port="${ADMIN_PORT}" \
  -state_file="${STATE_FILE}" \
  -static="${STATIC_DIR}" \
  -ssh_user="${SSH_USER}" \
  -ssh_key="${SSH_KEY}" \
  -ssh_port="${SSH_PORT}" \
  -repo_path="${REPO_PATH}" \
  "$@"
