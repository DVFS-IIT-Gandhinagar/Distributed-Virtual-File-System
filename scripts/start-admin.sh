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


# Ensure user environment (NVM, .profile, .bashrc, PATH) is loaded under systemd
if [ -s "$HOME/.profile" ]; then
    . "$HOME/.profile" 2>/dev/null || true
fi
if [ -s "$HOME/.bashrc" ]; then
    . "$HOME/.bashrc" 2>/dev/null || true
fi
if [ -s "$HOME/.nvm/nvm.sh" ]; then
    export NVM_DIR="$HOME/.nvm"
    . "$NVM_DIR/nvm.sh" 2>/dev/null || true
fi
export PATH="$PATH:/usr/local/bin:/usr/bin:/bin:$HOME/.local/bin"

# Build UI if npm is available, or use existing pre-built static files
if command -v npm >/dev/null 2>&1; then
    echo "[STARTUP] Building admin UI with npm..."
    (cd ./cmd/admin/ui && npm run build) || echo "[STARTUP WARNING] npm run build failed, continuing with existing files..."
elif [ -f "$STATIC_DIR/index.html" ]; then
    echo "[STARTUP] Found pre-built UI in $STATIC_DIR (npm not in PATH; skipping build)."
else
    echo "[STARTUP WARNING] npm not found and $STATIC_DIR/index.html does not exist. API will start, but web UI may be unavailable."
fi

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
