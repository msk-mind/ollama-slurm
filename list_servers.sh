#!/bin/bash
# List available llama.cpp servers from the registry

REGISTRY_URL="${REGISTRY_URL:-http://localhost:5000}"

# Parse arguments
OWNER=""
JSON_OUTPUT=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --owner|-u)
            OWNER="$2"
            shift 2
            ;;
        --json)
            JSON_OUTPUT=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "List available llama.cpp servers from the central registry"
            echo ""
            echo "Options:"
            echo "  --owner, -u USER    Show only servers owned by USER"
            echo "  --json              Output raw JSON"
            echo "  --help, -h          Show this help"
            echo ""
            echo "Environment:"
            echo "  REGISTRY_URL        Registry server URL (default: http://localhost:5000)"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Fetch servers
if [ -n "$OWNER" ]; then
    URL="${REGISTRY_URL}/servers/by-owner/${OWNER}"
else
    URL="${REGISTRY_URL}/servers"
fi

RESPONSE=$(curl -s "$URL" --connect-timeout 5 --max-time 10)

if [ $? -ne 0 ]; then
    echo "Error: Could not connect to registry at $REGISTRY_URL"
    echo "Make sure the registry server is running"
    exit 1
fi

if [ "$JSON_OUTPUT" = true ]; then
    echo "$RESPONSE" | jq .
    exit 0
fi

# Pretty print
echo "Available llama.cpp Servers"
echo "============================"
echo ""

COUNT=$(echo "$RESPONSE" | jq -r '.count')

if [ "$COUNT" = "0" ]; then
    echo "No servers currently running"
    exit 0
fi

echo "$RESPONSE" | jq -r '.servers[] | 
"Job ID:      \(.job_id)
Owner:       \(.owner // "unknown")
Model:       \(.model_name // "unknown")
Host:        \(.host):\(.port)
Uptime:      \(.uptime_hours // 0)h
Started:     \(.start_time // "unknown")

Connect:     ./connect_claude.sh \(.job_id)
SSH Tunnel:  ssh -L \(.port):\(.host):\(.port) <login_node>

---"
'
