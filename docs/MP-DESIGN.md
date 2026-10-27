# CC_DashBoard 多端（微信小程序 / H5 / App）设计文档

> 本文是**产品需求 + 技术设计**文档，回答「移动端要做成什么样、怎么实现」。
> 接口字段细节请查 [`APP-API.md`](./APP-API.md)，全系统接口见 [`API.md`](./API.md)，
> 安卓原生端的历史设计见 [`APP-DESIGN.md`](./APP-DESIGN.md)（**产品层仍有效、技术层已被本文取代**，对照见第 13 章）。
> 本文不含完整实现代码，只给数据结构、平台约束、页面规格与验收标准。

- 版本：v1.0
- 覆盖端：微信小程序（基础库 ≥ 2.30.0）、H5 手机浏览器、App（iOS / Android）
- 技术路线：uni-app（Vue 3 + Vite），一套源码编译三端
- 对齐的后端版本：`server` V0.2.0（API v1.3）
- 创建日期：2026-10-24

---

## 1. 文档定位与结论

### 1.1 一句话结论

**能做，而且是三端一套代码。** 现有 Web 前端（`web/src`，Vue 3.5 + Pinia + Vue Router + Tailwind + ECharts）和后端接口都是现成的，页面信息架构可以基本沿用；真正的约束全部来自**客户端的运行环境**，而不是功能实现难度。

### 1.2 多端策略：就低不就高

三端能力差异比想象中小——把约束逐条摊开后会发现问题只剩两项（见 2.1）。因此采用单一实现路径：

```svg
<svg viewBox="0 0 680 348" width="100%" xmlns="http://www.w3.org/2000/svg" font-family="-apple-system, 'PingFang SC', 'Microsoft YaHei', sans-serif">
  <text x="40" y="26" font-size="14" font-weight="500" fill="#0f172a">一份文档、一套源码、三端产物</text>

  <rect x="40" y="46" width="600" height="62" rx="8" fill="#ecfdf5" stroke="#10b981" stroke-width="0.5"/>
  <text x="56" y="68" font-size="13" fill="#065f46" dominant-baseline="central">公共层 · 与端无关 ≈85%</text>
  <text x="56" y="90" font-size="11" fill="#047857" dominant-baseline="central">页面规格 / 数据模型 / 接口契约 / WS 分发 / 格式化 / 降采样 / 空态三态</text>

  <rect x="40" y="120" width="600" height="62" rx="8" fill="#f1f5f9" stroke="#94a3b8" stroke-width="0.5"/>
  <text x="56" y="142" font-size="13" fill="#0f172a" dominant-baseline="central">平台适配层 · 条件编译 ≈15%</text>
  <text x="56" y="164" font-size="11" fill="#64748b" dominant-baseline="central">面板地址校验 / 连通性预检 / 通知能力 / 分享开关 / 包体预算</text>

  <rect x="40" y="194" width="600" height="36" rx="8" fill="#f8fafc" stroke="#cbd5e1" stroke-width="0.5"/>
  <text x="340" y="212" font-size="13" fill="#334155" text-anchor="middle" dominant-baseline="central">约定：一切按最严的小程序端写法，其余两端是超集</text>

  <rect x="40" y="252" width="180" height="56" rx="8" fill="#ffffff" stroke="#94a3b8" stroke-width="0.5"/>
  <text x="130" y="272" font-size="13" fill="#0f172a" text-anchor="middle" dominant-baseline="central">微信小程序</text>
  <text x="130" y="292" font-size="11" fill="#64748b" text-anchor="middle" dominant-baseline="central">约束最严，能力下限</text>

  <rect x="250" y="252" width="180" height="56" rx="8" fill="#ffffff" stroke="#94a3b8" stroke-width="0.5"/>
  <text x="340" y="272" font-size="13" fill="#0f172a" text-anchor="middle" dominant-baseline="central">H5 手机浏览器</text>
  <text x="340" y="292" font-size="11" fill="#64748b" text-anchor="middle" dominant-baseline="central">无需安装，分享链接</text>

  <rect x="460" y="252" width="180" height="56" rx="8" fill="#ffffff" stroke="#94a3b8" stroke-width="0.5"/>
  <text x="550" y="272" font-size="13" fill="#0f172a" text-anchor="middle" dominant-baseline="central">App</text>
  <text x="550" y="292" font-size="11" fill="#64748b" text-anchor="middle" dominant-baseline="central">通知栏能力最强</text>

  <line x1="340" y1="182" x2="340" y2="192" stroke="#cbd5e1" stroke-width="1"/>
  <line x1="340" y1="230" x2="340" y2="250" stroke="#94a3b8" stroke-width="1"/>
  <line x1="130" y1="250" x2="130" y2="240" stroke="#cbd5e1" stroke-width="1"/>
  <line x1="550" y1="250" x2="550" y2="240" stroke="#cbd5e1" stroke-width="1"/>
  <line x1="130" y1="240" x2="550" y2="240" stroke="#e2e8f0" stroke-width="0.5"/>
</svg>
```

「就低不就高」的收益是只有一条实现路径、三端表现完全一致、不需要为每个端写两套逻辑；代价是 H5 和 App 用不上更强能力（H5 明明能直接用 ECharts，却要跟着自绘）。对这种「以看为主」的面板，这个代价可以接受——自绘曲线组件约 200 行，换来的是三端零差异。

### 1.3 与现有 `web/` 的边界（重要）

`web/src/views/Admin.vue` 有 71 KB，SSH/PVE/群晖凭据录入、程序池、OTA、告警规则编辑全在里面。这些是桌面端的重活，**不搬进移动端**，也会撑爆小程序主包。合理分工：

| 目录 | 角色 | 状态 |
|---|---|---|
| `web/` | **桌面管理后台**：全部写入操作、凭据录入、程序池、OTA、规则编辑、CSV 导出 | 保持不动 |
| `miniprogram/`（新增，uni-app） | **移动端看板**：看数据 + 轻操作（审批节点、添加 agent 节点），编译出小程序 / H5 / App 三个产物 | 本文的设计对象 |
| `server/` | 后端，两端共享同一套 API | 不动 |

### 1.4 三份文档怎么分工

| 文档 | 对本项目的价值 |
|---|---|
| `MP-DESIGN.md`（本文） | 多端页面规格、平台矩阵、认证方案、适配清单、部署配置 |
| `APP-API.md` | 接口字段、坑点、WS 消息类型——**可直接复用，无需改动**。第 5 章的 Kotlin 数据模型改为 TS interface（7.1） |
| `APP-DESIGN.md` | 产品层（2.1 场景、5.2 卡片、5.3 仪表盘、6.x 详情、8.x 刷新、10 错误矩阵）**直接复用**；技术层（Compose/Room/OkHttp/EncryptedSharedPreferences）被本文取代（第 13 章） |
| `API.md` | 后端全量能力，扩展功能时查 |

### 1.5 目标场景与非目标

沿用 `APP-DESIGN.md` 2.1 / 2.2：单人自用、多台设备、以看为主。移动端额外**不做**：

| 不做 | 原因 |
|---|---|
| 局域网 IP 直连 | 小程序平台不允许（2.2）；H5/App 虽可行，但按「就低不就高」统一不做 |
| Web-view 套壳现有 Web 页面 | 微信明确不允许纯套壳；业务域名仍需备案；审核高概率被拒 |
| SSH / PVE / 群晖节点凭据录入 | 沿用安卓端结论：字段多、易错，留在 Web 后台 |
| 程序池 / OTA 上传 | 移动端无实际价值 |
| 告警规则编辑 | 低频、字段校验复杂，留在 Web 后台 |
| 后台常驻推送 | 小程序无法后台存活；订阅消息需授权 + 模板审核。首版不做，见 7.5 |
| 历史数据 CSV 导出 | 移动端下载文件价值低 |

---

## 2. 平台能力矩阵

### 2.1 三端差异全表

**这是全文最重要的一张表。** 摊开后可以发现，真正需要分端处理的地方只有「面板地址可达性」和「通知」两项，其余全部可以统一。

| 约束项 | 微信小程序 | H5 手机浏览器 | App（uni-app 打包） | 统一策略 |
|---|---|---|---|---|
| **面板地址** | 仅**已备案 HTTPS 域名**、隐式 443、无端口、无 IP | 任意 `http(s)`；跨域靠服务端 CORS（后端已有 `ACAO: *`）；HTTPS 页面不能请求 `http`（混合内容） | 任意 `http`，需配置明文放行 | ⚠️ **按小程序**：统一要求 `https://` 域名（2.2） |
| **认证** | **只能** Bearer API Key | 可用 Cookie 会话或 Bearer | Bearer 或 Cookie | 统一 **Bearer API Key**（4.1） |
| **凭据存储** | `storage` 明文 | `localStorage` 明文 | `plus.storage` 明文 | 统一**只存 Key 不存密码**（4.4） |
| **WS 保活** | 不能发 PING 帧 | 浏览器 API 不能发 PING 帧 | `plus.net` 不能发 PING 帧 | 统一**超时判活**（7.4） |
| **WS 条数** | 多连不可靠（切后台即断） | 同域约 6 条 | 无硬限 | 统一**单连接**（7.4） |
| **后台存活** | 30 分钟后被销毁 | 移动浏览器节流定时器 | 可后台，但受厂商省电策略限制 | 统一 `onHide` 主动断开（2.6） |
| **通知** | 订阅消息（需用户授权 + 模板审核） | Web Push（需 HTTPS + 用户授权） | 本地通知可行 | ⚠️ **首版统一不做**（7.5） |
| **图表** | 主包 2 MB，必须自绘 | 可直接用 ECharts | 可直接用 ECharts | 统一**自绘**（2.4） |
| **包体预算** | 主包 ≤ 2 MB / 分包合计 ≤ 20 MB | 无 | 无 | 按小程序预算执行（2.4） |
| **域名配置** | 后台白名单，request / socket 各 20 个 | 无（靠 CORS） | 无 | 仅小程序需要（10.1） |
| **导航栏** | 可自定义（需处理胶囊位置） | 浏览器自带 | 需处理状态栏高度 | 统一自定义导航栏（9.3） |

> **一个反直觉的好消息**：WS 保活三端都不能发 PING 帧——浏览器 WebSocket API、微信 `wx.connectSocket`、uni-app App 端的 `plus.net` 都不暴露该能力。只有原生 OkHttp 能（`APP-API.md` 6.3 的方案）。这反而让三端保活逻辑完全统一，不需要条件编译。

### 2.2 面板地址：最严的一端说了算

小程序只能访问**预先配置的、已备案的 HTTPS 域名**（隐式 443，不能带端口，不能是 IP）。这条约束最严，因此三端统一要求 `https://` 域名。

| 项 | 小程序要求 | 说明 |
|---|---|---|
| 协议 | HTTPS / WSS | 明文 HTTP 无任何配置项可放行（安卓还能靠 `network_security_config.xml`，小程序不行） |
| 备案 | 域名必须完成 ICP 备案（境内主体） | — |
| 主机 | 不能是 IP、不能是 `localhost` | — |
| 端口 | 不能带端口（隐式 443） | 反代必须挂 443 |
| TLS | ≥ 1.2，且必须是**受信任 CA** 签发 | 自签证书不行，小程序不提供「忽略证书错误」的能力。Let's Encrypt 即可 |
| 数量 | request / socket **各**最多 20 个域名 | — |
| 配置位置 | 微信公众平台 → 开发 → 开发管理 → 开发设置 → 服务器域名 | **`wss` 要在「socket 合法域名」单独配**，配在 request 里无效 |

**「不校验合法域名」开关的适用边界**（很多文章写错，务必记清）：

