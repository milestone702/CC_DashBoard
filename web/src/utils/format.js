// 展示层格式化工具

// 速率：Bytes/s → B/s · KB/s · MB/s
export function fmtRate(bytes) {
  if (!bytes || bytes < 0) return '0 B/s'
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s']
  let i = 0
  let v = bytes
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`
}

// 容量：Bytes → B · KB · MB · GB · TB
export function fmtBytes(b) {
  if (!b || b < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let v = b
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : 1)} ${units[i]}`
}

// 运行时长：秒 → 「3天5小时」/「5小时10分」
export function fmtUptime(sec) {
  if (!sec || sec <= 0) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d}天${h}小时`
  if (h > 0) return `${h}小时${m}分`
  return `${m}分钟`
}

export function fmtPct(v, digits = 2) {
  if (v === null || v === undefined || Number.isNaN(v)) return '—'
  return `${Number(v).toFixed(digits)}%`
}

// 时钟：unix 秒 → 「MM-dd HH:mm:ss」（同年内省略年份）
export function fmtClock(ts) {
  if (!ts) return '—'
  const d = new Date(ts * 1000)
  const pad = (n) => String(n).padStart(2, '0')
  const date = `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  return `${date} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

// 状态展示色 class（Tailwind）
export function statusDotClass(status) {
  if (status === 'online') return 'bg-online'
  if (status === 'alert') return 'bg-danger animate-pulse'
  return 'bg-offline'
}

export function statusText(status) {
  return { online: '在线', alert: '告警', offline: '离线' }[status] || status
}

// 版本号：数据源可能自带前缀（构建注入的是 V0.2.0，池条目也可能是 V 开头），
// 展示统一成 v0.2.0，避免出现 vV0.2.0 这种双前缀。
export function fmtVer(v) {
  const s = String(v ?? '').trim().replace(/^[vV]/, '')
  return s ? `v${s}` : ''
}
