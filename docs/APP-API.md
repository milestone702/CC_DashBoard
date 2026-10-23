# CC_DashBoard —— 安卓 App 对接 API

> 本文是面向 **Android 客户端开发**的独立对接文档，只包含 App 需要的接口。
> 完整接口（含 Agent 上报、SSH/PVE/群晖采集、程序池 OTA、Prometheus 导出）见 [`API.md`](./API.md)。
> **产品形态、页面设计、多面板与登录方案**见 [`APP-DESIGN.md`](./APP-DESIGN.md)。

- 版本：v1.1（对应 `server` V0.2.0 / API v1.3）
- 更新日期：2026-10-23

---

## 1. 快速开始

### 1.1 拿到 API Key

API Key 是给非浏览器客户端用的长期凭证，**不需要 Cookie、不会 7 天过期**。

服务端支持**多凭证**：每台设备 / 每个脚本各签一条，吊销某条不会影响其它设备。

在面板「节点管理 → API Key（App / 脚本接入）」填个名称（如「我的手机」）点「生成」，权限选：

| scope | 能做什么 | 建议场景 |
|---|---|---|
| `view` | 所有 GET 读接口 + WebSocket | 只读看板 App（**推荐**） |
| `admin` | 读 + 写（增删改节点、告警规则、设置） | 需要管理的 App |

也可用管理员会话直接签发（明文只返回这一次）：

```bash
curl -s -b ck.txt -X POST http://<master>:8080/api/v1/auth/api-keys \
     -H "Content-Type: application/json" -d '{"name":"我的手机","scope":"view"}'
# → {"key":{"id":2,"name":"我的手机","scope":"view","created_at":...},
#    "api_key":"cck_3f2a..."}

# 查看 / 吊销（吊销后该设备立即 401，其它设备不受影响）
curl -s -b ck.txt http://<master>:8080/api/v1/auth/api-keys
curl -s -b ck.txt -X DELETE http://<master>:8080/api/v1/auth/api-keys/2
```

> 旧版单 Key 接口 `/auth/api-key/rotate` 仍然可用（操作名为 `default` 的凭证），
> 但它会覆盖那一条，**多设备场景请改用 `/auth/api-keys`**。

### 1.2 第一个请求

```bash
curl -s http://<master>:8080/api/v1/auth/whoami -H "Authorization: Bearer cck_3f2a..."
# → {"scope":"view","via":"key"}
```

拿到 `scope` 就说明 Key 有效。`scope` 为空 = Key 无效/已吊销。

---

## 2. 基础约定

| 项目 | 约定 |
|---|---|
| Base URL | `http://<master>:8080/api/v1`（反代 HTTPS 后为 `https://<domain>/api/v1`） |
| 认证 | `Authorization: Bearer cck_...` |
| 编码 | 请求/响应均为 `application/json; charset=utf-8` |
| 时间戳 | Unix **秒**（`ts` / `last_seen` / `created_at` / `triggered_at`） |
| 内存/磁盘 | 字节 |
| 网络速率 | **Bytes/s**（不是 bit/s，UI 显示要除以 1024） |
| 空值 | `/nodes`、`/alerts`、`/alerts/rules`、`history.points` 无数据时是 `[]`；**`/summary.alerts` 与 `detail.events` 无数据时是 `null`**；`detail` 内的 `latest`/`live`/`detail`/`hardware` 也可能为 `null`；`node.latest` 字段在无数据时**整个缺失**（不是 `null`） |
| 分页 | 无分页参数；`/alerts` 最多 200 条，`/summary` 内嵌告警最多 20 条 |

### 2.1 错误响应

所有错误格式统一：

```json
{ "message": "需要访问密码" }
```

| 状态码 | 含义 | App 侧处理 |
|---|---|---|
| 400 | 参数错误（非法 range / 非法 JSON / 阈值越界） | 提示用户，检查入参 |
| 401 | Key 无效、已轮换、已吊销，或 `view` Key 调了写接口 | **引导用户重新填写 API Key** |
| 403 | 节点停用、CSRF（Bearer 不会触发） | 提示无权限 |
| 404 | 节点 / 规则不存在 | 返回列表页并刷新 |
| 500 | 服务端错误 | 提示稍后重试 |

> 401 是 App 最常遇到的错误。所有请求统一拦截 401 → 跳「设置 API Key」页。

