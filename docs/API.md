# CC_DashBoard API 对接文档

- 版本：v1.3（对应 `server` V0.2.0；补充 gzip 压缩响应的长度与 Range 约定）
- 适用对象：前端（Vue3）对接、采集端（Agent / 脚本）开发
- 更新日期：2026-10-23

> **只做 Android / 移动端 App？** 请看两份专用文档：
> [`APP-DESIGN.md`](./APP-DESIGN.md)（产品需求与技术设计：页面结构、多面板、仪表盘、登录方案）
> 和 [`APP-API.md`](./APP-API.md)（精简接口参考 + Kotlin 数据模型 + WebSocket 重连策略）。

---

## 1. 通用约定

| 项目 | 约定 |
|---|---|
| Base URL | `http://<master>:8080/api/v1` |
| 编码 | UTF-8，JSON（字段 snake_case） |
| 时间戳 | Unix 秒（`ts` / `last_seen` / `triggered_at` 等） |
| 字节单位 | 内存/磁盘为字节（`mem_used` 等）；网速为 **Bytes/s**（`net_rx` / `net_tx`） |
| 错误响应 | `{"message": "..."}` + 对应 HTTP 状态码 |
| 传输压缩 | 支持 gzip（带 `Accept-Encoding: gzip` 即压缩，JSON 通常省 80%+、JS 省 60%）；压缩响应不返回 `Content-Length`（按 chunked 传输），带 `Range` 的请求不压缩；OkHttp/浏览器自动处理 |
| 静态缓存 | `/assets/*`（文件名带内容 hash）返回 `Cache-Control: max-age=31536000, immutable`；入口 HTML 为 `no-cache` |
| CORS | `Access-Control-Allow-Origin: *` 且放行 `Authorization` 头，跨域直连可用；但**未开放 `Allow-Credentials`**，浏览器跨域请求无法携带会话 Cookie——跨域集成请改用 Bearer API Key |
| 开发代理 | Vite 已配置 `/api` → `http://127.0.0.1:8080`（含 WebSocket） |

**鉴权（三种凭证）**

| 凭证 | 面向 | 携带方式 | 有效期 |
|---|---|---|---|
| 会话 Cookie | 浏览器面板 | `Cookie: cc_session=...`（HMAC 签名） | 7 天 |
| API Key | App / 脚本 / 第三方集成 | `Authorization: Bearer cck_...` | 不过期；**支持多条**，单条吊销互不影响 |
| 节点 Token | Agent 上报与下载 | `X-CC-Node-Token: <节点Token>` | 不过期，轮换即失效 |

- `POST /report` / `POST /register` / `GET /pool/:id/download`：机器身份，请求头 `X-CC-Node-Token`；**未批准（pending）节点返回 403**。
- 用户身份分 `admin` / `view` 两级：
  - **后台写操作**（节点增删改、规则、安全设置、API Key 管理、install-script）必须 `admin`；
  - **前台读操作**（summary/nodes/history/detail/alerts/WS）：默认开放；管理员在后台开启「访问密码」后需 `view` 或 `admin` 会话；
  - 节点列表/详情对非管理员**隐藏 Token**，pending 节点仅管理员可见。
- **API Key**（见第 11 节）：与用户身份等价，`scope=admin` 可读写、`scope=view` 只读；服务端只存 SHA-256，
  明文仅在生成/轮换的响应里返回一次。非浏览器客户端无需托管 Cookie，且**不受 CSRF Origin 校验约束**
  （该凭证由客户端显式设置，浏览器不会自动附带）。两种凭证可同时存在，判定时 API Key 优先。
- 认证接口见第 11 节；首次使用需先 `POST /auth/setup` 设置管理员密码。

---

## 2. 时间范围参数（详情页历史图表）

`range` 取值（详情页需求：实时 + 1小时 / 24小时 / 7天 / 30天 / 半年 / 一年）：

| range 取值 | 页面标签 | 数据来源表 | 采样粒度 | 数据保留 |
|---|---|---|---|---|
| `1h` | 1小时 | `metrics_5s` | 5 秒 | 48 小时 |
| `24h` | 24小时 | `metrics_1m` | 1 分钟 | 35 天 |
| `7d` | 7天 | `metrics_1m` | 1 分钟 | 35 天 |
| `30d` | 30天 | `metrics_1h` | 1 小时 | 400 天 |
| `180d` | 半年 | `metrics_1h` | 1 小时 | 400 天 |
| `365d` | 一年 | `metrics_1h` | 1 小时 | 400 天 |

- 默认 `range=24h`；非法值返回 **400**。
- 查询接口会在请求前补齐最近的降采样窗口（roll-on-read），**刚上报的数据立即可见**，无需等待后台聚合。

**“当前实时信息”不走历史接口**，来源有两个：

1. `GET /nodes/:id/detail` 的 `latest` 字段（REST 拉取）；
2. WebSocket `type=metrics` 消息（推送，见第 8 节）。

---

## 3. 采集端上报

### POST /report

Agent / 脚本周期上报指标。

**请求头**：`X-CC-Node-Token: <节点Token>`，`Content-Type: application/json`

**请求体**（全部字段可选，缺省按 0 处理）：

| 字段 | 类型 | 说明 |
|---|---|---|
| `cpu` | number | CPU 使用率 % |
| `mem_used` / `mem_total` | int | 内存已用 / 总量（字节） |
| `disk_pct` | number | 主分区占用 % |
| `net_rx` / `net_tx` | int | 下行 / 上行速率（Bytes/s） |
| `load1` | number | 1 分钟负载 |
| `uptime` | int | 运行时长（秒） |
| `disk_read` / `disk_write` | int | 磁盘读 / 写速率（Bytes/s，进历史表） |
| `per_cpu` | number[] | 每核使用率 %（进历史表；缺省不落盘） |
| `detail` | object | 可选：系统详情 JSON，原样保存并在详情接口返回 |
| `live` | object | 可选：实时明细快照（每核 CPU / 内存细分 / 分网卡速率 / 磁盘 I/O / 温度 / 进程 Top5），服务端缓存并 WS 推送 |
| `hardware` | object | 可选：硬件清单（CPU / 内存 / 主板 BIOS / 磁盘型号 / 网卡 / 系统），详情页「硬件详情」弹窗展示 |
| `agent_version` | string | 可选：Agent 自身版本（程序池匹配 / 可升级提示 / OTA 依据） |
| `agent_goos` / `agent_arch` | string | 可选：目标平台，如 `linux` / `amd64`（ARM 细分为 `armv7` 等，与池产物命名对齐） |
| `public_ip` | string | 可选：Agent 自查的公网 IP（服务端看到的源 IP 为内网时兜底） |
| `boot_events` | array | 可选：开关机记录 `[{ts, type:"on"|"off"}]`，服务端按 `(node_id, ts, type)` **插入去重** |
| `agent_mem` | int | 可选：Agent **自身**内存占用（字节），用于排查采集端吃内存 |
| `temps`（在 `live` 内） | array | 可选：`[{label, value}]`。服务端自动取最高值写入 `temp_max`，供温度告警与曲线使用 |

