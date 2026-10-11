import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getJSON, postJSON, delJSON } from '../api'
import { mockNodes, mockSummary } from '../mock'

// 节点列表、全局聚合与 WebSocket 实时通道（单例）
export const useNodesStore = defineStore('nodes', () => {
  const nodes = ref([])
  const summary = ref({ total: 0, online: 0, offline: 0, alerting: 0 })
  const connected = ref(false) // WS 已连接
  const mockMode = ref(false) // 后端不可用，使用演示数据
  const liveById = ref({}) // 节点实时明细快照（WS type=live 推送）

  let ws = null
  let retry = 1000
  let stopped = false
  const timers = []

  async function fetchNodes() {
    try {
      const data = await getJSON('/nodes')
      nodes.value = data.nodes || []
      mockMode.value = false
    } catch {
      nodes.value = mockNodes
      mockMode.value = true
    }
  }

  async function fetchSummary() {
    if (mockMode.value) {
      summary.value = mockSummary
      return
    }
    try {
      summary.value = await getJSON('/summary')
    } catch {
      mockMode.value = true
      summary.value = mockSummary
    }
  }

  function connectWS() {
    if (stopped || mockMode.value || (ws && ws.readyState <= WebSocket.OPEN)) return
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
      if (stopped || mockMode.value) return
      retry = Math.min(retry * 2, 30000)
      setTimeout(connectWS, retry)
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
    if (mockMode.value) return
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
