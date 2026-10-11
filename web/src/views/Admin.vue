<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h2 class="text-xl font-semibold">节点管理</h2>
      <button
        class="rounded bg-emerald-700 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-600"
        @click="showForm = !showForm"
      >
        {{ showForm ? '收起' : '添加节点' }}
      </button>
    </div>

    <!-- 创建表单 -->
    <section v-if="showForm" class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div class="mb-3 flex gap-2 text-xs">
        <button
          v-for="m in modes"
          :key="m.value"
          class="rounded px-3 py-1.5"
          :class="form.access_mode === m.value ? 'bg-emerald-700 text-white' : 'bg-slate-800 text-slate-300'"
          @click="form.access_mode = m.value"
        >
          模式 {{ m.label }}：{{ m.name }}
        </button>
      </div>
      <div class="grid gap-3 sm:grid-cols-4">
        <input
          v-model="form.name"
          placeholder="节点名称 *"
          class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
        />
        <select v-model="form.os_type" class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring">
          <option v-for="o in osTypes" :key="o" :value="o">{{ osTypeLabels[o] || o }}</option>
        </select>
        <input
          v-model="form.address"
          placeholder="地址（如 192.168.1.10）"
          class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
        />
        <!-- 模式 A（Agent）：连接方向（反向推送 / 正向拉取） -->
        <template v-if="form.access_mode === 'agent'">
          <select
            v-model="form.direction"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
            title="连接方向"
          >
            <option value="push">反向 · Agent推送（推荐）</option>
            <option value="pull">正向 · 服务端拉取</option>
          </select>
          <input
            v-if="form.direction === 'pull'"
            v-model.number="form.port"
            type="number"
            min="1"
            max="65535"
            placeholder="Agent 监听端口（默认 9100）"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </template>
        <!-- 模式 C（API 直连）：PVE 与群晖字段不同 -->
        <template v-if="form.access_mode === 'api'">
          <input
            v-if="form.os_type === 'pve'"
            v-model="form.nodeName"
            placeholder="PVE 节点名（hostname）"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <input
            v-model="form.username"
            :placeholder="form.os_type === 'pve' ? 'API Token 标识（user@realm!tokenid）' : 'DSM 账号（如 admin）'"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <input
            v-model="form.password"
            type="password"
            :placeholder="form.os_type === 'pve' ? 'Token 密钥（= 号后半段）' : 'DSM 密码（建议专用监控账号、关闭两步验证）'"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <input
            v-model.number="form.port"
            type="number"
            min="1"
            max="65535"
            :placeholder="form.os_type === 'pve' ? '端口（默认 8006）' : '端口（5001 HTTPS / 5000 HTTP）'"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </template>
        <!-- 模式 B（SSH）：目标机无需安装 Agent，填写登录凭据 -->
        <template v-if="form.access_mode === 'ssh'">
          <input
            v-model="form.username"
            placeholder="SSH 用户名"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <input
            v-model="form.password"
            type="password"
            placeholder="SSH 密码"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <input
            v-model.number="form.port"
            type="number"
            min="1"
            max="65535"
            placeholder="端口（默认 22）"
            class="rounded bg-slate-950 px-3 py-2 text-sm text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </template>
        <button
          class="rounded bg-emerald-700 px-4 py-2 text-sm font-medium text-white hover:bg-emerald-600 disabled:opacity-50"
          :disabled="!form.name || creating"
          @click="create"
        >
          {{ creating ? '创建中…' : '创建' }}
        </button>
      </div>
      <p class="mt-2 text-[11px] text-slate-600">
        模式 A（Agent 上报）与模式 B（SSH 远程采集，目标机<b>无需安装 Agent</b>，需开放 SSH 端口并填写登录凭据）已可用；
        模式 C（API 直连）：<b>群晖 DSM 已可用</b>——系统类型选「群晖 NAS」后会自动切换，填 DSM 账号/密码（建议专用监控账号、关闭两步验证）、端口 5001，
        NAS 上无需安装任何组件，服务端直接拉取；PVE 开发中。
      </p>

      <!-- 创建成功：展示 Token -->
      <div v-if="created" class="mt-3 rounded-lg border border-emerald-900 bg-emerald-950/40 p-3">
        <p class="text-xs text-emerald-400">节点「{{ created.name }}」已创建，Token（仅此一次完整展示）：</p>
        <div class="mt-1.5 flex items-center gap-2">
          <code class="flex-1 truncate rounded bg-slate-950 px-2 py-1 text-xs text-slate-300">{{ created.token }}</code>
          <button class="rounded bg-slate-800 px-2.5 py-1 text-xs text-slate-300 hover:bg-slate-700" @click="copy(created.token)">
            {{ copied ? '已复制' : '复制' }}
          </button>
        </div>
        <p class="mt-1.5 text-[11px] text-slate-500">
          上报地址：{{ apiBase }}/api/v1/report · 指令示例：管理接口 GET /nodes/:{{ created.id }}/install-script
        </p>
      </div>
    </section>

    <!-- 节点表格 -->
    <section class="overflow-hidden rounded-xl border border-slate-800">
      <table class="w-full text-left text-sm">
        <thead class="bg-slate-900 text-xs text-slate-400">
          <tr>
            <th class="px-4 py-3">名称</th>
            <th class="px-4 py-3">系统</th>
            <th class="px-4 py-3">接入模式</th>
            <th class="px-4 py-3">地址</th>
            <th class="px-4 py-3">公网 IP</th>
            <th class="px-4 py-3">状态</th>
            <th class="px-4 py-3">最新 CPU</th>
            <th class="px-4 py-3">版本</th>
            <th class="px-4 py-3">排序</th>
            <th class="px-4 py-3">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-800 bg-slate-950">
          <tr
            v-for="(n, i) in nodes"
            :key="n.id"
            class="text-slate-300"
            :class="dragFrom >= 0 && dragOverIdx === i ? 'bg-emerald-900/40' : ''"
            @dragover.prevent="onDragOver(i)"
            @drop.prevent="onDrop(i)"
          >
            <td class="px-4 py-3">
              <router-link :to="`/device/${n.id}`" class="hover:text-emerald-400">{{ n.name }}</router-link>
            </td>
            <td class="px-4 py-3 text-xs">{{ n.os_type }}</td>
            <td class="px-4 py-3 text-xs">{{ n.access_mode }}</td>
            <td class="px-4 py-3 text-xs">{{ n.address || '—' }}</td>
            <td class="px-4 py-3 text-xs">{{ n.public_ip || '—' }}</td>
            <td class="px-4 py-3 text-xs">
              <span v-if="n.approved === false" class="text-warning">待批准</span>
              <span v-else class="flex items-center gap-1.5">
                <span class="h-2 w-2 rounded-full" :class="statusDotClass(n.status)"></span>
                {{ statusText(n.status) }}
              </span>
            </td>
            <td class="px-4 py-3 text-xs">{{ n.latest ? fmtPct(n.latest.cpu) : '—' }}</td>
            <td class="px-4 py-3 text-xs whitespace-nowrap">
              {{ n.agent_version ? 'v' + n.agent_version : '—' }}
              <span
                v-if="n.upgrade"
                class="ml-1 rounded bg-emerald-900/60 px-1 py-0.5 text-[10px] text-emerald-300"
                title="程序池中有可升级版本"
              >
                ↑ v{{ n.upgrade.version }}
              </span>
              <button
                v-if="n.upgrade"
                class="ml-1 rounded bg-emerald-700 px-1.5 py-0.5 text-[10px] text-emerald-50 hover:bg-emerald-600"
                title="立即升级该节点（不受全局 OTA 开关限制，下次上报生效）"
                @click="triggerUpgrade(n)"
              >
                升级
              </button>
            </td>
            <td class="select-none px-2 py-3 text-xs whitespace-nowrap">
              <span
                class="mr-1 cursor-grab text-slate-500 hover:text-emerald-400 active:cursor-grabbing"
                title="按住拖拽调整顺序"
                draggable="true"
                @dragstart="onDragStart(i, $event)"
                @dragend="onDragEnd"
              >
                ☰
              </span>
              <button
                class="px-1 text-slate-500 hover:text-emerald-400 disabled:opacity-30"
                title="上移"
                :disabled="i === 0"
                @click="moveNode(i, -1)"
              >
                ▲
              </button>
              <button
                class="px-1 text-slate-500 hover:text-emerald-400 disabled:opacity-30"
                title="下移"
                :disabled="i === nodes.length - 1"
                @click="moveNode(i, 1)"
              >
                ▼
              </button>
            </td>
            <td class="px-4 py-3 text-xs">
              <button
                class="mr-2 text-slate-500 hover:text-emerald-400"
                title="修改节点显示名称"
                @click="renameNode(n)"
              >
                改名
              </button>
              <button
                class="mr-2 text-slate-500 hover:text-sky-400"
                title="手动设置公网 IP（SSH/API 模式自动获取失败时的兜底）"
                @click="editPublicIP(n)"
              >
                公网IP
              </button>
              <template v-if="n.approved === false">
                <button class="mr-2 text-emerald-400 hover:text-emerald-300" @click="approve(n)">批准</button>
                <button class="text-slate-500 hover:text-danger" @click="remove(n)">拒绝</button>
              </template>
              <template v-else>
                <button
                  v-if="n.access_mode === 'ssh'"
                  class="mr-2 text-sky-400 hover:text-sky-300"
                  @click="openEdit(n)"
                >
                  凭据
                </button>
                <button
                  v-if="n.access_mode === 'ssh'"
                  class="mr-2 text-slate-500 hover:text-amber-400"
                  title="清除 TOFU 主机指纹，下次连接重新确认"
                  @click="rotateHostKey(n)"
                >
                  重置指纹
                </button>
                <button
                  v-if="n.access_mode === 'agent'"
                  class="mr-2 text-slate-500 hover:text-amber-400"
                  title="轮换上报 Token，旧 Token 立即失效"
                  @click="rotateToken(n)"
                >
                  换Token
                </button>
                <button class="text-slate-500 hover:text-danger" @click="remove(n)">删除</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="nodes.length === 0" class="px-4 py-8 text-center text-xs text-slate-600">
        暂无节点 — 点击右上角「添加节点」创建
      </p>
    </section>

    <!-- 程序池：上传 Agent / Dashboard 程序、版本提示与 OTA 总开关 -->
    <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h3 class="text-sm text-slate-400">程序池</h3>
        <label class="flex cursor-pointer items-center gap-2 text-xs text-slate-400" title="打开后 Agent 上报时将收到升级指令并自动升级（OpenWrt 除外）">
          <input type="checkbox" :checked="poolOta" class="accent-emerald-500" @change="togglePoolOta" />
          Agent 自动 OTA（{{ poolOta ? '已开启' : '已关闭，默认只提示' }}）
        </label>
      </div>
      <p v-if="serverHint" class="mb-3 rounded bg-amber-900/30 px-3 py-2 text-xs text-amber-300">{{ serverHint }}</p>

      <!-- 批量上传：按文件名自动识别组件/系统/架构/格式，版本号作为裸二进制的兜底 -->
      <div class="mb-4 flex flex-wrap items-center gap-2 rounded-lg bg-slate-950/60 p-3 text-xs">
        <input
          type="file"
          multiple
          class="text-slate-400 file:mr-2 file:rounded file:border-0 file:bg-slate-800 file:px-2 file:py-1 file:text-slate-300"
          @change="onPoolFiles"
        />
        <input
          v-model="poolForm.version"
          placeholder="兜底版本号（文件名无版本的裸二进制必填）"
          class="w-72 rounded bg-slate-900 px-2 py-1.5 text-slate-300 outline-none ring-slate-700 focus:ring"
        />
        <button
          class="rounded bg-emerald-700 px-3 py-1.5 text-emerald-50 hover:bg-emerald-600 disabled:opacity-50"
          :disabled="!poolFiles.length || poolUploading"
          @click="uploadPool"
        >
          {{ poolUploading ? '上传中…' : `批量上传（${poolFiles.length}）` }}
        </button>
        <span v-if="poolMsg" class="text-slate-400">{{ poolMsg }}</span>
      </div>

      <!-- 池清单 -->
      <table v-if="poolItems.length" class="w-full text-left text-xs">
        <thead class="text-slate-500">
          <tr>
            <th class="py-2">组件</th>
            <th>版本</th>
            <th>平台</th>
            <th>格式</th>
            <th>大小</th>
            <th>sha256</th>
            <th>上传时间</th>
            <th class="text-right">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-800">
          <tr v-for="it in poolItems" :key="it.id" class="text-slate-400">
            <td class="py-2">{{ it.component === 'server' ? 'Dashboard' : 'Agent' }}</td>
            <td>{{ it.version }}</td>
            <td>{{ [it.goos, it.arch].filter(Boolean).join('/') || '—' }}</td>
            <td>{{ it.format }}</td>
            <td>{{ fmtBytes(it.size) }}</td>
            <td class="max-w-[9rem] truncate" :title="it.sha256">{{ it.sha256.slice(0, 12) }}…</td>
            <td>{{ fmtClock(it.created_at) }}</td>
            <td class="text-right">
              <button class="text-slate-500 hover:text-danger" @click="removePool(it)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-else class="text-xs text-slate-600">
        池为空 — 上传后节点显示可升级提示；开启 OTA 开关后 Agent 将自动升级（OpenWrt 仅提示）
      </p>
    </section>

    <!-- SSH 凭据编辑（已存在的模式 B 节点补填用户名/密码） -->
    <section v-if="editing" class="rounded-xl border border-sky-900 bg-slate-900 p-4">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="text-sm text-slate-400">
          SSH 凭据 · {{ editing.name }}
          <span class="text-slate-600">（{{ editing.address }}:{{ editing.port || 22 }}）</span>
        </h3>
        <button class="rounded bg-slate-800 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="editing = null">
          收起 ✕
        </button>
      </div>
      <div class="flex flex-wrap items-end gap-3 text-xs">
        <div>
          <p class="mb-1 text-slate-500">用户名</p>
          <input
            v-model="editCreds.username"
            placeholder="如 root"
            class="rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </div>
        <div>
          <p class="mb-1 text-slate-500">密码</p>
          <input
            v-model="editCreds.password"
            type="password"
            placeholder="留空保持不变"
            class="rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </div>
        <div>
          <p class="mb-1 text-slate-500">端口</p>
          <input
            v-model.number="editCreds.port"
            type="number"
            min="1"
            max="65535"
            class="w-24 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </div>
        <button class="rounded bg-emerald-700 px-4 py-2 text-white hover:bg-emerald-600" @click="saveCreds">保存</button>
      </div>
      <p class="mt-2 text-[11px] text-slate-600">
        凭据以 AES-256-GCM 加密存储，保存后 SSH 引擎下个轮询周期（15s）生效。
      </p>
      <p v-if="editMsg" class="mt-1 text-[11px]" :class="editOk ? 'text-online' : 'text-danger'">{{ editMsg }}</p>
    </section>

    <!-- 告警规则 -->
    <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="text-sm text-slate-400">告警阈值规则</h3>
        <button class="rounded bg-slate-800 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="loadRules">
          刷新
        </button>
      </div>
      <div class="mb-3 flex flex-wrap items-center gap-2 text-xs">
        <select v-model="rule.metric" class="rounded bg-slate-950 px-2 py-1.5 text-slate-200">
          <option value="cpu">CPU</option>
          <option value="mem_pct">内存</option>
          <option value="disk_pct">磁盘</option>
        </select>
        <span class="text-slate-500">超过</span>
        <input
          v-model.number="rule.threshold"
          type="number"
          min="1"
          max="100"
          class="w-20 rounded bg-slate-950 px-2 py-1.5 text-slate-200"
        />
        <span class="text-slate-500">%，持续</span>
        <input
          v-model.number="rule.delayMin"
          type="number"
          min="0"
          max="1440"
          class="w-20 rounded bg-slate-950 px-2 py-1.5 text-slate-200"
          title="连续超过阈值的时长，0=立即触发"
        />
        <span class="text-slate-500">分钟触发（0=立即{{ editingRuleId ? '' : '，全局生效' }}）</span>
        <label v-if="editingRuleId" class="flex cursor-pointer items-center gap-1 text-slate-300">
          <input v-model="rule.enabled" type="checkbox" class="accent-emerald-600" />
          启用
        </label>
        <button
          class="rounded px-3 py-1.5 text-white disabled:opacity-50"
          :class="editingRuleId ? 'bg-sky-700 hover:bg-sky-600' : 'bg-emerald-700 hover:bg-emerald-600'"
          :disabled="!rule.threshold"
          @click="editingRuleId ? updateRule() : addRule()"
        >
          {{ editingRuleId ? '保存修改' : '添加' }}
        </button>
        <button
          v-if="editingRuleId"
          class="rounded bg-slate-800 px-3 py-1.5 text-slate-300 hover:bg-slate-700"
          @click="cancelEditRule"
        >
          取消
        </button>
        <span v-if="editingRuleId" class="text-slate-500">正在编辑规则 #{{ editingRuleId }}</span>
      </div>
      <ul v-if="rules.length" class="space-y-1.5 text-xs">
        <li v-for="r in rules" :key="r.id" class="flex items-center justify-between">
          <span class="text-slate-300" :class="{ 'opacity-50': !r.enabled }">
            {{ r.node_id === 0 ? '全局' : `节点#${r.node_id}` }} ·
            {{ { cpu: 'CPU', mem_pct: '内存', disk_pct: '磁盘' }[r.metric] || r.metric }}
            {{ r.op }} {{ r.threshold }}% ·
            {{ r.delay_sec > 0 ? '持续 ' + fmtDelay(r.delay_sec) + '触发' : '立即触发' }}
            <span v-if="!r.enabled" class="text-slate-600">（已停用）</span>
          </span>
          <span class="flex items-center gap-3">
            <button class="text-sky-500 hover:text-sky-400" @click="editRule(r)">编辑</button>
            <button class="text-slate-500 hover:text-danger" @click="delRule(r.id)">删除</button>
          </span>
        </li>
      </ul>
      <p v-else class="text-xs text-slate-600">暂无规则</p>
    </section>

    <!-- 安全设置 -->
    <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <h3 class="mb-3 text-sm text-slate-400">安全设置</h3>
      <div class="grid gap-5 lg:grid-cols-3">
        <!-- 修改管理员密码 -->
        <div>
          <h4 class="mb-2 text-xs text-slate-500">修改管理员密码</h4>
          <input
            v-model="pw.old"
            type="password"
            placeholder="原密码"
            class="mb-2 w-full rounded bg-slate-950 px-2.5 py-1.5 text-xs text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <input
            v-model="pw.new"
            type="password"
            placeholder="新密码（≥6 位）"
            class="mb-2 w-full rounded bg-slate-950 px-2.5 py-1.5 text-xs text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <button class="rounded bg-slate-800 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="changePw">
            修改密码
          </button>
          <p class="mt-1.5 text-[11px]" :class="pw.msgOk ? 'text-online' : 'text-slate-600'">{{ pw.msg }}</p>
        </div>

        <!-- 前台访问密码 -->
        <div>
          <h4 class="mb-2 text-xs text-slate-500">前台访问密码（概览 / 详情）</h4>
          <label class="mb-2 flex cursor-pointer items-center gap-2 text-xs text-slate-300">
            <input v-model="view.on" type="checkbox" class="accent-emerald-600" />
            开启访问密码（关闭时前台无需登录）
          </label>
          <input
            v-model="view.pwd"
            type="password"
            placeholder="单独密码（留空 = 使用管理员密码）"
            class="mb-2 w-full rounded bg-slate-950 px-2.5 py-1.5 text-xs text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
          <button class="rounded bg-slate-800 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="saveView">
            保存
          </button>
          <p class="mt-1.5 text-[11px]" :class="view.msgOk ? 'text-online' : 'text-slate-600'">{{ view.msg }}</p>
        </div>

        <!-- 加入密钥 -->
        <div>
          <h4 class="mb-2 text-xs text-slate-500">入网 Join Key（自注册 → 待批准）</h4>
          <div class="mb-2 flex items-center gap-2">
            <code class="flex-1 truncate rounded bg-slate-950 px-2 py-1.5 text-xs text-slate-300">
              {{ joinVisible ? joinKey || '加载中…' : '••••••••••••••••' }}
            </code>
            <button class="rounded bg-slate-800 px-2.5 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="toggleJoin">
              {{ joinVisible ? '隐藏' : '显示' }}
            </button>
            <button class="rounded bg-slate-800 px-2.5 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="copyJoin">
              {{ copied ? '已复制' : '复制' }}
            </button>
            <button class="rounded bg-slate-800 px-2.5 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="rotateJoin">
              重置
            </button>
          </div>
          <p class="text-[11px] leading-relaxed text-slate-600">
            设备持 Join Key 调用 <code class="text-slate-400">POST /api/v1/register</code> 自注册后为「待批准」状态，
            上报会被 403 拒绝；在上方表格点「批准」后才正式入网。
          </p>
        </div>

        <!-- 站点标题 -->
        <div>
          <h4 class="mb-2 text-xs text-slate-500">站点标题（左上角品牌名）</h4>
          <div class="mb-2 flex items-center gap-2">
            <input
              v-model="site.title"
              maxlength="32"
              placeholder="CC_DashBoard"
              class="flex-1 rounded bg-slate-950 px-2 py-1.5 text-xs text-slate-200 outline-none ring-emerald-700 focus:ring"
            />
            <button
              class="rounded bg-slate-800 px-2.5 py-1.5 text-xs text-slate-300 hover:bg-slate-700"
              @click="saveSite"
            >
              保存
            </button>
          </div>
          <p class="text-[11px]" :class="site.msgOk ? 'text-online' : 'text-slate-600'">{{ site.msg }}</p>
        </div>
      </div>
    </section>

    <!-- 告警通知设置 -->
    <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <h3 class="mb-3 text-sm text-slate-400">告警通知（随阈值规则触发）</h3>
      <div class="flex flex-wrap items-end gap-3 text-xs">
        <label class="flex cursor-pointer items-center gap-2 pb-2 text-slate-300">
          <input v-model="notify.enabled" type="checkbox" class="accent-emerald-600" />
          开启通知
        </label>
        <div>
          <p class="mb-1 text-slate-500">渠道</p>
          <select v-model="notify.channel" class="rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring">
            <option value="pushplus">pushplus（微信推送）</option>
            <option value="feishu">飞书机器人</option>
            <option value="dingtalk">钉钉机器人</option>
            <option value="wecom">企业微信机器人</option>
            <option value="webhook">通用 Webhook</option>
            <option value="smtp">SMTP 邮件</option>
          </select>
        </div>
        <div v-if="notify.channel === 'pushplus'">
          <p class="mb-1 text-slate-500">pushplus Token</p>
          <input
            v-model="notify.token"
            type="text"
            :placeholder="notify.tokenSet ? '已配置（留空保持不变）' : '在 pushplus.plus 获取'"
            class="w-64 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </div>
        <!-- SMTP 邮件渠道 -->
        <template v-else-if="notify.channel === 'smtp'">
          <div>
            <p class="mb-1 text-slate-500">SMTP 主机</p>
            <input
              v-model="notify.smtpHost"
              type="text"
              placeholder="smtp.example.com"
              class="w-48 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
            />
          </div>
          <div>
            <p class="mb-1 text-slate-500">端口</p>
            <input
              v-model.number="notify.smtpPort"
              type="number"
              min="1"
              max="65535"
              placeholder="465"
              class="w-24 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
              title="465=隐式 TLS（推荐）；587=STARTTLS；25=明文"
            />
          </div>
          <div>
            <p class="mb-1 text-slate-500">用户名</p>
            <input
              v-model="notify.smtpUser"
              type="text"
              placeholder="账号（留空=不认证）"
              class="w-44 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
            />
          </div>
          <div>
            <p class="mb-1 text-slate-500">密码/授权码</p>
            <input
              v-model="notify.smtpPass"
              type="password"
              :placeholder="notify.smtpPassSet ? '已配置（留空保持不变）' : 'SMTP 密码或应用授权码'"
              class="w-48 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
            />
          </div>
          <div>
            <p class="mb-1 text-slate-500">发件人</p>
            <input
              v-model="notify.smtpFrom"
              type="text"
              placeholder="alerts@example.com（留空=用户名）"
              class="w-56 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
            />
          </div>
          <div>
            <p class="mb-1 text-slate-500">收件人（逗号分隔）</p>
            <input
              v-model="notify.smtpTo"
              type="text"
              placeholder="a@example.com,b@example.com"
              class="w-64 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
            />
          </div>
        </template>
        <div v-else>
          <p class="mb-1 text-slate-500">
            {{ notify.channel === 'webhook' ? 'Webhook 地址' : '机器人 Webhook 地址' }}
          </p>
          <input
            v-model="notify.url"
            type="text"
            :placeholder="notify.urlSet ? '已配置（留空保持不变）' : notifyPlaceholder"
            class="w-72 rounded bg-slate-950 px-2.5 py-2 text-slate-200 outline-none ring-emerald-700 focus:ring"
          />
        </div>
        <button class="rounded bg-emerald-700 px-4 py-2 text-white hover:bg-emerald-600" @click="saveNotify">保存</button>
        <button class="rounded bg-slate-800 px-4 py-2 text-slate-300 hover:bg-slate-700" @click="testNotify">发送测试</button>
      </div>
      <p class="mt-2 text-[11px] text-slate-600">
        触发时机：指标突破阈值 / 恢复正常、节点离线 / 恢复，各推送一条。pushplus 免费版关注公众号即可接收。
      </p>
      <p class="mt-1 text-[11px]" :class="notify.msgOk ? 'text-online' : 'text-slate-600'">{{ notify.msg }}</p>
      <!-- 发送记录（成功/失败均落库） -->
      <div class="mt-3 border-t border-slate-800 pt-3">
        <div class="mb-1.5 flex items-center justify-between">
          <h4 class="text-xs text-slate-500">最近发送记录</h4>
          <button class="text-[11px] text-slate-500 hover:text-slate-300" @click="loadNotifyLogs">刷新</button>
        </div>
        <ul v-if="notifyLogs.length" class="space-y-1 text-[11px]">
          <li v-for="l in notifyLogs" :key="l.id" class="flex items-center gap-2">
            <span class="w-24 shrink-0 text-slate-600">{{ fmtClock(l.ts) }}</span>
            <span class="w-16 shrink-0 text-slate-500">{{ l.channel }}</span>
            <span class="min-w-0 flex-1 truncate text-slate-400">{{ l.title }}</span>
            <span class="shrink-0" :class="l.ok ? 'text-online' : 'text-danger'">
              {{ l.ok ? '成功' : '失败' }}
            </span>
            <span v-if="!l.ok && l.error" class="max-w-48 truncate text-slate-600" :title="l.error">{{ l.error }}</span>
          </li>
        </ul>
        <p v-else class="text-[11px] text-slate-600">暂无记录</p>
      </div>
    </section>
  </div>
