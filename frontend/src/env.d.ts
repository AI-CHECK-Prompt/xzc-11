/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<{}, {}, any>
  export default component
}

// Type definitions
interface Section {
  id: number
  code: string
  name: string
  line_code: string
  station_km: number
  description: string
  location_lat: number
  location_lng: number
}

interface Sensor {
  id: number
  section_id: number
  code: string
  type: 'displacement' | 'crack' | 'strain'
  position: string
  calibration: number
}

interface SensorData {
  id: number
  sensor_id: number
  value: number
  timestamp: string
}

interface Alert {
  id: number
  section_id: number
  sensor_id: number
  level: 'info' | 'warning' | 'danger'
  message: string
  deformation_rate: number
  threshold: number
  status: 'active' | 'resolved'
  triggered_at: string
  resolved_at: string | null
}

interface SectionRealtimeData {
  section_id: number
  section_code: string
  section_name: string
  latest_data: Record<number, SensorData>
  alerts: Alert[]
  updated_at: string
}

interface DeformationRate {
  sensor_id: number
  section_id?: number
  // 最严速率（mm/天），由后端多窗口分析确定，告警判定使用该字段
  rate: number
  // 触发该最严值的来源：endpoint / sliding / step
  rate_source?: 'endpoint' | 'sliding' | 'step'
  start_time: string
  end_time: string
  data_points: number
  last_value: number
  first_value: number
  // 三类速率明细（可选，向后兼容）
  endpoint_rate?: number
  max_sliding_rate?: number
  sliding_window?: string
  sliding_start_time?: string
  sliding_end_time?: string
  sliding_start_value?: number
  sliding_end_value?: number
  max_step_rate?: number
  step_from_time?: string
  step_to_time?: string
  step_from_value?: number
  step_to_value?: number
  // 窗口内极值
  min_value?: number
  max_value?: number
}

interface DashboardOverview {
  total_sections: number
  total_alerts: number
  danger_alerts: number
  warning_alerts: number
  active_alerts: number
}

interface WSMessage {
  type: string
  data: any
  timestamp: string
}