### 2.2 权限说明

- `view` Key：可用全部 `GET` 接口与 WebSocket；节点数据里的 `token` 字段被置为空串 `""`，`pending`（未批准）节点不可见。
- `admin` Key：额外可用 `POST/PUT/DELETE`，且 `token` 字段有值。
- 读接口是否需要认证取决于服务端「前台访问密码」开关：**开关关闭时匿名也能读**，开关打开后必须带凭证。App 一律带 Bearer 即可，两种情况都正常。

---

## 3. 接口一览

| 场景 | 方法 | 路径 | 权限 |
|---|---|---|---|
| 探测服务是否就绪 | GET | `/auth/status` | 公开 |
| 站点标题（App 顶部标题） | GET | `/site` | 公开 |
| 校验自己的 Key | GET | `/auth/whoami` | Key / 公开 |
| 概览统计 | GET | `/summary` | view |
| 节点列表 | GET | `/nodes`（可选 `?limit=&offset=`） | view |
| 单节点 | GET | `/nodes/:id` | view |
| 节点详情（实时+硬件+事件） | GET | `/nodes/:id/detail` | view |
| 历史曲线 | GET | `/nodes/:id/history?range=` | view |
| 告警列表 | GET | `/alerts?status=` | view |
| 规则列表 | GET | `/alerts/rules` | view |
| 实时推送 | WS | `/ws` | view |
| 新建节点 | POST | `/nodes` | admin |
| 修改节点 | PUT | `/nodes/:id` | admin |
| 删除节点 | DELETE | `/nodes/:id` | admin |
| 节点排序 | POST | `/nodes/reorder` | admin |
| 新建规则 | POST | `/alerts/rules` | admin |
| 修改规则 | PUT | `/alerts/rules/:id` | admin |
| 删除规则 | DELETE | `/alerts/rules/:id` | admin |
| 改站点标题 | PUT | `/site` | admin |

---

## 4. 接口详解

### 4.1 `GET /auth/status` — 服务探测

公开接口，用于登录页之前判断服务可用性与是否需要密码。

```json
{ "admin_set": true, "view_enabled": false }
```

- `admin_set`：管理员密码是否已初始化（false 时服务尚未配置完成）。
- `view_enabled`：前台访问密码是否开启（true 表示读接口需要凭证）。

### 4.2 `GET /site` — 站点标题

```json
{ "title": "CC_DashBoard" }
```

### 4.3 `GET /auth/whoami` — 校验 Key

```json
{ "scope": "view", "via": "key" }
```

- `scope`：`""`（未认证）| `"view"` | `"admin"`
- `via`：`"key"` | `"cookie"` | `""` —— 便于区分凭证来源

### 4.4 `GET /summary` — 概览

```json
{
  "total": 12,
  "online": 10,
  "offline": 2,
  "alerting": 1,
  "ws_clients": 3,
  "alerts": [ { "id": 3, "node_id": 1, "node_name": "nas01", "metric": "cpu",
                "level": "warning", "value": 92.5, "status": "firing",
                "triggered_at": 1791558543, "resolved_at": 0 } ]
}
```

- `online + offline == total`；`alerting` 为当前处于告警的节点数。
- `alerts`：仅 `firing` 状态，最多 20 条。**无告警时是 `null` 而不是 `[]`**，Kotlin 侧要写成 `List<Alert>? = null`。
- `ws_clients`：当前 WebSocket 连接数（App 可忽略，用于运维观测）。

### 4.5 `GET /nodes` — 节点列表（首页卡片）

```json
{
  "nodes": [
    {
      "id": 1,
      "name": "nas01",
      "os_type": "linux",
      "access_mode": "agent",
      "address": "192.168.1.10",
      "port": 0,
      "token": "",
      "enabled": true,
      "approved": true,
      "direction": "push",
      "report_interval": 5,
      "status": "online",
      "last_seen": 1791558543,
      "created_at": 1791000000,
      "sort_order": 1,
      "public_ip": "203.0.113.7",
      "agent_version": "0.1.0",
      "agent_goos": "linux",
      "agent_arch": "amd64",
      "latest": {
        "node_id": 1, "ts": 1791558543,
        "cpu": 42.5, "mem_used": 1073741824, "mem_total": 2147483648,
        "disk_pct": 55, "net_rx": 1000, "net_tx": 500,
        "load1": 0.5, "uptime": 360000,
        "disk_read": 12345, "disk_write": 54321,
        "per_cpu": [10.5, 20.5]
      }
    }
  ]
}
```