| 运行形态 | 是否受域名校验约束 |
|---|---|
| 微信开发者工具（模拟器） | 否（勾选「详情 → 本地设置 → 不校验合法域名」后） |
| 真机调试（扫码连开发者工具） | 否（同上开关生效） |
| 预览（扫码直接打开） | **是** |
| 体验版 | **是** |
| 正式版 | **是** |

自测阶段可以靠开关排查问题，但**不能作为交付形态**。

### 2.3 存储：三端都没有系统级加密

| 端 | 介质 | 加密 |
|---|---|---|
| 小程序 | `uni.setStorageSync` | 无，仅沙箱隔离。单 key ≤ 1 MB，总计 ≤ 10 MB |
| H5 | `localStorage` | 无，同源隔离。约 5 MB |
| App | `plus.storage` | 无，是应用私有目录下的明文文件 |

> App 端要做真正加密需引入原生插件（调用 iOS Keychain / Android Keystore），成本高且三端不通用。因此**不追求加密**，改用「最小权限 + 可吊销 + 不落密码」控制风险（4.4）。

### 2.4 包体积：按小程序预算执行

| 项 | 上限 | 推论 |
|---|---|---|
| 小程序主包 | 2 MB | **不能引 ECharts**（压缩后 300 KB+，小程序版 `ec-canvas` 更重） |
| 小程序分包合计 | 20 MB | 本项目用不到分包 |
| H5 / App | 无硬限制 | 但因为统一自绘，它们也不引图表库 |

因此：

- 曲线图用 **canvas 2d 自绘**（6.3）；
- 不引 UI 组件库（uView / uni-ui 全量会吃掉主包），手写组件；
- 图标用内联 SVG 字符串或 canvas 绘制，不用图片集；
- 不引 dayjs，用原生 `Date`。

### 2.5 网络并发与超时

| 项 | 值 | 说明 |
|---|---|---|
| `uni.request` 并发上限 | 小程序约 10 | 超出排队。首版单面板不构成问题 |
| 默认超时 | **60 秒** | 必须显式传 `timeout: 10000`，否则弱网下页面像卡死 |
| WS 同时连接 | 小程序多连不可靠 | 首版单面板单连接，多面板退化为「当前可见面板连 WS，其余轮询」 |

### 2.6 生命周期

| 事件 | 小程序 | H5 | App | 统一动作 |
|---|---|---|---|---|
| 切到后台 | 5 秒后 JS 被限制 | 定时器被节流 | 可继续运行 | `onHide`：关 socket + 清定时器 |
| 回到前台 | `onShow` | `visibilitychange` | `onShow` | `onShow`：重连 WS + 拉 `GET /nodes` 对齐 + 校验 Key |
| 长时间后台 | 30 分钟后被**销毁** | 可能被系统回收 | 一般存活 | 冷启动必须靠 storage 缓存 1.5 秒内出内容（7.6） |
| 页面卸载 | 二级页 `onUnload` | — | — | 清定时器与监听 |

### 2.7 审核相关（仅小程序）

| 项 | 说明 |
|---|---|
| 主体 | 个人主体可注册，工具类可选类目「工具 → 效率」 |
| 内容 | 内网运维看板，简介需说明用途；不得出现「绕过」「破解」「监控他人」类描述 |
| web-view | 不使用，避免套壳判定 |
| 用户隐私保护指引 | 需申报「网络请求」用途；本项目不采集用户信息、不读相册/位置/剪贴板（剪贴板仅用户主动点「粘贴 Key」时读取） |
| 登录 | 不使用微信登录（`wx.login`），无用户体系 |

---

## 3. 技术选型与工程结构

### 3.1 路线对比

| 方案 | 复用现有代码 | 主包体积 | 审核风险 | 结论 |
|---|---|---|---|---|
| **uni-app（Vue 3 + Vite）** | 高：`utils/` `stores/` 语法级复用，Pinia 官方支持 | 中 | 低 | **采用** |
| 原生小程序（wxml/wxss） | 无，需全部重写 | 低 | 低 | 备选（只做小程序时） |
| Taro（React） | 无，语法不同 | 中 | 低 | 不采用 |
| Flutter / Compose（`APP-DESIGN.md` 原方案） | 无 | — | 无 | 被 uni-app 取代（13.1） |
| web-view 套壳现有 Web | 100% | 极低 | **高，会被拒** | 不采用 |

选 uni-app 的核心理由不是「一套代码多端」这四个字，而是**现有 Vue 3 逻辑能直接搬**：`web/src/utils/format.js`（速率/容量/时长格式化）、`web/src/stores/nodes.js`（WS 分发与缓存策略）、`web/src/api/index.js`（请求封装形态）都是纯 JS，把 `fetch` 换成 `uni.request`、`WebSocket` 换成 `SocketChannel` 即可，业务逻辑不需要重新设计。

### 3.2 编译目标

| 端 | 构建命令 | 产物 | 发布方式 |
|---|---|---|---|
| 微信小程序 | `npm run dev:mp-weixin` / `build:mp-weixin` | `dist/dev/mp-weixin` | 微信开发者工具导入 → 上传 → 提审 |
| H5 | `npm run dev:h5` / `build:h5` | `dist/build/h5` | 静态文件部署到面板同域或任意静态托管 |
| App | `npm run dev:app` / `build:app` | `dist/build/app` | HBuilderX 云打包 / 本地打包（iOS 需证书，Android 需签名） |

**H5 的部署建议放在面板同域下的子路径**（如 `https://dash.example.com/m/`），这样：

- 不用改后端 CORS（同源）；
- 顺手解决了「H5 想用 Cookie 会话」的可能性；
- 但要给 `manifest.json` 的 `h5.router.base` 配 `/m/`，并让后端静态托管该目录或由反代转发。

### 3.3 目录结构

```
miniprogram/                      ← 新增，与 web/ 平级
├── src/
│   ├── pages.json                页面路由 / tabBar / 全局窗口样式
│   ├── manifest.json             appid、基础库版本、h5.base、App 权限
│   ├── App.vue                   onLaunch / onShow / onHide
│   ├── main.js                   createSSRApp + Pinia
│   ├── uni.scss                  主题变量（色板对齐 web/tailwind.config.js）
│   ├── api/
│   │   ├── request.js            uni.request Promise 封装 + 401 分类
│   │   ├── socket.js             SocketChannel 封装（三端统一）
│   │   └── index.js              getJSON / postJSON / putJSON / delJSON
│   ├── platform/                 ★ 平台适配层（唯一允许写条件编译的地方）
│   │   ├── env.js                平台标识、能力探测
│   │   ├── url.js                地址规范化与合法性校验（4.2）
│   │   ├── storage.js            storage 封装 + 体积保护
│   │   ├── notify.js             通知能力（首版空实现，7.5）
│   │   └── share.js              分享开关（小程序端禁用）
│   ├── stores/
│   │   ├── panels.js             面板配置（地址 + Key + 显示开关 + 节点过滤）
│   │   ├── nodes.js              节点列表 / 汇总 / WS 分发（移植自 web）
│   │   └── alerts.js             告警列表
│   ├── utils/
│   │   ├── format.js             直接复用 web/src/utils/format.js
│   │   ├── downsample.js         LTTB 降采样（6.3）
│   │   └── theme.js              色板常量 + 阈值配色
│   ├── components/
│   │   ├── GaugeRing.vue         环形仪表盘（canvas 2d）
│   │   ├── TrendChart.vue        历史曲线（canvas 2d）
│   │   ├── NodeCard.vue          首页节点卡片
│   │   ├── StatusDot.vue         状态灯（含 alert 脉冲）
│   │   ├── NavBar.vue            自定义导航栏（适配状态栏 + 胶囊）
│   │   ├── SectionCard.vue       详情页折叠区
│   │   ├── ErrorBar.vue          顶部错误条 + 重试
│   │   └── EmptyState.vue        空态（三种文案）
│   └── pages/
│       ├── welcome/              首次启动引导（添加面板）
│       ├── overview/             概览（tab 1）
│       ├── alerts/               告警（tab 2）
│       ├── settings/             设置（tab 3）
│       ├── device/               设备详情
│       └── panel/                面板编辑 / 节点选择 / 节点管理
├── package.json
└── vite.config.js
```

**架构约束**：条件编译（`#ifdef MP-WEIXIN` 等）**只允许出现在 `src/platform/` 下**。其他任何文件出现条件编译，都说明该能力没有抽象干净，应补到 `platform/` 里。这样第 8 章的适配清单天然收敛成一份短表。

### 3.4 现有代码复用映射

| 现有文件 | 小程序侧 | 处理方式 |
|---|---|---|
| `web/src/utils/format.js` | `src/utils/format.js` | **原样复制**。`fmtRate` / `fmtBytes` / `fmtUptime` / `fmtClock` / `fmtVer` 全可用；`statusDotClass` 返回 Tailwind class，改为返回色值对象（6.5） |
| `web/src/stores/nodes.js` | `src/stores/nodes.js` | 移植。WS `onmessage` 分发逻辑（`metrics` / `live` / `node_status` / `alert_*` / `node_*`）完全一致；`connectWS` 换成 `SocketChannel`（7.4） |
| `web/src/api/index.js` | `src/api/index.js` | 重写请求层：`fetch` → `uni.request`；`window.dispatchEvent('cc-unauthorized')` → store 状态 + 路由跳转 |
| `web/src/stores/auth.js` | `src/stores/panels.js` | **重新设计**：Web 靠 Cookie 会话，移动端靠 Bearer Key，没有 `login/logout` 语义，只有「面板 + Key」的增删改（第 4 章） |
| `web/src/views/Overview.vue` | `pages/overview/` | 复用信息架构（状态灯 / 系统徽标 / 双 IP / 三条指标 / 上下行 + 运行时长 / `group_tag` 分组 / 三段空态文案）；进度条改环形仪表盘 |
| `web/src/views/DeviceDetail.vue` | `pages/device/` | 22 区块压缩为 6 段（5.6），字段不变 |
| `web/src/views/Alerts.vue` | `pages/alerts/` | 列表 + 状态筛选，几乎可平移 |
| `web/src/views/Admin.vue`（71 KB） | `pages/panel/node-list` | **只取**「待审批 + 批准/拒绝 + 添加 agent 节点」；其余不做（1.5） |
| `web/src/components/MetricChart.vue`（ECharts） | `components/TrendChart.vue` | **重写**，canvas 2d 自绘（6.3） |
| `web/src/mock.js` | — | 不移植。不做演示数据（避免用户误判真实设备状态，与 Web 生产构建策略一致） |

### 3.5 依赖清单

| 用途 | 选择 | 说明 |
|---|---|---|
| 框架 | uni-app（Vue 3 + Vite） | `npx degit dcloudio/uni-preset-vue#vite my-project` |
| 状态 | Pinia | uni-app Vue 3 内置支持 |
| 样式 | 手写 SCSS + `uni.scss` 变量 | **不引 Tailwind**：`weapp-tailwindcss` 能转，但任意值/变体支持不全，维护成本高于手写 |
| 图表 | 无，canvas 2d 自绘 | 见 2.4 |
| 图标 | 内联 SVG 字符串 | 避免图片资源占包体 |
| 日期 | 原生 `Date` | 不引 dayjs |

---

## 4. 认证与凭据

### 4.1 三端统一走 Bearer API Key

后端有两种凭证（`API.md` 第 11 节）：会话 Cookie（`POST /auth/login` 下发，7 天）和 API Key（`cck_...`，不过期，支持多条）。

| | 会话 Cookie | API Key |
|---|---|---|
| 携带方式 | 浏览器自动带 `Cookie: cc_session=...` | 显式设 `Authorization: Bearer cck_...` |
| 有效期 | 7 天，需主动续期 | 不过期，直到吊销 |
| 小程序可控性 | ❌ 无 Cookie 容器 | ✅ 完全可控 |
| H5 可控性 | ⚠️ 需同源部署 | ✅ 任意域 |
| App 可控性 | ⚠️ 需自行管理 CookieJar | ✅ |
| CSRF 校验 | 需过 Origin 校验 | 服务端对 Bearer 直接跳过 |

