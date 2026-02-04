#!/bin/bash
# Wrapper script to connect Claude CLI to a running llama.cpp server job

# Get script directory before any directory changes
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

JOB_ID="${1:-}"
WORK_DIR="${2:-.}"

# Shift past job_id and work_dir to get remaining args for Claude
shift
shift 2>/dev/null || true

if [ -z "$JOB_ID" ]; then
  echo "Usage: $0 <slurm_job_id> [working_directory]"
  echo ""
  echo "This script connects Claude CLI to a running llama.cpp server"
  echo ""
  echo "Arguments:"
  echo "  slurm_job_id       - SLURM job ID of the running server"
  echo "  working_directory  - Directory where Claude should run (default: current directory)"
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

# Set up Claude environment
source "${SCRIPT_DIR}/setup_claude_env.sh"

# Launch Claude CLI
echo "Launching Claude CLI..."
echo "Server: ${LLAMA_SERVER_HOST}:${LLAMA_SERVER_PORT}"
echo "This may take 10-30 seconds to initialize..."
echo "Press Ctrl+D or type 'exit' to quit"
echo ""

exec claude "$@"