</template>

<script setup>
import { reactive, ref, computed, onMounted, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useNodesStore } from '../stores/nodes'
import { useAuthStore } from '../stores/auth'
import { getJSON, postJSON, putJSON, delJSON } from '../api'
import { siteTitle } from '../site'
import { fmtPct, statusDotClass, statusText, fmtClock, fmtBytes } from '../utils/format'

const store = useNodesStore()
const auth = useAuthStore()
const { nodes } = storeToRefs(store)

const apiBase = `${location.origin}/api/v1`
const showForm = ref(false)
const creating = ref(false)
const created = ref(null)
const copied = ref(false)

const osTypes = ['linux', 'windows', 'nas', 'openwrt', 'darwin', 'pve', 'esxi']
const osTypeLabels = {
  linux: 'Linux',
  windows: 'Windows',
  nas: '群晖 NAS',
  openwrt: 'OpenWrt',
  darwin: 'macOS',
  pve: 'PVE',
  esxi: 'ESXi',
}
const modes = [
  { value: 'agent', label: 'A', name: 'Agent 上报' },
  { value: 'ssh', label: 'B', name: 'SSH 轮询' },
  { value: 'api', label: 'C', name: 'API 直连（群晖/PVE）' },
]
const form = reactive({ name: '', os_type: 'linux', access_mode: 'agent', address: '', username: '', password: '', port: 22, direction: 'push', nodeName: '' })

