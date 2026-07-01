#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BASE_DIR="$(mktemp -d /tmp/ollama-slurm-rag-no-real-backend-smoke.XXXXXX)"
source "${SCRIPT_DIR}/smoke_lib.sh"

cleanup() {
  kill_pid_if_running "${BROKER_PID:-}"
}
trap cleanup EXIT

export BROKER_LISTEN_ADDR="127.0.0.1:18084"
export BROKER_JOB_STORE_PATH="${BASE_DIR}/jobs.json"
export BROKER_RUN_ROOT_PATH="${BASE_DIR}/runs"
export BROKER_REPO_ROOT_PATH="${REPO_ROOT}"
export BROKER_BACKEND="local"
export BROKER_LOCAL_MODE="command"
export BROKER_LOCAL_SCRIPT_PATH="${REPO_ROOT}/deploy/local/broker_worker.sh"
export BROKER_AUDIT_LOG_PATH="${BASE_DIR}/audit.jsonl"
export BROKER_AUDIT_VERIFY_MODE="warn"
unset BROKER_RUNTIME_LLAMACPP_BASE_URL
unset BROKER_RUNTIME_LLAMACPP_TIMEOUT_SECONDS

LOG_INPUT="${BASE_DIR}/build.log"
cat > "${LOG_INPUT}" <<'EOF'
fatal error: generated header missing
traceback: service failed to start
EOF

start_broker_server "${REPO_ROOT}"

SUBMIT_RESPONSE="$(curl -sf \
  -H 'Content-Type: application/json' \
  -X POST "http://${BROKER_LISTEN_ADDR}/v1/rag/compressions" \
  -d '{
    "query": "Why does the service fail?",
    "retrieval_strategies": ["bm25"],
    "input_refs": [
      {"type":"log","uri":"file://'"${LOG_INPUT}"'","classification":"internal"}
    ]
  }')"

JOB_ID="$(printf '%s' "${SUBMIT_RESPONSE}" | extract_job_id)"
echo "Submitted no-real-backend RAG job: ${JOB_ID}"

wait_for_job_state "${BROKER_LISTEN_ADDR}" "${JOB_ID}" 100 >/dev/null

RESULT_JSON="$(curl -sf "http://${BROKER_LISTEN_ADDR}/v1/jobs/${JOB_ID}/result")"
RESULT_JSON="${RESULT_JSON}" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["RESULT_JSON"])
result = payload["result"]["payload"]
diagnostics = payload.get("runtime_diagnostics") or {}

assert result["retrieval"]["runtime_backend_mode"] == "heuristic", result["retrieval"]
assert payload["execution_quality"] == "no_real_backend", payload
assert payload["degraded_local_execution"] is True, payload
assert payload["retry_recommended"] is True, payload
assert diagnostics["backend_mode"] == "heuristic", diagnostics
assert diagnostics["backend_name"] == "deterministic", diagnostics
assert diagnostics["endpoint_configured"] is False, diagnostics
assert diagnostics["llm_available"] is False, diagnostics
PY

echo "${RESULT_JSON}"
