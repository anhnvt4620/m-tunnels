#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="/opt/m-tunnel"
BIN_SRC="${1:-./bin/m-tunnel-gateway}"
CONFIG_SRC="${2:-./configs/gateway.yaml}"

sudo mkdir -p "$INSTALL_DIR/configs"
sudo cp "$BIN_SRC" "$INSTALL_DIR/m-tunnel-gateway"
sudo cp "$CONFIG_SRC" "$INSTALL_DIR/configs/gateway.yaml"
sudo chmod +x "$INSTALL_DIR/m-tunnel-gateway"

cat <<'EOF' | sudo tee /etc/systemd/system/m-tunnel-gateway.service >/dev/null
[Unit]
Description=M-Tunnel Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/m-tunnel
ExecStart=/opt/m-tunnel/m-tunnel-gateway -config /opt/m-tunnel/configs/gateway.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now m-tunnel-gateway.service
sudo systemctl status m-tunnel-gateway.service --no-pager
