#!/usr/bin/env bash
# install_registry.sh - Install and enable the llama server registry as a systemd service
set -euo pipefail

INSTALL_DIR="/opt/llama-registry"
DATA_DIR="${INSTALL_DIR}/data"
SERVICE_FILE="/etc/systemd/system/llama-registry.service"

if [[ $EUID -ne 0 ]]; then
    echo "Error: this script must be run as root (use sudo)." >&2
    exit 1
fi

echo "==> Installing llama server registry to ${INSTALL_DIR}"

# Install Flask
if ! python3 -c "import flask" 2>/dev/null; then
    echo "==> Installing Flask..."
    pip3 install -r "$(dirname "$0")/requirements.txt"
fi

# Create directories
mkdir -p "${INSTALL_DIR}" "${DATA_DIR}"
chown nobody:nogroup "${DATA_DIR}"

# Copy files
cp "$(dirname "$0")/registry_server.py" "${INSTALL_DIR}/"
cp "$(dirname "$0")/dashboard.html"     "${INSTALL_DIR}/"

# Install systemd service
cp "$(dirname "$0")/llama-registry.service" "${SERVICE_FILE}"
systemctl daemon-reload
systemctl enable llama-registry
systemctl restart llama-registry

echo ""
echo "==> Registry service installed and started."
echo "    Status : systemctl status llama-registry"
echo "    Logs   : journalctl -u llama-registry -f"
echo "    Health : curl http://localhost:5000/health"