- `status`：`online` / `offline` / **`alert`**（在线且有阈值告警时服务端自动改写）。
- `latest`：最新一条样本，**可能整个字段缺失**（节点从未上报过）→ App 需判空。
- `latest.per_cpu` 可能为空数组或缺失。
- 列表已按 `sort_order` 排序，App 直接渲染即可。
- 内存占用率需自己算：`mem_used / mem_total * 100`。
- `latest.node_id` 与所属节点一致，可直接用于局部更新时匹配。
  （v1.2 之前该字段恒为 0，若对接的是旧版本服务端，请用外层 `node.id`。）
- `upgrade`：**仅在程序池里存在匹配的新版本时出现**，形如 `{"version":"0.2.0","pool_id":3}`，用于显示「可升级」徽标；无新版本时字段整个缺失。只读 App 可以忽略。

`os_type` 枚举：`linux` / `windows` / `nas` / `openwrt` / `darwin` / `pve` / `esxi`
`access_mode` 枚举：`agent`（Agent 上报）/ `ssh`（远程轮询）/ `api`（PVE/ESXi）

### 4.6 `GET /nodes/:id` — 单节点

```json
{ "node": { "...同上的单个节点对象..." } }
```

### 4.7 `GET /nodes/:id/detail` — 详情页首屏

```json
{
  "node":     { "...Node..." },
  "latest":   { "...MetricsSample 或 null..." },
  "live":     { "...实时明细，结构与 6.2 的 live 消息一致..." },
  "detail":   { "hostname": "nas01", "kernel": "6.6.32", "cpu_model": "Intel N100",
                "arch": "x86_64", "mounts": [{"mountpoint": "/", "pct": 47}],
                "ifaces": [{"name": "eth0", "mac": "aa:bb:cc", "ipv4": "192.168.1.10"}] },
  "hardware": { "...硬件清单，字段由采集端定义..." },
  "events":   [ { "id": 9, "node_id": 1, "ts": 1791558543, "event": "online" } ],
  "boot_events": [ { "ts": 1791550400, "type": "on" }, { "ts": 1791546800, "type": "off" } ],
  "alerts":   [ { "...Alert，仅该节点的 firing..." } ]
}
```

- `events`：最近 5 条上下线事件，`event` 为 `online` / `offline`；**无记录时是 `null`**。
- `boot_events`：开关机记录，新→旧最多 50 条，`type` 为 `on` / `off`；无记录时是 `[]`。
- `alerts`：该节点当前的 firing 告警；无告警时是 `[]`。
- `detail` / `hardware` 由采集端定义、服务端原样透传，**字段不保证存在**，App 必须用可选字段解析。
- 以上除 `node` 外都可能为 `null` 或 `[]`（实测空态：`{"latest":null,"live":null,"detail":null,"hardware":null,"events":null,"boot_events":[],"alerts":[]}`）。

### 4.8 `GET /nodes/:id/history?range=<range>` — 历史曲线

| `range` | 页面标签 | 采样粒度 | 数据点上限 | 数据保留 |
|---|---|---|---|---|
| `1h` | 1 小时 | 5 秒 | 720 | 48 小时 |
| `24h`（默认） | 24 小时 | 1 分钟 | 1440 | 35 天 |
| `7d` | 7 天 | 1 分钟 | 10080 | 35 天 |
| `30d` | 30 天 | 1 小时 | 720 | 400 天 |
| `180d` | 半年 | 1 小时 | 4320 | 400 天 |
| `365d` | 一年 | 1 小时 | 8760 | 400 天 |

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

坑点（务必注意）：

- `points` 按 `ts` **升序**，无数据时是 `[]`。
- **`per_cpu` 是 JSON 数组的字符串**（如 `"[12.5,8.3]"`），不是数组；空串表示该窗口无数据。要用得二次解析。
- `7d` 有 **10080 个点**，安卓图表控件（MPAndroidChart / Vico）直接渲染会卡，建议先降采样再画。
- 追加 `&format=csv` 可拿到 CSV（`text/csv`，带 `Content-Disposition: attachment`），做导出功能时用。
- `range` 非法返回 400。