// 群晖/PVE 走服务端 API 直连：选中系统类型即自动切到模式 C，避免用户找不到入口
watch(
  () => form.os_type,
  (v) => {
    if (v === 'nas' || v === 'pve') form.access_mode = 'api'
  },
)

const rules = ref([])
const rule = reactive({ metric: 'cpu', threshold: 80, delayMin: 0, enabled: true, op: '>' })
const editingRuleId = ref(null)

function fmtDelay(sec) {
  if (sec >= 3600 && sec % 3600 === 0) return `${sec / 3600} 小时 `
  if (sec >= 60 && sec % 60 === 0) return `${sec / 60} 分钟 `
  return `${sec} 秒 `
}

async function create() {
  creating.value = true
  try {
    const body = {
      name: form.name,
      os_type: form.os_type,
      access_mode: form.access_mode,
      address: form.address,
    }
    if (form.access_mode === 'ssh') {
      body.port = form.port || 22
      body.username = form.username || undefined
      body.password = form.password || undefined
    } else if (form.access_mode === 'api') {
      body.port = form.port || (form.os_type === 'pve' ? 8006 : 5001)
      body.username = form.username || undefined
      body.password = form.password || undefined
      if (form.os_type === 'pve') body.node_name = form.nodeName || undefined
    } else {
      // 模式 A：正向拉取时带监听端口，反向时不传端口
      body.direction = form.direction
      body.port = form.direction === 'pull' ? form.port || 9100 : undefined
    }
    created.value = await store.createNode(body)
    form.name = ''
    form.address = ''
    form.username = ''
    form.password = ''
    form.nodeName = ''
    showForm.value = true
  } catch (e) {
    alert(`创建失败: ${e.message}`)
  } finally {
    creating.value = false
  }
}

