#!/usr/bin/env bash

set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: $0 <base-dir>" >&2
  exit 2
fi

BASE_DIR="$1"
STATE_DIR="${BASE_DIR}/fake-slurm"
BIN_DIR="${BASE_DIR}/bin"

mkdir -p "${STATE_DIR}" "${BIN_DIR}"

cat > "${BIN_DIR}/sbatch" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
STATE_DIR="${FAKE_SLURM_STATE_DIR:?}"
COUNTER_FILE="${STATE_DIR}/counter"
mkdir -p "${STATE_DIR}"
if [ ! -f "${COUNTER_FILE}" ]; then
  echo 1000 > "${COUNTER_FILE}"
fi
JOB_ID=$(cat "${COUNTER_FILE}")
NEXT_ID=$((JOB_ID + 1))
echo "${NEXT_ID}" > "${COUNTER_FILE}"

EXPORTS=""
SCRIPT=""
while [ $# -gt 0 ]; do
  case "$1" in
    --export)
      EXPORTS="$2"
      shift 2
      ;;
    --parsable)
      shift 1
      ;;
    --job-name)
      shift 2
      ;;
    *)
      SCRIPT="$1"
      shift 1
      ;;
  esac
done

echo "PENDING" > "${STATE_DIR}/${JOB_ID}.state"
echo "0:0" > "${STATE_DIR}/${JOB_ID}.exit"

IFS=',' read -r -a ENV_PARTS <<< "${EXPORTS}"
for part in "${ENV_PARTS[@]}"; do
  if [ -z "${part}" ] || [ "${part}" = "ALL" ]; then
    continue
  fi
  export "${part}"
done

export SLURM_JOB_ID="${JOB_ID}"
export SLURM_JOB_NAME="broker-worker"
echo "RUNNING" > "${STATE_DIR}/${JOB_ID}.state"

set +e
"${FAKE_SLURM_BASH}" "${SCRIPT}" > "${STATE_DIR}/${JOB_ID}.out" 2> "${STATE_DIR}/${JOB_ID}.err"
CODE=$?
set -e

if [ "${CODE}" -eq 0 ]; then
  echo "COMPLETED" > "${STATE_DIR}/${JOB_ID}.state"
else
  echo "FAILED" > "${STATE_DIR}/${JOB_ID}.state"
fi
echo "${CODE}:0" > "${STATE_DIR}/${JOB_ID}.exit"
echo "${JOB_ID}"
EOF

cat > "${BIN_DIR}/sacct" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
STATE_DIR="${FAKE_SLURM_STATE_DIR:?}"
JOB_ID=""
while [ $# -gt 0 ]; do
  case "$1" in
    --jobs)
      JOB_ID="$2"
      shift 2
      ;;
    *)
      shift 1
      ;;
  esac
done
STATE=$(cat "${STATE_DIR}/${JOB_ID}.state")
EXIT_CODE=$(cat "${STATE_DIR}/${JOB_ID}.exit")
printf '%s|%s\n' "${STATE}" "${EXIT_CODE}"
EOF

cat > "${BIN_DIR}/scancel" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
STATE_DIR="${FAKE_SLURM_STATE_DIR:?}"
JOB_ID="$1"
echo "CANCELLED" > "${STATE_DIR}/${JOB_ID}.state"
echo "0:0" > "${STATE_DIR}/${JOB_ID}.exit"
EOF

cat > "${BIN_DIR}/squeue" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
STATE_DIR="${FAKE_SLURM_STATE_DIR:?}"
JOB_ID=""
while [ $# -gt 0 ]; do
  case "$1" in
    --jobs)
      JOB_ID="$2"
      shift 2
      ;;
    *)
      shift 1
      ;;
  esac
done
cat "${STATE_DIR}/${JOB_ID}.state"
EOF

chmod +x "${BIN_DIR}/sbatch" "${BIN_DIR}/sacct" "${BIN_DIR}/scancel" "${BIN_DIR}/squeue"

echo "export FAKE_SLURM_STATE_DIR='${STATE_DIR}'"
echo "export FAKE_SLURM_BASH='/usr/bin/bash'"
echo "export FAKE_SLURM_PYTHON='/usr/bin/python3'"
echo "export PATH='${BIN_DIR}':\"\$PATH\""