**结论：三端统一用 Bearer。** 理由不是小程序只能用 Bearer，而是「只实现一条认证路径」的收益远大于 H5 端省一个输入框。若 H5 走 Cookie，就意味着要维护两套登录态、两套 401 处理、两套续期逻辑。

> `wx.request` 内部确实会记住响应里的 `Set-Cookie` 并在后续请求带上，但**开发者读不到、写不了、也不保证跨冷启动持久化**。依赖它不可靠，本项目不采用。

### 4.2 Key 怎么进入客户端

#### 方案 K1（首版采用，零后端改动）

用户在 **Web 后台** → 节点管理 → API Key → 填名称「我的手机」→ 权限选 `view` → 生成 → 复制明文（仅此一次）。然后在客户端「设置 → 添加面板」里粘贴「面板地址 + Key」。

客户端流程：

```
输入地址 + Key
  ↓
① 地址规范化与合法性校验（4.3）
② GET /auth/status            ← 公开，判断服务可达、admin_set 是否完成
③ GET /auth/whoami  (Bearer)  ← 校验 Key
     ├ scope="view"|"admin" → 成功，记录 scope
     ├ scope=""             → 红字「Key 无效或已被吊销」
     └ 401                  → 同上
④ GET /site                   ← 拿 title 作为面板默认名
⑤ GET /nodes                  ← 预热首屏（顺带验证读权限）
⑥ 保存面板配置
```

优点：不改后端、Key 可命名可审计、可在面板里单独吊销。
缺点：首次配置要跨设备操作一次（电脑上生成、手机上粘贴）。

#### 方案 K2（后续优化，需后端 3~5 行）

给 `POST /auth/login` 加可选字段：`{"password":"...","scope":"admin","issue_key":true}`，登录成功时顺带返回 `{"api_key":"cck_..."}`。

这样客户端就能实现 `APP-DESIGN.md` 3.2 的「地址 + 密码一步到位」体验。风险增量很小——原来的 `login` 已经能换到 7 天有效期的管理会话，多返回一条可吊销的长期 Key 没有本质变化。**首版不做，列为待决策 D2。**

#### 方案 K3（不采用）

「先 `login` 拿 Cookie，再调 `POST /auth/api-keys` 签发」——链条太长（要求 admin 会话 + 可控 Cookie 容器），三端各自都有坑，收益只是省一次复制粘贴。不用。

### 4.3 地址规范化（三端统一，按小程序最严规则）

安卓端要兼容 `192.168.1.10`、各种端口、明文 http（`APP-DESIGN.md` 4.2）。移动端因为小程序只能用 HTTPS 域名，规则收敛为：

| 用户输入 | 结果 | 说明 |
|---|---|---|
| `dash.example.com` | `https://dash.example.com` | 补 scheme |
| `https://dash.example.com/` | `https://dash.example.com` | 去尾斜杠 |
| `https://dash.example.com/cc` | `https://dash.example.com/cc` | **保留路径前缀**（反代子路径） |
| `http://dash.example.com` | `https://dash.example.com` + 提示「已改用 HTTPS」 | 明文不可用 |
| `192.168.1.10:8080` | ❌ 拒绝：「小程序不支持 IP 与端口，请填写公网 HTTPS 域名」 | 明确报错优于静默失败 |
| `https://dash.example.com:8443` | ❌ 拒绝：「不支持非 443 端口」 | 微信网关固定 443 |

规范化后的 `baseUrl` 与 `/api/v1` 拼接；保存 `rawInput` 供回显。

**额外提示**：小程序端域名必须出现在后台「request 合法域名 / socket 合法域名」列表里，否则请求直接失败，而 `fail` 回调只给 `errMsg: "request:fail"`，**不会告诉你这是域名白名单问题**。所以设置页要把当前请求域名展示出来（5.8），方便用户对照后台排查；错误矩阵（11 章）也单列了这一行。

### 4.4 面板数据模型

首版**只支持单面板**（多面板列为 D1），但数据结构按多面板设计，避免以后改存储格式：

```ts
// storage key: "cc.panels" → Panel[]
interface Panel {
  id: number            // 本地自增，非服务端 ID
  name: string          // 默认取 GET /site 的 title，用户可改
  baseUrl: string       // 规范化后，如 https://dash.example.com
  rawInput: string      // 用户原始输入，仅回显
  siteTitle: string
  apiKey: string        // cck_...，明文存 storage（2.3）
  keyScope: 'view' | 'admin'
  showOnHome: boolean   // 预留：多面板时控制首页显示
  nodeFilter: number[] | null  // null = 全部
  sortOrder: number
  lastOkAt: number      // 上次成功请求时间（Unix 秒）
  lastError: string     // 最近一次失败原因
}
```

**`node.id` 只在单个面板内唯一**（`APP-DESIGN.md` 4.3），多面板时必须用复合键 `(panelId, nodeId)`。首版单面板下用裸 `nodeId` 即可，但组件 props 统一命名 `panelId` + `nodeId`，为后续留出接口。

### 4.5 凭据安全边界

| 数据 | 位置 | 加密 |
|---|---|---|
| 面板配置（含 `apiKey`） | `cc.panels` | **无**（2.3） |
| 节点缓存 | `cc.cache.<panelId>.nodes` | 无 |
| 用户设置 | `cc.settings` | 无 |

明确**不保存**：管理员密码（只存 Key）、Cookie、节点 Token。这样即使 storage 泄露，攻击者拿到的是「一条可在面板里立即吊销的只读 Key」，而不是能改配置的管理密码。

| 措施 | 做法 |
|---|---|
| 最小权限 | 首版只读 → 一律用 `scope=view` 的 Key。`view` Key 无法增删节点、无法改配置 |
| 可吊销 | Key 在面板后台可单独删除，不影响其他设备（多凭证已落地） |
| 一键清除 | 设置页「清除所有数据」→ 清 storage + 提示去面板吊销该 Key |
| 日志 | 不 `console.log` 打印 `apiKey`；`uni.request` 的 `fail` 回调不要整体 dump header |
| 分享 | 面板配置属敏感数据，**三端都禁用页面转发**（小程序 `onShareAppMessage` 返回空；H5 关闭分享引导） |
| 截屏 | 小程序无 `FLAG_SECURE`，App 端可选实现；H5 无法禁止。接受这个缺口 |

### 4.6 会话生命周期

Key 不过期，所以没有「会话过期」，只有三种失败：

```
App 启动 / 回到前台
  └→ GET /auth/whoami  (Bearer)
        ├ scope 非空        → Key 有效，继续
        ├ scope 为空 / 401  → Key 已被吊销或面板数据被重置
        │     └ 不自动重试（无密码可换 Key），标记「需要重新配置」，进设置页重填 Key
        └ 网络错误          → 标记「离线」，保留缓存数据展示
```

> 关键设计：**没有密码就不能静默恢复，因此绝不做自动重试刷请求**（与 `APP-DESIGN.md` 4.5 结论一致）。只要 `whoami` 返回空 scope，就停下来让用户介入。

### 4.7 401 的三种含义必须区分

`API.md` 第 9 节：401 既是「Key 无效/吊销」，也是「`view` Key 调了写接口」。必须靠响应体 `message` 区分，否则会出现「只是权限不足，却把用户 Key 清掉了」的恶性体验。

| 触发 | `message` 特征 | 处理 |
|---|---|---|
| Key 无效 / 已吊销 | 一般为空或「未认证」 | 清 `keyScope`，标记「需重新配置」，跳设置页 |
| `view` Key 调写接口 | 含「需要管理员登录」 | **不清 Key**，仅提示「当前面板为只读权限」 |
| 面板开启访问密码但 Key 权限不足 | 含「需要访问密码」 | 提示检查 Key 的 scope |

实现约定：`request.js` 抛出带 `status` 与 `message` 的错误对象，由 store 层决定是否清凭据，**请求层不直接清**。

---

## 5. 页面结构与导航

### 5.1 页面地图

```
启动
 ├─ 冷启动 → 读 storage 面板配置
 │             ├─ 无面板  → 引导页（添加面板：地址 + Key）
 │             └─ 有面板  → 概览页（先渲染缓存，再拉新）
 │
tabBar（原生 tabBar，3 项，三端通用）
 ├── 概览 ─→ 设备详情（二级页）
 ├── 告警
 └── 设置
       ├─ 面板配置（编辑地址/Key/测试连接/选择可见节点/删除）
       ├─ 节点管理（仅 admin Key；待审批 + 添加 agent 节点）
       └─ 通用（WS 开关、轮询间隔、离线排最后、清除缓存、关于）
```

**为什么告警单独做一个 Tab**：小程序能用 `wx.setTabBarBadge` 在 tab 上打红点，告警是唯一需要「打扰用户」的信息，独立 Tab 是唯一能挂红点的位置。（H5/App 端 uni-app 同样支持 `uni.setTabBarBadge`，三端一致。）

**详情页是二级页**（`uni.navigateTo`），不是 Tab。返回时概览页保持原状态不重建——这是 tab 页常驻的收益。

### 5.2 概览页（tab 1）

```
┌──────────────────────────────────┐
│  CC_DashBoard            🔍  ⟳   │  ← 自定义导航栏：站点标题 / 搜索 / 刷新
├──────────────────────────────────┤
│  ● 8 在线   1 告警   2 离线       │  ← 汇总条（点「告警」跳 tab 2）
│  [全部][在线][告警][离线]          │  ← 状态筛选（横向可滚动）
├──────────────────────────────────┤
│  家里                      3 台   │  ← group_tag 分组头（全部未分组时不显示）
│ ┌──────────────────────────────┐ │
│ │ ● nas01              linux   │ │
│ │ 192.168.1.10 · 203.0.113.7   │ │
│ │   ◔42%     ◔63%     ◔55%    │ │
│ │   CPU      内存      磁盘     │ │
│ │ ↑ 320 KB/s  ↓ 1.2 MB/s       │ │
│ │                 运行 12天5h   │ │
│ └──────────────────────────────┘ │
└──────────────────────────────────┘
     [概览]     [告警]     [设置]
```

**与 Web 端对齐的分组逻辑**：Web 用 `group_tag` 分组（`Overview.vue` 的 `groupedNodes`），安卓设计文档用「面板」分组。移动端首版是**单面板**，所以对齐 Web —— 用 `n.group_tag` 分组，未分组（`tag === ''`）排最后，且分组间保持首次出现顺序（稳定排序）。

| 场景 | 表现 |
|---|---|
| 全部节点都无 `group_tag` | 不渲染分组头，卡片平铺 |
| 部分有 `group_tag` | 有标签的在前（按首次出现顺序），未分组在后 |
| 横屏 / 平板（`windowWidth ≥ 768px`） | 2 列网格（`APP-DESIGN.md` 5.1）。首版可只做 1 列，列为 D4 |
| 加载失败 | 顶部红色错误条 + 「重试」，**保留上次成功数据**（与 Web `lastError` 策略一致） |

**筛选三层**（沿用 `APP-DESIGN.md` 5.6，去掉「面板级」一层因为首版单面板）：

1. 面板级 `nodeFilter`（持久化，设置页配置）；
2. 全局状态筛选（`全部 / 在线 / 告警 / 离线`，会话级，不持久化）；
3. 搜索（`name` / `address` / `public_ip`，忽略大小写）。

**筛选后为空的三种文案**（不能统一写「没有设备」）：

| 情况 | 文案 | 附加操作 |
|---|---|---|
| `nodeFilter` 选中的节点全被删 | 「该面板暂无可见节点」 | 「去调整」→ 设置页 |
| 搜索/状态筛选无结果 | 「没有匹配的设备」 | 「清除筛选」 |
| 服务端真没节点 | 「这个面板还没有添加节点」 | 「去添加」→ 节点管理（需 admin） |

