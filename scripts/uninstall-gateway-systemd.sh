#!/usr/bin/env bash
set -euo pipefail

sudo systemctl stop m-tunnel-gateway.service || true
sudo systemctl disable m-tunnel-gateway.service || true
sudo rm -f /etc/systemd/system/m-tunnel-gateway.service
sudo systemctl daemon-reload
echo "M-Tunnel Gateway systemd service removed."
echo "Binary and config remain at /opt/m-tunnel — remove manually if desired."
