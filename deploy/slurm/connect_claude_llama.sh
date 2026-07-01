#!/bin/bash
# Wrapper script to connect Claude CLI to a running llama.cpp server job

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
source "${SCRIPT_DIR}/slurm_job_helpers.sh"

JOB_ID="${1:-}"
WORK_DIR="${2:-.}"

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
  print_active_llama_jobs || true
  exit 1
fi

CONNECTION_FILE="$(connection_file_path "${REPO_ROOT}" "${JOB_ID}")"

if [ ! -f "$CONNECTION_FILE" ] && slurm_controller_available && ! slurm_job_exists "$JOB_ID"; then
  echo "Error: Job $JOB_ID not found"
  echo ""
  echo "Active jobs:"
  print_active_jobs || true
  exit 1
fi

if [ ! -f "$CONNECTION_FILE" ]; then
  echo "Error: Connection file not found: $CONNECTION_FILE"
  echo ""
  if ! slurm_controller_available; then
    echo "Slurm controller is not reachable from this environment, so job status could not be verified."
    echo ""
  fi
  echo "The server may still be starting. Check log:"
  echo "  tail -f ${REPO_ROOT}/llama_server_${JOB_ID}.log"
  exit 1
fi

source "$CONNECTION_FILE"

if [ ! -d "$WORK_DIR" ]; then
  echo "Error: Working directory does not exist: $WORK_DIR"
  exit 1
fi

cd "$WORK_DIR"
echo "Working directory: $(pwd)"
echo ""

source "${REPO_ROOT}/setup_claude_env.sh"

echo "Launching Claude CLI..."
echo "Server: ${LLAMA_SERVER_HOST}:${LLAMA_SERVER_PORT}"
echo "This may take 10-30 seconds to initialize..."
echo "Press Ctrl+D or type 'exit' to quit"
echo ""

exec claude "$@"
