<template>
  <div class="space-y-4">
    <!-- 头部：标题 + 统计 + 筛选 -->
    <div class="flex flex-wrap items-center gap-3">
      <h2 class="text-xl font-semibold">告警中心</h2>
      <span class="rounded-full bg-slate-800 px-3 py-1 text-xs text-slate-400">
        未恢复 {{ firingCount }} · 共 {{ alerts.length }}
      </span>
      <span
        class="rounded-full px-3 py-1 text-xs"
        :class="store.connected ? 'bg-online/15 text-online' : 'bg-slate-700 text-slate-300'"
      >
        {{ store.connected ? '实时' : '离线' }}
      </span>

      <div class="ml-auto flex flex-wrap items-center gap-2 text-xs">
        <select v-model="statusFilter" class="rounded bg-slate-900 px-2.5 py-1.5 text-slate-300">
          <option value="firing">未恢复</option>
          <option value="resolved">已恢复</option>
          <option value="all">全部</option>
        </select>
        <select v-model="levelFilter" class="rounded bg-slate-900 px-2.5 py-1.5 text-slate-300">
          <option value="all">全部级别</option>
          <option value="critical">严重</option>
          <option value="warning">警告</option>
        </select>
        <select v-model.number="nodeFilter" class="rounded bg-slate-900 px-2.5 py-1.5 text-slate-300">
          <option :value="0">全部节点</option>
          <option v-for="n in nodeOptions" :key="n.id" :value="n.id">{{ n.name }}</option>
        </select>
        <input
          v-model="search"
          type="search"
          placeholder="搜索节点 / 指标…"
          class="w-40 rounded bg-slate-900 px-2.5 py-1.5 text-slate-300"
        />
        <button class="rounded bg-slate-800 px-3 py-1.5 text-slate-300 hover:bg-slate-700" @click="load">
          刷新
        </button>
      </div>
    </div>

    <!-- 错误提示 -->
    <div
      v-if="error"
      class="flex items-center gap-2 rounded-lg border border-red-900 bg-red-950/40 px-3 py-2 text-xs text-red-300"
    >
      <span>⚠ {{ error }}</span>
      <button class="ml-auto rounded bg-red-900/60 px-2.5 py-1 hover:bg-red-900" @click="load">重试</button>
    </div>

    <!-- 列表 -->
    <section class="overflow-hidden rounded-xl border border-slate-800">
      <!-- 桌面：表格 -->
      <div class="hidden overflow-x-auto md:block">
        <table v-if="filtered.length" class="w-full text-left text-sm">
        <thead class="bg-slate-900 text-xs text-slate-500">
          <tr>
            <th class="px-3 py-2 font-normal">级别</th>
            <th class="px-3 py-2 font-normal">节点</th>
            <th class="px-3 py-2 font-normal">指标</th>
            <th class="px-3 py-2 font-normal">当前值</th>
            <th class="px-3 py-2 font-normal">触发时间</th>
            <th class="px-3 py-2 font-normal">状态</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="a in filtered"
            :key="a.id"
            class="cursor-pointer border-t border-slate-800 hover:bg-slate-900/60"
            @click="$router.push(`/device/${a.node_id}`)"
          >
            <td class="px-3 py-2">
              <span
                class="rounded px-1.5 py-0.5 text-[11px]"
                :class="a.level === 'critical' ? 'bg-danger/20 text-danger' : 'bg-warning/20 text-warning'"
              >
                {{ a.level === 'critical' ? '严重' : '警告' }}
              </span>
            </td>
            <td class="px-3 py-2 text-slate-300">{{ a.node_name || `节点#${a.node_id}` }}</td>
            <td class="px-3 py-2 text-slate-400">{{ metricLabel(a.metric) }}</td>
            <td class="px-3 py-2" :class="a.metric === 'offline' ? 'text-slate-400' : 'text-slate-200'">
              {{ fmtValue(a.metric, a.value) }}
            </td>
            <td class="px-3 py-2 text-slate-500">{{ fmtClock(a.triggered_at) }}</td>
            <td class="px-3 py-2">
              <span v-if="a.status === 'firing'" class="text-danger">未恢复</span>
              <span v-else class="text-slate-500">
                已恢复 <span class="text-slate-600">{{ a.resolved_at ? fmtClock(a.resolved_at) : '' }}</span>
              </span>
            </td>
          </tr>
        </tbody>
      </table>
      </div>

      <!-- 手机：卡片式 -->
      <ul v-if="filtered.length" class="divide-y divide-slate-800 md:hidden">
        <li
          v-for="a in filtered"
          :key="a.id"
          class="cursor-pointer px-3 py-2.5 active:bg-slate-900"
          @click="$router.push(`/device/${a.node_id}`)"
        >
          <div class="flex items-center gap-2">
            <span
              class="shrink-0 rounded px-1.5 py-0.5 text-[10px]"
              :class="a.level === 'critical' ? 'bg-danger/20 text-danger' : 'bg-warning/20 text-warning'"
            >
              {{ a.level === 'critical' ? '严重' : '警告' }}
            </span>
            <span class="min-w-0 truncate text-sm text-slate-200">
              {{ a.node_name || `节点#${a.node_id}` }}
            </span>
            <span
              class="ml-auto shrink-0 text-[11px]"
              :class="a.status === 'firing' ? 'text-danger' : 'text-slate-500'"
            >
              {{ a.status === 'firing' ? '未恢复' : '已恢复' }}
            </span>
          </div>
          <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-slate-500">
            <span>{{ metricLabel(a.metric) }}</span>
            <span :class="a.metric === 'offline' ? 'text-slate-400' : 'text-slate-300'">
              {{ fmtValue(a.metric, a.value) }}
            </span>
            <span>{{ fmtClock(a.triggered_at) }}</span>
          </div>
        </li>
      </ul>

      <p v-else-if="loading" class="px-4 py-10 text-center text-sm text-slate-500">加载中…</p>
      <p v-else class="px-4 py-10 text-center text-sm text-slate-500">
        {{ alerts.length === 0 ? '暂无告警记录' : '没有符合筛选条件的告警' }}
      </p>
    </section>

    <p class="text-[11px] text-slate-600">
      列表最多显示最近 200 条；告警触发/恢复时通过 WebSocket 实时刷新，也可用右上角手动刷新。
      已恢复的告警保留 90 天后自动清理。
    </p>
  </div>
