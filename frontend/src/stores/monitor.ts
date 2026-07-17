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

  // 每个断面的"当前活跃告警数"：sectionId -> count
  // 与详情页 /sections/:id/alerts?status=active 同口径（status='active'），
  // 用于首页"监测断面概览"卡片右下角的"当前告警数"红标。
  // 缺失的 sectionId 视作 0（该断面当前无活跃告警）。
  const sectionActiveAlertCounts = ref<Record<number, number>>({})

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

  // 拉取每个断面的"当前活跃告警数"
  // 修复"断面卡片显示 3 条告警，进入详情只有 1 条"问题：
  // 之前卡片若按 activeAlerts 自行按 section_id 聚合，会受 WS 推送/前端过滤影响
  // 与详情页数据源产生偏差。改为统一调用后端聚合接口，详情页与卡片数据源完全一致。
  async function fetchSectionActiveAlertCounts() {
    try {
      const data = await api.getSectionActiveAlertCounts()
      // data 后端以 string key 返回（{ "1": 3 }），前端统一转 number key
      const converted: Record<number, number> = {}
      for (const [k, v] of Object.entries(data.data || {})) {
        converted[Number(k)] = v
      }
      sectionActiveAlertCounts.value = converted
    } catch (e) {
      console.error('获取断面活跃告警数失败:', e)
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
          // 修复"卡片红标落后于实际活跃告警"问题：新增告警时立即刷新聚合数，
          // 避免前端拿老 map 自增导致与服务端 status 过滤后口径不一致。
          fetchSectionActiveAlertCounts()
        } else if (msg.type === 'alert_resolved') {
          // 告警已自动关闭（数据恢复正常），从活跃列表中移除并刷新概览
          const payload = msg.data as { alert_ids: number[]; count: number; source: string }
          if (payload?.alert_ids?.length) {
            activeAlerts.value = activeAlerts.value.filter(
              a => !payload.alert_ids.includes(a.id)
            )
          }
          fetchOverview()
          // 自动恢复 / 人工解决后，断面活跃告警数会减少，重新拉取保证一致
          fetchSectionActiveAlertCounts()
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
    sectionActiveAlertCounts,
    alertsSummary,
    fetchOverview,
    fetchSections,
    fetchAlerts,
    fetchSectionActiveAlertCounts,
    connectWebSocket,
    disconnectWebSocket,
  }
})