> `node_id` 与 `ts` 由服务端按 Token 和接收时间**覆盖**，客户端传入无效。

**响应**：`{"ok": true, "ts": 1791558543}`；当 **OTA 总开关开启**且池中存在匹配的新版本时附带升级指令：

```json
{ "ok": true, "ts": 1791558543,
  "upgrade": { "version": "0.2.0", "url": "/api/v1/pool/3/download",
               "sha256": "…64位hex…", "size": 7340032 } }
```

- 下发条件：开关开启 + 池中 `(component=agent, goos=agent_goos, arch=agent_arch, format=bin)` 最新条目版本 ≠ `agent_version`；**OpenWrt 节点不下发**（仅面板提示）。
- Agent 下载 `url` 时携带 `X-CC-Node-Token`，校验 `sha256` 通过后自替换重启，失败回滚旧版本（详见第 21 节）。

**示例**

```bash
curl -X POST http://master:8080/api/v1/report \
  -H "X-CC-Node-Token: 86517ce4..." \
  -H "Content-Type: application/json" \
  -d '{"cpu":23.5,"mem_used":3221225472,"mem_total":8589934592,
       "disk_pct":47.2,"net_rx":1250000,"net_tx":320000,
       "load1":0.8,"uptime":93784,
       "detail":{"hostname":"nas01","kernel":"6.6.32","cpu_model":"N100",
                 "arch":"x86_64","mounts":[{"mountpoint":"/","pct":47}],
                 "ifaces":[{"name":"eth0","mac":"aa:bb:..","ipv4":"192.168.1.10"}]}}'
```

**错误**：`401` Token 无效；`403` 节点被停用（`enabled=false`）或待批准（`pending approval`）；`400` JSON 非法。

---

## 4. 全局聚合（首页）

### GET /summary

```json
{
  "total": 6,
  "online": 5,
  "offline": 1,
  "alerting": 1,
  "ws_clients": 2,
  "alerts": [
    {
      "id": 3, "node_id": 3, "node_name": "OpenWrt 软路由",
      "metric": "cpu", "level": "warning", "value": 92.4,
      "status": "firing", "triggered_at": 1791558000, "resolved_at": 0
    }
  ]
}
```

- `alerts`：最近 20 条 `firing` 告警；**无告警时该字段为 `null`（不是 `[]`）**，前端需判空。
- `ws_clients`：当前 WebSocket 连接数（观测用）。

---

## 5. 节点接口

### GET /nodes

节点列表（含最新指标），首页卡片数据源。

**可选分页**：`?limit=<n>&offset=<m>`。不传时返回全部（与旧版行为一致，响应里也没有 `total` 字段）；
传 `limit` 后响应附带 `total` / `limit` / `offset`，便于节点很多（200+）时分批加载。

```bash
curl -s "http://<master>:8080/api/v1/nodes?limit=50&offset=0" -H "Authorization: Bearer $KEY"
# → {"nodes":[...50 条...],"total":213,"limit":50,"offset":0}
```

```json
{
  "nodes": [
    {
      "id": 1, "name": "主服务器", "os_type": "linux", "access_mode": "agent",
      "address": "192.168.1.10", "public_ip": "120.236.10.88", "port": 0,
      "token": "86517ce4...",
      "enabled": true, "approved": true, "direction": "push",
      "report_interval": 5, "sort_order": 1,
      "agent_version": "0.1.0", "agent_goos": "linux", "agent_arch": "amd64",
      "upgrade": { "version": "0.2.0", "pool_id": 3 },
      "status": "alert",
      "last_seen": 1791558543, "created_at": 1791558000,
      "latest": {
        "node_id": 1, "ts": 1791558543,
        "cpu": 23.5, "mem_used": 3221225472, "mem_total": 8589934592,
        "disk_pct": 47.2, "net_rx": 1250000, "net_tx": 320000,
        "load1": 0.8, "uptime": 93784,
        "disk_read": 1500000, "disk_write": 800000, "per_cpu": [12.5, 8.3]
      }
    }
  ]
}
```

**`status` 展示语义**（服务端已计算好，前端直接用）：

| 值 | 含义 | 建议显示 |
|---|---|---|
| `online` | 在线且无告警 | 绿色 |
| `alert` | 在线但存在 firing 阈值告警 | 红/橙（脉冲动画） |
| `offline` | 离线（超过 3×上报周期未上报） | 灰色 |

- `latest` 在节点从未上报时**整个字段缺失**（`omitempty`），而不是 `null`。
- `mem_pct`（内存使用率 %）**由服务端算好下发**（`mem_pct` 字段），前端/App 直接用；`mem_total=0` 时为 0。
- `mem_pct`（内存使用率 %）由前端计算：`mem_used / mem_total * 100`。
- `public_ip`：公网 IP（优先取上报连接的源 IP，为内网时取 Agent 上报值），无则空串；地址行按“上内网、下公网”两行展示。**SSH 节点**由服务端在目标机执行查询命令（10 分钟一次，best-effort）；**API 节点**无自动获取，用 `PUT /nodes/:id` 的 `public_ip` 手动设置（管理页节点表「公网IP」按钮）。
- `agent_version` / `agent_goos` / `agent_arch`：Agent 身份元数据，旧版 Agent 为空串。
- `approved`：`false` 表示自注册（Join Key）后待批准，**仅管理员可见**；管理员在后台创建的节点默认 `true`。
- `group_tag`：**分组标签**（自由文本，≤24 字符，空串=未分组）。前端按它分区展示，便于把「家里 / 机房A / 云主机」分开；服务端不做分组校验，纯展示用途。
- `mute_until`：**告警静默截止时间**（unix 秒，0=未静默）。静默期内告警照常产生并记录，
  但**不发通知**——用于重启/升级等维护窗口，避免刷屏。
- `agent_mem`：Agent 自身内存占用（字节，0=未知）。**不是主机内存**，用于判断采集端自身开销。
- `last_error` / `last_error_at`：**最近一次采集失败的原因**（SSH 连接失败 / DSM 登录失败 / PVE API 报错等）与时间，
  采集成功后自动清空。用于向用户解释「为什么离线/没有数据」，Agent 模式节点通常为空。