async function remove(n) {
  if (!confirm(`确认删除节点「${n.name}」？其历史数据将一并删除。`)) return
  try {
    await store.deleteNode(n.id)
  } catch (e) {
    alert(`删除失败: ${e.message}`)
  }
}

async function copy(text) {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  } catch {
    /* 剪贴板不可用 */
  }
}

async function loadRules() {
  try {
    const r = await getJSON('/alerts/rules')
    rules.value = r.rules || []
  } catch {
    rules.value = []
  }
}

async function addRule() {
  try {
    await postJSON('/alerts/rules', {
      node_id: 0,
      metric: rule.metric,
      op: '>',
      threshold: rule.threshold,
      delay_sec: Math.round((rule.delayMin || 0) * 60),
    })
    await loadRules()
  } catch (e) {
    alert(`添加失败: ${e.message}`)
  }
}

// 编辑已有规则 —— 回填顶部表单，按钮切换为「保存修改」
function editRule(r) {
  editingRuleId.value = r.id
  rule.metric = r.metric
  rule.threshold = r.threshold
  rule.delayMin = Math.round((r.delay_sec || 0) / 60)
  rule.enabled = r.enabled
  rule.op = r.op || '>'
}

function cancelEditRule() {
  editingRuleId.value = null
}

async function updateRule() {
  try {
    await putJSON(`/alerts/rules/${editingRuleId.value}`, {
      node_id: rules.value.find((r) => r.id === editingRuleId.value)?.node_id ?? 0,
      metric: rule.metric,
      op: rule.op,
      threshold: rule.threshold,
      delay_sec: Math.round((rule.delayMin || 0) * 60),
      enabled: rule.enabled,
    })
    editingRuleId.value = null
    await loadRules()
  } catch (e) {
    alert(`保存失败: ${e.message}`)
  }
}

