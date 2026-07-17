import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api'

export const useMonitorStore = defineStore('monitor', () => {
  // WebSocket
  const wsConnected = ref(false)
  let ws: WebSocket | null = null
  let reconnectTimer: number | null = null

  // 数据
  const overview = ref<DashboardOverview | null>(null)
  const sections = ref<Section[]>([])
  const activeAlerts = ref<Alert[]>([])

  // 告警摘要
  const alertsSummary = computed(() => ({
    total: activeAlerts.value.length,
    danger: activeAlerts.value.filter(a => a.level === 'danger').length,
    warning: activeAlerts.value.filter(a => a.level === 'warning').length,
  }))

  // 获取仪表盘概览
  async function fetchOverview() {
    try {
      const data = await api.getDashboardOverview()
      overview.value = data
    } catch (e) {
      console.error('获取概览失败:', e)
    }
  }

  // 获取断面列表
  async function fetchSections() {
    try {
      const data = await api.getSections()
      sections.value = data.data || []
    } catch (e) {
      console.error('获取断面列表失败:', e)
    }
  }

  // 获取告警列表
  async function fetchAlerts() {
    try {
      const data = await api.getActiveAlerts()
      activeAlerts.value = data.data || []
    } catch (e) {
      console.error('获取告警列表失败:', e)
    }
  }

  // WebSocket 连接
  function connectWebSocket() {
    const protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${location.host}/ws`

    ws = new WebSocket(wsUrl)

    ws.onopen = () => {
      wsConnected.value = true
      console.log('【WS】已连接')
    }

    ws.onmessage = (event) => {
      try {
        const msg: WSMessage = JSON.parse(event.data)
        if (msg.type === 'alert') {
          const alert = msg.data as Alert
          if (alert.status === 'active') {
            const exists = activeAlerts.value.find(a => a.id === alert.id)
            if (!exists) {
              activeAlerts.value.unshift(alert)
            }
          }
          fetchOverview()
        } else if (msg.type === 'alert_resolved') {
          // 告警已自动关闭（数据恢复正常），从活跃列表中移除并刷新概览
          const payload = msg.data as { alert_ids: number[]; count: number; source: string }
          if (payload?.alert_ids?.length) {
            activeAlerts.value = activeAlerts.value.filter(
              a => !payload.alert_ids.includes(a.id)
            )
          }
          fetchOverview()
        } else if (msg.type === 'data_update') {
          // 数据更新，刷新概览
          fetchOverview()
        }
      } catch (e) {
        console.error('【WS】消息解析失败:', e)
      }
    }

    ws.onclose = () => {
      wsConnected.value = false
      console.log('【WS】已断开，5秒后重连...')
      reconnectTimer = window.setTimeout(connectWebSocket, 5000)
    }

    ws.onerror = (err) => {
      console.error('【WS】错误:', err)
    }
  }

  function disconnectWebSocket() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    if (ws) {
      ws.close()
      ws = null
    }
    wsConnected.value = false
  }

  return {
    wsConnected,
    overview,
    sections,
    activeAlerts,
    alertsSummary,
    fetchOverview,
    fetchSections,
    fetchAlerts,
    connectWebSocket,
    disconnectWebSocket,
  }
})