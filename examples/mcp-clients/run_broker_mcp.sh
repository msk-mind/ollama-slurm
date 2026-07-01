#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

export BROKER_JOB_STORE_PATH="${BROKER_JOB_STORE_PATH:-${REPO_ROOT}/.broker/jobs.json}"
export BROKER_RUN_ROOT_PATH="${BROKER_RUN_ROOT_PATH:-${REPO_ROOT}/.broker/runs}"
export BROKER_REPO_ROOT_PATH="${BROKER_REPO_ROOT_PATH:-${REPO_ROOT}}"
export BROKER_BACKEND="${BROKER_BACKEND:-local}"
export BROKER_LOCAL_MODE="${BROKER_LOCAL_MODE:-command}"
export BROKER_LOCAL_SCRIPT_PATH="${BROKER_LOCAL_SCRIPT_PATH:-${REPO_ROOT}/deploy/local/broker_worker.sh}"
export BROKER_SLURM_MODE="${BROKER_SLURM_MODE:-stub}"
export BROKER_SLURM_SUBMIT_CMD="${BROKER_SLURM_SUBMIT_CMD:-sbatch}"
export BROKER_SLURM_STATUS_CMD="${BROKER_SLURM_STATUS_CMD:-sacct}"
export BROKER_SLURM_CANCEL_CMD="${BROKER_SLURM_CANCEL_CMD:-scancel}"
export BROKER_SLURM_SCRIPT_PATH="${BROKER_SLURM_SCRIPT_PATH:-${REPO_ROOT}/deploy/slurm/broker_worker.slurm}"
export BROKER_MCP_ACTOR="${BROKER_MCP_ACTOR:-copilot-cli}"
export BROKER_MCP_ROLE="${BROKER_MCP_ROLE:-user}"

cd "${REPO_ROOT}"

exec env -u GOROOT GOENV=off \
  GOCACHE="${GOCACHE:-/tmp/ollama-slurm-gocache}" \
  GOPATH="${GOPATH:-/tmp/ollama-slurm-gopath}" \
  /usr/bin/go run ./broker/cmd/broker-mcp
