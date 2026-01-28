#!/bin/bash
# Configure environment for Claude CLI to connect to llama.cpp server
# Source this file or use connect_claude.sh wrapper

# Check if server connection info is set
if [ -z "$LLAMA_SERVER_HOST" ] || [ -z "$LLAMA_SERVER_PORT" ]; then
    echo "Error: LLAMA_SERVER_HOST and LLAMA_SERVER_PORT must be set"
    echo "Source the connection file first:"
    echo "  source llama_server_connection_<job_id>.txt"
    return 1 2>/dev/null || exit 1
fi

# Configure Claude CLI to use llama.cpp server
export ANTHROPIC_AUTH_TOKEN=ollama
export DISABLE_TELEMETRY=1
export DISABLE_ERROR_REPORTING=1
export CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1
export ANTHROPIC_BASE_URL="http://${LLAMA_SERVER_HOST}:${LLAMA_SERVER_PORT}"

echo "Claude CLI configured to connect to llama.cpp server"
echo "Base URL: $ANTHROPIC_BASE_URL"
echo ""
echo "You can now run: claude"