### 5.3 节点卡片信息架构

字段来源全部是 `GET /nodes` 的单个节点对象，与 `APP-DESIGN.md` 5.2 一致：

| 界面元素 | 字段 | 备注 |
|---|---|---|
| 状态灯 | `status` | `online` 绿 / `alert` 红脉冲 / `offline` 灰 |
| 名称 | `name` | 单行省略号 |
| 系统徽标 | `os_type` | 7 种，小写字母缩写 + 灰底圆角标签 |
| 可升级 | `upgrade` | **字段存在才显示**（仅 admin + 池中有新版）。首版用 view Key 实际不会出现 |
| 内网地址 | `address` | 空则不显示 |
| 公网 IP | `public_ip` | 空则不显示 |
| CPU % | `latest.cpu` | 四舍五入取整 |
| 内存 % | `latest.mem_pct`（优先）→ `mem_used/mem_total*100`（兜底） | ⚠️ `APP-DESIGN.md` 5.2 写「API 没有 `mem_pct` 必须自算」**已过时**——`APP-API.md` 4.5 的 `Sample` 已含 `mem_pct`。按 Web 的 `memPct()` 逻辑写：先看 `mem_pct`，再看兜底 |
| 磁盘 % | `latest.disk_pct` | — |
| 上传速率 | `latest.net_tx` | **`↑` 对应 `net_tx`**，与 Web 的 `title="上传"` 一致 |
| 下载速率 | `latest.net_rx` | **`↓` 对应 `net_rx`** |
| 运行时长 | `latest.uptime` | 秒 → `fmtUptime` |
| 采集失败 | `last_error` | 非空时在卡片底部显示一行黄字（Web 未展示，移动端建议加，弱网场景有用） |

**三种降级形态**（都必须实现，不能只做「有数据」一种）：

| 形态 | 判定 | 表现 |
|---|---|---|
| 离线 | `status === 'offline'` | 三个环画空轨道 + 中心 `—`；底部「离线 · 最后在线 3 小时前」（用 `last_seen` 算）；网速 `—`。**绝不显示 0%** |
| 从未上报 | `latest` 整个字段**缺失**（不是 `null`） | 整块指标区「等待首报数据」，环留空 |
| 正常 | `latest` 存在 | 完整渲染 |

### 5.4 告警页（tab 2）

```
┌──────────────────────────────────┐
│  告警                    ⟳       │
│  [触发中 3] [已恢复 12] [全部]    │
├──────────────────────────────────┤
│ 🔴 critical   nas01     CPU       │
│    92.5% · 触发于 5 分钟前         │
├──────────────────────────────────┤
│ 🟡 warning    openwrt   磁盘      │
│    78.3% · 触发于 2 小时前         │
└──────────────────────────────────┘
```

- 数据源：`GET /alerts?status=firing|resolved|all`，最多 200 条，按 id 倒序；
- `metric` 中文映射：`cpu`→CPU、`mem_pct`→内存、`disk_pct`→磁盘、`load1`→负载、`net_rx`→下行、`net_tx`→上行、`offline`→离线；
- `level`：`critical` 红、`warning` 黄；
- **`offline` 类告警的 `value` 是「已离线秒数」而不是百分比**，展示必须走 `fmtUptime`，不能加 `%`（`APP-API.md` 6.2 已注明）；
- 点击条目 → 跳对应设备详情；
- 实时性：收到 WS `alert_firing` / `alert_resolved` 时刷新列表（`nodes.js` 的 `alertTick` 机制可原样移植）+ 30 秒兜底轮询；
- tab 红点：`firing` 数量 > 0 时 `uni.setTabBarBadge({ index: 1, text: String(n) })`，为 0 时 `uni.removeTabBarBadge`。

### 5.5 设备详情页数据时序

```
进入详情
 ├ GET /nodes/:id/detail              ← 一次拿全：node/latest/live/detail/hardware/events/boot_events/alerts
 ├ GET /nodes/:id/history?range=24h   ← 默认 24 小时
 └ 复用概览页的 WS，按 node_id 过滤 metrics / live 消息 → 就地刷新实时区
```

- **不要**把 `detail` 拆成多个请求（弱网体验差）；
- WS 消息只更新「实时指标」与顶部状态，**不重绘历史曲线**；
- 从详情页返回再进入同一节点：`live` 可能已过期，`onShow` 时重新拉一次 `detail`。

### 5.6 详情页 6 段结构

Web 端详情页有 22 个区块，压缩为 6 段（`APP-DESIGN.md` 6.2 的结论，移动端完全沿用）：

| # | 区块 | 内容 | 数据来源 | 默认 |
|---|---|---|---|---|
| 1 | 顶部概览 | 名称、状态、系统、内网/公网 IP、运行时长 | `node` | 展开 |
| 2 | 实时指标 | CPU 大环 + 内存/磁盘小环 + 上下行 + 负载 | `latest` + WS `metrics` | 展开 |
| 3 | 历史曲线 | range 选择器 + 4 张图（CPU/内存/网络/磁盘 IO） | `history` | 展开 |
| 4 | 实时明细 | 每核 CPU 柱状、内存细分、温度、分网卡速率、进程 Top5 | `live`（WS） | 折叠 |
| 5 | 系统与硬件 | 主机名/内核/架构、挂载点、CPU 型号、内存条、主板、磁盘、网卡 | `detail` + `hardware` | 折叠 |
| 6 | 事件与告警 | 当前 firing 告警、上下线事件、开关机记录 | `alerts` + `events` + `boot_events` | 折叠 |

排列顺序即用户关心的顺序：**实时 → 趋势 → 细节 → 静态信息 → 历史事件**。

**`live` 是自由结构，必须容错解析**（`APP-DESIGN.md` 6.3）：

| 子结构 | 字段 | 用途 |
|---|---|---|
| 顶层 | `cpu_total`、`per_cpu[]`、`load1/5/15` | 每核柱状 + 负载 |
| `mem` | `total/used/available/free/buffers/cached/swap_*/percent` | 内存堆叠条（已用/缓存/空闲） |
| `disk_io` | `read_bps/write_bps/read_count/write_count` | 磁盘 IO 实时 |
| `nics[]` | `name/rx_bps/tx_bps/mac/ipv4` | 分网卡速率表 |
| `temps[]` | `label/value` | 温度标签 |
| `procs[]` | `pid/name/cpu/mem` | 进程 Top5 |
| `vms[]`（PVE） | `vmid/name/status/type` | 虚机/容器列表 |

任何一段缺失就**隐藏该段**，不显示空表或 `NaN`。字段名大小写、驼峰/下划线按文档写死（服务端透传，无统一约定）。

### 5.7 空数据形态（必须逐条区分）

实测空态：`{"latest":null,"live":null,"detail":null,"hardware":null,"events":null,"boot_events":[],"alerts":[]}`

| 字段 | 空值形态 | UI 表现 |
|---|---|---|
| `latest` | `null` | 环画空 + 「等待首报数据」 |
| `live` | `null` | **隐藏第 4 段整段** |
| `detail` / `hardware` | `null` | **隐藏第 5 段整段** |
| `events` | **`null`** | 显示「暂无事件」 |
| `boot_events` | `[]` | 显示「暂无开关机记录」 |
| `alerts` | `[]` | 显示「当前无告警」 |

> `events` 是 `null` 而 `boot_events` 是 `[]` —— **不要写统一的空判断**。`Array.isArray(null)` 返回 `false`，容易把「无事件」误判成「加载中」然后一直转圈。

### 5.8 设置页（tab 3）

```
┌──────────────────────────────────┐
│  设置                             │
├──────────────────────────────────┤
│  面板                             │
│  ┌────────────────────────────┐  │
│  │ 家里 NAS          ● 已连接  │  │
│  │ https://dash.example.com   │  │
│  │ 12 个节点 · 3 分钟前同步    │  │
│  │             [编辑] [删除]   │  │
│  └────────────────────────────┘  │
│  [+ 添加面板]                     │
├──────────────────────────────────┤
│  节点管理（需 admin Key）     →   │
├──────────────────────────────────┤
│  实时更新（WS）           [开关]  │
│  WS 断线轮询间隔          30 秒 → │
│  离线排最后               [开关]  │
│  清除缓存                     →   │
│  清除所有数据（含 Key）        →   │
├──────────────────────────────────┤
│  关于                             │
│  版本 v1.0.0                      │
│  后端 API 版本 v1.3               │
│  请求域名 dash.example.com        │  ← 排查域名白名单问题用（4.3）
└──────────────────────────────────┘
```

**面板编辑页**必须包含：

- 连接状态卡：`baseUrl`、`/auth/status` 的 `admin_set` / `view_enabled`、当前 `scope`、上次成功时间；
- **测试连接**：逐条跑 4.2 的验证链并显示每一步结果（① 地址格式 ② 可达性 ③ Key 有效性 ④ 读权限），便于排障；
- 首页可见节点选择（`nodeFilter`）：全选 / 全不选 / 逐项勾选，默认全选。规则见 `APP-DESIGN.md` 7.2——服务端新增节点在 `Only` 模式下默认不显示（刻意设计），同时给出「有 2 个新节点未显示」提示条；
- 删除面板：二次确认，同时清该面板的缓存与 Key。

**节点管理页**（仅 `keyScope === 'admin'` 可见入口）：

- 「待审批 (N)」独立分组（`GET /nodes` 里 `approved === false`，仅 admin 可见）；
- 点某条 → 底部弹层显示设备名 / `os_type` / `address` / 首次出现时间 → 「批准」`PUT /nodes/:id {"approved":true}` / 「拒绝」`DELETE /nodes/:id`（服务端无「拒绝」语义，删除即拒绝），都要二次确认；
- 添加节点：只做 **agent 模式**（名称、`os_type`、上报周期、启用开关）。提交后 `POST /nodes` 响应里的 `token` **必须展示并支持复制**（唯一可见机会），同时给出 `cc-agent -master <url> -token <token> -interval 5` 启动命令。

### 5.9 引导页（首次启动）

无面板配置时进入，只有两个输入框和一个按钮：

```
面板地址  [dash.example.com        ]
访问密钥  [cck_...            ] [粘贴]
          [测试并保存]
          ┌──────────────────────────────┐
          │ 还没有密钥？在电脑上打开面板   │
          │ → 节点管理 → API Key → 生成   │
          │ 权限选「只读(view)」          │
          └──────────────────────────────┘
```

设计要点：

- 密钥输入框提供「粘贴」按钮（`uni.getClipboardData`），Key 有 40+ 字符，比长按粘贴体验好；
- 密钥默认掩码显示，可点眼睛切换；
- 「测试并保存」期间按钮 loading 且不可重复点击（防重复创建面板）；
- 失败提示必须具体到步骤（4.2 的①②③④），不能只显示「连接失败」；
- 小程序端额外提示：**首次使用需在微信后台配置域名白名单**，否则会卡在「无法连接」。

---

## 6. 组件规格

### 6.1 环形仪表盘（关键性能决策）

`APP-DESIGN.md` 5.3 规定了「270° 圆环，起点 135°，缺口朝下」，并要求 Compose `Canvas` 自绘。**移动端不能无脑照搬**——小程序里同屏 canvas 数量一多就严重掉帧，而首页列表可能有几十个节点、每个卡片 3 个环。

因此按使用场景拆成两套实现：

| 场景 | 数量级 | 实现 | 理由 |
|---|---|---|---|
| **首页卡片** | 每卡片 1 个（一 canvas/CSS 画 3 环）× N 卡片 | **纯 CSS `conic-gradient`** | 零 canvas 开销，滚动流畅 |
| **详情页** | 1 个大环 + 2 个小环 | **canvas 2d** | 只有 3 个实例，可做圆角端点与 300ms 动画 |