- `upgrade`：程序池存在匹配的不同版本时出现 `{"version": "...", "pool_id": n}`，否则字段缺省（前端显示“可升级”徽标）。

### POST /nodes

创建节点，**Token 在响应中返回**。

请求：`{"name":"主服务器","os_type":"linux","access_mode":"agent","address":"192.168.1.10","port":0,"report_interval":5,"enabled":true,"direction":"push","group_tag":"机房A"}`

- `name` 必填；`os_type` 默认 `linux`（可选 `linux|windows|nas|openwrt|darwin|pve|esxi`）；
- `access_mode` 默认 `agent`（可选 `agent|ssh|api`）；`direction` 默认 `push`（可选 `push|pull`）。
- `group_tag` 可选：分组标签（≤24 字符，空=未分组），仅用于前端分区展示。
- `access_mode=ssh|api` 时的凭据字段（AES-256-GCM 加密存 `auth_enc`，**任何接口不回传明文**）：
  `username`、`password`、`private_key`、`node_name`（PVE 节点名），见第 13 / 17 节。
- 响应：`{"node": {...含 token ...}}`

### GET /nodes/:id

单节点信息（含 `latest`），响应 `{"node": {...}}`。

### PUT /nodes/:id

部分更新，请求体字段均为可选：

```json
{ "name": "新名字", "address": "10.0.0.2", "enabled": false,
  "report_interval": 10, "username": "root", "password": "新密码",
  "approved": true, "rotate_token": true }
```

- `rotate_token: true` 轮换上报 Token（旧 Token 立即失效）。
- `group_tag: "机房A"` 设置分组（**传空串=取消分组**，不传=保持不变）。
- `mute_minutes: 60` 开启告警静默 60 分钟（**0=解除**，上限 7 天）。静默期内告警照常记录但不发通知。
- 凭据字段 `username` / `password` / `private_key` / `node_name`：**不传的字段保持不变**，`password` 传空串 `""` 表示清除。
- `approved: true|false` 批准 / 撤回自注册（pending）节点。
- `rotate_host_key: true` 重置 SSH 的 TOFU 主机指纹（见第 13 节）。
- `public_ip: "1.2.3.4"`（可选）手动指定公网 IP（留空清除）：SSH 模式默认由服务端在目标机上每 10 分钟自动查询；**API 模式（PVE/群晖）无法自动获取**，用此字段手动设置。
- 响应：`{"node": {...}}`（含最新 token）

### POST /nodes/:id/upgrade（管理员）

手动触发单节点 OTA：标记后**该节点下次上报时即下发升级指令，不受全局 OTA 开关限制**（OpenWrt 节点拒绝）。

- 前置：节点为 Agent 接入、已上报 `agent_version/goos/arch`、池中有匹配的 `bin` 条目且版本不同；
- 响应：`{"ok": true, "version": "0.2.0", "sha256": "…"}`；错误 400 + 原因（非 Agent / OpenWrt / 无匹配池条目 / 已是最新）。

### DELETE /nodes/:id

删除节点及其全部历史/告警/事件。响应 `{"ok": true}`。

---

## 6. 设备详情（实时信息）

### GET /nodes/:id/detail

详情页首屏数据：**当前实时信息**（`latest` 定宽样本 + `live` 实时明细）+ 系统详情 + **硬件清单** + 近期事件 + 告警。

```json
{
  "node": { "...同 GET /nodes 单节点": "..." },
  "latest": {
    "node_id": 1, "ts": 1791558543,
    "cpu": 42.5, "mem_used": 1000, "mem_total": 4000,
    "disk_pct": 33, "net_rx": 100, "net_tx": 50,
    "load1": 0.2, "uptime": 60
  },
  "detail": {
    "hostname": "nas01", "kernel": "6.6.32",
    "cpu_model": "Intel N100", "arch": "x86_64",
    "mounts": [{ "mountpoint": "/", "pct": 47 }],
    "ifaces": [{ "name": "eth0", "mac": "aa:bb:cc", "ipv4": "192.168.1.10" }]
  },
  "events": [ { "id": 9, "node_id": 1, "ts": 1791558543, "event": "online" } ],
  "boot_events": [ { "ts": 1791550400, "type": "on" }, { "ts": 1791546800, "type": "off" } ],
  "alerts": [ { "id": 3, "metric": "cpu", "status": "firing", "...": "..." } ]
}
```

- 响应另含 `"live"`（实时明细：`per_cpu` / `mem` 细分 / `nics` / `disk_io` / `temps` / `procs`，服务端内存缓存）
  与 `"hardware"`（硬件清单：`cpu` / `memory` / `board` / `disks` / `nics` / `system`）。
- 空值形态（实测）：`latest` / `live` / `detail` / `hardware` / `events` 无数据时是 `null`；`boot_events` / `alerts` 无数据时是 `[]`。
- `boot_events`：开关机记录（新→旧，最多 50 条；`type`: `on` 开机 / `off` 关机），来源 Agent 上报并按 `(node_id, ts, type)` 去重。
- `detail` / `hardware` 结构由采集端定义、服务端原样透传，字段以实际为准。

### GET /nodes/:id/history?range=<range>

历史时序（时图表数据源），`range` 见第 2 节。

```json
{
  "range": "24h",
  "source_table": "metrics_1m",
  "interval_sec": 60,
  "from": 1791472143,
  "to": 1791558543,
  "points": [
    { "ts": 1791558500, "cpu": 42.5, "mem_used": 1000, "mem_total": 4000,
      "disk_pct": 33, "net_rx": 100, "net_tx": 50, "load1": 0.2, "uptime": 60,
      "disk_read": 1500000, "disk_write": 800000, "per_cpu": "[12.5,8.3]" }
  ]
}
```

- `points` 按 `ts` 升序；无数据时为 `[]`（不是 `null`）。
- `mem_pct` 同样由服务端算好（`mem_pct` 字段），无需前端再除。
- `disk_read`/`disk_write` 为聚合窗口平均（Bytes/s）；`per_cpu` 为 JSON 数组**文本**（聚合窗口取窗口内首个非空样本），空串=该窗口无每核数据。
- **CSV 导出**：追加 `&format=csv` 返回 `text/csv`（`Content-Disposition: attachment`），列：`ts,datetime,cpu_pct,mem_used,mem_total,disk_pct,net_rx_bps,net_tx_bps,load1,uptime_sec,disk_read_bps,disk_write_bps,per_cpu`；文件名 `node<id>_<range>.csv`。
- **错误**：`400` range 非法；`404` 节点不存在。
- 前端画图建议：1 年视图最多 8760 点，ECharts 可直接渲染；内存百分比由 `mem_used/mem_total` 计算。