### 4.9 `GET /alerts?status=firing|resolved|all` — 告警列表

默认 `all`，按记录 id 倒序（id 自增，约等于触发时间倒序），最多 200 条。

```json
{
  "alerts": [
    { "id": 3, "node_id": 1, "node_name": "nas01", "metric": "cpu",
      "level": "warning", "value": 92.5, "status": "firing",
      "triggered_at": 1791558543, "resolved_at": 0 }
  ]
}
```

- `metric`：`cpu` / `mem_pct` / `disk_pct` / **`offline`**（离线告警）
- `level`：`warning`（阈值突破）/ `critical`（≥95 或离线）
- `resolved_at` 为 0 表示尚未恢复。

### 4.10 `GET /alerts/rules` — 阈值规则

```json
{
  "rules": [
    { "id": 1, "node_id": 0, "metric": "cpu", "op": ">",
      "threshold": 80, "delay_sec": 300, "enabled": true }
  ]
}
```

- `node_id = 0` 表示全局规则（对所有节点生效）。
- `delay_sec`：连续超阈值满 N 秒才告警，0 = 立即。

### 4.11 管理接口（需要 `admin` Key）

| 方法 | 路径 | 请求体 | 响应 |
|---|---|---|---|
| POST | `/nodes` | 见下 | `{"node":{...}}` |
| PUT | `/nodes/:id` | 部分字段即可（指针语义，只传要改的） | `{"node":{...}}` |
| DELETE | `/nodes/:id` | — | `{"ok":true}` |
| POST | `/nodes/reorder` | `{"ids":[3,1,2]}` | `{"ok":true}` |
| POST | `/alerts/rules` | 见下 | `{"rule":{...}}` |
| PUT | `/alerts/rules/:id` | 全字段覆盖 | `{"rule":{...}}` |
| DELETE | `/alerts/rules/:id` | — | `{"ok":true}` |
| PUT | `/site` | `{"title":"..."}` | `{"title":"..."}` |

新建节点最小请求体：

```json
{ "name": "nas01", "os_type": "linux", "access_mode": "agent", "report_interval": 5 }
```

> `access_mode=ssh|api` 时还需 `username` / `password`（或 `private_key`）/ `node_name`（PVE 节点名）。
> 这些凭据由服务端 AES 加密存储，**任何接口都不会回传**。

修改节点的特殊动作：

```json
{ "approved": true }        // 批准自注册节点 / false 撤回
{ "rotate_token": true }    // 轮换上报 Token（旧值立即失效）
{ "enabled": false }        // 停用（停止采集与告警）
```

告警规则请求体（`PUT` 为全字段覆盖）：

```json
{ "node_id": 0, "metric": "cpu", "op": ">", "threshold": 80, "delay_sec": 300, "enabled": true }
```

- `metric` 仅 `cpu` / `mem_pct` / `disk_pct`；`threshold` 0~100；`op` 为 `>` 或 `>=`；`delay_sec` 0~86400。
- 越界/非法值返回 400。

---

## 5. 数据结构（Kotlin）

API 字段是 `snake_case`，Kotlin 属性习惯用 `camelCase`，因此**多词字段都需要 `@SerialName` 映射**（下方已全部标注，可整体复制）：

