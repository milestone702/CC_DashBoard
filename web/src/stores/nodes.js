import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getJSON, postJSON, delJSON } from '../api'
import { mockNodes, mockSummary } from '../mock'

// 节点列表、全局聚合与 WebSocket 实时通道（单例）
export const useNodesStore = defineStore('nodes', () => {
  const nodes = ref([])
  const summary = ref({ total: 0, online: 0, offline: 0, alerting: 0 })
  const connected = ref(false) // WS 已连接
  const mockMode = ref(false) // 演示数据（仅开发构建 + VITE_USE_MOCK=1 时启用）
  const liveById = ref({}) // 节点实时明细快照（WS type=live 推送）
  const lastError = ref('') // 最近一次加载失败原因（空串=正常）
  const alertTick = ref(0) // 告警事件计数：WS 收到 alert_firing/alert_resolved 时 +1，供告警页刷新

  // 生产构建永不展示假数据：演示模式必须显式开启
  const USE_MOCK = import.meta.env.DEV && import.meta.env.VITE_USE_MOCK === '1'

  let ws = null
  let retry = 1000
  let stopped = false
  let reconnectTimer = null // 重连定时器（纳入统一清理，避免 dispose 后仍重连）
  const timers = []

  // 加载失败时保留上一次成功的数据，只记录错误：
  // 绝不能把「网络抖动/401/500」变成「展示演示数据」，否则用户会误判真实设备状态。
  async function fetchNodes() {
    try {
      const data = await getJSON('/nodes')
      nodes.value = data.nodes || []
      mockMode.value = false
      lastError.value = ''
    } catch (e) {
      lastError.value = e.message || '节点列表加载失败'
      if (USE_MOCK && nodes.value.length === 0) {
        nodes.value = mockNodes
        mockMode.value = true
      }
    }
  }

  async function fetchSummary() {
    if (USE_MOCK && mockMode.value) {
      summary.value = mockSummary
      return
    }
    try {
      summary.value = await getJSON('/summary')
    } catch (e) {
      lastError.value = lastError.value || e.message || '汇总数据加载失败'
      if (USE_MOCK && summary.value.total === 0) {
        summary.value = mockSummary
        mockMode.value = true
      }
    }
  }

  function connectWS() {
    if (stopped || (USE_MOCK && mockMode.value) || (ws && ws.readyState <= WebSocket.OPEN)) return
    const proto = location.protocol === 'https:' ? 'wss' : 'ws'
    const sock = new WebSocket(`${proto}://${location.host}/api/v1/ws`)
    ws = sock
    sock.onopen = () => {
      connected.value = true
      retry = 1000
    }
    sock.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data)
        if (msg.type === 'metrics') {
          const n = nodes.value.find((x) => x.id === msg.node_id)
          if (n) {
            n.latest = msg.data
            if (n.status === 'offline') {
              n.status = 'online'
              fetchSummary()
            }
          }
        } else if (msg.type === 'live') {
          liveById.value[msg.node_id] = msg.data
        } else if (msg.type === 'node_status') {
          // 上下线实时推送（离线巡检/上线恢复时服务端主动推）
          const n = nodes.value.find((x) => x.id === msg.node_id)
          if (n && n.status !== msg.status) {
            n.status = msg.status
            fetchSummary()
          }
        } else if (msg.type === 'alert_firing' || msg.type === 'alert_resolved') {
          alertTick.value++ // 告警页 watch 此计数并重新拉取（比轮询及时）
        } else if (msg.type === 'node_added' || msg.type === 'node_removed') {
          fetchNodes()
          fetchSummary()
        }
      } catch {
        /* 忽略非 JSON 帧 */
      }
    }
    sock.onclose = () => {
      connected.value = false
      if (stopped || (USE_MOCK && mockMode.value)) return
      retry = Math.min(retry * 2, 30000)
      clearTimeout(reconnectTimer)
      reconnectTimer = setTimeout(connectWS, retry)
    }
    sock.onerror = () => sock.close()
  }

  async function createNode(body) {
    const res = await postJSON('/nodes', body)
    await fetchNodes()
    await fetchSummary()
    return res.node
  }

  async function deleteNode(id) {
    await delJSON(`/nodes/${id}`)
    await fetchNodes()
    await fetchSummary()
  }

  // 节点调序：整表 id序列提交，服务端写 sort_order（数据库持久化，概览同步）
  async function reorderNodes(ids) {
    await postJSON('/nodes/reorder', { ids })
    await fetchNodes()
  }

  // 应用启动 / 登录成功后调用：拉数据 + 建 WS + 30 秒兜底轮询（可重复调用，先关旧连接再重建）
  async function init() {
    stopped = true
    clearTimeout(reconnectTimer) // 取消挂起的重连，避免旧定时器在新连接建立后又开一条
    if (ws) {
      ws.close()
      ws = null
    }
    connected.value = false
    stopped = false
    timers.forEach(clearInterval)
    timers.length = 0
    await fetchNodes()
    await fetchSummary()
    if (USE_MOCK && mockMode.value) return // 仅演示模式下不建实时通道
    connectWS()
    timers.push(
      setInterval(() => {
        fetchSummary()
        if (!connected.value) connectWS()
      }, 30000),
    )
  }

  function dispose() {
    stopped = true
    clearTimeout(reconnectTimer)
    timers.forEach(clearInterval)
    timers.length = 0
    if (ws) {
      ws.close()
      ws = null
    }
    connected.value = false
  }

  return {
    nodes,
    summary,
    connected,
    mockMode,
    liveById,
    lastError,
    alertTick,
    init,
    dispose,
    fetchNodes,
    fetchSummary,
    connectWS,
    createNode,
    deleteNode,
    reorderNodes,
  }
})
