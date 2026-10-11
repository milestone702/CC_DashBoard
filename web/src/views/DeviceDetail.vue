<template>
  <div class="space-y-5">
    <!-- 头部：返回 + 节点信息 + 硬件详情 + 时间范围 -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div class="flex flex-wrap items-center gap-3">
        <router-link to="/" class="rounded bg-slate-800 px-2.5 py-1.5 text-xs text-slate-300 hover:bg-slate-700">
          ← 概览
        </router-link>
        <h2 class="text-xl font-semibold">{{ node?.name || `节点 #${id}` }}</h2>
        <span v-if="node" class="flex flex-col gap-0.5 text-xs text-slate-400">
          <span class="flex items-center gap-1.5">
            <span class="h-2 w-2 rounded-full" :class="statusDotClass(node.status)"></span>
            {{ statusText(node.status) }} · {{ node.os_type }} · {{ node.address || '—' }}
          </span>
          <!-- 下行公网 IP -->
          <span class="pl-3.5 text-slate-500">{{ node.public_ip || '—' }}</span>
        </span>
        <span
          v-if="node?.agent_version"
          class="rounded bg-slate-800 px-2 py-1 text-[11px] text-slate-400"
          title="Agent 版本"
        >
          Agent v{{ node.agent_version }}
        </span>
        <span
          v-if="node?.upgrade"
          class="rounded bg-emerald-900/60 px-2 py-1 text-[11px] text-emerald-300"
          title="程序池中有可升级版本"
        >
          可升级 → v{{ node.upgrade.version }}
        </span>
        <button
          v-if="auth.isAdmin && node?.upgrade"
          class="rounded bg-emerald-700 px-2.5 py-1 text-[11px] text-emerald-50 hover:bg-emerald-600"
          title="立即升级该节点（不受全局 OTA 开关限制，下次上报生效）"
          @click="triggerUpgrade"
        >
          立即升级
        </button>
        <button
          class="rounded bg-slate-800 px-3 py-1.5 text-xs text-slate-300 ring-1 ring-slate-700 hover:bg-slate-700"
          @click="showHw = true"
        >
          ⚙ 硬件详情
        </button>
      </div>
      <div class="flex gap-2 text-xs">
        <button
          v-for="r in ranges"
          :key="r"
          class="rounded bg-slate-800 px-3 py-1.5 text-slate-300 hover:bg-slate-700"
          :class="{ '!bg-emerald-700 text-white': r === activeRange }"
          @click="activeRange = r"
        >
          {{ r }}
        </button>
      </div>
    </div>

    <!-- 当前实时：6 项核心 -->
    <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="text-sm text-slate-400">当前实时</h3>
        <span class="text-[11px] text-slate-600">
          {{ liveTs ? `采样于 ${fmtClock(liveTs)}` : latest ? `采样于 ${fmtClock(latest.ts)}` : '等待上报…' }}
        </span>
      </div>
      <div class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-7">
        <div v-for="t in tiles" :key="t.label" class="rounded-lg bg-slate-950/60 px-3 py-2.5">
          <p class="text-[11px] text-slate-500">{{ t.label }}</p>
          <p class="mt-0.5 text-lg font-semibold text-slate-200">{{ t.value }}</p>
          <p v-if="t.sub" class="mt-0.5 truncate text-[10px] text-slate-600">{{ t.sub }}</p>
        </div>
      </div>
    </section>

    <!-- 实时明细：每核 / 内存细分 / 分网卡 / 进程 -->
    <div class="grid gap-4 lg:grid-cols-2">
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-3 text-sm text-slate-400">每核 CPU</h3>
        <div v-if="perCpu.length" class="grid grid-cols-2 gap-x-4 gap-y-2 sm:grid-cols-2">
          <div v-for="(v, i) in perCpu" :key="i">
            <div class="flex justify-between text-[11px] text-slate-400">
              <span>Core {{ i }}</span>
              <span :class="{ 'text-warning': v >= 75, 'text-danger': v >= 90 }">{{ v }}%</span>
            </div>
            <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-slate-800">
              <div
                class="h-full rounded-full transition-all"
                :class="v >= 90 ? 'bg-danger' : v >= 75 ? 'bg-warning' : 'bg-emerald-500'"
                :style="{ width: v + '%' }"
              ></div>
            </div>
          </div>
        </div>
        <p v-else class="text-xs text-slate-600">暂无数据</p>
      </section>

      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-3 text-sm text-slate-400">内存细分</h3>
        <dl v-if="memRows.length" class="grid grid-cols-2 gap-2 text-xs sm:grid-cols-3">
          <div v-for="kv in memRows" :key="kv[0]" class="rounded bg-slate-950/60 px-2.5 py-2">
            <dt class="text-slate-500">{{ kv[0] }}</dt>
            <dd class="mt-0.5 font-medium text-slate-200">{{ kv[1] }}</dd>
          </div>
        </dl>
        <p v-else class="text-xs text-slate-600">暂无数据</p>
        <h3 class="mb-2 mt-4 text-sm text-slate-400">温度</h3>
        <div v-if="temps.length" class="flex flex-wrap gap-2">
          <span v-for="t in temps" :key="t.label" class="rounded bg-slate-950/60 px-2 py-1 text-[11px] text-slate-300">
            {{ t.label }} {{ t.value }}°C
          </span>
        </div>
        <p v-else class="text-xs text-slate-600">{{ live ? '无温度传感器' : '暂无数据' }}</p>
      </section>

      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-3 text-sm text-slate-400">网络接口实时</h3>
        <table v-if="nics.length" class="w-full text-left text-xs">
          <thead class="text-slate-500">
            <tr><th class="pb-1.5">接口</th><th class="pb-1.5">IPv4</th><th class="pb-1.5 text-right">下行</th><th class="pb-1.5 text-right">上行</th></tr>
          </thead>
          <tbody class="divide-y divide-slate-800">
            <tr v-for="n in nics" :key="n.name">
              <td class="py-1.5 text-slate-300">{{ n.name }}</td>
              <td class="py-1.5 text-slate-500">{{ n.ipv4 || '—' }}</td>
              <td class="py-1.5 text-right text-slate-300">{{ fmtRate(n.rx_bps) }}</td>
              <td class="py-1.5 text-right text-slate-300">{{ fmtRate(n.tx_bps) }}</td>
            </tr>
          </tbody>
        </table>
        <p v-else class="text-xs text-slate-600">暂无数据</p>
      </section>

      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-3 text-sm text-slate-400">进程 Top5（按 CPU）</h3>
        <table v-if="procs.length" class="w-full text-left text-xs">
          <thead class="text-slate-500">
            <tr><th class="pb-1.5">PID</th><th class="pb-1.5">名称</th><th class="pb-1.5 text-right">CPU</th><th class="pb-1.5 text-right">内存</th></tr>
          </thead>
          <tbody class="divide-y divide-slate-800">
            <tr v-for="p in procs" :key="p.pid">
              <td class="py-1.5 text-slate-500">{{ p.pid }}</td>
              <td class="max-w-[160px] truncate py-1.5 text-slate-300">{{ p.name }}</td>
              <td class="py-1.5 text-right" :class="p.cpu >= 90 ? 'text-danger' : 'text-slate-300'">{{ p.cpu }}%</td>
              <td class="py-1.5 text-right text-slate-300">{{ p.mem }}%</td>
            </tr>
          </tbody>
        </table>
        <p v-else class="text-xs text-slate-600">暂无数据</p>
      </section>
    </div>

    <!-- 虚拟机 / 容器汇总（PVE 模式 C，来自 detail.vms） -->
    <section v-if="vms.length" class="rounded-xl border border-slate-800 bg-slate-900 p-4">
      <div class="mb-3 flex items-center justify-between">
        <h3 class="text-sm text-slate-400">虚拟机 / 容器（{{ vms.length }}）</h3>
        <span class="text-[11px] text-slate-600">PVE cluster/resources 汇总</span>
      </div>
      <table class="w-full text-left text-xs">
        <thead class="text-slate-500">
          <tr>
            <th class="pb-1.5">VMID</th>
            <th class="pb-1.5">名称</th>
            <th class="pb-1.5">类型</th>
            <th class="pb-1.5">状态</th>
            <th class="pb-1.5 text-right">vCPU 使用</th>
            <th class="pb-1.5 text-right">内存</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-slate-800">
          <tr v-for="vm in vms" :key="vm.vmid">
            <td class="py-1.5 text-slate-400">{{ vm.vmid }}</td>
            <td class="max-w-[200px] truncate py-1.5 text-slate-200">{{ vm.name || '—' }}</td>
            <td class="py-1.5 text-slate-500">{{ vm.type === 'lxc' ? 'LXC' : 'KVM' }}</td>
            <td class="py-1.5">
              <span
                class="rounded px-1.5 py-0.5 text-[10px]"
                :class="vm.status === 'running' ? 'bg-online/15 text-online' : 'bg-slate-800 text-slate-500'"
              >
                {{ vm.status === 'running' ? '运行中' : vm.status || '停止' }}
              </span>
            </td>
            <td class="py-1.5 text-right text-slate-300">{{ ((vm.cpu || 0) * 100).toFixed(2) }}%</td>
            <td class="py-1.5 text-right text-slate-300">
              <template v-if="vm.maxmem">{{ fmtBytes(vm.mem) }} / {{ fmtBytes(vm.maxmem) }}</template>
              <template v-else>—</template>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <!-- 历史趋势图表（X 轴固定为所选范围） -->
    <div class="grid gap-4 lg:grid-cols-2">
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-1 text-sm text-slate-400">CPU 使用率</h3>
        <MetricChart
          v-if="points.length"
          :points="points"
          :series="cpuSeries"
          :time-mode="timeMode"
          :y-max="100"
          :range-start="historyFrom"
          :range-end="historyTo"
        />
        <p v-else class="flex h-64 items-center justify-center text-xs text-slate-600">暂无数据（该范围）</p>
      </section>
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-1 text-sm text-slate-400">内存使用率</h3>
        <MetricChart
          v-if="points.length"
          :points="points"
          :series="memSeries"
          :time-mode="timeMode"
          :y-max="100"
          :range-start="historyFrom"
          :range-end="historyTo"
        />
        <p v-else class="flex h-64 items-center justify-center text-xs text-slate-600">暂无数据（该范围）</p>
      </section>
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-1 text-sm text-slate-400">网络吞吐</h3>
        <MetricChart
          v-if="points.length"
          :points="points"
          :series="netSeries"
          :time-mode="timeMode"
          :range-start="historyFrom"
          :range-end="historyTo"
        />
        <p v-else class="flex h-64 items-center justify-center text-xs text-slate-600">暂无数据（该范围）</p>
      </section>
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-1 text-sm text-slate-400">磁盘占用</h3>
        <MetricChart
          v-if="points.length"
          :points="points"
          :series="diskSeries"
          :time-mode="timeMode"
          :y-max="100"
          :range-start="historyFrom"
          :range-end="historyTo"
        />
        <p v-else class="flex h-64 items-center justify-center text-xs text-slate-600">暂无数据（该范围）</p>
      </section>
      <!-- 磁盘 I/O 历史曲线（Agent disk_read/disk_write 落盘） -->
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-1 text-sm text-slate-400">磁盘 I/O</h3>
        <MetricChart
          v-if="points.length"
          :points="points"
          :series="ioSeries"
          :time-mode="timeMode"
          :range-start="historyFrom"
          :range-end="historyTo"
        />
        <p v-else class="flex h-64 items-center justify-center text-xs text-slate-600">暂无数据（该范围）</p>
      </section>
      <!-- 每核 CPU 历史曲线（per_cpu JSON 落盘，聚合窗口取单点） -->
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-1 text-sm text-slate-400">每核 CPU</h3>
        <MetricChart
          v-if="points.length && hasPerCpu"
          :points="points"
          :series="perCpuSeries"
          :time-mode="timeMode"
          :y-max="100"
          :range-start="historyFrom"
          :range-end="historyTo"
        />
        <p v-else class="flex h-64 items-center justify-center text-xs text-slate-600">
          {{ points.length ? '该节点未上报每核数据' : '暂无数据（该范围）' }}
        </p>
      </section>
    </div>

    <!-- 明细面板 -->
    <div class="grid gap-4 lg:grid-cols-3">
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-2 text-sm text-slate-400">存储与挂载点</h3>
        <ul v-if="mounts.length" class="space-y-2 text-xs">
          <li v-for="m in mounts" :key="m.mountpoint" class="flex items-center justify-between gap-2">
            <span class="truncate text-slate-300">{{ m.mountpoint }}</span>
            <span class="shrink-0 text-slate-500">{{ fmtBytes(m.used) }} / {{ fmtBytes(m.total) }} · {{ m.pct }}%</span>
          </li>
        </ul>
        <p v-else class="text-xs text-slate-600">暂无数据</p>
      </section>
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-2 text-sm text-slate-400">硬件与系统</h3>
        <dl v-if="sysInfo.length" class="space-y-1.5 text-xs">
          <div v-for="kv in sysInfo" :key="kv[0]" class="flex justify-between gap-2">
            <dt class="text-slate-500">{{ kv[0] }}</dt>
            <dd class="truncate text-right text-slate-300">{{ kv[1] }}</dd>
          </div>
        </dl>
        <p v-else class="text-xs text-slate-600">暂无数据</p>
        <button class="mt-3 w-full rounded bg-slate-800 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="showHw = true">
          查看完整硬件详情 →
        </button>
      </section>
      <section class="rounded-xl border border-slate-800 bg-slate-900 p-4">
        <h3 class="mb-2 text-sm text-slate-400">当前告警</h3>
        <ul v-if="alerts.length" class="space-y-1.5 text-xs">
          <li v-for="a in alerts" :key="a.id" class="flex justify-between">
            <span :class="a.level === 'critical' ? 'text-danger' : 'text-warning'">
              {{ metricLabel(a.metric) }} 触发（{{ a.value }}）
            </span>
            <span class="text-slate-600">{{ fmtClock(a.triggered_at) }}</span>
          </li>
        </ul>
        <p v-else class="text-xs text-slate-600">无告警 ✓</p>
        <h3 class="mb-2 mt-4 text-sm text-slate-400">近期事件</h3>
        <ul v-if="events.length" class="space-y-1.5 text-xs">
          <li v-for="e in events" :key="e.id" class="flex justify-between">
            <span :class="e.event === 'online' ? 'text-online' : 'text-slate-400'">
              {{ e.event === 'online' ? '上线' : '离线' }}
            </span>
            <span class="text-slate-600">{{ fmtClock(e.ts) }}</span>
          </li>
        </ul>
        <p v-else class="text-xs text-slate-600">暂无事件</p>

        <h3 class="mb-2 mt-4 text-sm text-slate-400">开关机记录</h3>
        <ul v-if="bootEvents.length" class="max-h-48 space-y-1.5 overflow-y-auto text-xs">
          <li v-for="e in bootEvents" :key="e.ts + e.type" class="flex justify-between">
            <span :class="e.type === 'on' ? 'text-online' : 'text-slate-400'">
              {{ e.type === 'on' ? '开机' : '关机' }}
            </span>
            <span class="text-slate-600">{{ fmtClock(e.ts) }}</span>
          </li>
        </ul>
        <p v-else class="text-xs text-slate-600">暂无记录（等待 Agent 上报）</p>
      </section>
    </div>

    <!-- 硬件详情弹窗 -->
    <div
      v-if="showHw"
      class="fixed inset-0 z-50 flex items-center justify-center bg-slate-950/80 p-4"
      @click.self="showHw = false"
    >
      <div class="max-h-[85vh] w-full max-w-3xl overflow-y-auto rounded-xl border border-slate-700 bg-slate-900 p-5">
        <div class="mb-4 flex items-center justify-between">
          <h3 class="text-lg font-semibold">硬件详情 · {{ node?.name || '' }}</h3>
          <button class="rounded bg-slate-800 px-3 py-1.5 text-xs text-slate-300 hover:bg-slate-700" @click="showHw = false">
            关闭 ✕
          </button>
        </div>

        <template v-if="hw">
          <div class="grid gap-4 sm:grid-cols-2">
            <div class="rounded-lg bg-slate-950/60 p-3">
              <h4 class="mb-2 text-xs text-slate-400">处理器</h4>
              <dl class="space-y-1 text-xs">
                <div class="flex justify-between"><dt class="text-slate-500">型号</dt><dd class="text-slate-200">{{ hw.cpu?.model || '—' }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">物理/逻辑核</dt><dd class="text-slate-200">{{ hw.cpu?.cores_physical ?? '—' }} / {{ hw.cpu?.cores_logical ?? '—' }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">频率</dt><dd class="text-slate-200">{{ hw.cpu?.mhz ? hw.cpu.mhz + ' MHz' : '—' }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">架构</dt><dd class="text-slate-200">{{ hw.cpu?.arch || '—' }}</dd></div>
              </dl>
            </div>
            <div class="rounded-lg bg-slate-950/60 p-3">
              <h4 class="mb-2 text-xs text-slate-400">内存 / 主板</h4>
              <dl class="space-y-1 text-xs">
                <div class="flex justify-between"><dt class="text-slate-500">物理内存</dt><dd class="text-slate-200">{{ fmtBytes(hw.memory?.total) }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">Swap</dt><dd class="text-slate-200">{{ fmtBytes(hw.memory?.swap_total) }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">厂商</dt><dd class="text-slate-200">{{ hw.board?.vendor || '—' }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">产品</dt><dd class="text-slate-200">{{ hw.board?.product || '—' }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">序列号</dt><dd class="text-slate-200">{{ hw.board?.serial || '—' }}</dd></div>
                <div class="flex justify-between"><dt class="text-slate-500">BIOS</dt><dd class="text-slate-200">{{ hw.board?.bios || '—' }} {{ hw.board?.bios_date ? '· ' + hw.board.bios_date : '' }}</dd></div>
              </dl>
            </div>
          </div>

          <div class="mt-4 rounded-lg bg-slate-950/60 p-3">
            <h4 class="mb-2 text-xs text-slate-400">磁盘</h4>
            <table v-if="hw.disks?.length" class="w-full text-left text-xs">
              <thead class="text-slate-500">
                <tr><th class="pb-1.5">设备</th><th class="pb-1.5">型号</th><th class="pb-1.5">挂载点</th><th class="pb-1.5 text-right">容量</th><th class="pb-1.5 text-right">占用</th></tr>
              </thead>
              <tbody class="divide-y divide-slate-800">
                <tr v-for="(d, i) in hw.disks" :key="i">
                  <td class="py-1.5 text-slate-300">{{ d.name }}</td>
                  <td class="py-1.5 text-slate-400">{{ d.model || '—' }}</td>
                  <td class="py-1.5 text-slate-400">{{ d.mountpoint }}</td>
                  <td class="py-1.5 text-right text-slate-300">{{ fmtBytes(d.total) }}</td>
                  <td class="py-1.5 text-right text-slate-300">{{ d.pct }}%</td>
                </tr>
              </tbody>
            </table>
            <p v-else class="text-xs text-slate-600">暂无数据</p>
          </div>

          <div class="mt-4 rounded-lg bg-slate-950/60 p-3">
            <h4 class="mb-2 text-xs text-slate-400">网络接口</h4>
            <table v-if="hw.nics?.length" class="w-full text-left text-xs">
              <thead class="text-slate-500">
                <tr><th class="pb-1.5">名称</th><th class="pb-1.5">MAC</th><th class="pb-1.5">IPv4</th></tr>
              </thead>
              <tbody class="divide-y divide-slate-800">
                <tr v-for="(n, i) in hw.nics" :key="i">
                  <td class="py-1.5 text-slate-300">{{ n.name }}</td>
                  <td class="py-1.5 text-slate-400">{{ n.mac || '—' }}</td>
                  <td class="py-1.5 text-slate-400">{{ n.ipv4 || '—' }}</td>
                </tr>
              </tbody>
            </table>
            <p v-else class="text-xs text-slate-600">暂无数据</p>
          </div>

          <div class="mt-4 rounded-lg bg-slate-950/60 p-3">
            <h4 class="mb-2 text-xs text-slate-400">系统</h4>
            <dl class="grid grid-cols-2 gap-1 text-xs sm:grid-cols-4">
              <div><dt class="text-slate-500">主机名</dt><dd class="text-slate-200">{{ hw.system?.hostname || '—' }}</dd></div>
              <div><dt class="text-slate-500">平台</dt><dd class="text-slate-200">{{ hw.system?.platform || '—' }}</dd></div>
              <div><dt class="text-slate-500">内核</dt><dd class="text-slate-200">{{ hw.system?.kernel || '—' }}</dd></div>
              <div><dt class="text-slate-500">OS</dt><dd class="text-slate-200">{{ hw.system?.os || '—' }}</dd></div>
            </dl>
          </div>
        </template>
        <p v-else class="py-8 text-center text-xs text-slate-600">
          暂无硬件数据 — 等待 Agent 首次上报（约 1 个周期）
        </p>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { storeToRefs } from 'pinia'
import { useNodesStore } from '../stores/nodes'
import { useAuthStore } from '../stores/auth'
import { getJSON, postJSON } from '../api'
import MetricChart from '../components/MetricChart.vue'
import { fmtRate, fmtBytes, fmtUptime, fmtPct, statusDotClass, statusText } from '../utils/format'

const props = defineProps({ id: { type: String, required: true } })

const auth = useAuthStore()

// 手动触发 OTA：标记后节点下次上报即下发升级指令（不受全局 OTA 开关限制）
async function triggerUpgrade() {
  const n = node.value
  if (!n?.upgrade) return
  if (!confirm(`立即升级「${n.name}」到 v${n.upgrade.version}？\n（节点下次上报时生效）`)) return
  try {
    const r = await postJSON(`/nodes/${n.id}/upgrade`, {})
    alert(`已标记升级：v${r.version}（等待节点下次上报自动下载升级）`)
    loadDetail()
  } catch (e) {
    alert(`下发失败: ${e.message}`)
  }
}

// 页面标签 → API range 参数（docs/API.md 第 2 节：180d=半年，365d=一年）
const rangeMap = { '1小时': '1h', '24小时': '24h', '7天': '7d', '30天': '30d', '半年': '180d', '一年': '365d' }
const ranges = Object.keys(rangeMap)
const activeRange = ref('24小时')

const detail = ref(null)
const points = ref([])
const historyFrom = ref(0) // 当前范围起止（unix 秒），固定图表 X 轴
const historyTo = ref(0)
const showHw = ref(false)
let pollTimer = null

const store = useNodesStore()
const { nodes, liveById } = storeToRefs(store)

const node = computed(() => nodes.value.find((n) => n.id === Number(props.id)) || detail.value?.node || null)
const latest = computed(() => node.value?.latest || detail.value?.latest || null)
// 实时明细：WS 推送优先，详情轮询兜底
const live = computed(() => liveById.value[Number(props.id)] || detail.value?.live || null)
const liveTs = computed(() => live.value?.ts || null)

const timeMode = computed(() => (['1小时', '24小时'].includes(activeRange.value) ? 'time' : 'date'))

const temps = computed(() => live.value?.temps || [])
const perCpu = computed(() => live.value?.per_cpu || [])
const nics = computed(() => live.value?.nics || [])
const procs = computed(() => live.value?.procs || [])

const tiles = computed(() => {
  const s = latest.value
  const l = live.value
  if (!s && !l) {
    return ['CPU', '内存', '磁盘', '下行', '上行', '温度', '运行时长'].map((label) => ({ label, value: '—' }))
  }
  const cpu = l?.cpu_total ?? s?.cpu ?? 0
  const mem = l?.mem
  const topTemp = temps.value.length ? Math.max(...temps.value.map((t) => t.value)) : null
  const io = l?.disk_io
  // 磁盘 I/O：优先 IOPS（ESXi），否则按字节速率（Agent）；可附带延迟
  let ioSub = ''
  if (io) {
    const hasIops = io.read_iops != null || io.write_iops != null
    ioSub = hasIops
      ? `读 ${io.read_iops ?? 0} · 写 ${io.write_iops ?? 0} IOPS`
      : `读 ${fmtRate(io.read_bps)} · 写 ${fmtRate(io.write_bps)}`
    if (io.lat_ms) ioSub += ` · ${io.lat_ms}ms`
  }
  return [
    { label: 'CPU', value: fmtPct(cpu), sub: l ? `负载 ${l.load1 ?? '—'} / ${l.load5 ?? '—'} / ${l.load15 ?? '—'}` : '' },
    {
      label: '内存',
      value: mem ? fmtPct(mem.percent) : s ? fmtPct(s.mem_total ? (s.mem_used / s.mem_total) * 100 : 0) : '—',
      sub: mem ? `${fmtBytes(mem.used)} / ${fmtBytes(mem.total)}` : s ? `${fmtBytes(s.mem_used)} / ${fmtBytes(s.mem_total)}` : '',
    },
    { label: '磁盘', value: s ? fmtPct(s.disk_pct) : '—', sub: ioSub },
    { label: '下行', value: s ? fmtRate(s.net_rx) : '—' },
    { label: '上行', value: s ? fmtRate(s.net_tx) : '—' },
    {
      label: '温度',
      value: topTemp !== null ? `${topTemp}°C` : live ? '无传感器' : '—',
      sub: temps.value.length > 1 ? `${temps.value.length} 个传感器` : '',
    },
    { label: '运行时长', value: s ? fmtUptime(s.uptime) : '—' },
  ]
})

const memRows = computed(() => {
  const m = live.value?.mem
  if (!m) return []
  return [
    ['已用', fmtBytes(m.used)],
    ['可用', fmtBytes(m.available)],
    ['空闲', fmtBytes(m.free)],
    ['缓存', fmtBytes(m.cached)],
    ['缓冲', fmtBytes(m.buffers)],
    ['Swap', `${fmtBytes(m.swap_used)} / ${fmtBytes(m.swap_total)}`],
  ]
})

const cpuSeries = [{ key: 'cpu', name: 'CPU', color: '#10b981', format: 'pct' }]
const memSeries = [
  {
    key: 'mem_pct',
    name: '内存',
    color: '#38bdf8',
    format: 'pct',
    get: (p) => (p.mem_total > 0 ? Math.round((p.mem_used / p.mem_total) * 1000) / 10 : 0),
  },
]
const netSeries = [
  { key: 'net_rx', name: '下行', color: '#38bdf8', format: 'bytes' },
  { key: 'net_tx', name: '上行', color: '#a78bfa', format: 'bytes' },
]
const diskSeries = [{ key: 'disk_pct', name: '磁盘', color: '#f59e0b', format: 'pct' }]

// 磁盘 I/O 与每核历史曲线
const ioSeries = [
  { key: 'disk_read', name: '读取', color: '#38bdf8', format: 'bytes' },
  { key: 'disk_write', name: '写入', color: '#f472b6', format: 'bytes' },
]

// per_cpu 为 JSON 文本（服务端聚合窗口取单点）→ 解析缓存（按 point 对象）
const perCpuCache = new WeakMap()
function perCpuOf(p) {
  let v = perCpuCache.get(p)
  if (v === undefined) {
    v = null
    if (p.per_cpu) {
      try {
        v = JSON.parse(p.per_cpu)
      } catch {
        v = null
      }
    }
    perCpuCache.set(p, v)
  }
  return Array.isArray(v) ? v : null
}
const coreCount = computed(() => {
  for (const p of points.value) {
    const a = perCpuOf(p)
    if (a && a.length) return a.length
  }
  return 0
})
const hasPerCpu = computed(() => coreCount.value > 0)
const perCpuColors = ['#10b981', '#38bdf8', '#f59e0b', '#f472b6', '#a78bfa', '#22d3ee', '#fb923c', '#e879f9', '#4ade80', '#60a5fa']
const perCpuSeries = computed(() =>
  Array.from({ length: coreCount.value }, (_, i) => ({
    name: `Core ${i}`,
    color: perCpuColors[i % perCpuColors.length],
    format: 'pct',
    get: (p) => {
      const a = perCpuOf(p)
      return a && a[i] != null ? a[i] : null
    },
  })),
)

const d = computed(() => detail.value?.detail || null)
const hw = computed(() => detail.value?.hardware || null)
const vms = computed(() => d.value?.vms || [])
const mounts = computed(() => d.value?.mounts || [])
const events = computed(() => detail.value?.events || [])
const bootEvents = computed(() => detail.value?.boot_events || [])
const alerts = computed(() => detail.value?.alerts || [])

const sysInfo = computed(() => {
  if (!d.value) return []
  const boot = d.value.boot_time ? fmtClock(d.value.boot_time) : '—'
  const cores = live.value?.per_cpu?.length || d.value.cpu_cores
  return [
    ['主机名', d.value.hostname || '—'],
    ['系统平台', d.value.platform || '—'],
    ['内核', d.value.kernel || '—'],
    ['架构', d.value.arch || '—'],
    ['CPU 型号', d.value.cpu_model || '—'],
    ['核心数', cores ?? '—'],
    ['启动时间', boot],
  ]
})

function metricLabel(m) {
  return { cpu: 'CPU', mem_pct: '内存', disk_pct: '磁盘', offline: '离线' }[m] || m
}

function fmtClock(ts) {
  if (!ts) return '—'
  const d0 = new Date(ts * 1000)
  const pad = (n) => String(n).padStart(2, '0')
  return `${d0.getFullYear()}-${pad(d0.getMonth() + 1)}-${pad(d0.getDate())} ${pad(d0.getHours())}:${pad(d0.getMinutes())}:${pad(d0.getSeconds())}`
}

async function loadDetail() {
  try {
    detail.value = await getJSON(`/nodes/${props.id}/detail`)
  } catch {
    /* 节点不存在或后端未连 */
  }
}

async function loadHistory() {
  try {
    const r = await getJSON(`/nodes/${props.id}/history?range=${rangeMap[activeRange.value]}`)
    points.value = r.points || []
    historyFrom.value = r.from || 0
    historyTo.value = r.to || 0
  } catch {
    points.value = []
  }
}

watch(activeRange, () => loadHistory())
watch(
  () => props.id,
  () => {
    detail.value = null
    points.value = []
    showHw.value = false
    loadDetail()
    loadHistory()
  },
)

onMounted(() => {
  loadDetail()
  loadHistory()
  pollTimer = setInterval(loadDetail, 5000) // WS 为主，轮询兜底（含硬件清单）
})
onBeforeUnmount(() => clearInterval(pollTimer))
</script>