async function delRule(id) {
  try {
    await delJSON(`/alerts/rules/${id}`)
    if (editingRuleId.value === id) editingRuleId.value = null
    await loadRules()
  } catch (e) {
    alert(`删除失败: ${e.message}`)
  }
}

// 轮换上报 Token / 重置 SSH TOFU 主机指纹
async function rotateToken(n) {
  if (!confirm(`轮换「${n.name}」的上报 Token？旧 Token 立即失效，需更新 Agent 配置。`)) return
  try {
    const r = await putJSON(`/nodes/${n.id}`, { rotate_token: true })
    const newToken = r.node?.token || ''
    await store.fetchNodes()
    await copy(newToken)
    alert(`Token 已轮换（已复制到剪贴板）：\n\n${newToken}`)
  } catch (e) {
    alert(`轮换失败: ${e.message}`)
  }
}

async function rotateHostKey(n) {
  if (!confirm(`重置「${n.name}」的主机指纹？下次 SSH 连接将重新执行 TOFU 确认。`)) return
  try {
    await putJSON(`/nodes/${n.id}`, { rotate_host_key: true })
    alert('主机指纹已重置，下个轮询周期重新确认。')
  } catch (e) {
    alert(`重置失败: ${e.message}`)
  }
}

// ---------- 入网审批 ----------
async function approve(n) {
  try {
    await putJSON(`/nodes/${n.id}`, { approved: true })
    await store.fetchNodes()
  } catch (e) {
    alert(`批准失败: ${e.message}`)
  }
}

