# 构建产物说明（build.md）

运行 `build.ps1` 后，所有产物生成在 **`build/`** 目录：

```powershell
powershell -ExecutionPolicy Bypass -File build.ps1
```

版本号在构建时**交互生成**并记录到仓库根 `VERSION` 文件：显示上次版本并提示输入，**直接回车 = 自动 +0.1**（如 V0.3 → V0.4），输入 `V2.0` 升大版本；首次构建以 `V0.{git提交数}` 播种。非交互：`-Version V2.0` 指定、`-Auto` 自动递增（stdin 重定向时自动降级为 `-Auto` 防挂起）。`V…` 形式注入二进制（`-version` 可查看）；deb/rpm/ipk 用去 V 的数字形式（打包规范要求）。
deb/rpm 需要本机安装 [nfpm](https://goreleaser.com/nfpm/)（`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`），未安装则跳过该两类包；ipk 由内置工具 `tools/mkipk` 构建，无额外依赖。

## 图标与版本信息

- **Windows exe**：内嵌应用图标（资源管理器 / 任务栏可见）与 VERSIONINFO（右键属性的文件说明、版本号）。由 `build.ps1` 调 `goversioninfo` 生成 `resource_windows_amd64.syso` 链入，缺失时自动 `go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest`，仍失败则跳过并告警（Linux 产物不受 .syso 影响）。
- **图标源**：`packaging/icon.ico`（16–256 多尺寸，由 `web/img/CC_DASHBOARD.png` 生成）；`packaging/icon-256.png` 供 Linux 包使用。
- **deb/rpm**：图标装到 `/usr/share/icons/hicolor/256x256/apps/{cc-agent,cc-dashboard}.png`；**ipk** 装到 `/usr/share/pixmaps/cc-agent.png`。
- `.syso` 为生成物已入 `.gitignore`；不经 `build.ps1` 直接 `go build` 的 Windows 产物无图标属正常。

## 该用哪个文件？

### 服务端（Master，装一台就够）

| 文件 | 平台 | 说明 |
|---|---|---|
| `cc-server-windows-amd64.exe` | Windows 10/11、Server（x86_64） | 直接运行或 `--install` 注册服务 |
| `cc-server-linux-amd64` | Linux x86_64：Ubuntu / Debian / CentOS / 飞牛 / 群晖 VM 等 | 单二进制，已内嵌面板 |
| `cc-server-linux-arm64` | Linux ARM64：树莓派 4/5（64 位系统）、瑞芯微 / 晶晨盒子 | 同上 |
| `cc-server-linux-armv7` | Linux ARMv7（32 位）：树莓派 2/3、旧款矿渣 | 同上 |
| `cc-dashboard_*_amd64.deb` | Debian / Ubuntu（x86_64） | 安装即启动（`apt install ./xxx.deb`） |
| `cc-dashboard_*_amd64.rpm` | RHEL / CentOS / Fedora / openSUSE（x86_64） | 安装即启动 |
| `cc-dashboard_*_arm64.deb` / `.rpm` | 上述发行版的 ARM64 机型 | 同上 |

> 不确定架构？SSH 到目标机执行 `uname -m`：`x86_64`→amd64，`aarch64`→arm64，`armv7l`→armv7。

### Agent（每个被监控机器装一个，模式 A）

| 文件 | 平台 | 说明 |
|---|---|---|
| `cc-agent-windows-amd64.exe` | Windows（x86_64） | `cc-agent --install` 交互式注册服务 |
| `cc-agent-linux-amd64` | Linux x86_64 服务器 / NAS | 见手册 3.2 节 |
| `cc-agent-linux-arm64` | ARM64（树莓派 4/5 等） | 同上 |
| `cc-agent-linux-armv7` | ARMv7 32 位（树莓派 2/3） | 同上 |
| `cc-agent-linux-mipsle` | MIPS 小端：OpenWrt 主流路由器（MT7621 等） | 裸二进制，配合 procd 使用 |

### OpenWrt 安装包（ipk，推荐路由器使用）

| 文件 | 架构 |
|---|---|
| `cc-agent-<版本>-mipsel_24kc.ipk` | MIPS 小端（MT7621 / 联发科主流，如小米 / 极路由） |
| `cc-agent-<版本>-arm_cortex-a7.ipk` | ARMv7（多数 ARM 路由器） |
| `cc-agent-<版本>-aarch64_cortex-a53.ipk` | ARM64（部分高端软路由） |
| `cc-agent-<版本>-x86_64.ipk` | x86 软路由（iStoreOS / 官方 OpenWrt x86） |

```bash
opkg install cc-agent-*.ipk     # 装完自动注册开机自启
# 然后编辑 /etc/cc-agent/config.example.json → /etc/cc-agent/config.json 填入 master/token
/etc/init.d/cc-agent start
```

> 不确定 ipk 架构：路由器上执行 `opkg print-architecture`，对照 build.ps1 中的映射表。

### Agent 的 deb / rpm（Linux 服务器不想手动跑二进制时）

| 文件 | 平台 |
|---|---|
| `cc-agent_*_amd64.deb` / `cc-agent_*_arm64.deb` | Debian / Ubuntu |
| `cc-agent-*-1.x86_64.rpm` / `cc-agent-*-1.aarch64.rpm` | RHEL / CentOS / Fedora |

```bash
sudo apt install ./cc-agent_*_amd64.deb     # 或 yum install ./cc-agent-*.rpm
# 装完服务已注册（等配置就绪才启动）：
sudo cp /etc/cc-agent/config.example.json /etc/cc-agent/config.json   # 填 master/token
sudo systemctl start cc-agent
```

## 不产生安装包的方式

- **Docker**：见《docs/使用部署手册.md》2.5 节（`server/deploy/`）。
- **裸二进制直跑**：任选上表对应平台的 `cc-server-*` / `cc-agent-*`，配合 `--install` 注册服务。
- **开发调试**：`cd server && go run ./cmd/master`；前端 `cd web && npm run dev`。

## 目录约定

- `build/` — 构建产物（git 忽略，可随时重新生成）
- `web/dist/` — 前端 vite 构建输出（中间产物，git 忽略）
- `server/internal/webui/dist/` — 嵌入服务端的前端副本（由 build.ps1 同步；**仅 `.gitkeep` 入库**，保证空目录可编译）
- `data/` — 运行时 SQLite 与主密钥（git 忽略，备份时带上 `master.key`）