### GET /nodes/:id/install-script?type=sh|ps1|openwrt

返回 `text/plain` 一键安装指令（含该节点 Token）。默认 `sh`。

---

## 7. 告警接口

### GET /alerts?status=firing|resolved|all

默认 `all`，按记录 id 倒序（id 自增，近似触发时间倒序），最多 200 条。

```json
{ "alerts": [ { "id": 3, "node_id": 3, "node_name": "OpenWrt 软路由",
  "metric": "cpu", "level": "warning", "value": 92.4,
  "status": "firing", "triggered_at": 1791558000, "resolved_at": 0 } ] }
```

- `metric` 枚举：`cpu` | `mem_pct` | `disk_pct` | `offline`
- `level`：`warning`（阈值突破）| `critical`（≥95 或离线）
- 离线判定：超过 `3 × report_interval` 秒未上报（最小 15 秒），恢复上报自动 `resolved`。

### 告警规则

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/alerts/rules` | 全部规则 `{"rules":[...]}` |
| POST | `/alerts/rules` | 创建，body：`{"node_id":0,"metric":"cpu","op":">","threshold":80,"delay_sec":300,"enabled":true}`（`node_id=0` 全局；**`delay_sec`=触发延时秒数：连续超阈值满 N 秒才告警，0=立即**，5 分钟=300） |
| PUT | `/alerts/rules/:id` | 更新规则，body 同 POST（全字段覆盖；404=规则不存在） |
| DELETE | `/alerts/rules/:id` | 删除规则 |

- `metric` 枚举：`cpu` | `mem_pct` | `disk_pct` | `load1` | `net_rx` | `net_tx` | `temp`；
  `threshold` 上限按指标量纲不同（百分比类 0~100、`load1` 0~1000、网速类 0~1TiB/s、`temp` 0~150℃）；
  `op` 为 `>`（默认）或 `>=`。
- `temp` 取样本里的最高温度（服务端从 `live.temps` 提取）；**节点无温度传感器时为 0**，不会误触发。
- 触发时机：**上报时同步评估**，前端在收到 `metrics` WS 消息后立刻查 `/alerts` 即可看到新告警。

---

## 8. WebSocket 实时推流

### 连接

```
ws://<master>:8080/api/v1/ws
```

- **鉴权**：未开启「访问密码」时匿名可连；开启后需 `view`/`admin` 凭证——浏览器用会话 Cookie，
  App / 脚本用请求头 `Authorization: Bearer cck_...`，或 `?access_token=cck_...`（部分 WS 客户端无法自定义头）。
  仅在握手时校验一次，连上后会话过期不会主动断开。
- 建议前端断线后指数退避重连（1s / 2s / 4s，上限 30s）。
- Vite 开发环境已代理 `/api/v1/ws`。

### 消息格式

均为 JSON 文本帧，通过 `type` 区分：

**① 指标更新（核心）**

```json
{
  "type": "metrics",
  "node_id": 1,
  "data": {
    "node_id": 1, "ts": 1791558543,
    "cpu": 23.5, "mem_used": 3221225472, "mem_total": 8589934592,
    "disk_pct": 47.2, "net_rx": 1250000, "net_tx": 320000,
    "load1": 0.8, "uptime": 93784,
    "disk_read": 1500000, "disk_write": 800000, "per_cpu": [12.5, 8.3]
  }
}
```

**② 实时明细（live）** —— 与 metrics 成对推送，详情页实时区消费：

```json
{
  "type": "live",
  "node_id": 1,
  "data": {
    "ts": 1791558543, "cpu_total": 23.9, "per_cpu": [30.1, 12.5],
    "load1": 1.2, "load5": 0.9, "load15": 0.7,
    "mem": {"total": 1, "used": 1, "available": 1, "free": 1, "buffers": 0, "cached": 0, "swap_total": 0, "swap_used": 0, "percent": 42.1},
    "disk_io": {"read_bps": 1024, "write_bps": 2048, "read_count": 1, "write_count": 1},
    "nics": [{"name": "eth0", "rx_bps": 1000, "tx_bps": 500, "mac": "...", "ipv4": "..."}],
    "temps": [{"label": "core0", "value": 45.0}],
    "procs": [{"pid": 123, "name": "app", "cpu": 2.5, "mem": 1.2}]
  }
}
```

**③ 节点变更**

```json
{ "type": "node_added",   "node_id": 7 }
{ "type": "node_removed", "node_id": 7 }
{ "type": "node_pending", "node_id": 8 }
```

**④ 上下线状态（实时推送）**

```json
{ "type": "node_status", "node_id": 7, "status": "online" }
{ "type": "node_status", "node_id": 7, "status": "offline" }
```

`online`：节点从离线恢复（采集入库 / Agent 上报触发）；`offline`：离线巡检判定（10 秒周期，超过 3×上报周期未收到数据）。前端收到后直接更新该节点状态灯并刷新 `/summary`。

**⑤ 告警事件（实时推送）**

```json
{ "type": "alert_firing",   "node_id": 7, "metric": "cpu", "level": "warning", "value": 92.5 }
{ "type": "alert_resolved", "node_id": 7, "metric": "cpu" }
```

- `metric` 取值同告警接口：`cpu` / `mem_pct` / `disk_pct` / `load1` / `net_rx` / `net_tx` / `offline`。
- 离线告警的 `value` 为「已离线秒数」。
- 收到后刷新 `/alerts?status=firing` 或就地更新告警列表；**这比轮询更及时**，建议前端优先使用。

### 推送时机

每次 `POST /report`（或 SSH/API 采集入库）成功后立即推送该节点的 `metrics` 与 `live`（含实时明细时）。
`node_added` / `node_removed` / `node_pending` 为管理端增删与自注册事件；**上下线通过 `node_status` 实时推送**，30s 轮询仅作断线兜底。

### 前端建议用法

```
首屏：GET /summary + GET /nodes（含 latest）
之后：WS metrics → 按 node_id 更新对应卡片
告警：收到 metrics 后按需拉 /alerts?firing（或每 30s 轮询）
管理操作（增删改节点）后：重新 GET /nodes
```

---

## 9. 错误码汇总

| 状态码 | 场景 |
|---|---|
| 400 | 参数错误（JSON 非法 / id 非法 / range 非法 / name 缺失 / API Key 的 scope 非法 / 规则指标或阈值越界） |
| 401 | `X-CC-Node-Token` 无效 / `Authorization: Bearer` 无效、已吊销 / 面板会话缺失或过期（开启访问密码后的读写接口） / 用 `view` 权限的 Key 调写接口 |
| 403 | 节点已停用（`enabled=false`） / 跨站 Origin 拒绝（CSRF 防护，**仅对无 Bearer 的浏览器凭证生效**；WS 握手同样校验 Origin） / join key 错误 |
| 404 | 节点 / 规则 / API Key 不存在 |
| 429 | **登录失败过多被临时锁定**（连续 5 次 → 1 分钟，10 次 → 5 分钟，15 次 → 30 分钟）；响应带 `Retry-After` 秒数 |
| 500 | 服务端内部错误（DB 等） |

---

## 10. 对接注意与待实现项

1. **详情页 range 映射**：页面「半年」→ `180d`，「一年」→ `365d`。
2. **实时信息**：详情页顶部“当前实时”卡片用 `detail.latest` 初始化，之后由 WS `metrics` 刷新。
3. **采集模式均已实现**：模式 B（SSH）见第 13 节、正向连接（`direction=pull`）见第 14 节、ESXi 见第 15 节、
   PVE 见第 17 节、群晖 DSM 见第 18 节，数据统一落到 `/nodes`、`/history` 同一套结构，前端无需区分。
4. 仍待完善：
   - `install-script` 返回的安装指令中，Agent 二进制下载地址目前是占位注释（正式分发走程序池 `GET /pool/:id/download`，需节点 Token）。

---

## 11. 认证与入网审批接口（新增）

均为 `application/json`；会话通过 Set-Cookie 下发（HttpOnly），浏览器自动携带。

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/auth/status` | 公开 | `{"admin_set": bool, "view_enabled": bool}` |
| GET | `/auth/whoami` | 公开 | `{"scope": "" \| "view" \| "admin", "via": "" \| "cookie" \| "key"}` |
| POST | `/auth/setup` | 公开（仅一次） | `{"password": "≥6位"}` 设置管理员密码并直接登录 |
| POST | `/auth/login` | 公开 | `{"password":"...","scope":"admin"\|"view"}` → 下发会话 Cookie |
| POST | `/auth/logout` | 任意 | 清除会话 |
| POST | `/auth/refresh` | 会话 | 会话续期：有效 Cookie 换新 Cookie（再延 7 天）；会话已过期返回 401。**Bearer Key 不过期，无需调用** |
| GET | `/auth/metrics-token` | admin | `{"set":bool}` — `/metrics` 抓取令牌是否已设置（不回传明文） |
| PUT | `/auth/metrics-token` | admin | `{"token":"..."}` 设置/替换；**空串=关闭校验**（`/metrics` 恢复开放） |
| GET | `/backup` | admin | **配置导出**：下载 JSON（节点/规则/站点/展示类设置），**不含** Token、SSH 凭据、API Key 哈希、各类密码 |
| POST | `/restore` | admin | **配置恢复**：body 为备份 JSON。**只新增不覆盖**（同名节点/同参数规则跳过）；导入的 Agent 节点生成**新 Token**；规则按 `node_name` 映射到新节点 ID |
| GET | `/audit?limit=100` | admin | **操作审计**：`{"logs":[{id,ts,actor,method,path,status,ip}]}`，记录管理员的 POST/PUT/DELETE（成功才记），保留 90 天 |
| POST | `/auth/password` | admin | `{"old_password","new_password"}` 修改管理员密码 |
| GET | `/auth/view` | admin | `{"enabled":bool,"password_separate":bool}` |
| PUT | `/auth/view` | admin | `{"enabled":bool,"password":"可选,≥6位"}` 前台访问密码开关（留空=用管理员密码） |
| GET | `/auth/join-key` | admin | `{"join_key":"..."}` 不存在自动生成 |
| POST | `/auth/join-key/rotate` | admin | 重置 Join Key（旧值立即失效） |
| GET | `/auth/api-key` | admin | `{"set":bool,"scope":"admin"\|"view"}` — 兼容接口，仅反映 `default` 凭证；**不返回明文** |
| POST | `/auth/api-key/rotate` | admin | **兼容接口**，重置 `default` 凭证：`{"scope":"admin"\|"view"}`（可省，默认 admin）→ `{"api_key":"cck_...","scope":"..."}`，**明文仅此一次** |
| DELETE | `/auth/api-key` | admin | **兼容接口**，吊销 `default` 凭证 |
| GET | `/auth/api-keys` | admin | **多凭证列表**：`{"keys":[{"id","name","scope","created_at"}]}`（不含明文/哈希） |
| POST | `/auth/api-keys` | admin | **新建凭证**：`{"name","scope":"admin"\|"view"}` → `{"key":{...},"api_key":"cck_..."}`，明文仅此一次 |
| DELETE | `/auth/api-keys/:id` | admin | **吊销指定凭证**（只影响该条，其它设备不受影响） |
| POST | `/register` | Join Key | 自注册入网：`{"join_key","name?","os_type?","address?"}` → `{"node_id","token","approved":false}` |
| PUT | `/nodes/:id` | admin | body 增加 `"approved": true/false`（批准 / 撤回） |

