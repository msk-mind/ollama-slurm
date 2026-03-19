#!/bin/bash
# Configure environment for OpenClaw CLI to connect to llama.cpp server
# Source this file or use connect_openclaw_llama.sh wrapper

# Check if server connection info is set
if [ -z "$LLAMA_SERVER_HOST" ] || [ -z "$LLAMA_SERVER_PORT" ]; then
    echo "Error: LLAMA_SERVER_HOST and LLAMA_SERVER_PORT must be set"
    echo "Source the connection file first:"
    echo "  source llama_server_connection_<job_id>.txt"
    return 1 2>/dev/null || exit 1
fi

# Derive model name from MODEL_FILE (basename without .gguf for display)
MODEL_NAME="${MODEL_FILE##*/}"
MODEL_NAME="${MODEL_NAME%.gguf}"

# Configure OpenClaw to use llama.cpp server (OpenAI-compatible API)
export OPENAI_BASE_URL="http://${LLAMA_SERVER_HOST}:${LLAMA_SERVER_PORT}/v1"
export OPENAI_API_KEY=ollama
export OPENCLAW_LOCAL_MODEL="${MODEL_NAME}"
export DISABLE_TELEMETRY=1

echo "OpenClaw configured to connect to llama.cpp server"
echo "Base URL: $OPENAI_BASE_URL"
echo "Model:    $OPENCLAW_LOCAL_MODEL"
echo ""
echo "You can now run: openclaw"