```kotlin
// 建议的 Json 配置
val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }

@Serializable
data class Node(
    val id: Long,
    val name: String,
    @SerialName("os_type") val osType: String = "linux",
    @SerialName("access_mode") val accessMode: String = "agent",
    val address: String = "",
    val port: Int = 0,
    val token: String = "",          // view 权限下恒为空串
    val enabled: Boolean = true,
    val approved: Boolean = true,
    val direction: String = "push",
    @SerialName("report_interval") val reportInterval: Int = 5,
    val status: String = "offline",  // online | offline | alert
    @SerialName("last_seen") val lastSeen: Long = 0,
    @SerialName("created_at") val createdAt: Long = 0,
    @SerialName("sort_order") val sortOrder: Int = 0,
    @SerialName("public_ip") val publicIp: String = "",
    @SerialName("agent_version") val agentVersion: String = "",
    @SerialName("agent_goos") val agentGoos: String = "",
    @SerialName("agent_arch") val agentArch: String = "",
    @SerialName("agent_mem") val agentMem: Long = 0,            // Agent 自身内存（不是主机内存）
    @SerialName("group_tag") val groupTag: String = "",        // 分组标签（空=未分组）
    @SerialName("mute_until") val muteUntil: Long = 0,         // 告警静默截止（0=未静默）
    @SerialName("last_error") val lastError: String = "",      // 采集失败原因（成功采集后清空）
    @SerialName("last_error_at") val lastErrorAt: Long = 0,
    val latest: Sample? = null,
    val upgrade: UpgradeHint? = null,   // 池中有新版本时出现，否则字段缺失
)

@Serializable
data class UpgradeHint(
    val version: String = "",
    @SerialName("pool_id") val poolId: Long = 0,
)

@Serializable
data class Sample(
    @SerialName("node_id") val nodeId: Long = 0,
    val ts: Long = 0,
    val cpu: Double = 0.0,
    @SerialName("mem_used") val memUsed: Long = 0,
    @SerialName("mem_total") val memTotal: Long = 0,
    @SerialName("mem_pct") val memPct: Double = 0.0,   // 服务端已算好，直接用
    @SerialName("disk_pct") val diskPct: Double = 0.0,
    @SerialName("net_rx") val netRx: Long = 0,
    @SerialName("net_tx") val netTx: Long = 0,
    val load1: Double = 0.0,
    val uptime: Long = 0,
    @SerialName("disk_read") val diskRead: Long = 0,
    @SerialName("disk_write") val diskWrite: Long = 0,
    @SerialName("temp_max") val tempMax: Double = 0.0,   // 最高温度 ℃（0=无传感器）
    @SerialName("per_cpu") val perCpu: List<Double> = emptyList(),
)

@Serializable
data class Alert(
    val id: Long,
    @SerialName("node_id") val nodeId: Long = 0,
    @SerialName("node_name") val nodeName: String = "",
    val metric: String,
    val level: String = "warning",
    val value: Double = 0.0,
    val status: String = "firing",
    @SerialName("triggered_at") val triggeredAt: Long = 0,
    @SerialName("resolved_at") val resolvedAt: Long = 0,
)

@Serializable
data class AlertRule(
    val id: Long = 0,
    @SerialName("node_id") val nodeId: Long = 0,
    val metric: String,
    val op: String = ">",
    val threshold: Double,
    @SerialName("delay_sec") val delaySec: Int = 0,
    val enabled: Boolean = true,
)

@Serializable
data class HistoryPoint(
    val ts: Long,
    val cpu: Double = 0.0,
    @SerialName("mem_used") val memUsed: Long = 0,
    @SerialName("mem_total") val memTotal: Long = 0,
    @SerialName("mem_pct") val memPct: Double = 0.0,   // 服务端已算好
    @SerialName("disk_pct") val diskPct: Double = 0.0,
    @SerialName("net_rx") val netRx: Long = 0,
    @SerialName("net_tx") val netTx: Long = 0,
    val load1: Double = 0.0,
    val uptime: Long = 0,
    @SerialName("disk_read") val diskRead: Long = 0,
    @SerialName("disk_write") val diskWrite: Long = 0,
    @SerialName("per_cpu") val perCpuRaw: String = "",  // 注意：是字符串，如 "[12.5,8.3]"
)
```

> 所有字段都给了默认值，服务端新增字段时不会解析失败。**务必开启 `ignoreUnknownKeys = true`**。

`detail` / `hardware` 是自由结构，建议直接当 `JsonObject` 或 `Map<String, JsonElement>` 接，不要硬编码 data class：

```kotlin
@Serializable
data class NodeDetailResp(
    val node: Node,
    val latest: Sample? = null,
    val live: JsonObject? = null,
    val detail: JsonObject? = null,
    @SerialName("boot_events") val bootEvents: List<BootEvent> = emptyList(),
    val hardware: JsonObject? = null,
    val events: List<NodeEvent>? = null,   // 无记录时是 null，不是 []
    val alerts: List<Alert> = emptyList(),
)

@Serializable
data class Summary(
    val total: Int = 0,
    val online: Int = 0,
    val offline: Int = 0,
    val alerting: Int = 0,
    @SerialName("ws_clients") val wsClients: Int = 0,
    val alerts: List<Alert>? = null,       // 无告警时是 null，不是 []
)

@Serializable
data class BootEvent(val ts: Long, val type: String)   // on | off
@Serializable
data class NodeEvent(val id: Long = 0, @SerialName("node_id") val nodeId: Long = 0,
                     val ts: Long = 0, val event: String = "")
```