**规格（两套共用）**：

| 项 | 首页卡片 | 详情页 |
|---|---|---|
| 直径 | 128rpx（≈64dp） | 主环 240rpx（≈120dp），小环 160rpx |
| 环宽 | 16rpx（≈8dp） | 主环 20rpx，小环 16rpx |
| 起始角 / 扫过角 | 135° / 270° | 同 |
| 中心数值 | 28rpx / 600 | 主环 48rpx / 600，小环 32rpx |
| 中心标签 | 22rpx / 400 灰 | 26rpx / 400 灰 |

**阈值配色**（与 Web `Overview.vue` / `DeviceDetail.vue`、`tailwind.config.js` 完全一致）：

| 值 | 颜色 | 语义 |
|---|---|---|
| `< 75%` | `#10b981` emerald | 正常 |
| `75% ~ 89%` | `#f59e0b` warning | 注意 |
| `≥ 90%` | `#ef4444` danger | 告警 |
| 轨道 | `#1e293b` slate-800 | — |

**CSS 实现要点（首页卡片）**：

```scss
.gauge {
  width: 128rpx; height: 128rpx; border-radius: 50%;
  // 135° 起，进度占 p%，其余到 270° 是轨道，剩 90° 透明（缺口）
  background: conic-gradient(from 135deg,
      var(--gauge-color) 0deg calc(var(--p) * 2.7deg),
      #1e293b calc(var(--p) * 2.7deg) 270deg,
      transparent 270deg 360deg);
  -webkit-mask: radial-gradient(circle, transparent 0 40%, #000 40%);
          mask: radial-gradient(circle, transparent 0 40%, #000 40%);
}
```

- 用 `--p`（0~100）驱动，Vue 里 `:style="{ '--p': pct, '--gauge-color': color }"`；
- **降级兜底**：若目标机型不支持 `conic-gradient`（老 Android WebView），退化为「横向细进度条 + 数字」，样式沿用 Web 的 `h-1.5` 圆角条。不要用图片兜底（占包体）；
- 端点退化：CSS 版是方端点，**接受这个差异**；详情页 canvas 版才做圆角端点。

**canvas 实现要点（详情页）**：

1. `<canvas type="2d" :id="cid" :canvas-id="cid">`，用 `uni.createSelectorQuery().in(this).select('#'+cid).fields({node:true,size:true})` 拿 node；
2. 必须按 `dpr` 设置 `node.width = w * dpr`，再 `ctx.scale(dpr, dpr)`，否则真机模糊；
3. `ctx.arc(cx, cy, r, toRad(135), toRad(135 + 270 * p/100))`，`lineWidth` 按 rpx 换算，`lineCap:'round'`；
4. 轨道整圈画：`arc(cx, cy, r, toRad(135), toRad(405))`；
5. 动画用 `node.requestAnimationFrame`，300ms `easeOutCubic`；**首次进入不动画**（避免整屏扫动），只在增量更新时动画（`APP-DESIGN.md` 5.3）；
6. `percent` 做 `clamp(0, 100)`；`NaN` / `null` / 缺失一律按「无数据」渲染空环 + 中心 `—`，不画进度。

### 6.2 状态灯与系统徽标

**StatusDot**：

| `status` | 颜色 | 动效 |
|---|---|---|
| `online` | `#10b981` | 静态 |
| `alert` | `#ef4444` | 呼吸脉冲（CSS `animation: pulse 1.5s infinite`，只改 `opacity`） |
| `offline` | `#9ca3af` | 静态 |

尺寸：卡片 20rpx，详情页顶部 24rpx。`alert` 脉冲用 CSS 而非 canvas，避免额外实例。

**OsBadge**：7 种 `os_type`，用小写缩写 + 灰底标签，不引图标资源：

| `os_type` | 显示 |
|---|---|
| `linux` / `windows` / `nas` / `openwrt` / `darwin` / `pve` / `esxi` | 原样小写（与 Web `Overview.vue` 的 `{{ n.os_type }}` 一致，不翻译） |

样式：`background:#1e293b; color:#94a3b8; font-size:20rpx; padding:2rpx 10rpx; border-radius:6rpx; text-transform:uppercase`。

### 6.3 历史曲线（TrendChart）

**选型**：canvas 2d 自绘，不引 ECharts / uCharts（2.4）。需求很简单——折线 + 面积填充 + 少量网格线，约 200 行。

**规格**：

| 项 | 值 |
|---|---|
| 图表数量 | 4 张（CPU / 内存 / 网络 / 磁盘 IO），折叠时**不渲染**（`v-if` 而非 `v-show`，避免多余 canvas 实例） |
| 高度 | 每张 320rpx |
| 网格 | 3 条横向虚线 `#1e293b`，不画纵线 |
| 坐标标签 | 左侧 3 个数值（最大/中/最小），底部 2 个时间（起/止），22rpx `#64748b` |
| 折线 | 2rpx，主色 `#10b981` |
| 面积 | 同色 `rgba(16,185,129,0.12)` 渐变到底部 |
| 网络图 | 双折线：`net_rx` 绿、`net_tx` 蓝（`#3b82f6`），共享 Y 轴 |
| 空数据 | 显示「该时间段暂无数据」，不画空坐标系 |

**range 与降采样**（`APP-API.md` 4.8）：

| range | 标签 | 上限点数 | 是否降采样 |
|---|---|---|---|
| `1h` | 1 小时 | 720 | 否 |
| `24h` | 24 小时 | 1440 | 视宽度 |
| `7d` | 7 天 | 10080 | **必须** |
| `30d` / `180d` / `365d` | 30 天 / 半年 / 一年 | 720 / 4320 / 8760 | 后两者必须 |

- 降采样目标点数 = `画布 CSS 宽度 × 2`（约 700~800 点），用 **LTTB**（Largest-Triangle-Three-Buckets）保留峰值形态；
- `points[].per_cpu` 是**字符串**（如 `"[12.5,8.3]"`），需要在 store 层二次解析；空串表示该窗口无数据。首版不画每核曲线，只解析后丢弃，避免无谓开销；
- `mem_pct` 同理优先用服务端值，缺失时逐点用 `mem_used/mem_total*100` 兜底；
- **range 切换时保留旧图**直到新数据到达（避免闪白），加 loading 遮罩；
- 首版**不做**触摸提示（tooltip），列为 D5。若要做，用 `<canvas>` 的 `touchstart/touchmove` + 重绘高亮竖线。

### 6.4 NodeCard 布局尺寸

```
卡片：padding 32rpx，圆角 24rpx，背景 #0f172a，边框 1rpx #1e293b
├─ 首行：状态灯 20rpx + 名称 30rpx/500 + 右侧 OsBadge
├─ 次行：address（28rpx #64748b）· public_ip（28rpx #94a3b8）
├─ 指标行：3 个圆环横排，间距均分（flex:1），高 128rpx + 标签 22rpx
└─ 底行：↑上行  ↓下行  ·  运行时长（22rpx #94a3b8，上边框 1rpx #1e293b）
```

- 卡片间距 24rpx；整卡 `hover-class` 给点击反馈（小程序无 `:hover`）；
- 全卡可点击 → 详情页；点击态用 `background:#111c33` 而非缩放（避免列表抖动）。

### 6.5 色板与格式化

**色板**（对齐 `web/src/style.css` 与 `web/tailwind.config.js`）：

| 语义 | 值 | 用途 |
|---|---|---|
| 页面底 | `#020617` slate-950 | body |
| 卡片底 | `#0f172a` slate-900 | 卡片、弹层 |
| 描边 / 轨道 | `#1e293b` slate-800 | 边框、进度条轨道 |
| 主文字 | `#e2e8f0` slate-200 | — |
| 次文字 | `#94a3b8` slate-400 | — |
| 弱文字 | `#64748b` slate-500 | 标签、时间 |
| 在线 / 正常 | `#10b981` | 状态灯、折线、品牌色 |
| 警告 | `#f59e0b` | 75%~89% |
| 危险 | `#ef4444` | ≥90%、critical 告警 |
| 离线 | `#9ca3af` | 离线状态灯 |
| 链接蓝 | `#3b82f6` | 网络图下行线 |

**格式化函数**直接复用 `web/src/utils/format.js`：`fmtRate` / `fmtBytes` / `fmtUptime` / `fmtClock` / `fmtVer` / `fmtPct` / `statusText`。

唯一需要改的是 `statusDotClass`——它返回 Tailwind class 名（`'bg-online'`），移动端没有 Tailwind，改为返回色值：

```js
export function statusColor(status) {
  if (status === 'online') return '#10b981'
  if (status === 'alert') return '#ef4444'
  return '#9ca3af'
}
export function gaugeColor(status, pct) {
  if (status === 'offline') return '#1e293b'
  if (pct >= 90) return '#ef4444'
  if (pct >= 75) return '#f59e0b'
  return '#10b981'
}
```

### 6.6 空态与错误条

**EmptyState** 三种模式（对应 5.2 的三种文案），props：`icon`（内联 SVG）、`title`、`desc`、`actionText`、`@action`。

**ErrorBar** 固定在内容区顶部：红底 `rgba(239,68,68,0.12)` + `#fca5a5` 文字 + 右侧「重试」按钮；文案区分「部分面板连接失败」「无法连接面板」「Key 已失效」三种，不要把技术错误原文抛给用户。

---

## 7. 数据层与实时通道

### 7.1 数据模型（TypeScript）

`APP-API.md` 第 5 章给的是 Kotlin 版本，这里换成 TS。**所有字段可选**（服务端会加字段，也会整段缺失），并统一 `snake_case` 与接口一致，不做 camelCase 转换——多一层映射只会多一处出错点。

```ts
export interface Sample {
  node_id?: number
  ts?: number
  cpu?: number
  mem_used?: number
  mem_total?: number
  mem_pct?: number          // 服务端已算好，优先使用
  disk_pct?: number
  net_rx?: number           // Bytes/s，↓ 下载
  net_tx?: number           // Bytes/s，↑ 上传
  load1?: number
  uptime?: number           // 秒
  disk_read?: number
  disk_write?: number
  temp_max?: number         // ℃，0 = 无传感器
  per_cpu?: number[]
}

export interface NodeItem {
  id: number
  name: string
  os_type?: string          // linux|windows|nas|openwrt|darwin|pve|esxi
  access_mode?: string      // agent|ssh|api
  address?: string
  public_ip?: string
  status?: 'online' | 'offline' | 'alert'
  last_seen?: number
  sort_order?: number
  group_tag?: string        // 空 = 未分组
  approved?: boolean
  enabled?: boolean
  token?: string            // view Key 下恒为空串
  agent_version?: string
  report_interval?: number
  mute_until?: number
  last_error?: string
  last_error_at?: number
  latest?: Sample          // ⚠️ 可能整个字段缺失，不是 null
  upgrade?: { version: string; pool_id: number }  // 仅 admin + 有新版本时出现
}

export interface Alert {
  id: number
  node_id: number
  node_name: string
  metric: string            // cpu|mem_pct|disk_pct|load1|net_rx|net_tx|offline
  level: 'warning' | 'critical'
  value: number
  status: 'firing' | 'resolved'
  triggered_at: number
  resolved_at: number       // 0 = 未恢复
}

export interface Summary {
  total: number
  online: number
  offline: number
  alerting: number
  ws_clients?: number
  alerts?: Alert[] | null   // 无告警时是 null，不是 []
}
```

`live` / `detail` / `hardware` 是自由结构，直接用 `Record<string, any>`，**不要硬编码 interface**（字段由采集端决定，`APP-API.md` 4.7）。

### 7.2 请求封装

