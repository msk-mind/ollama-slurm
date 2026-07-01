#!/bin/bash
#SBATCH --job-name=broker-worker
#SBATCH --output=.broker/slurm/broker_worker_%j.log
#SBATCH --error=.broker/slurm/broker_worker_%j.err

set -euo pipefail

if [ -n "${BROKER_ARRAY_MANIFEST:-}" ]; then
  if [ -z "${SLURM_ARRAY_TASK_ID:-}" ]; then
    echo "SLURM_ARRAY_TASK_ID is required when BROKER_ARRAY_MANIFEST is set"
    exit 2
  fi
  if [ ! -f "${BROKER_ARRAY_MANIFEST}" ]; then
    echo "Missing BROKER_ARRAY_MANIFEST: ${BROKER_ARRAY_MANIFEST}"
    exit 2
  fi
  eval "$(
    python3 - "${BROKER_ARRAY_MANIFEST}" "${SLURM_ARRAY_TASK_ID}" <<'PY'
import json
import shlex
import sys

manifest_path = sys.argv[1]
task_index = int(sys.argv[2])
with open(manifest_path, "r", encoding="utf-8") as handle:
    manifest = json.load(handle)
entry = manifest[task_index]
mapping = {
    "BROKER_JOB_ID": entry["broker_job_id"],
    "BROKER_TASK_TYPE": entry["broker_task_type"],
    "BROKER_OUTPUT_DIR": entry["broker_output_dir"],
}
if entry.get("broker_output_schema"):
    mapping["BROKER_OUTPUT_SCHEMA"] = entry["broker_output_schema"]
for key, value in mapping.items():
    print(f"export {key}={shlex.quote(str(value))}")
PY
  )"
fi

if [ -z "${BROKER_JOB_ID:-}" ]; then
  echo "BROKER_JOB_ID is required"
  exit 2
fi

if [ -z "${BROKER_TASK_TYPE:-}" ]; then
  echo "BROKER_TASK_TYPE is required"
  exit 2
fi

OUTPUT_DIR="${BROKER_OUTPUT_DIR:-.broker/runs/${BROKER_JOB_ID}}"
REPO_ROOT="${BROKER_REPO_ROOT:-.}"
HEARTBEAT_PATH="${OUTPUT_DIR}/heartbeat.json"
STDOUT_LOG_PATH="${OUTPUT_DIR}/stdout.log"
STDERR_LOG_PATH="${OUTPUT_DIR}/stderr.log"
mkdir -p "${OUTPUT_DIR}"

exec > >(tee -a "${STDOUT_LOG_PATH}")
exec 2> >(tee -a "${STDERR_LOG_PATH}" >&2)

STARTED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
HOSTNAME_VALUE="$(hostname)"

write_heartbeat() {
  local state="$1"
  local phase="$2"
  local percent="$3"
  local message="$4"
  local metrics_json="${5:-{}}"
  local timestamp
  timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  cat > "${HEARTBEAT_PATH}" <<EOF
{
  "job_id": "${BROKER_JOB_ID}",
  "state": "${state}",
  "phase": "${phase}",
  "percent": ${percent},
  "message": "${message}",
  "timestamp": "${timestamp}",
  "metrics": ${metrics_json}
}
EOF
}

finalize_run() {
  local exit_code="$1"
  local status="completed"
  local heartbeat_state="completed"
  if [ "${exit_code}" -ne 0 ]; then
    status="failed"
    heartbeat_state="failed"
  fi

  cat > "${OUTPUT_DIR}/run-metadata.json" <<EOF
{
  "broker_job_id": "${BROKER_JOB_ID}",
  "task_type": "${BROKER_TASK_TYPE}",
  "slurm_job_id": "${SLURM_JOB_ID:-}",
  "slurm_job_name": "${SLURM_JOB_NAME:-}",
  "node": "${HOSTNAME_VALUE}",
  "started_at": "${STARTED_AT}",
  "completed_at": "${COMPLETED_AT}",
  "status": "${status}"
}
EOF
  write_heartbeat "${heartbeat_state}" "completed" 100 "Worker finished" "{\"exit_code\": ${exit_code}}"
}

on_exit() {
  local exit_code="$?"
  COMPLETED_AT="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  trap - EXIT
  finalize_run "${exit_code}"
}

trap on_exit EXIT