**API Key（非浏览器客户端）**

推荐用**多凭证**接口：每台设备 / 每个脚本各签一条，吊销互不影响。

```bash
# 1) 为每台设备签发（管理员会话；面板「API Key」区块亦可）
curl -s -b ck.txt -X POST http://<master>:8080/api/v1/auth/api-keys \
     -H "Content-Type: application/json" -d '{"name":"我的手机","scope":"view"}'
# → {"key":{"id":2,"name":"我的手机","scope":"view","created_at":...},"api_key":"cck_3f2a..."}

# 2) 之后的请求只需 Bearer 头，无需任何 Cookie
curl -s http://<master>:8080/api/v1/nodes -H "Authorization: Bearer cck_3f2a..."

# 3) WebSocket：请求头或 query 二选一（部分 WS 客户端无法自定义头）
ws://<master>:8080/api/v1/ws                                    # 头：Authorization: Bearer cck_...
ws://<master>:8080/api/v1/ws?access_token=cck_3f2a...           # query 兜底（会进访问日志，慎用公网）

# 4) 查看 / 吊销
curl -s -b ck.txt http://<master>:8080/api/v1/auth/api-keys
curl -s -b ck.txt -X DELETE http://<master>:8080/api/v1/auth/api-keys/2
```

- 仅存 SHA-256 哈希，明文丢失只能重新生成；`scope=view` 的 Key 调用写接口返回 **401**。
- **吊销某条只影响该设备**，其它 Key 继续可用（旧版单 Key 的 `rotate` 会踢掉所有设备，已改为兼容接口 `default` 一条）。
- 旧的 `/auth/api-key`、`/auth/api-key/rotate`、`DELETE /auth/api-key` 仍然可用，
  它们操作的是名为 `default` 的那条凭证，便于老客户端平滑过渡。