```js
// api/request.js 的核心形态
function request(path, { method = 'GET', data, timeout = 10000 } = {}) {
  const panel = usePanelsStore().active
  return new Promise((resolve, reject) => {
    uni.request({
      url: panel.baseUrl + '/api/v1' + path,
      method,
      data,
      timeout,
      header: {
        Authorization: 'Bearer ' + panel.apiKey,   // 全程 Bearer，不需要 Cookie
        Accept: 'application/json',
      },
      success(res) {
        if (res.statusCode >= 200 && res.statusCode < 300) return resolve(res.data)
        reject(normalizeError(res))                // 只做分类，不清凭据（4.7）
      },
      fail(err) {
        reject({ status: 0, message: err.errMsg || 'network error' })
      },
    })
  })
}
```

**要点**：

- `timeout` 必须显式传（默认 60 秒太长，2.5）；
- 不需要 `CookieJar`，不需要处理 CSRF（带 Bearer 的请求服务端跳过 Origin 校验）；
- 401 **只分类不清凭据**，由 store 决定；
- 服务端已开 gzip，`uni.request` 自动带 `Accept-Encoding` 并解压，无需配置；
- 204 响应（`DELETE /nodes/:id` 之类）`res.data` 为空，调用方要容错。

### 7.3 错误分类

| 分类 | 判定 | store 层动作 |
|---|---|---|
| `network` | `status === 0` / `errMsg` 含 `fail` | 标记面板「离线」，保留缓存，展示 ErrorBar |
| `domain` | `errMsg` 含 `domain` 或 `not in domain list` | 提示「域名未加入小程序白名单」，附当前域名（4.3） |
| `auth` | 401 且 message 不含「管理员」「访问密码」 | 清 `keyScope`，跳设置页 |
| `forbidden` | 401 且 message 含「需要管理员登录」 | **不清凭据**，提示「当前为只读权限」 |
| `notfound` | 404 | 提示「设备已删除」+ 返回并刷新 |
| `server` | 500 | 通用错误条 + 重试 |
| `badreq` | 400 | 提示参数问题（开发期问题） |
| `locked` | 429 | 提示 `Retry-After` 秒数后重试（仅登录相关） |

### 7.4 WebSocket 通道

```js
// api/socket.js —— 三端统一封装
class SocketChannel {
  open(url, header) {                       // url: wss://host/api/v1/ws
    this.task = uni.connectSocket({
      url, header,                            // header 携带 Authorization
      success: () => {},
      fail: (e) => this.onFail(e),
    })
    this.task.onOpen(...); this.task.onMessage(...);
    this.task.onClose(...); this.task.onError(...);
  }
}
```

**鉴权**：三端都用请求头 `Authorization: Bearer cck_...`（`API.md` 8 节支持）。若某端实测 `<header>` 被丢弃，退化为 `?access_token=cck_...` —— 但要注意 **query 方式会进服务端访问日志**（`API.md` 11 节已警示），只在不得已时用，并在文档/设置里告知用户。

**保活（三端统一，2.1 已说明都不能发 PING）**：

```
记录 lastMsgAt = 收到任意消息的时刻
定时器每 10 秒检查：
  now - lastMsgAt > max(30s, 3 × report_interval) → 判定假活，closeSocket 后走重连
```

**重连退避**：`1s → 2s → 4s → 8s → 15s → 30s`（上限 30s，每次成功连接后重置），与 `APP-DESIGN.md` 8.3 一致。退避期间改用 30 秒 `GET /nodes` 轮询兜底。

**重连成功后必须立即 `GET /nodes` 重新对齐**——断线期间的消息不会补发（`APP-API.md` 6.3）。

**消息分发**（直接移植 `web/src/stores/nodes.js`）：

| `type` | 动作 |
|---|---|
| `metrics` | 按 `node_id` 就地更新对应节点的 `latest`；若原状态是 `offline` 则改 `online` 并刷 `summary` |
| `live` | **仅当详情页正在看该节点时**存入 `liveById`，否则丢弃（省内存） |
| `node_status` | 更新状态灯；状态变化时刷 `summary` |
| `alert_firing` / `alert_resolved` | `alertTick++`，告警页 watch 该计数重新拉取 + 更新 tab 红点 |
| `node_added` / `node_removed` | 重拉 `GET /nodes` + `summary`；若正看该节点详情则退回概览 |
| `node_pending` | 设置 tab 打红点，刷新待审批分组 |

**不要收到 `metrics` 就整表重载**——只更新对应 item，用 `:key`（`panelId + nodeId`）保证其它卡片不重建。

### 7.5 通知（首版不做，但要留接口）

| 端 | 可行方案 | 限制 |
|---|---|---|
| 小程序 | 订阅消息 | 需用户主动授权 + 模板审核；一次性授权只能推一条 |
| H5 | Web Push | 需 HTTPS + 用户授权；iOS Safari 支持较差 |
| App | 本地通知 | 需后台定时任务；uni-app 要引原生插件 |

服务端**没有**推送通道（`APP-DESIGN.md` 8.5），三端都只能靠客户端主动拉。首版统一不做，`platform/notify.js` 留空实现，接口签名固定为 `requestPermission()` / `notify(alert)`，后续按端填充。

真的要做时优先级：**App 端本地通知**（每 15 分钟拉 `/alerts?status=firing`，有新告警发本地通知）> H5 Web Push > 小程序订阅消息。列为 D6。

### 7.6 缓存策略

**三级状态**：本地缓存 → 网络刷新 → 实时推送。

```
冷启动
 ├ 读 storage 缓存（上次节点列表 + 时间戳）→ 立刻渲染（标记「缓存」）
 ├ 并发 GET /nodes + GET /summary
 │    ├ 成功 → 覆盖缓存、去掉「缓存」标记
 │    └ 失败 → 顶部 ErrorBar + 重试，保留缓存数据
 └ 连 WS → 增量更新
```

| 项 | 规则 |
|---|---|
| 缓存 key | `cc.cache.<panelId>.nodes` / `cc.cache.<panelId>.summary` |
| 缓存内容 | 节点列表 + 每个节点的 `latest`；**不缓存历史曲线**（体积大、时效性弱） |
| 保留期 | 7 天，超期清空（避免看到陈旧数据误判） |
| 体积保护 | 小程序单 key ≤ **1 MB**。100 节点 × ~600 B ≈ 60 KB，安全；但写入前要 `JSON.stringify` 后检查长度，超 800 KB 就丢弃 `live`/`detail` 只存精简字段 |
| 写频率 | 每次 `fetchNodes` 成功写一次（不是每帧）；WS `metrics` 只改内存，**不写 storage**（否则每 5 秒一次全量序列化，卡顿且磨损存储） |

**「缓存」标记**：冷启动时若数据来自 storage，汇总条下方显示一行灰字「显示的是缓存数据，正在更新…」，拿到网络数据后移除。

### 7.7 Store 结构

```
panelsStore     // 面板配置：增删改、activeId、Key 校验、TestConnection
  ├─ active: Panel | null
  ├─ list: Panel[]
  └─ actions: add / update / remove / testConnection / clearAll

nodesStore      // 节点与汇总 + WS（移植自 web/src/stores/nodes.js）
  ├─ nodes: NodeItem[]
  ├─ summary: Summary
  ├─ liveById: Record<number, any>
  ├─ connected: boolean
  ├─ lastError: string
  ├─ cacheAge: number
  └─ actions: init / fetchNodes / fetchSummary / connectWS / dispose

alertsStore     // 告警列表
  ├─ alerts: Alert[]
  ├─ filter: 'firing' | 'resolved' | 'all'
  └─ actions: fetch / setFilter

settingsStore   // 用户偏好
  ├─ wsEnabled, pollInterval, offlineLast
  └─ 持久化到 cc.settings
```

**依赖方向**：`panelsStore` 是根（`request.js` 从它读 `baseUrl` + `apiKey`），`nodesStore` / `alertsStore` 依赖 `panelsStore`，`settingsStore` 独立。不要出现「全局当前面板」的隐式状态之外的第二处面板引用。

---

## 8. 平台适配层清单

### 8.1 设计原则

**条件编译只允许出现在 `src/platform/` 下**（3.3）。任何其他文件出现 `#ifdef`，都说明该能力没抽象干净，应补进 `platform/`。这样整份适配清单天然收敛成一张短表。

### 8.2 各文件职责

| 文件 | 职责 | 是否需要条件编译 |
|---|---|---|
| `platform/env.js` | 平台标识、能力探测（`isMP` / `isH5` / `isApp`、`supportsConicGradient`） | 是（读 `process.env.UNI_PLATFORM`） |
| `platform/url.js` | 地址规范化与合法性校验（4.3） | 是（小程序端额外校验 IP/端口，给更明确的文案） |
| `platform/storage.js` | storage 读写 + 体积保护 + 命名空间 | 否（`uni.*` 已抹平），但**上限常量**按端不同（小程序 1 MB/key） |
| `platform/notify.js` | 通知能力 | 是（首版空实现，7.5） |
| `platform/share.js` | 分享开关（小程序禁用页面转发） | 是 |

### 8.3 条件编译点全表

| # | 位置 | 平台差异 | 处理 |
|---|---|---|---|
| 1 | `platform/env.js` | 平台标识 | `// #ifdef MP-WEIXIN` 等 |
| 2 | `platform/url.js` | 小程序拒绝 IP/端口并给专门文案；H5/App 可放宽为警告 | 条件编译 |
| 3 | `platform/storage.js` | 单 key 上限：小程序 1 MB，H5 约 5 MB，App 无硬限 | 常量表 |
| 4 | `platform/notify.js` | 三端能力完全不同 | 条件编译 |
| 5 | `platform/share.js` | 小程序需 `onShareAppMessage` 返回空；H5 需隐藏分享引导；App 无此概念 | 条件编译 |
| 6 | `components/NavBar.vue` | 小程序需避让右上胶囊；App 需状态栏高度；H5 无状态栏 | 样式层用 `env.js` 拿值，不写宏 |
| 7 | `pages/settings/index.vue` | 「微信小程序域名白名单」提示块仅小程序显示 | `v-if="isMP"` |
| 8 | `manifest.json` | appid / `h5.router.base` / App 权限与图标 | 各自的字段，无需编译 |
| 9 | `pages.json` | tabBar 图标路径三端相同 | 无差异 |

**就这些。** 如果一个新需求需要第 10 处条件编译，先回头看看是不是抽象层漏了东西。

### 8.4 manifest.json / pages.json 关键配置

```jsonc
// manifest.json 关键片段
{
  "name": "CC_DashBoard",
  "appid": "__UNI__XXXXXXX",          // 小程序端另需微信 appid（在微信工具里填）
  "mp-weixin": {
    "appid": "wxXXXXXXXXXXXX",        // 微信小程序 appid
    "setting": { "urlCheck": true },  // 生产必须为 true（2.2）
    "libVersion": "2.30.0",
    "permission": {},                 // 不需要任何系统权限
    "requiredPrivateInfos": []        // 不需要位置等隐私接口
  },
  "h5": {
    "router": { "base": "/m/" },      // 部署在面板同域子路径（3.2）
    "title": "CC_DashBoard"
  },
  "app-plus": {
    "distribute": { "android": { "permissions": [] } }
  }
}
```

```jsonc
// pages.json 关键片段
{
  "pages": [
    { "path": "pages/overview/index", "style": { "navigationStyle": "custom" } },
    { "path": "pages/alerts/index",   "style": { "navigationStyle": "custom" } },
    { "path": "pages/settings/index", "style": { "navigationStyle": "custom" } },
    { "path": "pages/welcome/index",  "style": { "navigationStyle": "custom" } },
    { "path": "pages/device/index",   "style": { "navigationStyle": "custom" } }
  ],
  "tabBar": {
    "color": "#64748b",
    "selectedColor": "#10b981",
    "backgroundColor": "#0f172a",
    "borderStyle": "black",
    "list": [
      { "pagePath": "pages/overview/index", "text": "概览", "iconPath": "static/tab/home.png",           "selectedIconPath": "static/tab/home-on.png" },
      { "pagePath": "pages/alerts/index",   "text": "告警", "iconPath": "static/tab/bell.png",           "selectedIconPath": "static/tab/bell-on.png" },
      { "pagePath": "pages/settings/index", "text": "设置", "iconPath": "static/tab/settings.png",       "selectedIconPath": "static/tab/settings-on.png" }
    ]
  },
  "globalStyle": {
    "backgroundColor": "#020617",
    "backgroundColorTop": "#020617",
    "backgroundColorBottom": "#020617",
    "navigationBarTextStyle": "white"
  }
}
```