// 修改节点显示名称（PUT /nodes/:id 已支持 name 字段）
async function renameNode(n) {
  const name = prompt(`修改「${n.name}」的名称：`, n.name)
  if (name === null) return
  const trimmed = name.trim()
  if (!trimmed || trimmed === n.name) return
  try {
    await putJSON(`/nodes/${n.id}`, { name: trimmed })
    await store.fetchNodes()
  } catch (e) {
    alert(`改名失败: ${e.message}`)
  }
}

// 上移/下移：整表 id序列提交，服务端写 sort_order（数据库持久化，概览同步生效）
async function moveNode(i, dir) {
  const ids = nodes.value.map((n) => n.id)
  const j = i + dir
  if (j < 0 || j >= ids.length) return
  const t = ids[i]
  ids[i] = ids[j]
  ids[j] = t
  try {
    await store.reorderNodes(ids)
  } catch (e) {
    alert(`排序失败: ${e.message}`)
  }
}

// 拖拽排序（桌面端，原生 HTML5 DnD）：☰ 手柄拖起 → 目标行高亮 → 松手整表提交；
// 触屏不支持原生拖拽，保留 ▲▼ 按钮兜底。
const dragFrom = ref(-1)
const dragOverIdx = ref(-1)

function onDragStart(i, e) {
  dragFrom.value = i
  e.dataTransfer.effectAllowed = 'move'
  e.dataTransfer.setData('text/plain', String(i)) // Firefox 不 setData 不会启动拖拽
}

