#!/bin/bash
# Register a llama.cpp server with the central registry
# Called from llama_server.slurm after server starts

REGISTRY_URL="${REGISTRY_URL:-http://localhost:5000}"
JOB_ID="$1"
HOST="$2"
PORT="$3"
MODEL_FILE="$4"
NOTIFY_EMAIL="$5"

if [ -z "$JOB_ID" ] || [ -z "$HOST" ] || [ -z "$PORT" ]; then
    echo "Usage: $0 <job_id> <host> <port> [model_file] [email]"
    exit 1
fi

# Build JSON payload
MODEL_NAME=$(basename "$MODEL_FILE" 2>/dev/null || echo "unknown")
START_TIME=$(date -Iseconds)

JSON_DATA=$(cat <<EOF
{
  "host": "$HOST",
  "port": "$PORT",
  "owner": "$USER",
  "model_file": "$MODEL_FILE",
  "model_name": "$MODEL_NAME",
  "start_time": "$START_TIME",
  "slurm_node": "$SLURM_NODELIST",
  "notify_email": "$NOTIFY_EMAIL"
}
EOF
)

# Register with retry
MAX_RETRIES=3
RETRY=0

while [ $RETRY -lt $MAX_RETRIES ]; do
    if curl -X POST \
        -H "Content-Type: application/json" \
        -d "$JSON_DATA" \
        "${REGISTRY_URL}/servers/${JOB_ID}" \
        --connect-timeout 5 \
        --max-time 10 \
        --silent \
        --show-error; then
        echo ""
        echo "Successfully registered with server registry"
        echo "Job ID: $JOB_ID"
        echo "Users can discover this server at: ${REGISTRY_URL}/servers"
        exit 0
    fi
    
    RETRY=$((RETRY + 1))
    if [ $RETRY -lt $MAX_RETRIES ]; then
        echo "Registry registration failed, retrying in 5s... ($RETRY/$MAX_RETRIES)"
        sleep 5
    fi
done

echo "Warning: Could not register with server registry after $MAX_RETRIES attempts"
echo "Server is still running but won't be discoverable via registry"
exit 1