> tabBar 图标必须是 **PNG 图片文件**（不支持 SVG / 字体图标），尺寸建议 81×81，单个 ≤ 40 KB。6 张图（3 普通 + 3 选中）合计约 10~20 KB，可接受。这是唯一必须存在的图片资源。

### 8.5 三端发布检查清单

| 端 | 必须确认 |
|---|---|
| 小程序 | ① 域名白名单（request + socket 都已配）② `urlCheck: true` ③ 隐私保护指引已填 ④ 类目「工具 → 效率」⑤ 体验版真机验证过 HTTPS 与 wss |
| H5 | ① 部署路径与 `h5.router.base` 一致 ② 面板同域或后端 CORS 已开（已有 `ACAO: *`）③ 若页面是 https，面板也必须 https（混合内容） |
| App | ① Android 需配 `usesCleartextTraffic`（若允许 http 面板）② iOS 需 ATS 例外 ③ 签名证书 ④ 图标与启动图 |

---

## 9. 样式与主题

### 9.1 尺度：rpx 优先

设计稿基准 **750rpx = 屏幕宽**。所有尺寸用 `rpx`，只有需要固定物理尺寸的（1px 描边、状态栏高度）用 `px`。

| 项 | 值 |
|---|---|
| 卡片内边距 | 32rpx |
| 卡片圆角 | 24rpx |
| 卡片间距 | 24rpx |
| 页面左右留白 | 24rpx |
| 正文 | 30rpx |
| 次要文字 | 28rpx |
| 弱文字 / 标签 | 22rpx |
| 标题 | 34rpx / 500 |
| 详情页大数值 | 48rpx / 600 |

### 9.2 主题：首版只做深色

Web 端本身就是深色（`body { @apply bg-slate-950 text-slate-200 }`），移动端**首版只做深色**，省一半设计成本（与 `APP-DESIGN.md` 7.5 的建议一致）。

- 不跟随系统深色模式：`pages.json` 不配 `darkmode`，避免浅色下色板错乱；
- 后续要做浅色时，6.5 的色板已经在 `utils/theme.js` 里集中，换一份值即可。

`uni.scss` 变量：

```scss
$cc-bg:        #020617;
$cc-card:      #0f172a;
$cc-border:    #1e293b;
$cc-text:      #e2e8f0;
$cc-text-2:    #94a3b8;
$cc-text-3:    #64748b;
$cc-online:    #10b981;
$cc-warning:   #f59e0b;
$cc-danger:    #ef4444;
$cc-offline:   #9ca3af;
$cc-link:      #3b82f6;
```

### 9.3 自定义导航栏与安全区

三端都用自定义导航栏（`navigationStyle: "custom"`），因为需要放「搜索 / 刷新」两个按钮，原生导航栏放不下。

| 项 | 处理 |
|---|---|
| 状态栏高度 | `uni.getSystemInfoSync().statusBarHeight` |
| 小程序胶囊避让 | `uni.getMenuButtonBoundingClientRect()` 拿胶囊位置，右侧内容必须让开 |
| 底部安全区 | `padding-bottom: constant(safe-area-inset-bottom)` + `env(safe-area-inset-bottom)` |
| 导航栏总高 | `statusBarHeight + 44px`（小程序端要按胶囊位置校正） |

**只有 NavBar 一个组件需要处理这些差异**（8.3 第 6 条），页面里不要重复写。

### 9.4 WXSS 已知限制（会踩的坑）

| 限制 | 影响 | 规避 |
|---|---|---|
| 不支持 `*` 通配选择器 | 全局 reset 常用写法失效 | 逐个元素写，或用 `page` / `view` 标签选择器 |
| 不支持 `:hover` | 按钮无悬停反馈 | 用组件的 `hover-class` 属性 |
| `background-image` 不能用本地相对路径 | 背景图不显示 | 用 base64 或在线 URL；或改用 `<image>` 组件 |
| **`canvas type="2d"` 是原生组件，层级最高** | **弹层 / 下拉菜单会被 canvas 盖住** | 弹层出现时 `v-if` 卸载 canvas，或用 `cover-view`（布局能力弱）。这是本项目最容易踩的坑，凡是画环/画曲线的地方都要考虑 |
| `position: fixed` 在 `scroll-view` 内失效 | 吸顶失效 | 用页面级滚动（`page` 滚动）+ `sticky`，或把 fixed 元素放到 `scroll-view` 外 |
| 不支持 `@font-face` 加载本地字体 | 自定义字体无效 | 用系统字体；必须自定义时 `uni.loadFontFace` 加载网络字体 |
| `conic-gradient` 依赖 WebView 内核 | 老 Android 不支持 | 6.1 已给降级方案 |

---

## 10. 部署与配置

### 10.1 小程序：域名白名单（最容易卡住的一步）

在微信公众平台 → 开发 → 开发管理 → 开发设置 → 服务器域名，配置：

| 类型 | 填什么 | 说明 |
|---|---|---|
| request 合法域名 | `https://dash.example.com` | 只填 `https://` + 域名，**不带端口、不带路径** |
| socket 合法域名 | `wss://dash.example.com` | **必须单独配**，配在 request 里无效（2.2） |
| downloadFile 合法域名 | 首版不需要（不下载文件） | — |
| uploadFile 合法域名 | 首版不需要 | — |

前置条件：

1. 域名已完成 **ICP 备案**；
2. 证书是**受信任 CA** 签发（Let's Encrypt 免费证书即可），TLS ≥ 1.2；
3. 反代挂在 **443**，对外不暴露其他端口。

> 若面板是通过「域名 + 子路径」访问（如 `https://dash.example.com/cc`），白名单只填 `https://dash.example.com` 即可，路径不影响白名单。

### 10.2 反向代理要求（wss 必须支持 Upgrade）

`docs/使用部署手册.md` 9.1 已有 nginx HTTPS + WS 的完整配置，此处只列与移动端相关的**硬要求**：

```nginx
server {
    listen 443 ssl http2;
    server_name dash.example.com;

    ssl_certificate     /etc/letsencrypt/live/dash.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/dash.example.com/privkey.pem;
    ssl_protocols TLSv1.2 TLSv1.3;

    # REST
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
    }

    # WebSocket：Upgrade 三件套 + 长超时
    location /api/v1/ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_read_timeout 3600s;   # 默认 60s 会掐断空闲连接（部署手册 9 节已提示）
        proxy_send_timeout 3600s;
    }

    # H5（若部署在同域子路径）
    location /m/ {
        alias /opt/cc-dashboard/h5/;
        try_files $uri $uri/ /m/index.html;
    }
}
```

**三个必查项**：

1. `proxy_read_timeout` 默认 60 秒——服务端不主动发心跳（7.4），空闲 60 秒就会被 nginx 掐断，表现为「WS 每分钟断一次」；
2. `http2` 与 `Upgrade` 共存：`listen 443 ssl http2` 下 WebSocket 仍走 HTTP/1.1 Upgrade，nginx 会正确处理，但**不要**在 `location` 里再强制 `http2`；
3. 若用 Caddy，`reverse_proxy` 默认已处理 Upgrade，无需额外配置。

### 10.3 H5 部署

**推荐：部署到面板同域子路径 `/m/`**（3.2）。这样做的好处是不用碰 CORS，且将来若想给 H5 开 Cookie 会话也不需要 `SameSite=None; Secure`。

步骤：

1. `npm run build:h5`，产物在 `dist/build/h5`；
2. 拷到服务器 `/opt/cc-dashboard/h5/`；
3. nginx 加 10.2 里的 `location /m/` 段；
4. 验证：手机浏览器访问 `https://dash.example.com/m/`。

**注意**：`h5.router.base` 必须和部署路径一致，否则静态资源 404、白屏。

### 10.4 App 打包

| 项 | 说明 |
|---|---|
| 打包方式 | HBuilderX 云打包（简单）或本地打包（可进 CI） |
| Android | 需签名证书（自签名也行，用户安装时提示「未知来源」）；`minSdkVersion ≥ 23` |
| iOS | 需 Apple 开发者账号与描述文件；内部分发可用 TestFlight 或企业证书（后者审核严格） |
| 明文 HTTP | 若允许 http 面板，Android 需 `usesCleartextTraffic="true"`，iOS 需 ATS 例外。三端统一要求 https（2.2），所以**默认不开**，保持安全 |
| 权限 | 不需要任何系统权限（不读位置、相册、通讯录） |
| 图标 / 启动图 | 一套即可，三端共用 |

### 10.5 后端需要改什么

**首版：零改动。** 现有接口完全够用：

| 需求 | 现有能力 | 结论 |
|---|---|---|
| Bearer 鉴权 | `Authorization: Bearer cck_...` | ✅ |
| WS 鉴权 | 请求头 / `?access_token=` 双支持 | ✅ |
| 多凭证吊销 | `/auth/api-keys` 增删查 | ✅ |
| 节点列表 / 详情 / 历史 / 告警 | 全部就绪 | ✅ |
| 跨域（H5 独立域名场景） | `Access-Control-Allow-Origin: *` + 放行 `Authorization` | ✅ |

只有一项**可选**优化：方案 K2 的「密码换 Key」（4.2），需要给 `POST /auth/login` 加可选返回字段，约 3~5 行。列为 D2。

---

## 11. 错误处理矩阵

在 `APP-DESIGN.md` 第 10 章基础上，补充移动端/小程序特有场景。

| 场景 | 检测方式 | UI 表现 | 恢复动作 |
|---|---|---|---|
| 面板地址不可达 | `status: 0` / `errMsg` 含 `fail` | 分组内红条「无法连接」 | 重试；检查地址 / 网络 |
| **域名未加入白名单**（仅小程序） | `errMsg` 含 `domain` / `not in domain list` | 「域名 xxxx 未加入小程序白名单」+ 域名原文 | 去微信后台配置（10.1），**不给重试按钮**（重试无效） |
| **HTTPS 页面请求 http 面板**（仅 H5） | 浏览器控制台混合内容拦截，请求直接失败 | 「请使用 https 面板地址」 | 改地址 |
| **TLS 证书不受信任** | `errMsg` 含 `ssl` / `certificate` | 「面板证书无效或已过期」 | 修证书，**不提供「忽略证书」开关**（4.5 安全边界） |
| 地址格式非法（IP / 端口） | 本地校验（4.3） | 输入框下方红字 | 重输 |
| Key 无效 / 已吊销 | `/auth/whoami` → `scope=""` 或 401 | 分组显示「需要重新配置」 | 进设置页重填 Key |
| 面板未初始化 | `GET /auth/status` → `admin_set=false` | 「面板尚未初始化，请先用浏览器设置管理员密码」 | 无（需人工） |
| 只读 Key 做写操作 | 写接口 → 401 且 message 含「需要管理员登录」 | 「当前面板无管理员权限」 | **不清 Key**（4.7） |
| 节点已被删除 | `/nodes/:id/detail` → 404 | 「设备已删除」+ 返回 | 返回首页并刷新 |
| 服务端 500 | 状态码 500 | 通用错误条 + 重试 | 重试 |
| **WS 假活**（NAT 超时） | `now - lastMsgAt > 3 × 报告周期` | 顶部「实时更新已断开，正在重试」 | 主动重连，指数退避（7.4） |
| **小程序被后台销毁后回前台** | `onShow` 且 `socket` 已失效 | 无感（自动恢复） | 重连 + 重拉 `GET /nodes` |
| 缓存超期（> 7 天） | 读取时判断 `ts` | 无缓存可用，直接进加载态 | 正常请求 |
| 存储写入超限 | `setStorageSync` 抛异常 / 长度检查 | 静默丢弃缓存，不弹错 | 只保留精简字段（7.6） |