---

## 6. WebSocket 实时推送

### 6.1 连接

```
ws://<master>:8080/api/v1/ws
```

三种鉴权方式（选一种）：

| 方式 | 写法 | 适用 |
|---|---|---|
| 请求头 | `Authorization: Bearer cck_...` | OkHttp / Retrofit 等能自定义头的库 ✅ 推荐 |
| Query | `ws://.../api/v1/ws?access_token=cck_...` | 不能设 header 的 WS 库（如 RN 的 WebSocket）；会进访问日志 |
| Cookie | `Cookie: cc_session=...` | 仅浏览器场景 |

握手失败返回 `401`（凭证无效 / 已吊销）。**只在握手时校验一次**，连上后不会再断开。
服务端不处理 App 发来的消息（读取后丢弃），因此 App 只需接收。

### 6.2 消息类型

所有消息都是 JSON 文本帧，用 `type` 区分。**服务端只推不收**，App 不需要发消息。

```jsonc
// ① 指标更新（核心，每次节点上报推一条）
{ "type": "metrics", "node_id": 1, "data": { "node_id":1, "ts":1791558543,
  "cpu":23.5, "mem_used":3221225472, "mem_total":8589934592, "disk_pct":47.2,
  "net_rx":1250000, "net_tx":320000, "load1":0.8, "uptime":93784,
  "disk_read":1500000, "disk_write":800000, "per_cpu":[12.5,8.3] } }

// ② 实时明细（与 metrics 成对推送，详情页用）
{ "type": "live", "node_id": 1, "data": {
  "ts":1791558543, "cpu_total":23.9, "per_cpu":[30.1,12.5],
  "load1":1.2, "load5":0.9, "load15":0.7,
  "mem": {"total":1,"used":1,"available":1,"free":1,"buffers":0,"cached":0,
          "swap_total":0,"swap_used":0,"percent":42.1},
  "disk_io": {"read_bps":1024,"write_bps":2048,"read_count":1,"write_count":1},
  "nics": [{"name":"eth0","rx_bps":1000,"tx_bps":500,"mac":"...","ipv4":"..."}],
  "temps": [{"label":"core0","value":45.0}],
  "procs": [{"pid":123,"name":"app","cpu":2.5,"mem":1.2}] } }

// ③ 上下线
{ "type": "node_status", "node_id": 7, "status": "online" }   // online | offline

// ④ 节点增删（管理端操作触发）
{ "type": "node_added",   "node_id": 7 }
{ "type": "node_removed", "node_id": 7 }
{ "type": "node_pending", "node_id": 8 }   // 新设备自注册，待批准

// ⑤ 告警事件（触发/恢复时各一条，比轮询更及时）
{ "type": "alert_firing",   "node_id": 7, "metric": "cpu", "level": "warning", "value": 92.5 }
{ "type": "alert_resolved", "node_id": 7, "metric": "cpu" }
```

`metric` 取值：`cpu` / `mem_pct` / `disk_pct` / `load1` / `net_rx` / `net_tx` / `offline`；离线告警的 `value` 是「已离线秒数」。

用 `type` 分发到 sealed class：

```kotlin
@Serializable
sealed interface WsEvent {
    @Serializable @SerialName("metrics") data class Metrics(
        @SerialName("node_id") val nodeId: Long, val data: Sample) : WsEvent
    @Serializable @SerialName("live") data class Live(
        @SerialName("node_id") val nodeId: Long, val data: JsonObject) : WsEvent
    @Serializable @SerialName("node_status") data class NodeStatus(
        @SerialName("node_id") val nodeId: Long, val status: String) : WsEvent
    @Serializable @SerialName("alert_firing") data class AlertFiring(
        @SerialName("node_id") val nodeId: Long, val metric: String,
        val level: String = "warning", val value: Double = 0.0) : WsEvent
    @Serializable @SerialName("alert_resolved") data class AlertResolved(
        @SerialName("node_id") val nodeId: Long, val metric: String) : WsEvent
    @Serializable @SerialName("node_added") data class NodeAdded(
        @SerialName("node_id") val nodeId: Long) : WsEvent
    @Serializable @SerialName("node_removed") data class NodeRemoved(
        @SerialName("node_id") val nodeId: Long) : WsEvent
    @Serializable @SerialName("node_pending") data class NodePending(
        @SerialName("node_id") val nodeId: Long) : WsEvent
}
```

