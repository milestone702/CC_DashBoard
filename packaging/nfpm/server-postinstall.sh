#!/bin/sh
# cc-dashboard 服务端安装：建数据目录 + 注册并启动服务（T-301/T-302）
set -e
mkdir -p /var/lib/cc-dashboard
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload || true
  systemctl enable cc-dashboard.service || true
  systemctl restart cc-dashboard.service || true
fi
echo "CC_DashBoard 已启动: http://<主机IP>:8080/ （首次进入设置管理员密码）"
exit 0
