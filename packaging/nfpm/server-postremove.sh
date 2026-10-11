#!/bin/sh
# cc-dashboard 卸载后刷新 systemd；数据目录 /var/lib/cc-dashboard 保留（T-302）
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
fi
exit 0
