#!/bin/sh
# cc-agent 卸载前停止并取消自启（T-301）
if command -v systemctl >/dev/null 2>&1; then
  systemctl stop cc-agent.service || true
  systemctl disable cc-agent.service || true
fi
exit 0
