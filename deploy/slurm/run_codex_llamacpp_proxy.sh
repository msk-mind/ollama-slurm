#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
source "${SCRIPT_DIR}/slurm_job_helpers.sh"

JOB_ID=""
LISTEN_HOST="127.0.0.1"
LISTEN_PORT="1234"
DUMP_DIR=""
MODEL_ALIASES=()

usage() {
  cat <<EOF
Usage: $0 --job-id <slurm_job_id> [OPTIONS]

Start the supported Codex-to-llama.cpp compatibility proxy for a running Slurm job.

Options:
  --job-id ID          Required Slurm job ID for an existing llama.cpp server job
  --listen-host HOST   Local listen host (default: 127.0.0.1)
  --listen-port PORT   Local listen port (default: 1234)
  --model-alias NAME   Extra model alias to publish in the rewritten catalog (repeatable)
  --dump-dir PATH      Optional dump directory for request/response debugging
  --help, -h           Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --job-id)
      JOB_ID="$2"
      shift 2
      ;;
    --listen-host)
      LISTEN_HOST="$2"
      shift 2
      ;;
    --listen-port)
      LISTEN_PORT="$2"
      shift 2
      ;;
    --model-alias)
      MODEL_ALIASES+=("$2")
      shift 2
      ;;
    --dump-dir)
      DUMP_DIR="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "$JOB_ID" ]]; then
  echo "--job-id is required" >&2
  usage >&2
  exit 1
fi

CONNECTION_FILE="$(connection_file_path "${REPO_ROOT}" "${JOB_ID}")"
if [[ ! -f "$CONNECTION_FILE" ]]; then
  echo "Connection file not found: $CONNECTION_FILE" >&2
  exit 1
fi

source "$CONNECTION_FILE"

if [[ -z "${LLAMA_SERVER_HOST:-}" || -z "${LLAMA_SERVER_PORT:-}" ]]; then
  echo "Connection file is missing LLAMA_SERVER_HOST or LLAMA_SERVER_PORT" >&2
  exit 1
fi

ARGS=(
  "--listen-host" "$LISTEN_HOST"
  "--listen-port" "$LISTEN_PORT"
  "--upstream" "http://${LLAMA_SERVER_HOST}:${LLAMA_SERVER_PORT}"
)

if [[ -n "$DUMP_DIR" ]]; then
  ARGS+=("--dump-dir" "$DUMP_DIR")
fi

if [[ -n "${MODEL_FILE:-}" ]]; then
  MODEL_BASENAME="$(basename "$MODEL_FILE")"
  MODEL_STEM="${MODEL_BASENAME%.gguf}"
  ARGS+=("--model-alias" "$MODEL_BASENAME")
  if [[ "$MODEL_STEM" != "$MODEL_BASENAME" ]]; then
    ARGS+=("--model-alias" "$MODEL_STEM")
  fi
fi

for alias in "${MODEL_ALIASES[@]}"; do
  ARGS+=("--model-alias" "$alias")
done

exec python3 "${SCRIPT_DIR}/codex_llamacpp_proxy.py" "${ARGS[@]}"