**入网审批流程**

```
管理员生成 Join Key（后台安全设置）
  → 设备调用 POST /register 得到 token（approved=false）
  → Agent 携 token 上报 → 403 {"message":"pending approval"}
  → 管理员在节点管理点「批准」（PUT /nodes/:id {"approved":true}）
  → 上报放行，节点进入概览
```

管理员在面板直接「添加节点」创建的节点默认已批准（approved=true），无需走上述流程。

**错误码补充**：`401`（无会话 / 密码错误 / 节点 Token 或 API Key 无效）、`403`（pending approval、join key 错误、节点停用）、`400`（密码 <6 位、重复初始化、API Key 的 scope 非法）。

---

## 12. 告警通知接口（新增）

| 方法 | 路径 | 权限 | 说明 |
|---|---|---|---|
| GET | `/notify/config` | admin | `{"enabled","channel":"pushplus\|feishu\|dingtalk\|wecom\|webhook\|smtp\|ntfy\|telegram","token_set","url_set",smtp_host,port,user,from,to,smtp_pass_set}`（密文不回传） |
| PUT | `/notify/config` | admin | `{"enabled","channel","token?","url?",smtp_host?,smtp_port?,smtp_user?,smtp_pass?,smtp_from?,smtp_to?}` — 密文字段**不传或空 = 保持不变** |
| POST | `/notify/test` | admin | 无视开关强制发一条测试消息；失败返回 502 + 原因（结果落库） |
| GET | `/notify/logs` | admin | 最近发送记录 `{"logs":[{id,ts,channel,title,ok,error}]}`（最多 50 条，保留 30 天） |

- **触发时机**（与告警阈值规则联动，状态变化时各发一条）：指标突破阈值 → 「告警触发 · 节点名」；指标恢复 → 「告警恢复」；节点离线 → 「节点离线」；恢复在线 → 「节点恢复在线」。
- `pushplus`：POST `https://www.pushplus.plus/send`，body `{token,title,content}`，关注 pushplus.plus 公众号后填 Token 即可收微信。
- `feishu` / `dingtalk`：POST 配置的机器人 Webhook URL（飞书 `open.feishu.cn/open-apis/bot/v2/hook/...`、钉钉 `oapi.dingtalk.com/robot/send?access_token=...`），
  body 分别为 `{"msg_type":"text","content":{"text":"标题\n内容"}}` 与 `{"msgtype":"text","text":{"content":...}}`。
- `webhook`：POST 配置的 URL，body `{title,content,source:"CC_DashBoard"}`。
- `smtp`：net/smtp 直发——465 隐式 TLS、587 STARTTLS（协商）、25 明文；PLAIN 认证（Go 强制 TLS 上下文）；主题 RFC 2047（=?UTF-8?B?）、正文 base64；收件人逗号分隔多址；`smtp_from` 空则用用户名。
- `ntfy`：**手机推送，无需自建 App**——`url` 填主题地址（`https://ntfy.sh/<topic>` 或自建实例），
  `token` 可选（受保护主题的访问令牌）。手机装 ntfy App 订阅同一主题即可收到系统通知；
  发送方式为 POST 纯文本（`Title` 头带标题），实测可用。适合替代 FCM 这类需要客户端的方案。
- `telegram`：Bot API——`token` 填 bot token（@BotFather 获取），`url` 填 `chat_id`
  （个人 `123456789`，群组 `-1001234567890`）。需先把 bot 拉进目标会话并发一条消息。
- 通知为异步发送，**失败自动重试 3 次（立即 / +1s / +3s）**；成功/最终失败均写入 `notify_logs`（后台「告警通知」区可见），不阻塞采集链路。

---

## 13. 模式 B（SSH）接入说明（新增）

目标机**无需安装任何 Agent**，Master 定时（默认 15s/次）通过 SSH 执行内联采集命令，复用 `/report` 之后的入库/告警/WS 链路。

**创建节点**（`POST /nodes`）：

```json
{
  "name": "小院路由器", "os_type": "linux", "access_mode": "ssh",
  "address": "192.168.6.1", "port": 22,
  "username": "root", "password": "******"
}
```

- 也支持 `"private_key": "-----BEGIN OPENSSH PRIVATE KEY-----..."`（与二选一或并存）。
- 凭据序列化后 **AES-256-GCM 加密**存入 `auth_enc`，任何接口**不回传**明文。
- `PUT /nodes/:id` 更新凭据：传 `username` / `password` / `port` 字段即覆盖，**不传的字段保持不变**（密码传空串 `""` 表示清除）。
- 连接策略：**长连接复用**（连接缓存 + 会话探活，失效自动重拨；采完不断开），拨号超时 10s。
- **主机密钥（TOFU）**：首次连接自动记录 SHA256 指纹入库；之后不匹配即拒绝并提示重置方法：`PUT /nodes/:id {"rotate_host_key":true}`。
- **认证方式**：密码（`password` + **`keyboard-interactive` 双方法**——ESXi/部分设备禁用 password 只留 kbd-interactive，已兼容）或私钥（`private_key`）。
- 采集内容：Linux 解析 `/proc/stat|meminfo|loadavg|net/dev` + `df`（CPU/网速跨轮差分，首轮为开机以来均值）；Windows 经 `-EncodedCommand` 执行 PowerShell CIM 查询（CPU/内存/磁盘/运行时长；v1 暂无分网卡速率）。
- 主机密钥：TOFU 首连信任（见第 13 节），可用 `rotate_host_key` 重置；ESXi 若连接报认证失败，确认已开 SSH 且密码正确（password/keyboard-interactive 均已支持）。

---

## 14. 正向连接（Agent listen，方向 pull）

反向（push）之外的第二种模式：**Agent 开端口等 Master 来拉**，适合服务端能主动访问 Agent 的内网环境。单节点二选一，创建时确定。

**节点字段**：`direction: "push"(默认) | "pull"`；`pull` 时 `port` = Agent 监听端口（默认 9100），`address` = Agent 所在主机 IP。

```json
POST /nodes
{ "name": "机房A宿主机", "os_type": "linux", "access_mode": "agent",
  "direction": "pull", "address": "192.168.1.50", "port": 9100 }
```

**Agent 侧**：

```bash
cc-agent -mode listen -listen :9100 -token <该节点Token>
# GET /metrics 返回最新采样 JSON；请求头 X-CC-Node-Token 不匹配 → 401
```