cat > "${OUTPUT_DIR}/run-metadata.json" <<EOF
{
  "broker_job_id": "${BROKER_JOB_ID}",
  "task_type": "${BROKER_TASK_TYPE}",
  "slurm_job_id": "${SLURM_JOB_ID:-}",
  "slurm_job_name": "${SLURM_JOB_NAME:-}",
  "node": "${HOSTNAME_VALUE}",
  "started_at": "${STARTED_AT}",
  "status": "started"
}
EOF

write_heartbeat "running" "starting" 1 "Worker process started" "{}"

echo "Broker worker started"
echo "Broker job ID: ${BROKER_JOB_ID}"
echo "Task type: ${BROKER_TASK_TYPE}"
echo "Output dir: ${OUTPUT_DIR}"
echo "Repo root: ${REPO_ROOT}"
echo "Node: ${HOSTNAME_VALUE}"

JOB_SPEC_PATH="${OUTPUT_DIR}/job_spec.json"
EXECUTION_PLAN_PATH="${OUTPUT_DIR}/execution_plan.json"
INPUT_MANIFEST_PATH="${OUTPUT_DIR}/input_manifest.json"
echo "Execution plan: ${EXECUTION_PLAN_PATH}"

if [ ! -f "${JOB_SPEC_PATH}" ]; then
  echo "Missing job spec: ${JOB_SPEC_PATH}"
  exit 2
fi

if [ ! -f "${EXECUTION_PLAN_PATH}" ]; then
  echo "Missing execution plan: ${EXECUTION_PLAN_PATH}"
  exit 2
fi

if [ ! -f "${INPUT_MANIFEST_PATH}" ]; then
  echo "Missing input manifest: ${INPUT_MANIFEST_PATH}"
  exit 2
fi

write_heartbeat "running" "resolving_inputs" 5 "Validated staged inputs" "{}"

case "${BROKER_TASK_TYPE}" in
document_summary)
  write_heartbeat "running" "running_model" 10 "Launching document summary worker" "{}"
  python3 "${REPO_ROOT}/workers/document-summary/main.py" \
    --job-spec "${JOB_SPEC_PATH}" \
    --input-manifest "${INPUT_MANIFEST_PATH}" \
    --output-dir "${OUTPUT_DIR}" \
    --heartbeat-path "${HEARTBEAT_PATH}"
  ;;
log_analysis)
  write_heartbeat "running" "running_model" 10 "Launching log analysis worker" "{}"
  python3 "${REPO_ROOT}/workers/log-analysis/main.py" \
    --job-spec "${JOB_SPEC_PATH}" \
    --input-manifest "${INPUT_MANIFEST_PATH}" \
    --output-dir "${OUTPUT_DIR}" \
    --heartbeat-path "${HEARTBEAT_PATH}"
  ;;
repo_summary)
  write_heartbeat "running" "running_model" 10 "Launching repository summary worker" "{}"
  python3 "${REPO_ROOT}/workers/repo-summary/main.py" \
    --job-spec "${JOB_SPEC_PATH}" \
    --input-manifest "${INPUT_MANIFEST_PATH}" \
    --output-dir "${OUTPUT_DIR}" \
    --heartbeat-path "${HEARTBEAT_PATH}"
  ;;
rag_compress|debug_with_local_context|summarize_logs|inspect_repo|propose_patch)
  write_heartbeat "running" "running_model" 10 "Launching RAG compression worker" "{}"
  python3 "${REPO_ROOT}/workers/rag-compression/main.py" \
    --job-spec "${JOB_SPEC_PATH}" \
    --execution-plan "${EXECUTION_PLAN_PATH}" \
    --input-manifest "${INPUT_MANIFEST_PATH}" \
    --output-dir "${OUTPUT_DIR}" \
    --heartbeat-path "${HEARTBEAT_PATH}"
  ;;
*)
  write_heartbeat "running" "postprocessing" 80 "Writing placeholder result" "{}"
  cat > "${OUTPUT_DIR}/result.json" <<EOF
{
  "schema_name": "placeholder_v1",
  "schema_version": "1.0.0",
  "payload": {
    "summary": "No concrete worker is implemented yet for task type ${BROKER_TASK_TYPE}.",
    "task_type": "${BROKER_TASK_TYPE}",
    "broker_job_id": "${BROKER_JOB_ID}"
  }
}
EOF

  cat > "${OUTPUT_DIR}/artifacts.json" <<EOF
[]
EOF
  ;;
esac

echo "Broker worker completed"