**统一原则**：技术错误不要原文抛给用户（`request:fail` 用户看不懂），要映射成可执行的中文提示；但**排查信息要能在设置页看到**（4.3、5.8 的域名展示）。

---

## 12. 里程碑与验收标准

### M1 打通连接（1 周）

- [ ] uni-app 工程初始化，三端都能跑起来（`dev:mp-weixin` / `dev:h5` / `dev:app`）
- [ ] 地址规范化覆盖 4.3 的 6 种输入，IP / 端口 / http 都给出明确报错
- [ ] `GET /auth/status` → `/auth/whoami` → `/site` → `/nodes` 验证链跑通，每步有结果回显
- [ ] 面板配置存入 storage，冷启动能恢复
- [ ] 首页用最简列表渲染名称 + 状态

**验收**：在三端分别输入「域名 + Key」，都能看到节点名称列表。小程序的**真机预览**（不是真机调试）也能连通——这一步过了才说明域名白名单配对了。

### M2 首页成型（1.5 周）

- [ ] 卡片含状态灯、系统徽标、内网/公网 IP
- [ ] 三个环形仪表盘（CSS `conic-gradient`），阈值配色 75/90
- [ ] 上行/下行速率与运行时长，**方向不能反**（↑ = `net_tx`）
- [ ] 离线节点显示 `—` 而不是 0%
- [ ] `group_tag` 分组、状态筛选、搜索、三种空态文案
- [ ] 下拉刷新、加载态、错误条、缓存标记

**验收**：与 Web 端**同屏对比**，同一节点的 CPU/内存/磁盘/上下行数值一致；断网后显示缓存数据 + 错误条；列表滚动 60fps（小程序真机，用开发者工具的性能面板看）。

### M3 实时与详情（1.5 周）

- [ ] WS 连接成功，`metrics` 增量更新（不整表重载）
- [ ] 假活检测与指数退避重连，`proxy_read_timeout` 配好后不再每分钟断
- [ ] 详情页 6 段区块，实时区随 WS 刷新
- [ ] 历史曲线 6 档 range + `per_cpu` 字符串解析 + 7d 降采样
- [ ] range 切换保留旧图 + loading 遮罩

**验收**：`POST /report` 后 1 秒内卡片数值变化；7d 曲线拖动不卡；切后台 5 分钟回来能自动恢复。

### M4 告警与设置（1 周）

- [ ] 告警页三种筛选、`offline` 类 value 不带 `%`
- [ ] tab 红点随 `alert_firing` / `alert_resolved` 实时变化
- [ ] 设置页：面板编辑、测试连接（四步回显）、可见节点选择
- [ ] 清除缓存 / 清除所有数据
- [ ] `admin` Key 下：待审批分组、批准/拒绝、添加 agent 节点 + Token 一次性展示

**验收**：新设备自注册 → 设置 tab 出红点 → 批准 → 出现在首页（`APP-DESIGN.md` M5 的全链路）。

### M5 三端出包与打磨（1 周）

- [ ] 小程序提交审核通过（域名白名单、隐私指引、类目都就位）
- [ ] H5 部署在 `/m/` 可访问，手机浏览器体验无横向滚动
- [ ] App 打出 Android APK（iOS 视证书情况），图标与启动图正常
- [ ] 三端同屏对比：同一份数据，三端数值与配色一致
- [ ] 弱网模拟（开发者工具限速 100 KB/s）下首屏 3 秒内可用

**验收**：三端各走一遍「冷启动 → 看概览 → 进详情 → 看曲线 → 看告警」全流程，无白屏、无报错、无数据错位。

---

## 13. 与 APP-DESIGN.md 的差异对照

`APP-DESIGN.md` 是安卓原生（Kotlin + Compose）方案。本项目用 uni-app 三端取代它之后，那份文档的**产品层仍然有效**，**技术层基本作废**。

### 13.1 保留 / 作废 / 重做

| `APP-DESIGN.md` 章节 | 状态 | 说明 |
|---|---|---|
| 1 文档定位 | 部分保留 | 本文取代其技术定位 |
| 2.1 目标场景 / 2.2 Non-goals / 2.3 非功能需求 | ✅ **直接复用** | 冷启动 1.5 秒、仪表盘 300ms、内存 < 150 MB 等目标照旧 |
| 3.1 页面地图 / 3.2 首登流程 / 3.3 审批流程 | ⚠️ 改造 | 首登流程改「地址 + Key」（4.2）；页面地图加了自定义导航栏 |
| 4.1 三种方案（A/B/C） | ⚠️ 收窄 | 只剩方案 A 的变体（4.2 的 K1/K2），方案 B（Cookie）三端统一不用 |
| 4.2 地址规范化 | ❌ 简化 | 移动端只接受 https 域名，规则从 6 种收敛为 4 种（4.3） |
| 4.3 面板数据模型 | ✅ 复用 | Kotlin data class → TS interface（4.4、7.1） |
| 4.4 凭据存储（EncryptedSharedPreferences） | ❌ **作废** | 三端都没有等价物，改为「只存 Key 不存密码」（4.5） |
| 4.5 会话生命周期 | ✅ 复用 | 结论一致：不做自动重试刷请求（4.6） |
| 4.6 多面板隔离 | ⏸ 延后 | 首版单面板（D1） |
| 5.1 首页结构 / 5.2 卡片信息架构 | ✅ **直接复用** | 唯一修正：`mem_pct` 已由服务端提供（5.3） |
| 5.3 仪表盘规格 | ⚠️ 拆分实现 | 首页 CSS `conic-gradient`、详情页 canvas 2d（6.1），角度与配色完全沿用 |
| 5.4 网络速率区 | ✅ 复用 | ↑=`net_tx`、↓=`net_rx`，格式化用 `fmtRate` |
| 5.5 状态与告警标识 | ✅ 复用 | — |
| 5.6 筛选与排序 | ✅ 复用（少一层） | 去掉「面板级」筛选（单面板） |
| 5.7 加载与缓存 | ✅ 复用 | 加了小程序 1 MB/key 的体积保护（7.6） |
| 6.1~6.5 设备详情页 | ✅ **直接复用** | 6 段结构、空态矩阵、`live` 容错规则全部照搬 |
| 7.1~7.2 面板管理 / 节点选择 | ✅ 复用 | — |
| 7.3 添加节点 / 7.4 审批节点 | ✅ 复用 | 仅 admin Key 可用 |
| 7.5 其它设置项 | ⚠️ 收窄 | 主题只做深色；「后台通知」不做（7.5） |
| 8.1~8.4 WS 连接 / 退避 / 前后台 | ⚠️ 改造 | 保活改「超时判活」（三端都不能发 PING，2.1）；退避策略照搬 |
| 8.5 后台通知 | ❌ 重做 | WorkManager 方案只适用原生；三端各自受限（7.5） |
| 9 技术架构（Compose / Room / OkHttp / Vico / Hilt） | ❌ **作废** | 被 uni-app + Pinia + `uni.request` + canvas 取代（第 3 章） |
| 9.3 本地存储表（SQLite） | ❌ 作废 | 改为 storage 存 JSON（4.4、7.6） |
| 9.4 列表性能（LazyColumn） | ⚠️ 改造 | 小程序用 `scroll-view` + `:key`，性能关注点变成 canvas 数量（6.1） |
| 9.5 依赖建议 | ❌ 作废 | 见 3.5 |
| 10 错误处理矩阵 | ✅ 复用 + 扩展 | 补充小程序/H5 特有场景（第 11 章） |
| 11 安全与隐私 | ⚠️ 部分改造 | 加密存储、`FLAG_SECURE`、`allowBackup` 都无等价物；其余（日志脱敏、不忽略证书）照旧（4.5） |
| 12 兼容性与边界情况 | ✅ **直接复用** | 时区、`mem_total=0`、> 200 节点等结论不变 |
| 13 里程碑 | ⚠️ 改造 | 重排为三端出包（第 12 章） |
| 14 后端能力现状 | ✅ 复用 | 已落地的多 Key / WS 鉴权可直接用 |
| 15 界面草图 | ✅ 复用 | 布局参照，尺寸改 rpx |

### 13.2 一句话总结

`APP-DESIGN.md` 里凡是**关于「给人看什么」**的内容（信息架构、字段映射、仪表盘角度、空态、错误语义）全部保留；凡是**关于「用 Android 怎么实现」**的内容（Compose、Room、OkHttp、EncryptedSharedPreferences、Vico、network_security_config）全部作废。

### 13.3 对 `APP-API.md` 的影响

几乎为零——接口层与语言无关。只有一处需要替换：第 5 章的 Kotlin 数据模型换成 7.1 的 TS interface。另外第 7 章「安卓对接清单」（明文放行、OkHttp 配置、`EncryptedSharedPreferences`）对三端都不适用，改为本文 7.2 / 4.5。

---

## 14. 待决策清单

按优先级排列，每项都给了默认选择，不决策就按默认走。

| # | 待决事项 | 默认（不决策就按此执行） | 影响 |
|---|---|---|---|
| **D1** | 是否支持多面板 | **首版不做**（单面板），数据结构已预留 | 做了要加面板切换 UI、WS 多连接退化、复合键 `(panelId, nodeId)`。工作量约 +1 周 |
| **D2** | 后端是否加「密码换 Key」（方案 K2） | **不做**，首版用 K1（Web 后台生成 Key 粘贴） | 不做则首次配置需跨设备操作一次；做则体验对齐 `APP-DESIGN.md` 3.2，后端约 3~5 行 |
| **D3** | H5 部署在面板同域 `/m/` 还是独立域名 | **同域子路径** | 独立域名要确认 CORS（已有）、要单独配证书；同域则 nginx 加一段 `location` |
| **D4** | 平板 / 横屏多列布局 | **首版只做 1 列** | 加多列只需改 CSS grid 断点，约 0.5 天 |
| **D5** | 曲线触摸提示（tooltip） | **不做** | 要做需处理 `touchstart/move` + 重绘高亮线，约 1 天，且三端触摸事件字段不同 |
| **D6** | 后台告警通知 | **不做** | App 端可行（本地通知），小程序受订阅消息限制，H5 受 Web Push 限制。见 7.5 |
| **D7** | `APP-DESIGN.md` 的 Compose 安卓路线是否归档 | **归档**，标注「已被 MP-DESIGN.md 取代」 | 若将来需要原生级通知/小组件，可重新启用该路线 |

---

## 附：与本文相关的关键数字速查

| 项 | 值 |
|---|---|
| 小程序主包上限 | 2 MB |
| 小程序 storage 单 key 上限 | 1 MB |
| 小程序 storage 总量上限 | 10 MB |
| 小程序 request / socket 域名上限 | 各 20 个 |
| 端口 | 固定 443（不可改） |
| 默认请求超时 | 必须显式设 10000 ms（默认 60 s） |
| 仪表盘角度 | 起 135°，扫 270°，缺口朝下 |
| 仪表盘阈值 | 75% 黄 / 90% 红 |
| 曲线降采样目标 | 画布宽度 × 2（约 700~800 点） |
| WS 退避 | 1→2→4→8→15→30 s（上限 30 s） |
| WS 假活判定 | 超 `max(30s, 3×报告周期)` 无消息 |
| 缓存保留期 | 7 天 |
| tabBar 图标 | PNG 81×81，单个 ≤ 40 KB |
| 设计稿基准 | 750 rpx = 屏幕宽 |

