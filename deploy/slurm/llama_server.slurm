#!/bin/bash
#SBATCH --job-name=llama-server
#SBATCH --output=llama_server_%j.log
#SBATCH --error=llama_server_%j.err

set -e

echo "=========================================="
echo "llama.cpp Server SLURM Job"
echo "=========================================="
echo "Job ID: $SLURM_JOB_ID"
echo "Node: $SLURM_NODELIST"
echo "Start time: $(date)"
echo "Model: $(basename "$MODEL_FILE")"
echo "Model path: $MODEL_FILE"
echo "GPUs: $SLURM_GPUS_ON_NODE"
echo "Context size: $CONTEXT_SIZE"
echo "GPU layers: $N_GPU_LAYERS"
echo "=========================================="
echo ""

echo "Detecting CUDA toolkit..."

CUDA_FOUND=""
if [ -n "$CUDA_HOME" ] && [ -d "$CUDA_HOME" ] && [ -f "$CUDA_HOME/bin/nvcc" ]; then
  CUDA_FOUND="$CUDA_HOME"
fi

if [ -z "$CUDA_FOUND" ]; then
  CUDA_PATHS=("/usr/local/cuda")
  for minor in {9..0}; do
    CUDA_PATHS+=("/usr/local/cuda-13.$minor")
  done
  for minor in {9..0}; do
    CUDA_PATHS+=("/usr/local/cuda-12.$minor")
  done
  for minor in {8..0}; do
    CUDA_PATHS+=("/usr/local/cuda-11.$minor")
  done
  CUDA_PATHS+=("/opt/cuda")

  for cuda_path in "${CUDA_PATHS[@]}"; do
    if [ -d "$cuda_path" ] && [ -f "$cuda_path/bin/nvcc" ]; then
      CUDA_FOUND="$cuda_path"
      break
    fi
  done
fi

if [ -n "$CUDA_FOUND" ]; then
  echo "Found CUDA toolkit at: $CUDA_FOUND"
  export CUDA_HOME="$CUDA_FOUND"
  export PATH="$CUDA_FOUND/bin:$PATH"
  export LD_LIBRARY_PATH="$CUDA_FOUND/lib64:$CUDA_FOUND/lib:${LD_LIBRARY_PATH:-}"
  CUDA_VERSION=$("$CUDA_FOUND/bin/nvcc" --version | grep "release" | awk '{print $5}' | tr -d ',')
  echo "CUDA version: $CUDA_VERSION"
else
  echo "Warning: CUDA toolkit not found in standard locations"
  echo "GPU acceleration may not be available"
fi
echo ""

PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("", 0)); print(s.getsockname()[1]); s.close()')
HOST=$(hostname)

echo "Starting llama.cpp server on ${HOST}:${PORT}"
echo ""

CONNECTION_FILE="${SCRIPT_DIR}/llama_server_connection_${SLURM_JOB_ID}.txt"
cat >"$CONNECTION_FILE" <<EOF
# Connection information for llama.cpp server job ${SLURM_JOB_ID}
export LLAMA_SERVER_HOST="${HOST}"
export LLAMA_SERVER_PORT="${PORT}"
export LLAMA_JOB_ID="${SLURM_JOB_ID}"
export MODEL_FILE="${MODEL_FILE}"
EOF

echo "Connection file created: $CONNECTION_FILE"
echo ""

if command -v nvidia-smi &>/dev/null; then
  echo "GPU Information:"
  nvidia-smi --query-gpu=index,name,memory.total --format=csv,noheader
  echo ""
fi

if [ ! -f "$MODEL_FILE" ]; then
  echo "Error: Model file not found: $MODEL_FILE"
  exit 1
fi

LLAMA_ARGS=(
  --model "$MODEL_FILE"
  --host "$HOST"
  --port "$PORT"
  -c "$CONTEXT_SIZE"
  -b 32768
  -ub 1024
  --parallel 1
  --jinja
  --n-gpu-layers "$N_GPU_LAYERS"
)

if [ -n "$EXTRA_ARGS" ]; then
  eval "LLAMA_ARGS+=($EXTRA_ARGS)"
fi

echo "Launching llama-server with settings:"
echo "  Context: $CONTEXT_SIZE"
echo "  Batch: 32768"
echo "  Ubatch: 1024"
echo "  Parallel: 1"
echo "  GPU layers: $N_GPU_LAYERS"
[ -n "$EXTRA_ARGS" ] && echo "  Extra args: $EXTRA_ARGS"
echo ""

llama-server "${LLAMA_ARGS[@]}" &
SERVER_PID=$!

echo "Waiting for llama.cpp server to start..."
sleep 10

MAX_WAIT=120
COUNTER=0
while ! curl -s "http://${HOST}:${PORT}/health" >/dev/null 2>&1; do
  if [ $COUNTER -ge $MAX_WAIT ]; then
    echo "Error: llama.cpp server failed to start within ${MAX_WAIT} seconds"
    echo "Check if server is running but health endpoint is not responding"
    ps aux | grep llama-server | grep -v grep
    kill $SERVER_PID 2>/dev/null || true
    exit 1
  fi
  sleep 2
  COUNTER=$((COUNTER + 2))
done

echo "llama.cpp server is ready!"
echo ""

if [ -n "$REGISTRY_URL" ]; then
  echo "Registering with server registry..."
  "${SCRIPT_DIR}/register_server.sh" "$SLURM_JOB_ID" "$HOST" "$PORT" "$MODEL_FILE" || true
  echo ""
fi

echo "Sending email notification..."
"${SCRIPT_DIR}/send_notification.sh" "$SLURM_JOB_ID" "$HOST" "$PORT" "$MODEL_FILE" "$NOTIFY_EMAIL" || true
echo ""

if [ -n "$NTFY_TOPIC" ] || [ -n "$NTFY_SERVER" ]; then
  echo "Sending ntfy push notification..."
  "${SCRIPT_DIR}/send_ntfy_notification.sh" "$SLURM_JOB_ID" "$HOST" "$PORT" "$MODEL_FILE" "$NTFY_TOPIC" || true
  echo ""
fi

echo "=========================================="
echo "llama.cpp server is running and ready!"
echo "Host: $HOST"
echo "Port: $PORT"
echo "Model: $(basename "$MODEL_FILE")"
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
if [ -n "$REGISTRY_URL" ]; then
  echo "Also discoverable at:"
  echo "  ${REGISTRY_URL}/servers"
  echo ""
fi

wait $SERVER_PID
EXIT_CODE=$?

if [ -n "$REGISTRY_URL" ]; then
  echo "Unregistering from server registry..."
  curl -X DELETE "${REGISTRY_URL}/servers/${SLURM_JOB_ID}" --silent --show-error || true
fi

echo ""
echo "=========================================="
echo "llama.cpp server stopped"
echo "End time: $(date)"
echo "Exit code: $EXIT_CODE"
echo "=========================================="

exit $EXIT_CODE
