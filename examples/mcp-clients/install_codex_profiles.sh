#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CODEX_HOME="${CODEX_HOME:-${HOME}/.codex}"
PROFILE_DIR="${CODEX_HOME}"
TEMPLATE_DIR="${SCRIPT_DIR}/codex-profiles"

mkdir -p "${PROFILE_DIR}"

install_profile() {
  local template_name="$1"
  local profile_name="$2"
  local output_path="${PROFILE_DIR}/${profile_name}.config.toml"

  sed "s|__REPO_ROOT__|${REPO_ROOT}|g" "${TEMPLATE_DIR}/${template_name}" > "${output_path}"
  echo "Installed ${output_path}"
}

install_profile "slurm-broker.config.toml.template" "slurm-broker"
install_profile "local-broker.config.toml.template" "local-broker"

cat <<EOF

Profiles installed.

Use:
  codex -p slurm-broker
  codex -p local-broker

Verify:
  codex --strict-config -p slurm-broker --version
  codex --strict-config -p local-broker --version
EOF