function onDragOver(i) {
  if (dragFrom.value >= 0) dragOverIdx.value = i
}

function onDragEnd() {
  dragFrom.value = -1
  dragOverIdx.value = -1
}

async function onDrop(i) {
  const from = dragFrom.value
  dragFrom.value = -1
  dragOverIdx.value = -1
  if (from < 0 || from === i) return
  const ids = nodes.value.map((n) => n.id)
  const [moved] = ids.splice(from, 1)
  ids.splice(i, 0, moved)
  try {
    await store.reorderNodes(ids)
  } catch (e) {
    alert(`排序失败: ${e.message}`)
  }
}

// ---------- 安全设置 ----------
const pw = reactive({ old: '', new: '', msg: '', msgOk: false })
const view = reactive({ on: false, pwd: '', msg: '', msgOk: false })
const joinKey = ref('')
// Join Key 默认打码，点「显示」才展开（避免进页面就明文挂在屏幕上）
const joinVisible = ref(false)

const notifyPlaceholder = computed(
  () =>
    ({
      feishu: 'https://open.feishu.cn/open-apis/bot/v2/hook/...',
      dingtalk: 'https://oapi.dingtalk.com/robot/send?access_token=...',
      wecom: 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=...',
      webhook: 'https://...',
    })[notify.channel] || 'https://...',
)

async function changePw() {
  try {
    await postJSON('/auth/password', { old_password: pw.old, new_password: pw.new })
    pw.msg = '已修改'
    pw.msgOk = true
    pw.old = ''
    pw.new = ''
  } catch (e) {
    pw.msg = e.message || '修改失败'
    pw.msgOk = false
  }
}

async function loadView() {
  try {
    const r = await getJSON('/auth/view')
    view.on = r.enabled
  } catch {
    /* 忽略 */
  }
}

async function saveView() {
  try {
    await putJSON('/auth/view', { enabled: view.on, password: view.pwd || undefined })
    view.pwd = ''
    view.msg = '已保存'
    view.msgOk = true
    await auth.refresh()
  } catch (e) {
    view.msg = e.message || '保存失败'
    view.msgOk = false
  }
}

async function loadJoin() {
  try {
    const r = await getJSON('/auth/join-key')
    joinKey.value = r.join_key
  } catch (e) {
    alert(e.message)
  }
}

async function toggleJoin() {
  if (joinVisible.value) {
    joinVisible.value = false
    return
  }
  if (!joinKey.value) await loadJoin()
  joinVisible.value = true
}

async function copyJoin() {
  if (!joinKey.value) await loadJoin()
  if (joinKey.value) await copy(joinKey.value)
}

async function rotateJoin() {
  if (!confirm('重置 Join Key 后旧密钥立即失效，确认？')) return
  try {
    const r = await postJSON('/auth/join-key/rotate', {})
    joinKey.value = r.join_key
    joinVisible.value = true // 刚轮换完直接展示新密钥，方便复制
  } catch (e) {
    alert(e.message)
  }
}

// ---------- 告警通知 ----------
const notify = reactive({
  enabled: false,
  channel: 'pushplus',
  token: '',
  url: '',
  tokenSet: false,
  urlSet: false,
  // SMTP 渠道
  smtpHost: '',
  smtpPort: 465,
  smtpUser: '',
  smtpPass: '',
  smtpFrom: '',
  smtpTo: '',
  smtpPassSet: false,
  msg: '',
  msgOk: false,
})

async function loadNotify() {
  try {
    const r = await getJSON('/notify/config')
    notify.enabled = r.enabled
    notify.channel = r.channel
    notify.tokenSet = r.token_set
    notify.urlSet = r.url_set
    notify.smtpHost = r.smtp_host || ''
    notify.smtpPort = r.smtp_port || 465
    notify.smtpUser = r.smtp_user || ''
    notify.smtpFrom = r.smtp_from || ''
    notify.smtpTo = r.smtp_to || ''
    notify.smtpPassSet = !!r.smtp_pass_set
  } catch {
    /* 忽略 */
  }
}

async function saveNotify() {
  try {
    const body = {
      enabled: notify.enabled,
      channel: notify.channel,
      token: notify.token || undefined,
      url: notify.url || undefined,
    }
    if (notify.channel === 'smtp') {
      body.smtp_host = notify.smtpHost || undefined
      body.smtp_port = notify.smtpPort || undefined
      body.smtp_user = notify.smtpUser || undefined
      body.smtp_pass = notify.smtpPass || undefined // 空=保持
      body.smtp_from = notify.smtpFrom || undefined
      body.smtp_to = notify.smtpTo || undefined
    }
    await putJSON('/notify/config', body)
    notify.token = ''
    notify.url = ''
    notify.smtpPass = ''
    notify.msg = '已保存'
    notify.msgOk = true
    await loadNotify()
  } catch (e) {
    notify.msg = e.message || '保存失败'
    notify.msgOk = false
  }
}

async function testNotify() {
  notify.msg = '发送中…'
  try {
    await postJSON('/notify/test', {})
    notify.msg = '测试消息已发送'
    notify.msgOk = true
    await loadNotifyLogs()
  } catch (e) {
    notify.msg = e.message || '发送失败'
    notify.msgOk = false
    await loadNotifyLogs()
  }
}

