#!/bin/bash
# Submit Ollama server to SLURM for Claude CLI connection

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

# Default parameters
TIME_LIMIT="8:00:00"
MODEL="llama3.2"
PARTITION=""
CPUS=8
MEM="32G"
GPUS=1
GPU_TYPE=""
NO_TIME_LIMIT=false
PRINT_JOB_ID_ONLY=false

while [[ $# -gt 0 ]]; do
  case $1 in
    --time)
      TIME_LIMIT="$2"
      shift 2
      ;;
    --no-time-limit)
      NO_TIME_LIMIT=true
      shift
      ;;
    --model)
      MODEL="$2"
      shift 2
      ;;
    --partition|-p)
      PARTITION="$2"
      shift 2
      ;;
    --cpus)
      CPUS="$2"
      shift 2
      ;;
    --mem)
      MEM="$2"
      shift 2
      ;;
    --gpus)
      GPUS="$2"
      shift 2
      ;;
    --gpu-type)
      GPU_TYPE="$2"
      shift 2
      ;;
    --help|-h)
      echo "Usage: $0 [OPTIONS]"
      echo ""
      echo "Submit Ollama server to SLURM for Claude CLI connection"
      echo ""
      echo "Options:"
      echo "  --time TIME         Time limit (default: 8:00:00)"
      echo "  --no-time-limit     Remove time limit"
      echo "  --model MODEL       Ollama model to use (default: llama3.2)"
      echo "  --partition PART    SLURM partition (default: none)"
      echo "  --cpus N            Number of CPUs (default: 8)"
      echo "  --mem SIZE          Memory allocation (default: 32G)"
      echo "  --gpus N            Number of GPUs (default: 1)"
      echo "  --gpu-type TYPE     GPU type: a100, v100, p40 (default: none)"
      echo "  --print-job-id      Print only the submitted Slurm job ID"
      echo "  --help, -h          Show this help message"
      echo ""
      echo "Examples:"
      echo "  $0                                  # Default 4 hour limit, 1 GPU"
      echo "  $0 --no-time-limit                  # No time limit"
      echo "  $0 --time 8:00:00 --gpus 2          # 8 hour limit, 2 GPUs"
      echo "  $0 --model llama3.1 --cpus 16       # Custom model and CPUs"
      exit 0
      ;;
    --print-job-id)
      PRINT_JOB_ID_ONLY=true
      shift
      ;;
    *)
      echo "Unknown option: $1"
      echo "Use --help for usage information"
      exit 1
      ;;
  esac
done

SBATCH_OPTS=""
if [ "$NO_TIME_LIMIT" = true ]; then
  if [ "$PRINT_JOB_ID_ONLY" != true ]; then
    echo "Submitting Ollama server with no time limit"
  fi
else
  SBATCH_OPTS="--time=${TIME_LIMIT}"
  if [ "$PRINT_JOB_ID_ONLY" != true ]; then
    echo "Submitting Ollama server with time limit: $TIME_LIMIT"
  fi
fi

if [ -n "$PARTITION" ]; then
  SBATCH_OPTS="${SBATCH_OPTS} --partition=${PARTITION}"
fi

GPU_GRES="gpu:${GPUS}"
if [ -n "$GPU_TYPE" ]; then
  GPU_GRES="gpu:${GPU_TYPE}:${GPUS}"
fi

JOB_ID=$(sbatch ${SBATCH_OPTS} \
  --cpus-per-task="${CPUS}" \
  --mem="${MEM}" \
  --gres="${GPU_GRES}" \
  --output="${REPO_ROOT}/ollama_server_%j.log" \
  --error="${REPO_ROOT}/ollama_server_%j.err" \
  --job-name="ollama-server" \
  --export=ALL,OLLAMA_MODEL="${MODEL}",SCRIPT_DIR="${REPO_ROOT}" \
  "${SCRIPT_DIR}/ollama_server.slurm" | awk '{print $4}')

if [ "$PRINT_JOB_ID_ONLY" = true ]; then
  echo "$JOB_ID"
  exit 0
fi

echo "Job submitted: $JOB_ID"
echo "Model: $MODEL"
[ -n "$PARTITION" ] && echo "Partition: $PARTITION"
echo "CPUs: $CPUS"
echo "Memory: $MEM"
echo "GPUs: $GPUS"
[ -n "$GPU_TYPE" ] && echo "GPU type: $GPU_TYPE"
echo ""
echo "Monitor job status:"
echo "  squeue -j $JOB_ID"
echo ""
echo "View logs:"
echo "  tail -f ${REPO_ROOT}/ollama_server_${JOB_ID}.log"
echo ""
echo "Once running, connect with:"
echo "  ./connect_claude.sh $JOB_ID"
echo ""
echo "Cancel job:"
echo "  scancel $JOB_ID"