### 6.3 三个必须知道的限制

1. **服务端不发心跳，也不会主动检测死连接**。移动网络 NAT 超时后连接会「假活」（`onOpen` 状态但收不到数据）。建议 **OkHttp 开启 `pingInterval(20, TimeUnit.SECONDS)`**：
   客户端定期发 WebSocket PING，服务端 gorilla 会自动回 PONG，既能保活也能让 OkHttp 及时发现半开连接并回调 `onFailure`。
   除此之外仍建议记录「最后一次收到消息的时间」，超过 `3 × 上报周期`（一般 15~30 秒）无数据就主动重连。
2. **消息可能被丢弃**。服务端单连接缓冲 64 条，满了直接丢（避免拖慢上报链路）。所以**不能假设不丢帧**，要定期（如 30 秒）用 `GET /nodes` 或 `GET /summary` 兜底校准。
3. **不能靠 WS 判断全量数据**。WS 只推增量变化，断线期间的更新不会补发。重连成功后必须重新拉一次 `GET /nodes`。

### 6.4 推荐的数据流

```
启动：GET /auth/status → GET /site → GET /nodes（首屏）
      → 连 WS
WS metrics  → 按 node_id 就地更新列表项（局部刷新，别整表重载）
WS node_status → 更新状态灯 + 刷新 GET /summary
WS node_added/removed/pending → 重新拉 GET /nodes（管理页）
每 30s → GET /summary 兜底校准（防丢帧）
断线 → 指数退避重连 1s / 2s / 4s / 8s / 15s / 30s（上限 30s）
重连成功 → 立即 GET /nodes 重新对齐
```

---

## 7. 安卓对接清单

### 7.1 明文 HTTP 必须处理（否则一定失败）

Android 9（API 28）起默认禁止明文 HTTP，直连 `http://192.168.x.x:8080` 会抛
`java.io.IOException: Cleartext HTTP traffic to ... not permitted`。

方案一（局域网自用，推荐）：`res/xml/network_security_config.xml`

```xml
<?xml version="1.0" encoding="utf-8"?>
<network-security-config>
    <!-- 只对固定服务器地址放行明文，其余仍强制 HTTPS -->
    <domain-config cleartextTrafficPermitted="true">
        <domain includeSubdomains="true">192.168.1.10</domain>
        <domain includeSubdomains="true">localhost</domain>
    </domain-config>
</network-security-config>
```

`AndroidManifest.xml`：

```xml
<application
    android:networkSecurityConfig="@xml/network_security_config"
    ...>
```

> **重要**：`<domain>` 只能写具体主机名或 IP，**不支持 CIDR 网段**（写 `192.168.0.0` 不表示整个网段）。
> 如果 App 让用户自己填写服务器地址（内网工具类常见），域名事先无法枚举，只能整体放行：
>
> ```xml
> <network-security-config>
>     <base-config cleartextTrafficPermitted="true" />
> </network-security-config>
> ```
>
> 这等于允许所有明文请求，仅适合纯内网自用工具；面向公网的产品请改用方案二。

方案二（公网/长期）：后端用 Nginx/Caddy 反代上 HTTPS，App 只连 `https://` 域名。

### 7.2 API Key 不要硬编码

App 会被反编译，字符串常量能被直接扒出来。建议：

- 首次启动让用户手动粘贴 Key，存 `EncryptedSharedPreferences` 或 DataStore（配 `MasterKey`）；
- 不写进 `BuildConfig`、不写进 `strings.xml`；
- 提供「设置 → API Key」页面，支持修改与连通性测试（调 `GET /auth/whoami` 看 `scope`）。

### 7.3 OkHttp 配置要点

> 服务端已启用 gzip：OkHttp 会自动带上 `Accept-Encoding: gzip` 并透明解压，无需额外配置。
> 移动网络下 `/nodes` 这类 JSON 响应体积约为原始的 1/5。

