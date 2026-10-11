#!/bin/sh
# cc-agent 卸载后刷新 systemd（T-301）
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
exit 0
