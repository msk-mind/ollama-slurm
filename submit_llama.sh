#!/bin/bash
# Submit llama.cpp server to SLURM for Claude CLI connection

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_DIR="${SCRIPT_DIR}/model_configs"

# Default parameters
TIME_LIMIT="4:00:00"
MODEL_FILE=""
MODEL_CONFIG=""
PARTITION=""
QOS=""
CPUS=8
MEM="32G"
GPUS=1
NO_TIME_LIMIT=false
CONTEXT_SIZE=131072
N_GPU_LAYERS=-1
EXTRA_ARGS=""

# Parse arguments
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
            MODEL_FILE="$2"
            shift 2
            ;;
        --config)
            MODEL_CONFIG="$2"
            shift 2
            ;;
        --partition|-p)
            PARTITION="$2"
            shift 2
            ;;
        --qos)
            QOS="$2"
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
        --context|-c)
            CONTEXT_SIZE="$2"
            shift 2
            ;;
        --gpu-layers)
            N_GPU_LAYERS="$2"
            shift 2
            ;;
        --extra-args)
            EXTRA_ARGS="$2"
            shift 2
            ;;
        --list-configs)
            echo "Available model configurations:"
            echo ""
            if [ -d "$CONFIG_DIR" ]; then
                for config in "$CONFIG_DIR"/*.conf; do
                    [ -f "$config" ] || continue
                    basename "$config" .conf
                done
            else
                echo "No configurations found in $CONFIG_DIR"
            fi
            exit 0
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Submit llama.cpp server to SLURM for Claude CLI connection"
            echo ""
            echo "Options:"
            echo "  --model FILE        Path to GGUF model file (required if no --config)"
            echo "  --config NAME       Use predefined model config"
            echo "  --list-configs      List available model configurations"
            echo "  --time TIME         Time limit (default: 4:00:00)"
            echo "  --no-time-limit     Remove time limit"
            echo "  --partition PART    SLURM partition (default: none)"
            echo "  --qos QOS           SLURM QOS (default: none)"
            echo "  --cpus N            Number of CPUs (default: 8)"
            echo "  --mem SIZE          Memory allocation (default: 32G)"
            echo "  --gpus N            Number of GPUs (default: 1)"
            echo "  --context N         Context size (default: 131072)"
            echo "  --gpu-layers N      GPU layers, -1 for all (default: -1)"
            echo "  --extra-args STR    Additional llama-server arguments"
            echo "  --help, -h          Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0 --config qwen3-30b                # Use saved config"
            echo "  $0 --model ~/.cache/llama.cpp/model.gguf"
            echo "  $0 --model model.gguf --gpus 2 --context 16384"
            echo "  $0 --config glm-4.7 --no-time-limit"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Load configuration if specified
if [ -n "$MODEL_CONFIG" ]; then
    CONFIG_FILE="${CONFIG_DIR}/${MODEL_CONFIG}.conf"
    if [ ! -f "$CONFIG_FILE" ]; then
        echo "Error: Configuration not found: $CONFIG_FILE"
        echo ""
        echo "Available configurations:"
        ls -1 "$CONFIG_DIR"/*.conf 2>/dev/null | xargs -n1 basename -s .conf || echo "None"
        exit 1
    fi
    
    echo "Loading configuration: $MODEL_CONFIG"
    source "$CONFIG_FILE"
fi

# Validate model file
if [ -z "$MODEL_FILE" ]; then
    echo "Error: No model specified. Use --model or --config"
    echo "Use --list-configs to see available configurations"
    exit 1
fi

# Expand ~ in model path
MODEL_FILE="${MODEL_FILE/#\~/$HOME}"

if [ ! -f "$MODEL_FILE" ]; then
    echo "Error: Model file not found: $MODEL_FILE"
    exit 1
fi

# Build SBATCH options
SBATCH_OPTS=""
if [ "$NO_TIME_LIMIT" = true ]; then
    echo "Submitting llama.cpp server with no time limit"
else
    SBATCH_OPTS="--time=${TIME_LIMIT}"
    echo "Submitting llama.cpp server with time limit: $TIME_LIMIT"
fi

# Add partition if specified
if [ -n "$PARTITION" ]; then
    SBATCH_OPTS="${SBATCH_OPTS} --partition=${PARTITION}"
fi

# Add QOS if specified
if [ -n "$QOS" ]; then
    SBATCH_OPTS="${SBATCH_OPTS} --qos=${QOS}"
fi

# Submit the job
JOB_ID=$(sbatch ${SBATCH_OPTS} \
    --cpus-per-task="${CPUS}" \
    --mem="${MEM}" \
    --gres="gpu:${GPUS}" \
    --output="llama_server_%j.log" \
    --error="llama_server_%j.err" \
    --job-name="llama-server" \
    --export=ALL,MODEL_FILE="${MODEL_FILE}",CONTEXT_SIZE="${CONTEXT_SIZE}",N_GPU_LAYERS="${N_GPU_LAYERS}",EXTRA_ARGS="${EXTRA_ARGS}" \
    "${SCRIPT_DIR}/llama_server.slurm" | awk '{print $4}')

echo "Job submitted: $JOB_ID"
echo "Model: $(basename $MODEL_FILE)"
echo "Model path: $MODEL_FILE"
[ -n "$PARTITION" ] && echo "Partition: $PARTITION"
[ -n "$QOS" ] && echo "QOS: $QOS"
echo "CPUs: $CPUS"
echo "Memory: $MEM"
echo "GPUs: $GPUS"
echo "Context size: $CONTEXT_SIZE"
echo "GPU layers: $N_GPU_LAYERS"
echo ""
echo "Monitor job status:"
echo "  squeue -j $JOB_ID"
echo ""
echo "View logs:"
echo "  tail -f llama_server_${JOB_ID}.log"
echo ""
echo "Once running, connect with:"
echo "  ./connect_claude.sh $JOB_ID"
echo ""
echo "Cancel job:"
echo "  scancel $JOB_ID"
