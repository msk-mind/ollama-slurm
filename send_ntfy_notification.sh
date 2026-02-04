#!/bin/bash
# Send push notification via ntfy when llama.cpp server is ready
# Usage: ./send_ntfy_notification.sh <job_id> <host> <port> <model_file> [ntfy_topic]

JOB_ID="$1"
HOST="$2"
PORT="$3"
MODEL_FILE="$4"
NTFY_TOPIC="${5:-${NTFY_TOPIC:-llama-servers}}"
NTFY_SERVER="${NTFY_SERVER:-https://ntfy.sh}"

if [ -z "$JOB_ID" ] || [ -z "$HOST" ] || [ -z "$PORT" ]; then
    echo "Usage: $0 <job_id> <host> <port> <model_file> [ntfy_topic]"
    exit 1
fi

MODEL_NAME=$(basename "$MODEL_FILE" 2>/dev/null || echo "unknown")
START_TIME=$(date)
REGISTRY_URL="${REGISTRY_URL:-}"

# Build notification message
TITLE="🦙 Llama Server Ready - Job $JOB_ID"
MESSAGE="Model: $MODEL_NAME
Host: $HOST:$PORT
Started: $START_TIME

Quick connect:
./connect_claude.sh $JOB_ID"

# Add registry link if available
if [ -n "$REGISTRY_URL" ]; then
    MESSAGE="$MESSAGE

Dashboard: ${REGISTRY_URL}/"
fi

# Build actions (clickable buttons in notification)
ACTIONS=""
if [ -n "$REGISTRY_URL" ]; then
    ACTIONS="view, Open Dashboard, ${REGISTRY_URL}/"
fi

# Send notification with retry
MAX_RETRIES=3
RETRY=0

while [ $RETRY -lt $MAX_RETRIES ]; do
    RESPONSE=$(curl -X POST "${NTFY_SERVER}/${NTFY_TOPIC}" \
        -H "Title: $TITLE" \
        -H "Priority: high" \
        -H "Tags: white_check_mark,llama" \
        ${ACTIONS:+-H "Actions: $ACTIONS"} \
        -d "$MESSAGE" \
        --silent \
        --show-error \
        --write-out "\n%{http_code}" \
        --connect-timeout 5 \
        --max-time 10 2>&1)
    
    HTTP_CODE=$(echo "$RESPONSE" | tail -n1)
    
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "201" ]; then
        echo "Push notification sent successfully to ntfy topic: $NTFY_TOPIC"
        echo "Subscribers can view at: ${NTFY_SERVER}/${NTFY_TOPIC}"
        exit 0
    fi
    
    RETRY=$((RETRY + 1))
    if [ $RETRY -lt $MAX_RETRIES ]; then
        echo "Ntfy notification failed (HTTP $HTTP_CODE), retrying in 3s... ($RETRY/$MAX_RETRIES)"
        sleep 3
    fi
done

echo "Warning: Could not send ntfy notification after $MAX_RETRIES attempts"
echo "Check if ntfy server is accessible: ${NTFY_SERVER}"
echo ""
echo "Notification would have been:"
echo "Topic: $NTFY_TOPIC"
echo "Title: $TITLE"
echo "Message:"
echo "$MESSAGE"

exit 1