// 通知发送记录
const notifyLogs = ref([])

async function loadNotifyLogs() {
  try {
    const r = await getJSON('/notify/logs')
    notifyLogs.value = r.logs || []
  } catch {
    notifyLogs.value = []
  }
}

// ---------- SSH 凭据编辑 ----------
const editing = ref(null)
const editCreds = reactive({ username: '', password: '', port: 22 })
const editMsg = ref('')
const editOk = ref(false)

function openEdit(n) {
  editing.value = n
  editCreds.username = ''
  editCreds.password = ''
  editCreds.port = n.port || 22
  editMsg.value = ''
}

async function saveCreds() {
  try {
    const body = { port: editCreds.port || 22 }
    if (editCreds.username) body.username = editCreds.username
    if (editCreds.password) body.password = editCreds.password
    await putJSON(`/nodes/${editing.value.id}`, body)
    editMsg.value = '已保存，下一个轮询周期生效'
    editOk.value = true
    editing.value = null
    await store.fetchNodes()
  } catch (e) {
    editMsg.value = e.message || '保存失败'
    editOk.value = false
  }
}

const site = reactive({ title: '', msg: '', msgOk: false })

async function loadSiteTitle() {
  try {
    const r = await getJSON('/site')
    site.title = r.title
  } catch {
    /* 保持空输入框占位 */
  }
}

async function saveSite() {
  try {
    const r = await putJSON('/site', { title: site.title })
    site.msg = '已保存'
    site.msgOk = true
    siteTitle.value = r.title
    document.title = r.title
  } catch (e) {
    site.msg = `保存失败: ${e.message}`
    site.msgOk = false
  }
}

// ---------- 程序池 ----------
const poolItems = ref([])
const poolOta = ref(false)
const serverVersion = ref('')
const poolForm = reactive({ version: '' })
const poolFiles = ref([])
const poolMsg = ref('')
const poolUploading = ref(false)

async function loadPool() {
  try {
    const r = await getJSON('/pool')
    poolItems.value = r.items || []
    poolOta.value = !!r.ota_enabled
    serverVersion.value = r.server_version || ''
  } catch {
    /* 无权限或失败：保持空 */
  }
}

// 服务端版本提示：池中最新 server 条目 ≠ 当前版本即提示（仅提示，不做自动升级）
// 比较前去 v/V 前缀，避免 V0.3 与 0.3 形式差异造成误提示
const normVer = (s) => (s || '').trim().replace(/^[vV]/, '')
const serverHint = computed(() => {
  const latest = poolItems.value.find((it) => it.component === 'server')
  if (!latest || !serverVersion.value || normVer(latest.version) === normVer(serverVersion.value)) return ''
  return `服务端当前 ${serverVersion.value}，池中有新版本 ${latest.version}（请手动替换二进制升级）`
})

function onPoolFiles(e) {
  poolFiles.value = Array.from(e.target.files || [])
  poolMsg.value = ''
}

async function uploadPool() {
  if (!poolFiles.value.length || poolUploading.value) return
  poolUploading.value = true
  poolMsg.value = ''
  try {
    const fd = new FormData()
    poolFiles.value.forEach((f) => fd.append('files', f))
    if (poolForm.version) fd.append('version', poolForm.version)
    const res = await fetch('/api/v1/pool/batch', { method: 'POST', body: fd })
    if (!res.ok) {
      const j = await res.json().catch(() => ({}))
      throw new Error(j.message || `HTTP ${res.status}`)
    }
    const r = await res.json()
    const up = (r.uploaded || []).length
    const sk = r.skipped || []
    poolMsg.value = `成功 ${up} 个` + (sk.length ? `，跳过 ${sk.length}：` + sk.map((s) => `${s.name}（${s.reason}）`).join('；') : '')
    poolFiles.value = []
    await loadPool()
  } catch (e) {
    poolMsg.value = `上传失败: ${e.message}`
  } finally {
    poolUploading.value = false
  }
}

// 手动触发单节点升级：标记后节点下次上报即下发 upgrade 指令（不受全局 OTA 开关限制）
async function triggerUpgrade(n) {
  if (!confirm(`立即升级「${n.name}」到 v${n.upgrade.version}？\n（节点下次上报时生效，不受全局 OTA 开关限制）`)) return
  try {
    const r = await postJSON(`/nodes/${n.id}/upgrade`, {})
    alert(`已标记升级：${n.name} → v${r.version}（等待节点下次上报自动下载升级）`)
  } catch (e) {
    alert(`下发失败: ${e.message}`)
  }
}

// 手动设置公网 IP（SSH 自动查询失败 / API 模式无法自动获取时的兜底）
function editPublicIP(n) {
  const v = prompt(`设置「${n.name}」的公网 IP（留空清除）`, n.public_ip || '')
  if (v === null) return
  putJSON(`/nodes/${n.id}`, { public_ip: v.trim() })
    .then(() => store.fetchNodes())
    .catch((e) => alert(`保存失败: ${e.message}`))
}

async function removePool(it) {
  if (!confirm(`删除 ${it.component} v${it.version}（${[it.goos, it.arch].filter(Boolean).join('/')}）？`)) return
  try {
    await delJSON(`/pool/${it.id}`)
    await loadPool()
  } catch (e) {
    alert(`删除失败: ${e.message}`)
  }
}

async function togglePoolOta() {
  try {
    const r = await putJSON('/pool/ota', { enabled: !poolOta.value })
    poolOta.value = !!r.ota_enabled
  } catch (e) {
    alert(`开关失败: ${e.message}`)
  }
}

onMounted(() => {
  store.fetchNodes()
  loadRules()
  loadView()
  loadJoin()
  loadNotify()
  loadNotifyLogs()
  loadSiteTitle()
  loadPool()
})
</script>
