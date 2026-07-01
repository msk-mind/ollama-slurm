#!/usr/bin/env bash

run_squeue() {
  if command -v timeout >/dev/null 2>&1; then
    timeout 2s squeue "$@"
  else
    squeue "$@"
  fi
}

connection_file_path() {
  local repo_root="$1"
  local job_id="$2"
  printf '%s/llama_server_connection_%s.txt\n' "$repo_root" "$job_id"
}

slurm_job_exists() {
  local job_id="$1"
  run_squeue -h -j "$job_id" >/dev/null 2>&1
}

slurm_controller_available() {
  run_squeue -h >/dev/null 2>&1
}

print_active_llama_jobs() {
  if ! slurm_controller_available; then
    echo "Slurm controller is not reachable from this environment."
    return 1
  fi

  run_squeue -u "$USER" -n llama-server -o "%.10i %.9P %.20j %.8T %.10M %.6D %R"
}

print_active_jobs() {
  if ! slurm_controller_available; then
    echo "Slurm controller is not reachable from this environment."
    return 1
  fi

  run_squeue -u "$USER"
}

wait_for_connection_file() {
  local repo_root="$1"
  local job_id="$2"
  local timeout_seconds="${3:-240}"
  local connection_file
  local deadline

  connection_file="$(connection_file_path "$repo_root" "$job_id")"
  deadline=$((SECONDS + timeout_seconds))

  while (( SECONDS < deadline )); do
    if [[ -f "$connection_file" ]]; then
      printf '%s\n' "$connection_file"
      return 0
    fi
    sleep 2
  done

  return 1
}
