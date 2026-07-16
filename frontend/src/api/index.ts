import axios from 'axios'

const api = axios.create({
  baseURL: '/api/v1',
  timeout: 10000,
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

// 解决告警
export async function resolveAlert(alertId: number) {
  const { data } = await api.put(`/alerts/${alertId}/resolve`)
  return data
}