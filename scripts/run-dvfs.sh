#!/bin/bash

# Default number of clients
NUM_CLIENTS=${1:-3}

echo "Building Docker image..."
docker compose build

echo "Starting DVFS Server and $NUM_CLIENTS Clients..."
docker compose up -d --scale client=$NUM_CLIENTS

# Wait a moment for containers to start
sleep 2

echo "Opening terminals..."

# Function to open a new terminal window
open_terminal() {
    local title=$1
    local cmd=$2

    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS Terminal.app
        osascript <<EOF
tell application "Terminal"
    do script "echo -n -e "\033]0;$title\007"; $cmd"
end tell
EOF
    elif command -v gnome-terminal >/dev/null 2>&1; then
        # GNOME Terminal
        gnome-terminal --title="$title" -- sh -c "$cmd"
    elif command -v x-terminal-emulator >/dev/null 2>&1; then
        # Generic Linux emulator
        x-terminal-emulator -e "sh -c "$cmd""
    elif command -v xterm >/dev/null 2>&1; then
        # xterm
        xterm -T "$title" -e "sh -c "$cmd"" &
    else
        echo "Could not find a supported terminal emulator (gnome-terminal, xterm, Terminal.app)."
        echo "Please manually run: $cmd"
    fi
}

# Open Server Log Window
open_terminal "DVFS Server Logs" "docker compose logs -f server"

# Get client container names
CLIENT_CONTAINERS=$(docker compose ps client --format "{{.Name}}")

for container in $CLIENT_CONTAINERS; do
    echo "Attaching to client: $container"
    # Open Client Interactive Window
    # We pass the full path to the current directory to ensure context if needed, 
    # but docker exec works globally with container names.
    open_terminal "DVFS Client - $container" "docker exec -it $container /bin/sh -c 'make exec-client USER=user_$container IP_ADDR=server'"
done

echo "All windows opened. You can now interact with the clients in their respective terminals."
