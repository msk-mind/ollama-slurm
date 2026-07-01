#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BASE_DIR="$(mktemp -d /tmp/ollama-slurm-rag-smoke.XXXXXX)"
source "${SCRIPT_DIR}/smoke_lib.sh"

cleanup() {
  kill_pid_if_running "${BROKER_PID:-}"
  kill_pid_if_running "${FAKE_LLM_PID:-}"
}
trap cleanup EXIT

FAKE_COUNT_FILE="${BASE_DIR}/fake-llm-count.txt"
python3 "${SCRIPT_DIR}/fake_openai_server.py" \
  --listen-port 18090 \
  --count-file "${FAKE_COUNT_FILE}" &
FAKE_LLM_PID=$!

wait_for_http_ok "http://127.0.0.1:18090/healthz"

export BROKER_LISTEN_ADDR="127.0.0.1:18082"
export BROKER_JOB_STORE_PATH="${BASE_DIR}/jobs.json"
export BROKER_RUN_ROOT_PATH="${BASE_DIR}/runs"
export BROKER_REPO_ROOT_PATH="${REPO_ROOT}"
export BROKER_BACKEND="local"
export BROKER_LOCAL_MODE="command"
export BROKER_LOCAL_SCRIPT_PATH="${REPO_ROOT}/deploy/local/broker_worker.sh"
export BROKER_AUDIT_LOG_PATH="${BASE_DIR}/audit.jsonl"
export BROKER_AUDIT_VERIFY_MODE="warn"
export BROKER_RUNTIME_LLAMACPP_BASE_URL="http://127.0.0.1:18090"
export BROKER_RUNTIME_LLAMACPP_TIMEOUT_SECONDS="10"

RAG_REPO="${BASE_DIR}/repo"
mkdir -p "${RAG_REPO}/src"
cat > "${RAG_REPO}/src/main.py" <<'EOF'
def run_service():
    raise RuntimeError("smoke failure")
EOF
cat > "${RAG_REPO}/build.log" <<'EOF'
fatal error: generated header missing
traceback: service failed to start
EOF

start_broker_server "${REPO_ROOT}"

SUBMIT_RESPONSE="$(curl -sf \
  -H 'Content-Type: application/json' \
  -X POST "http://${BROKER_LISTEN_ADDR}/v1/rag/compressions" \
  -d '{
    "query": "Why does the service fail?",
    "input_refs": [
      {"type":"repo","uri":"file://'"${RAG_REPO}"'","classification":"internal"}
    ],
    "constraints": {
      "retrieved_chunk_budget": 16000,
      "per_chunk_compression_budget": 192,
      "final_evidence_pack_budget": 1200,
      "remote_model_context_budget": 4000
    },
    "execution_profile": {
      "backend": "local",
      "tier": "p40-rag-compression"
    }
  }')"

JOB_ID="$(printf '%s' "${SUBMIT_RESPONSE}" | extract_job_id)"
echo "Submitted RAG job: ${JOB_ID}"

wait_for_job_state "${BROKER_LISTEN_ADDR}" "${JOB_ID}" 100 >/dev/null

RESULT_JSON="$(curl -sf "http://${BROKER_LISTEN_ADDR}/v1/jobs/${JOB_ID}/result")"
RESULT_JSON="${RESULT_JSON}" python3 - <<'PY'
import json
import os

payload = json.loads(os.environ["RESULT_JSON"])
result = payload["result"]["payload"]
artifacts = payload.get("artifacts") or []

assert result["retrieval"]["runtime_backend_mode"] == "real", result["retrieval"]
assert result["provenance"]["runtime_backend"] == "llama.cpp", result["provenance"]
assert payload["execution_quality"] == "real_local", payload
assert payload["degraded_local_execution"] is False, payload
assert payload["retry_recommended"] is False, payload
assert any(item.get("artifact_id") == "artifact_runtime_context" for item in artifacts), artifacts
PY

REQUEST_COUNT="$(cat "${FAKE_COUNT_FILE}")"
if [ "${REQUEST_COUNT}" -le 0 ]; then
  echo "expected fake llama.cpp server to receive requests"
  exit 1
fi

echo "fake llama.cpp requests: ${REQUEST_COUNT}"
echo "${RESULT_JSON}"
