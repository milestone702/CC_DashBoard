#!/bin/sh
# cc-agent deb/rpm 安装后注册开机自启（T-301）
set -e
mkdir -p /etc/cc-agent
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  systemctl enable cc-agent.service || true
  # 立即拉起：无 config.json 时 unit 的 ConditionPathExists 会自动跳过启动
  systemctl restart cc-agent.service || true
fi
echo "cc-agent 已安装并注册为系统服务。"
echo "写入配置后自动启动: cp /etc/cc-agent/config.example.json /etc/cc-agent/config.json && systemctl start cc-agent"
exit 0
