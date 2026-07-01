#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BASE_DIR="$(mktemp -d /tmp/ollama-slurm-smoke.XXXXXX)"
source "${SCRIPT_DIR}/smoke_lib.sh"

cleanup() {
  kill_pid_if_running "${BROKER_PID:-}"
}
trap cleanup EXIT

eval "$("${SCRIPT_DIR}/fake_slurm_env.sh" "${BASE_DIR}")"

export BROKER_LISTEN_ADDR="127.0.0.1:18081"
export BROKER_JOB_STORE_PATH="${BASE_DIR}/jobs.json"
export BROKER_RUN_ROOT_PATH="${BASE_DIR}/runs"
export BROKER_REPO_ROOT_PATH="${REPO_ROOT}"
export BROKER_SLURM_MODE="command"
export BROKER_SLURM_SUBMIT_CMD="sbatch"
export BROKER_SLURM_STATUS_CMD="sacct"
export BROKER_SLURM_CANCEL_CMD="scancel"
export BROKER_SLURM_SCRIPT_PATH="${REPO_ROOT}/deploy/slurm/broker_worker.slurm"

INPUT_FILE="${BASE_DIR}/source.txt"
cat > "${INPUT_FILE}" <<'EOF'
Smoke demo document.
- alpha
- beta
EOF

start_broker_server "${REPO_ROOT}"

SUBMIT_PAYLOAD="$(cat <<EOF
{
  "task_type": "document_summary",
  "input_refs": [
    {
      "type": "file",
      "uri": "file://${INPUT_FILE}",
      "classification": "internal"
    }
  ],
  "output_schema": {
    "name": "document_summary_v1"
  }
}
EOF
)"

SUBMIT_RESPONSE="$(env -u GOROOT GOCACHE=/tmp/ollama-slurm-gocache GOPATH=/tmp/ollama-slurm-gopath \
  BROKER_BASE_URL="http://${BROKER_LISTEN_ADDR}" \
  /usr/bin/go run "${REPO_ROOT}/broker/cmd/broker-cli" submit \
    --task-type document_summary \
    --input-uri "file://${INPUT_FILE}" \
    --schema document_summary_v1)"

JOB_ID="$(printf '%s' "${SUBMIT_RESPONSE}" | extract_job_id)"
echo "Submitted job: ${JOB_ID}"

wait_for_job_state "${BROKER_LISTEN_ADDR}" "${JOB_ID}" >/dev/null

env -u GOROOT GOCACHE=/tmp/ollama-slurm-gocache GOPATH=/tmp/ollama-slurm-gopath \
  BROKER_BASE_URL="http://${BROKER_LISTEN_ADDR}" \
  /usr/bin/go run "${REPO_ROOT}/broker/cmd/broker-cli" result "${JOB_ID}"
