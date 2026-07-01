#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

WITH_LOOPBACK_BIND=0
for arg in "$@"; do
  case "${arg}" in
    --with-loopback-bind)
      WITH_LOOPBACK_BIND=1
      ;;
    *)
      echo "unknown argument: ${arg}" >&2
      echo "usage: $0 [--with-loopback-bind]" >&2
      exit 2
      ;;
  esac
done

echo "==> smoke_command_mode.sh"
bash "${SCRIPT_DIR}/smoke_command_mode.sh"

if [ "${WITH_LOOPBACK_BIND}" -eq 1 ]; then
  echo "==> smoke_rag_llamacpp_runtime.sh"
  bash "${SCRIPT_DIR}/smoke_rag_llamacpp_runtime.sh"
  echo "==> smoke_rag_llamacpp_unavailable.sh"
  bash "${SCRIPT_DIR}/smoke_rag_llamacpp_unavailable.sh"
  echo "==> smoke_rag_no_real_backend.sh"
  bash "${SCRIPT_DIR}/smoke_rag_no_real_backend.sh"
else
  echo "==> skipping loopback-binding RAG smokes (pass --with-loopback-bind to enable)"
fi