```kotlin
val client = OkHttpClient.Builder()
    .connectTimeout(10, TimeUnit.SECONDS)
    .readTimeout(20, TimeUnit.SECONDS)
    .pingInterval(20, TimeUnit.SECONDS)   // 保活 + 半开检测，见 6.3
    .addInterceptor { chain ->
        val req = chain.request().newBuilder()
            .header("Authorization", "Bearer $apiKey")
            .header("Accept", "application/json")
            .build()
        chain.proceed(req)
    }
    .build()
```

- **不需要 CookieJar**：全程 Bearer，不涉及 Cookie。
- **不需要处理 CSRF**：带 Bearer 的请求服务端跳过 Origin 校验。
- WS 用同一个 client：`client.newWebSocket(Request.Builder().url("ws://.../api/v1/ws").build(), listener)`，
  Interceptor 会为握手请求自动带上 `Authorization`。

### 7.4 401 统一处理

```kotlin
if (response.code == 401) {
    // Key 失效 / 被吊销 / 被重新生成 → 清本地 Key，跳设置页
    eventBus.emit(AppEvent.NeedApiKey)
}
```

`view` Key 调写接口也返回 401（message 为「需要管理员登录」），可据此区分「权限不足」与「Key 失效」：
解析 `message` 字段，前者不应清除本地 Key。

### 7.5 数值格式化

```kotlin
fun fmtBytes(v: Long): String {          // 内存、磁盘 IO
    if (v < 1024) return "$v B"
    val units = listOf("KB", "MB", "GB", "TB", "PB")
    var d = v.toDouble() / 1024; var i = 0
    while (d >= 1024 && i < units.lastIndex) { d /= 1024; i++ }
    return "%.1f %s".format(d, units[i])
}

fun fmtRate(v: Long): String = fmtBytes(v) + "/s"   // net_rx / net_tx 已是 Bytes/s

fun fmtUptime(sec: Long): String {                  // uptime 是秒
    val d = sec / 86400; val h = sec % 86400 / 3600; val m = sec % 3600 / 60
    return if (d > 0) "${d}天${h}小时" else if (h > 0) "${h}小时${m}分" else "${m}分"
}

// 时间戳是 Unix 秒
val timeText = SimpleDateFormat("MM-dd HH:mm", Locale.getDefault())
    .format(Date(sample.ts * 1000))
```

### 7.6 空态与兼容

- `latest` 在节点从未上报时**整个字段缺失**（不是 `null`）→ 卡片显示「等待数据」而不是 0%。
- `per_cpu` 可能缺失或为空数组。
- `detail` / `hardware` 的字段由采集端决定，**不要**假定 `hostname` 一定存在。
- 服务端未来可能加字段，`ignoreUnknownKeys = true` 必须开。

---

## 8. 速查（curl）

```bash
BASE=http://<master>:8080/api/v1
KEY=cck_xxxxxxxx

# 探测 + 校验
curl -s $BASE/auth/status
curl -s $BASE/auth/whoami -H "Authorization: Bearer $KEY"

# 概览 / 列表
curl -s $BASE/summary -H "Authorization: Bearer $KEY"
curl -s $BASE/nodes   -H "Authorization: Bearer $KEY"

# 详情 / 历史
curl -s $BASE/nodes/1/detail -H "Authorization: Bearer $KEY"
curl -s "$BASE/nodes/1/history?range=24h" -H "Authorization: Bearer $KEY"
curl -s -o n1.csv "$BASE/nodes/1/history?range=7d&format=csv" -H "Authorization: Bearer $KEY"

# 告警
curl -s "$BASE/alerts?status=firing" -H "Authorization: Bearer $KEY"
curl -s $BASE/alerts/rules -H "Authorization: Bearer $KEY"

# 管理（需 admin Key）
curl -s -X POST $BASE/nodes -H "Authorization: Bearer $KEY" \
     -H "Content-Type: application/json" \
     -d '{"name":"nas01","os_type":"linux","access_mode":"agent","report_interval":5}'
curl -s -X PUT $BASE/nodes/1 -H "Authorization: Bearer $KEY" \
     -H "Content-Type: application/json" -d '{"approved":true}'

# WebSocket（wscat 等工具）
# 头方式：wscat -c ws://<master>:8080/api/v1/ws -H "Authorization: Bearer $KEY"
# Query：wscat -c "ws://<master>:8080/api/v1/ws?access_token=$KEY"
```
