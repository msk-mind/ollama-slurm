#!/usr/bin/env bash

wait_for_http_ok() {
  local url="$1"
  local attempts="${2:-50}"
  local sleep_seconds="${3:-0.1}"

  for _ in $(seq 1 "${attempts}"); do
    if curl -sf "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep "${sleep_seconds}"
  done

  echo "timed out waiting for ${url}" >&2
  return 1
}

kill_pid_if_running() {
  local pid="${1:-}"
  if [ -n "${pid}" ] && kill -0 "${pid}" 2>/dev/null; then
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  fi
}

start_broker_server() {
  local repo_root="$1"

  env -u GOROOT GOCACHE=/tmp/ollama-slurm-gocache GOPATH=/tmp/ollama-slurm-gopath \
    /usr/bin/go run "${repo_root}/broker/cmd/broker-server" &
  BROKER_PID=$!

  wait_for_http_ok "http://${BROKER_LISTEN_ADDR}/healthz"
}

extract_job_id() {
  python3 -c 'import json,sys; print(json.load(sys.stdin)["job_id"])'
}

extract_job_state() {
  python3 -c 'import json,sys; print(json.load(sys.stdin)["state"])'
}

wait_for_job_state() {
  local broker_addr="$1"
  local job_id="$2"
  local attempts="${3:-50}"
  local sleep_seconds="${4:-0.1}"

  local job_json=""
  local state=""
  for _ in $(seq 1 "${attempts}"); do
    job_json="$(curl -sf "http://${broker_addr}/v1/jobs/${job_id}")"
    state="$(printf '%s' "${job_json}" | extract_job_state)"
    if [ "${state}" = "succeeded" ]; then
      printf '%s\n' "${job_json}"
      return 0
    fi
    if [ "${state}" = "failed" ]; then
      printf '%s\n' "${job_json}" >&2
      return 1
    fi
    sleep "${sleep_seconds}"
  done

  echo "timed out waiting for job ${job_id}; last_state=${state:-unknown}" >&2
  if [ -n "${job_json}" ]; then
    printf '%s\n' "${job_json}" >&2
  fi
  return 1
}
