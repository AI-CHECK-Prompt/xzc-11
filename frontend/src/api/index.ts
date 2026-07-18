import axios from 'axios'
import { useUserStore } from '../stores/user'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 10000,
})

// 请求拦截器：自动注入当前操作者到 X-User 头
//
// 修复"处理人字段为空"问题：之前 axios 只发 Content-Type，
// 后端 middleware 拿不到当前运维账号，store.ResolveAlert 写入的
// handler 只能是兜底 'unknown'。这里把 useUserStore 的 currentOperator
// 拼到所有请求的 X-User 头里，让"按处理人统计"准确归到具体运维名下。
//
// 注意：拦截器在模块加载时注册一次，store 的引用是稳定的
// （Pinia createPinia 后 useUserStore 始终返回同一实例），
// 不会因为路由切换产生 stale 闭包。
api.interceptors.request.use((config) => {
  try {
    const userStore = useUserStore()
    const op = userStore.currentOperator?.trim()
    if (op) {
      config.headers = config.headers || {}
      ;(config.headers as Record<string, string>)['X-User'] = op
    }
  } catch {
    // Pinia 还未初始化时（理论上 main.ts 启动顺序保证不会发生）忽略
  }
  return config
})

// 获取仪表盘概览
export async function getDashboardOverview(): Promise<DashboardOverview> {
  const { data } = await api.get('/dashboard/overview')
  return data
}

// 获取所有断面
export async function getSections(): Promise<{ data: Section[]; total: number }> {
  const { data } = await api.get('/sections')
  return data
}

// 获取单个断面
export async function getSection(id: number): Promise<{ data: Section }> {
  const { data } = await api.get(`/sections/${id}`)
  return data
}

// 获取断面传感器
export async function getSectionSensors(id: number): Promise<{ data: Sensor[]; total: number }> {
  const { data } = await api.get(`/sections/${id}/sensors`)
  return data
}

// 获取断面实时数据
export async function getSectionRealtimeData(id: number) {
  const { data } = await api.get(`/sections/${id}/realtime`)
  return data
}

// 获取传感器历史数据
export async function getSensorData(
  sensorId: number,
  start: string,
  end: string,
  interval?: string
): Promise<{ data: SensorData[]; total: number; aggregated?: boolean }> {
  const params: any = { start, end }
  if (interval) params.interval = interval
  const { data } = await api.get(`/sensors/${sensorId}/data`, { params })
  return data
}

// 获取传感器变形速率
export async function getSensorDeformationRate(sensorId: number): Promise<{ data: DeformationRate }> {
  const { data } = await api.get(`/sensors/${sensorId}/rate`)
  return data
}

// 获取活跃告警
export async function getActiveAlerts(): Promise<{ data: Alert[]; total: number }> {
  const { data } = await api.get('/alerts/active')
  return data
}

// 获取断面告警
// status 可选：'active' 仅活跃告警（实时面板用），'resolved' 仅已解决，不传则不过滤
export async function getSectionAlerts(
  sectionId: number,
  limit = 50,
  status?: 'active' | 'resolved'
): Promise<{ data: Alert[]; total: number }> {
  const params: any = { limit }
  if (status) params.status = status
  const { data } = await api.get(`/sections/${sectionId}/alerts`, { params })
  return data
}

// 获取每个断面的"当前活跃告警数"（用于首页"监测断面概览"卡片右下角红标）
// 与详情页 /sections/:id/alerts?status=active 同口径（status='active'），
// 避免卡片红标与详情页活跃告警列表出现"3条/1条"这种不一致。
// 返回 Record<sectionId, count>；无活跃告警的断面不会出现在 data 中，前端按 0 处理。
export async function getSectionActiveAlertCounts(): Promise<{
  data: Record<string, number>
  total_sections: number
}> {
  const { data } = await api.get('/sections/active-alert-counts')
  return data
}

// 解决告警
export async function resolveAlert(alertId: number) {
  const { data } = await api.put(`/alerts/${alertId}/resolve`)
  return data
}

// ====== 健康度评分 ======

// 获取断面健康度排名看板
export async function getHealthRank(lineCode = '3'): Promise<HealthRankResponse> {
  const { data } = await api.get('/health-dashboard/rank', { params: { line_code: lineCode } })
  return data
}

// 获取指定断面的最新健康度评分（包含明细和复核中间数据）
export async function getSectionHealth(sectionId: number): Promise<SectionHealthResponse> {
  const { data } = await api.get(`/sections/${sectionId}/health`)
  return data
}

// 获取指定断面的历史健康度曲线
// interval 为 TimescaleDB time_bucket 字符串，如 '1 day' / '1 hour'
export async function getSectionHealthHistory(
  sectionId: number,
  start: string,
  end: string,
  interval = '1 day'
): Promise<HealthHistoryResponse> {
  const { data } = await api.get(`/sections/${sectionId}/health/history`, {
    params: { start, end, interval },
  })
  return data
}

// 手动触发某断面的健康度重算
export async function recomputeSectionHealth(sectionId: number) {
  const { data } = await api.post(`/sections/${sectionId}/health/recompute`)
  return data
}