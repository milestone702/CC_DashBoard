<template>
  <div ref="el" class="h-64 w-full"></div>
</template>

<script setup>
// ECharts 折线图（按需注册：Line + Grid + Tooltip + Legend + Canvas）
import { onMounted, onBeforeUnmount, ref, watch, nextTick } from 'vue'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import { GridComponent, TooltipComponent, LegendComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { fmtRate } from '../utils/format'

echarts.use([LineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer])

const props = defineProps({
  points: { type: Array, default: () => [] },
  // series: [{ key, name, color, get?(p), format?: 'pct'|'bytes'|'raw' }]
  series: { type: Array, default: () => [] },
  timeMode: { type: String, default: 'time' }, // time→HH:mm；date→MM-dd
  yMax: { type: Number, default: 0 }, // >0 固定轴上限（百分比图 = 100）
  rangeStart: { type: Number, default: 0 }, // X 轴起点（unix 秒），>0 时固定范围
  rangeEnd: { type: Number, default: 0 }, // X 轴终点（unix 秒）
})

const el = ref(null)
let chart = null

const pad = (n) => String(n).padStart(2, '0')

function render() {
  if (!chart) return
  if (!props.points.length) {
    chart.clear()
    return
  }
  const fmt = props.series[0]?.format || 'raw'
  const option = {
    backgroundColor: 'transparent',
    grid: { left: 52, right: 14, top: 30, bottom: 26 },
    tooltip: {
      trigger: 'axis',
      backgroundColor: '#0f172a',
      borderColor: '#334155',
      textStyle: { color: '#e2e8f0', fontSize: 11 },
      valueFormatter: (v) =>
        fmt === 'bytes' ? fmtRate(v) : fmt === 'pct' ? `${Number(v).toFixed(2)}%` : v,
    },
    legend: { top: 0, textStyle: { color: '#94a3b8', fontSize: 11 } },
    xAxis: {
      type: 'time',
      // 固定为所选时间范围，数据稀疏时坐标轴也不收缩
      ...(props.rangeStart > 0 && props.rangeEnd > props.rangeStart
        ? { min: props.rangeStart * 1000, max: props.rangeEnd * 1000 }
        : {}),
      axisLabel: {
        color: '#64748b',
        fontSize: 10,
        formatter: (v) => {
          const d = new Date(v)
          return props.timeMode === 'date'
            ? `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
            : `${pad(d.getHours())}:${pad(d.getMinutes())}`
        },
      },
      axisLine: { lineStyle: { color: '#1e293b' } },
      splitLine: { show: false },
    },
    yAxis: {
      type: 'value',
      max: props.yMax > 0 ? props.yMax : null,
      axisLabel: {
        color: '#64748b',
        fontSize: 10,
        formatter: (v) => (fmt === 'bytes' ? fmtRate(v) : fmt === 'pct' ? `${v}%` : v),
      },
      splitLine: { lineStyle: { color: '#1e293b' } },
    },
    series: props.series.map((s) => ({
      name: s.name,
      type: 'line',
      showSymbol: false,
      smooth: false,
      lineStyle: { width: 1.5, color: s.color },
      itemStyle: { color: s.color },
      data: props.points.map((p) => [p.ts * 1000, s.get ? s.get(p) : p[s.key]]),
    })),
  }
  chart.setOption(option, true)
}

function onResize() {
  chart?.resize()
}

onMounted(() => {
  chart = echarts.init(el.value)
  render()
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  if (chart) {
    chart.dispose()
    chart = null
  }
})

watch(
  () => [props.points, props.series, props.timeMode, props.rangeStart, props.rangeEnd],
  () => nextTick(render),
  { deep: true },
)
</script>
