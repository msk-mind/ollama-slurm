#!/bin/bash
#SBATCH --job-name=ollama-server
#SBATCH --output=ollama_server_%j.log
#SBATCH --error=ollama_server_%j.err

set -e

echo "=========================================="
echo "Ollama Server SLURM Job"
echo "=========================================="
echo "Job ID: $SLURM_JOB_ID"
echo "Node: $SLURM_NODELIST"
echo "Start time: $(date)"
echo "Model: ${OLLAMA_MODEL:-llama3.2}"
echo "GPUs: $SLURM_GPUS_ON_NODE"
echo "=========================================="
echo ""

# Load required modules (adjust for your cluster)
# module load ollama  # Uncomment if ollama is a module
# module load cuda    # Uncomment if CUDA is a module

# Find available port
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
HOST=$(hostname)

echo "Starting Ollama server on ${HOST}:${PORT}"
echo ""

# Create connection file for Claude CLI
CONNECTION_FILE="${SLURM_SUBMIT_DIR}/ollama_server_connection_${SLURM_JOB_ID}.txt"
cat > "$CONNECTION_FILE" <<EOF
# Connection information for Ollama server job ${SLURM_JOB_ID}
export OLLAMA_SERVER_HOST="${HOST}"
export OLLAMA_SERVER_PORT="${PORT}"
export OLLAMA_JOB_ID="${SLURM_JOB_ID}"
export OLLAMA_MODEL="${OLLAMA_MODEL:-llama3.2}"
EOF

echo "Connection file created: $CONNECTION_FILE"
echo ""

# Set Ollama environment variables
export OLLAMA_HOST="${HOST}:${PORT}"
export OLLAMA_ORIGINS="*"
export OLLAMA_NUM_PARALLEL=1

# Show GPU information
if command -v nvidia-smi &> /dev/null; then
    echo "GPU Information:"
    nvidia-smi --query-gpu=index,name,memory.total --format=csv,noheader
    echo ""
fi

# Start Ollama server
echo "Launching Ollama serve..."
ollama serve &
OLLAMA_PID=$!

# Wait for server to be ready
echo "Waiting for Ollama server to start..."
sleep 5

MAX_WAIT=60
COUNTER=0
while ! curl -s "http://localhost:${PORT}/" >/dev/null 2>&1; do
    if [ $COUNTER -ge $MAX_WAIT ]; then
        echo "Error: Ollama server failed to start within ${MAX_WAIT} seconds"
        kill $OLLAMA_PID 2>/dev/null || true
        exit 1
    fi
    sleep 1
    COUNTER=$((COUNTER + 1))
done

echo "Ollama server is ready!"
echo ""

# Pull the model if specified
MODEL="${OLLAMA_MODEL:-llama3.2}"
echo "Ensuring model is available: $MODEL"
ollama pull "$MODEL"
echo ""

echo "=========================================="
echo "Ollama server is running and ready!"
echo "Host: $HOST"
echo "Port: $PORT"
echo "Model: $MODEL"
echo "Job ID: $SLURM_JOB_ID"
echo "=========================================="
echo ""
echo "Connect using:"
echo "  ./connect_claude.sh $SLURM_JOB_ID"
echo ""
echo "Or manually:"
echo "  source $CONNECTION_FILE"
echo "  source setup_claude_env.sh"
echo "  claude"
echo ""

# Keep the job running
wait $OLLAMA_PID
EXIT_CODE=$?

echo ""
echo "=========================================="
echo "Ollama server stopped"
echo "End time: $(date)"
echo "Exit code: $EXIT_CODE"
echo "=========================================="

exit $EXIT_CODE
