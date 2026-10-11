<template>
  <div>
    <div class="mb-5 flex flex-wrap items-center gap-3">
      <h2 class="text-xl font-semibold">全局设备概览</h2>
      <span class="rounded-full bg-slate-800 px-3 py-1 text-xs text-slate-400">
        在线 {{ summary.online }} / 总计 {{ summary.total }} · 告警 {{ summary.alerting }}
      </span>
      <span class="rounded-full px-3 py-1 text-xs" :class="badgeClass">{{ badgeText }}</span>

      <!-- 筛选与搜索 -->
      <div class="ml-auto flex flex-wrap items-center gap-2 text-xs">
        <select
          v-model.number="cols"
          :disabled="!auth.isAdmin"
          :title="auth.isAdmin ? '每行卡片数（保存到服务端数据库）' : '全局布局，仅管理员可调整'"
          class="rounded bg-slate-900 px-2.5 py-1.5 text-slate-300 outline-none ring-slate-700 focus:ring disabled:cursor-not-allowed disabled:opacity-50"
        >
          <option :value="0">每行·默认</option>
          <option v-for="c in [2, 3, 4, 6]" :key="c" :value="c">每行 {{ c }}</option>
        </select>
        <select
          v-model="typeFilter"
          class="rounded bg-slate-900 px-2.5 py-1.5 text-slate-300 outline-none ring-slate-700 focus:ring"
        >
          <option value="all">全部类型</option>
          <option v-for="t in osTypes" :key="t" :value="t">{{ t }}</option>
        </select>
        <select
          v-model="statusFilter"
          class="rounded bg-slate-900 px-2.5 py-1.5 text-slate-300 outline-none ring-slate-700 focus:ring"
        >
          <option value="all">全部状态</option>
          <option value="online">在线</option>
          <option value="alert">告警</option>
          <option value="offline">离线</option>
        </select>
        <input
          v-model="search"
          type="search"
          placeholder="搜索名称 / 地址…"
          class="w-44 rounded bg-slate-900 px-2.5 py-1.5 text-slate-300 outline-none ring-slate-700 focus:ring"
        />
      </div>
    </div>

    <!-- 响应式设备卡片阵列：PC 4 列 / Pad 2 列 / 手机 1 列（pending 节点不展示；cols>0 时固定列数） -->
    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4" :style="gridStyle">
      <div
        v-for="n in filteredNodes"
        :key="n.id"
        class="cursor-pointer rounded-xl border border-slate-800 bg-slate-900 p-4 transition hover:border-emerald-600"
        @click="$router.push(`/device/${n.id}`)"
      >
        <div class="flex items-center justify-between gap-2">
          <div class="flex min-w-0 items-center gap-2">
            <span class="h-2.5 w-2.5 shrink-0 rounded-full" :class="statusDotClass(n.status)"></span>
            <span class="truncate font-medium">{{ n.name }}</span>
          </div>
          <span class="shrink-0 rounded bg-slate-800 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-slate-400">
            {{ n.os_type }}
          </span>
        </div>
        <!-- 上行内网 IP，下行公网 IP -->
        <p class="mt-1 text-xs text-slate-500">{{ n.address || '—' }}</p>
        <p class="mt-0.5 text-xs text-slate-400">{{ n.public_ip || '—' }}</p>

        <!-- CPU / 内存 / 磁盘进度条 -->
        <div class="mt-4 space-y-2.5">
          <div v-for="m in bars(n)" :key="m.label">
            <div class="flex items-center justify-between text-[11px] text-slate-400">
              <span>{{ m.label }}</span>
              <span :class="{ 'text-danger': m.value >= 90, 'text-warning': m.value >= 75 && m.value < 90 }">
                {{ n.status === 'offline' ? '—' : m.value + '%' }}
              </span>
            </div>
            <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-slate-800">
              <div
                class="h-full rounded-full transition-all"
                :class="barColor(n, m.value)"
                :style="{ width: (n.status === 'offline' ? 0 : m.value) + '%' }"
              ></div>
            </div>
          </div>
        </div>

        <!-- 网速 / 运行时长 -->
        <div class="mt-4 flex items-center justify-between border-t border-slate-800 pt-3 text-[11px] text-slate-400">
          <span title="上传">↑ {{ fmtRate(src(n).net_tx) }}</span>
          <span title="下载">↓ {{ fmtRate(src(n).net_rx) }}</span>
          <span>{{ n.status === 'offline' ? '离线' : '运行 ' + fmtUptime(src(n).uptime) }}</span>
        </div>
      </div>
    </div>

    <p v-if="visibleNodes.length === 0" class="mt-10 text-center text-sm text-slate-500">加载中…</p>
    <p v-else-if="filteredNodes.length === 0" class="mt-10 text-center text-sm text-slate-500">
      没有匹配的设备，试试调整筛选条件
    </p>
  </div>