</template>

<script setup>
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useNodesStore } from '../stores/nodes'
import { getJSON } from '../api'
import { fmtClock, fmtRate, fmtUptime } from '../utils/format'

const store = useNodesStore()

const alerts = ref([])
const loading = ref(false)
const error = ref('')
const statusFilter = ref('firing') // 默认只看未恢复
const levelFilter = ref('all')
const nodeFilter = ref(0)
const search = ref('')

// 指标展示名与单位（与后端 metric 枚举一致）
const METRIC_LABEL = {
  cpu: 'CPU 使用率',
  mem_pct: '内存使用率',
  disk_pct: '磁盘占用',
  load1: '1 分钟负载',
  net_rx: '下行速率',
  net_tx: '上行速率',
  offline: '离线',
}
const metricLabel = (m) => METRIC_LABEL[m] || m

// 按量纲格式化数值（离线告警的 value 是「已离线秒数」）
function fmtValue(metric, v) {
  switch (metric) {
    case 'cpu':
    case 'mem_pct':
    case 'disk_pct':
      return `${Number(v).toFixed(1)}%`
    case 'load1':
      return Number(v).toFixed(2)
    case 'net_rx':
    case 'net_tx':
      return fmtRate(v)
    case 'offline':
      return `离线 ${fmtUptime(v)}`
    default:
      return String(v)
  }
}

const firingCount = computed(() => alerts.value.filter((a) => a.status === 'firing').length)

// 节点筛选选项：从当前结果里推导（避免依赖节点列表是否已加载）
const nodeOptions = computed(() => {
  const seen = new Map()
  for (const a of alerts.value) {
    if (!seen.has(a.node_id)) seen.set(a.node_id, { id: a.node_id, name: a.node_name || `节点#${a.node_id}` })
  }
  return [...seen.values()]
})

const filtered = computed(() =>
  alerts.value.filter((a) => {
    if (levelFilter.value !== 'all' && a.level !== levelFilter.value) return false
    if (nodeFilter.value && a.node_id !== nodeFilter.value) return false
    const q = search.value.trim().toLowerCase()
    if (q) {
      const hay = `${a.node_name || ''} ${metricLabel(a.metric)} ${a.metric}`.toLowerCase()
      if (!hay.includes(q)) return false
    }
    return true
  }),
)

async function load() {
  loading.value = true
  error.value = ''
  try {
    // 状态筛选交给服务端，减少传输；级别/节点/搜索在前端过滤
    alerts.value = (await getJSON(`/alerts?status=${statusFilter.value}`)).alerts || []
  } catch (e) {
    error.value = e.message || '告警加载失败'
  } finally {
    loading.value = false
  }
}

// 切换状态筛选立即重新拉取；其它筛选纯前端过滤
watch(statusFilter, load)

// WS 推送 alert_firing/alert_resolved：防抖后刷新，避免批量告警时刷接口
let reloadTimer = null
watch(
  () => store.alertTick,
  () => {
    clearTimeout(reloadTimer)
    reloadTimer = setTimeout(load, 400)
  },
)

onMounted(load)
onBeforeUnmount(() => clearTimeout(reloadTimer))
</script>
