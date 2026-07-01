#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
PROXY_SCRIPT="${SCRIPT_DIR}/run_codex_llamacpp_proxy.sh"
SUBMIT_SCRIPT="${SCRIPT_DIR}/submit_llama.sh"
source "${SCRIPT_DIR}/slurm_job_helpers.sh"

MODEL_CONFIG=""
WORK_DIR="."
TIME_LIMIT="1:00:00"
PARTITION=""
QOS=""
LISTEN_HOST="127.0.0.1"
LISTEN_PORT="1234"
MODEL_ALIAS=""
DUMP_DIR=""
KEEP_JOB=false
CODEX_MODEL=""
PROMPT=""
EXTRA_CODEX_ARGS=()
JOB_ID=""
PROXY_PID=""

usage() {
  cat <<EOF
Usage: $0 --config <model_config> --prompt <prompt> [OPTIONS] [-- <extra codex args>]

Submit a llama.cpp Slurm job, wait for readiness, start the Codex compatibility proxy,
run Codex against the local endpoint, and clean up automatically.

Required:
  --config NAME        Saved llama.cpp config name, for example gpt-oss-20b.p40
  --prompt TEXT        Prompt to send to Codex

Options:
  --work-dir DIR       Working directory for Codex (default: current directory)
  --time TIME          Slurm time limit (default: 1:00:00)
  --partition PART     Slurm partition
  --qos QOS            Slurm QoS
  --listen-host HOST   Local proxy listen host (default: 127.0.0.1)
  --listen-port PORT   Local proxy listen port (default: 1234)
  --model-alias NAME   Model alias exposed to Codex; default derives from config basename
  --codex-model NAME   Codex model name to request; default is the derived alias
  --dump-dir PATH      Optional proxy dump directory for protocol debugging
  --keep-job           Leave the Slurm job running after Codex exits
  --help, -h           Show this help

Anything after \`--\` is passed through to \`codex exec\`.
EOF
}

cleanup() {
  local exit_code="$?"
  if [[ -n "${PROXY_PID}" ]] && kill -0 "${PROXY_PID}" 2>/dev/null; then
    kill "${PROXY_PID}" 2>/dev/null || true
    wait "${PROXY_PID}" 2>/dev/null || true
  fi

  if [[ "${KEEP_JOB}" != true ]] && [[ -n "${JOB_ID}" ]]; then
    scancel "${JOB_ID}" >/dev/null 2>&1 || true
  fi

  exit "${exit_code}"
}
trap cleanup EXIT INT TERM

while [[ $# -gt 0 ]]; do
  case "$1" in
    --config)
      MODEL_CONFIG="$2"
      shift 2
      ;;
    --prompt)
      PROMPT="$2"
      shift 2
      ;;
    --work-dir)
      WORK_DIR="$2"
      shift 2
      ;;
    --time)
      TIME_LIMIT="$2"
      shift 2
      ;;
    --partition)
      PARTITION="$2"
      shift 2
      ;;
    --qos)
      QOS="$2"
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
      MODEL_ALIAS="$2"
      shift 2
      ;;
    --codex-model)
      CODEX_MODEL="$2"
      shift 2
      ;;
    --dump-dir)
      DUMP_DIR="$2"
      shift 2
      ;;
    --keep-job)
      KEEP_JOB=true
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    --)
      shift
      EXTRA_CODEX_ARGS=("$@")
      break
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

if [[ -z "${MODEL_CONFIG}" || -z "${PROMPT}" ]]; then
  echo "--config and --prompt are required" >&2
  usage >&2
  exit 1
fi

if [[ ! -d "${WORK_DIR}" ]]; then
  echo "Working directory does not exist: ${WORK_DIR}" >&2
  exit 1
fi

if [[ -z "${MODEL_ALIAS}" ]]; then
  MODEL_ALIAS="${MODEL_CONFIG}"
fi

if [[ -z "${CODEX_MODEL}" ]]; then
  CODEX_MODEL="${MODEL_ALIAS}"
fi

SUBMIT_ARGS=(
  --config "${MODEL_CONFIG}"
  --time "${TIME_LIMIT}"
)

if [[ -n "${PARTITION}" ]]; then
  SUBMIT_ARGS+=(--partition "${PARTITION}")
fi

if [[ -n "${QOS}" ]]; then
  SUBMIT_ARGS+=(--qos "${QOS}")
fi

echo "Submitting llama.cpp Slurm job with config: ${MODEL_CONFIG}"
JOB_ID="$("${SUBMIT_SCRIPT}" "${SUBMIT_ARGS[@]}" --print-job-id)"
if [[ -z "${JOB_ID}" ]]; then
  echo "Failed to determine Slurm job ID from submit script" >&2
  exit 1
fi
echo "Job submitted: ${JOB_ID}"

echo "Waiting for connection file for job ${JOB_ID}"
if ! CONNECTION_FILE="$(wait_for_connection_file "${REPO_ROOT}" "${JOB_ID}" 240)"; then
  echo "Timed out waiting for connection file: ${CONNECTION_FILE}" >&2
  exit 1
fi

echo "Starting Codex compatibility proxy"
PROXY_ARGS=(
  --job-id "${JOB_ID}"
  --listen-host "${LISTEN_HOST}"
  --listen-port "${LISTEN_PORT}"
  --model-alias "${MODEL_ALIAS}"
)

if [[ -n "${DUMP_DIR}" ]]; then
  PROXY_ARGS+=(--dump-dir "${DUMP_DIR}")
fi

"${PROXY_SCRIPT}" "${PROXY_ARGS[@]}" &
PROXY_PID="$!"

echo "Waiting for proxy health on http://${LISTEN_HOST}:${LISTEN_PORT}/health"
for _ in $(seq 1 60); do
  if curl -sf "http://${LISTEN_HOST}:${LISTEN_PORT}/health" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! curl -sf "http://${LISTEN_HOST}:${LISTEN_PORT}/health" >/dev/null 2>&1; then
  echo "Proxy did not become healthy" >&2
  exit 1
fi

echo "Running Codex against local proxy"
OPENAI_API_KEY="${OPENAI_API_KEY:-dummy}" \
codex --ask-for-approval never --sandbox read-only exec \
  -C "${WORK_DIR}" \
  -m "${CODEX_MODEL}" \
  -c 'model_provider="openai-custom"' \
  -c 'model_providers.openai-custom.name="OpenAI Custom"' \
  -c "model_providers.openai-custom.base_url=\"http://${LISTEN_HOST}:${LISTEN_PORT}/v1\"" \
  -c 'model_providers.openai-custom.wire_api="responses"' \
  -c 'model_providers.openai-custom.env_key="OPENAI_API_KEY"' \
  "${EXTRA_CODEX_ARGS[@]}" \
  "${PROMPT}"