</template>

<script setup>
import { computed, ref, watch, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useNodesStore } from '../stores/nodes'
import { useAuthStore } from '../stores/auth'
import { getJSON, putJSON } from '../api'
import { fmtRate, fmtUptime, statusDotClass } from '../utils/format'

const store = useNodesStore()
const auth = useAuthStore()
const { nodes, summary, connected, mockMode } = storeToRefs(store)

// 每行卡片数：存服务端数据库（换设备/清缓存不丢）；0=响应式默认。
// 全局设置：仅管理员可改（访客回读即用，避免 view 会话 PUT 401 被弹回登录页）。
const cols = ref(0)
const gridStyle = computed(() =>
  cols.value > 0 ? { gridTemplateColumns: `repeat(${cols.value}, minmax(0, 1fr))` } : {},
)
async function loadCols() {
  try {
    const r = await getJSON('/overview/cols')
    cols.value = r.cols || 0
  } catch {
    /* 未登录/失败：保持默认 */
  }
}
watch(cols, async (v) => {
  if (!auth.isAdmin) return // 访客只读
  try {
    await putJSON('/overview/cols', { cols: v })
  } catch {
    /* 保存失败静默；下次改动重试 */
  }
})
onMounted(loadCols)

// 待审批节点不进概览（服务端已过滤，这里双保险）
const visibleNodes = computed(() => nodes.value.filter((n) => n.approved !== false))

// 筛选：类型 / 状态 / 关键词（名称、地址）
const osTypes = ['linux', 'windows', 'nas', 'openwrt', 'darwin', 'pve', 'esxi']
const typeFilter = ref('all')
const statusFilter = ref('all')
const search = ref('')

const filteredNodes = computed(() =>
  visibleNodes.value.filter((n) => {
    if (typeFilter.value !== 'all' && n.os_type !== typeFilter.value) return false
    if (statusFilter.value !== 'all' && n.status !== statusFilter.value) return false
    const q = search.value.trim().toLowerCase()
    if (q) {
      const hay = `${n.name} ${n.address || ''} ${n.public_ip || ''}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  }),
)

// 统一取数：API 节点在 latest 下；mock 数据为扁平字段
const src = (n) => n.latest || n

const badgeClass = computed(() =>
  mockMode.value ? 'bg-warning/20 text-warning' : connected.value ? 'bg-online/15 text-online' : 'bg-slate-700 text-slate-300',
)
const badgeText = computed(() =>
  mockMode.value ? '演示数据（后端未连接）' : connected.value ? '实时推送中' : '已连接 · WS 等待重连',
)

function memPct(n) {
  const s = src(n)
  if (s.mem_pct !== undefined) return Math.round(s.mem_pct)
  if (s.mem_total > 0) return Math.round((s.mem_used / s.mem_total) * 100)
  return 0
}

function bars(n) {
  const s = src(n)
  return [
    { label: 'CPU', value: Math.round(s.cpu || 0) },
    { label: '内存', value: memPct(n) },
    { label: '磁盘', value: Math.round(s.disk_pct || 0) },
  ]
}

function barColor(n, v) {
  if (n.status === 'offline') return 'bg-slate-700'
  if (v >= 90) return 'bg-danger'
  if (v >= 75) return 'bg-warning'
  return 'bg-emerald-500'
}
</script>
