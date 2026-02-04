#!/bin/bash
# Send email notification when llama.cpp server is ready
# Usage: ./send_notification.sh <job_id> <host> <port> <model_file> [email]

JOB_ID="$1"
HOST="$2"
PORT="$3"
MODEL_FILE="$4"
EMAIL="${5:-$USER@$(hostname -d)}"

if [ -z "$JOB_ID" ] || [ -z "$HOST" ] || [ -z "$PORT" ]; then
    echo "Usage: $0 <job_id> <host> <port> <model_file> [email]"
    exit 1
fi

MODEL_NAME=$(basename "$MODEL_FILE" 2>/dev/null || echo "unknown")
START_TIME=$(date)
REGISTRY_URL="${REGISTRY_URL:-}"

# Create email body
EMAIL_SUBJECT="Llama Server Ready - Job $JOB_ID"

EMAIL_BODY=$(cat <<EOF
Your llama.cpp server is now running and ready to use!

========================================
Server Details
========================================
Job ID:      $JOB_ID
Model:       $MODEL_NAME
Host:        $HOST
Port:        $PORT
Started:     $START_TIME

========================================
How to Connect
========================================

Option 1: Quick Connect (if on shared filesystem)
--------------------------------------------------
cd $(pwd)
./connect_claude.sh $JOB_ID

Option 2: Manual Connection
--------------------------------------------------
source $CONNECTION_FILE
source setup_claude_env.sh
claude

Option 3: From Outside Cluster (SSH Tunnel)
--------------------------------------------------
# Create tunnel from your local machine:
ssh -L $PORT:$HOST:$PORT <your-username>@<login-node>

# Then in another terminal:
export ANTHROPIC_BASE_URL="http://localhost:$PORT"
claude

EOF
)

# Add registry info if available
if [ -n "$REGISTRY_URL" ]; then
EMAIL_BODY+=$(cat <<EOF

Option 4: Web Dashboard
--------------------------------------------------
View all servers at: ${REGISTRY_URL}/

EOF
)
fi

# Add monitoring info
EMAIL_BODY+=$(cat <<EOF

========================================
Monitoring & Management
========================================
Check job status:
  squeue -j $JOB_ID

View logs:
  tail -f llama_server_${JOB_ID}.log

Cancel job:
  scancel $JOB_ID

========================================
Connection file: $CONNECTION_FILE
Working directory: $(pwd)
========================================

This is an automated notification from the llama.cpp SLURM integration.
EOF
)

# Send email using available method
if command -v mail &> /dev/null; then
    echo "$EMAIL_BODY" | mail -s "$EMAIL_SUBJECT" "$EMAIL"
    echo "Email notification sent to: $EMAIL"
elif command -v sendmail &> /dev/null; then
    (
        echo "To: $EMAIL"
        echo "Subject: $EMAIL_SUBJECT"
        echo "From: slurm-notify@$(hostname -f)"
        echo ""
        echo "$EMAIL_BODY"
    ) | sendmail -t
    echo "Email notification sent to: $EMAIL"
elif command -v mutt &> /dev/null; then
    echo "$EMAIL_BODY" | mutt -s "$EMAIL_SUBJECT" "$EMAIL"
    echo "Email notification sent to: $EMAIL"
else
    echo "Warning: No email client found (mail, sendmail, or mutt)"
    echo "Email would have been sent to: $EMAIL"
    echo ""
    echo "Email content:"
    echo "=============="
    echo "$EMAIL_BODY"
    return 1
fi

return 0
