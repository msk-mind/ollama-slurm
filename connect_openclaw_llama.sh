#!/bin/bash
# Wrapper script to connect OpenClaw CLI to a running llama.cpp server job

# Get script directory before any directory changes
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

JOB_ID="${1:-}"
WORK_DIR="${2:-.}"

# Shift past job_id and work_dir to get remaining args for openclaw
shift
shift 2>/dev/null || true

if [ -z "$JOB_ID" ]; then
  echo "Usage: $0 <slurm_job_id> [working_directory]"
  echo ""
  echo "This script connects OpenClaw CLI to a running llama.cpp server"
  echo ""
  echo "Arguments:"
  echo "  slurm_job_id       - SLURM job ID of the running server"
  echo "  working_directory  - Directory where OpenClaw should run (default: current directory)"
  echo ""
  echo "Active llama server jobs:"
  squeue -u "$USER" -n llama-server -o "%.10i %.9P %.20j %.8T %.10M %.6D %R"
  exit 1
fi

CONNECTION_FILE="${SCRIPT_DIR}/llama_server_connection_${JOB_ID}.txt"

# Check if job exists
if ! squeue -j "$JOB_ID" &>/dev/null; then
  echo "Error: Job $JOB_ID not found"
  echo ""
  echo "Active jobs:"
  squeue -u "$USER"
  exit 1
fi

# Check if connection file exists
if [ ! -f "$CONNECTION_FILE" ]; then
  echo "Error: Connection file not found: $CONNECTION_FILE"
  echo ""
  echo "The server may still be starting. Check log:"
  echo "  tail -f llama_server_${JOB_ID}.log"
  exit 1
fi

# Source connection info
source "$CONNECTION_FILE"

# Validate and change to working directory
if [ ! -d "$WORK_DIR" ]; then
  echo "Error: Working directory does not exist: $WORK_DIR"
  exit 1
fi

cd "$WORK_DIR"
echo "Working directory: $(pwd)"
echo ""

# Set up OpenClaw environment
source "${SCRIPT_DIR}/setup_openclaw_env.sh"

# Launch OpenClaw CLI
echo "Launching OpenClaw CLI..."
echo "Server: ${LLAMA_SERVER_HOST}:${LLAMA_SERVER_PORT}"
echo "Press Ctrl+D or type 'exit' to quit"
echo ""

exec openclaw "$@"