**Master 侧**：采集引擎每 5 秒扫描 `direction=pull` 的节点，按其 `report_interval` 节流，`GET http://address:port/metrics` 拉取后进入与 `/report` 相同的入库/告警/WS 链路。拉取超时 10s，失败不影响离线判定（由 last_seen 超时走离线巡检）。

---

## 15. ESXi 接入（模式 B 变体）

**添加步骤**：目标 ESXi 开启 SSH（Host 设置 → 服务 → SSH 启动）→ 面板添加节点：`os_type="esxi"`、`access_mode="ssh"`、地址 + root 密码。

采集实现：SSH 执行 `esxcli`（物理内存/运行时长/存储清单/网卡与吞吐）+ `esxtop -b -d 2 -n 1`（CPU/每核/负载/内存）。

- **真机校准（2026-10，ESXi 8 实测）**：
  - `esxcli system stats uptime get` 返回**微秒**（≥1e9 自动按微秒换算为秒，否则按秒）；
  - `esxtop -b` 输出 **PDH-CSV**（约 1 万列），列名含反斜杠转义（`Physical Cpu(_Total)\% Util Time`），须用 Contains+Suffix 组合匹配；
  - **CPU% 优先取 `% Util Time`**（与 ESXi 界面「CPU 使用率」同口径，真机 8.94≈9%），无 Util 列时回退 `% Processor Time`；每核同理；
  - `Cpu Load (1 Minute Avg)` 为 **0~1 比例**（0.10=10%），负载 = 比例 × 核数；个别版本为百分比按幅度自适应；
  - 内存已用 = `Machine MBytes - Free MBytes`；
  - **存储**：`esxcli storage filesystem list` → 挂载点明细（容量/已用/%，取最大 VMFS 卷为磁盘占用）；
  - **网络**：`esxcli network nic stats get` 的累计 `Bytes received/sent` 跨轮差分 → 吞吐；`nic list` 提供 MAC/速率/链路；vmk IPv4 按 MAC 关联；
  - 经典 worldID 表格格式作为回退（旧版本 ESXi）。
- 指标：**CPU%（总/每核）、内存%、负载、存储占用与挂载明细、网络吞吐与分网卡、运行时长、主机名**；`live` 实时明细（每核/分网卡速率/内存细分）经 WS + REST 双通道下发。
- 单测覆盖：CSV（Util/Processor 回退）、经典格式、微秒换算、filesystem/nic stats/ip 解析（均含真机样本）。

---

## 16. --install 交互式服务注册

```bash
cc-agent --install       # 问答：Master/Token/模式/监听端口/周期
                          # → 写入 exe 同目录 cc-agent.config.json
                          # → 注册服务（Windows=sc、Linux=systemd、OpenWrt=procd）
cc-agent --config <文件>  # 服务模式加载配置；显式命令行 flag 优先
cc-dashboard --install      # 问答：端口/数据目录/前端目录 → 同样注册服务
```

- 非管理员权限时自动降级：打印手动注册命令（sc/systemd ExecStart）后退出，配置文件仍会写入；
- 配置文件含 Token，权限 0600，勿外传。

---

## 17. PVE 接入（模式 C，Proxmox REST API）

**添加步骤**：PVE 数据中心 → 访问控制 → API Token（如 `root@pam!cc`，权限 Administrator 或 PVEAuditor）→ 面板添加节点：

```json
POST /nodes
{ "name": "PVE宿主机", "os_type": "pve", "access_mode": "api",
  "address": "192.168.1.10", "port": 8006,
  "node_name": "pve1",
  "username": "root@pam!cc", "password": "<Token密钥（=号后半段）>" }
```

- 凭据映射：`username` = Token 标识（`user@realm!tokenid`）、`password` = 密钥、`node_name` = PVE 节点名（hostname，API 路径 `/nodes/{node}` 需要），三者 AES 加密存 `auth_enc`，接口不回传。
- 采集：每 15s（可配）`GET /api2/json/nodes/{node}/status`（CPU/内存/运行时长/网络累计差分）+ `GET /api2/json/cluster/resources?type=vm`（**虚机/容器汇总**写入 `detail.vms`，详情页展示面板）。
- 鉴权头：`Authorization: PVEAPIToken=<标识>=<密钥>`；HTTPS 自签证书跳过校验（局域网定位）；PVE 默认端口 8006。
- 解析器有 httptest 单测（含鉴权失败路径）；`os_type=pve` 走模式 A（装 Agent）或模式 B（SSH，PVE 底层 Debian 直接可用）同样可行。

---

## 18. 群晖 Synology DSM 接入（模式 C 变体）

**添加步骤**：DSM 创建专用监控账号（**关闭两步验证**）→ 面板添加节点：`os_type="nas"`、`access_mode="api"`、地址 + 端口（**5001=HTTPS / 5000=HTTP**）+ DSM 账号密码。

- 认证：`SYNO.API.Auth`（version=3，兼容 DSM 6/7）登录换 SID，**SID 缓存 2 小时**，接口失效自动重登一次。
- 采集（**DSM 7.2.2 真机校准 2026-10-10**）：`SYNO.Core.System.Utilization` CPU=`user_load+system_load+other_load`（DSM7；DSM6 兼容 user/system/idle）、内存=`total_real/avail_real/buffer/cached`（**KB**）、**网络 rx/tx 为瞬时速率 B/s（非累计）**、`disk.total` 为 B/s+IOPS；`SYNO.Core.System` **method=info**（getinfo 已下线）→ `up_time`/`sys_temp`/机型；`SYNO.Core.Network` get → `server_name` 主机名；`SYNO.Core.System.Process` list → **进程 Top5**（cpu 0.1% 粒度、mem KB）；`SYNO.Storage.CGI.Storage` load_info → **卷容量**（disk_pct 与挂载点；`space.utilization` 是繁忙度非容量）。
- HTTPS 自签跳过校验；PVE 与群晖同用 `access_mode=api`，**按 `os_type` 分派**（`pve`→PVE API，`nas`→DSM API）。
- 每核 CPU：DSM 无 per-core 字段 → 面板每核区显示暂无数据（平台不支持）。
- 单测覆盖：DSM7/DSM6 双口径、网络速率直采、进程排序、存储容量、up_time 解析、登录与主机名缓存。

---

## 19. 构建与打包（build.ps1 第 5 步）

`powershell -ExecutionPolicy Bypass -File build.ps1` 依次：前端 → 嵌入 → 服务端 4 目标 → Agent 5 目标 → **打包**：

