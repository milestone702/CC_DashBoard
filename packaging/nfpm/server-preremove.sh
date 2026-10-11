#!/bin/sh
# cc-dashboard 卸载前停止服务（T-302）
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop cc-dashboard.service || true
  systemctl disable cc-dashboard.service || true
fi
exit 0