- **ipk**（OpenWrt）：`tools/mkipk` 纯 Go 构建（ipkg 格式：gzip 压缩的 tar，成员 = ./debian-binary + ./control.tar.gz + ./data.tar.gz；**不支持 Debian ar 归档**，opkg 解析会报 "Malformed package file"）；架构映射 mipsle→`mipsel_24kc`、armv7→`arm_cortex-a7`、arm64→`aarch64_cortex-a53`、amd64→`x86_64`（可用 `-arch` 参数按设备调整）；
- **deb/rpm**：检测到 `nfpm` 时自动打包 amd64/arm64（模板 `nfpm-agent.yml`）；安装：`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`；
- msi 不再需要——服务注册由 `--install` 方案覆盖（见第 16 节）。

---

## 20. 指标导出

### GET /metrics — Prometheus 抓取端点

`Content-Type: text/plain; version=0.0.4`，暴露**全部已批准节点**的最新指标（手写 exposition，无第三方依赖）：

| 指标 | 类型 | 说明 |
|---|---|---|
| `cc_node_up{id,name}` | gauge | 1=在线 0=离线 |
| `cc_node_cpu_percent` | gauge | CPU % |
| `cc_node_mem_percent` | gauge | 内存 % |
| `cc_node_disk_percent` | gauge | 主分区/主数据store % |
| `cc_node_load1` | gauge | 1 分钟负载 |
| `cc_node_net_rx_bytes_per_second` / `cc_node_net_tx_bytes_per_second` | gauge | 网络速率 |
| `cc_node_uptime_seconds` | gauge | 运行时长 |
| `cc_node_disk_read_bytes_per_second` / `cc_node_disk_write_bytes_per_second` | gauge | 磁盘 I/O（历史落盘列） |
| `cc_node_temp_max` | gauge | 最高温度 ℃（0=无传感器） |
| `cc_scrape_nodes_total` | gauge | 已批准节点总数 |

- 鉴权：**默认开放**（Prometheus 无法携带 Cookie）；可在面板「安全设置」里设置**抓取令牌**，
  设置后需带 `?token=<令牌>`（或 `Authorization: Bearer`），否则 401。留空保存即恢复开放。
  Prometheus 侧用 `params` 配置：

```yaml
  - job_name: cc_dashboard
    metrics_path: /metrics
    params:
      token: ["<令牌>"]
    static_configs:
      - targets: ['master:8080']
```
- Prometheus 配置示例：

```yaml
scrape_configs:
  - job_name: cc_dashboard
    metrics_path: /metrics
    static_configs:
      - targets: ['master:8080']
```

### GET /healthz — 健康检查

供容器 liveness/readiness 探针与反代上游检测，**无需鉴权**、不暴露业务数据：

```json
{ "status": "ok" }
```

- 数据库不可用时返回 `503` + `{"status":"degraded","db":"down"}`，便于编排系统摘除实例。
- 与 `/metrics` 一样不在 `/api/v1` 下，注意反代规则要放行。

---

## 21. 程序池与 Agent OTA（新增）

管理端上传 Agent / Dashboard 程序到"池"，节点按版本与架构对比给出可升级提示；打开 OTA 开关后 Agent 自动升级。

### GET /pool（管理员）

池清单 + OTA 开关 + 当前服务端版本：

```json
{ "items": [ { "id": 3, "component": "agent", "version": "0.2.0",
               "goos": "linux", "arch": "amd64", "format": "bin",
               "filename": "9f2c…_cc-agent-<版本>-linux-amd64", "sha256": "…",
               "size": 7340032, "note": "", "created_at": 1791558000 } ],
  "ota_enabled": false, "server_version": "0.1.0" }
```

- `component`: `agent` | `server`（Dashboard 服务端）；`format`: `bin` | `deb` | `rpm` | `ipk`。
- **版本比较规则**：不做 semver 排序，按 `(component, goos, arch)` 下**最新上传**条目与当前版本不一致即“可升级”。
- `server_version` 与池中最新 `server` 条目不一致时，管理页提示服务端有新版本（**仅提示，不自动升级**）。

### POST /pool/upload（管理员，multipart/form-data）

单文件上传，元数据由表单显式提供。字段：`file`（必填）+ `component`、`version`（必填）、`goos`、`arch`、`format`（默认 `bin`）、`note`。
文件流式落盘并计算 sha256，按内容地址命名；单文件上限 256MB。响应 `{"item": {...}}`。

### POST /pool/batch（管理员，multipart/form-data）——推荐

**一次性上传整批构建产物，按文件名自动识别元数据**：

- 字段：`files[]`（多文件，≤64 个）+ `version`（**兜底版本**：文件名能解析出版本时优先用文件名，仅当解析不出时才用此值，如被手工改名的 `cc-agent-linux-amd64`）；
- 识别规则（`build.ps1` 产物命名）：`cc-agent|cc-dashboard`（旧名 `cc-server` 仍兼容识别）→ 组件；`-linux-/-windows-` → 系统；`amd64/arm64/armv7/mipsle` 与 `x86_64/aarch64/mipsel_24kc/arm_cortex-a7/aarch64_cortex-a53` → 架构；`.deb/.rpm/.ipk` 与裸二进制 → 格式；`_<ver>_` / `-<ver>-1.` / `-<ver>-<opkg_arch>` / `-<ver>-linux-`（裸二进制）→ 版本；
- 响应：`{"uploaded": [{"name","item"}], "skipped": [{"name","reason"}]}`——无法识别或缺版本的文件进 `skipped` 并给出原因。

### PUT /pool/ota（管理员）

`{"enabled": true|false}` —— OTA 总开关，**默认关闭**；开启后 `/report` 才会下发 `upgrade` 指令。响应 `{"ok": true, "ota_enabled": true}`。

### DELETE /pool/:id（管理员）

删除条目及其文件，响应 `{"ok": true}`。

### GET /pool/:id/download（节点 Token）

Agent OTA 拉取安装包，请求头 `X-CC-Node-Token`；非批准/启用节点 401。返回文件流。

### 升级执行与回滚（Agent 侧约定）

1. 收到 `upgrade` 后后台下载 → `sha256` 校验（不符即废弃）→ 写 `.pending` 标记（含旧版备份 `.old`）→ 替换自身 → 重启；
2. 重启后连续观测到两次新的上报成功 → 确认完成，清理标记与备份；
3. 10 分钟内未能稳定上报 → 自动还原 `.old` 并重启（回滚）；
4. 同一版本 15 分钟内失败不重试；**OpenWrt 不执行升级**（服务端不下发 + Agent 双重防御）；
5. Windows 服务模式经 `net stop/start` 重启，前台模式拉起新进程后退出；Linux systemd 托管退出后由 `Restart=always` 拉起，前台仅替换文件并提示手动重启。
