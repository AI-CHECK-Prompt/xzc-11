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

// 健康度评分相关类型
type HealthGrade = 'excellent' | 'normal' | 'attention' | 'degraded' | 'danger'

interface SectionHealthRankItem {
  section_id: number
  section_code: string
  section_name: string
  line_code: string
  position_type: string
  total_score: number
  grade: HealthGrade
  displacement_score: number
  crack_score: number
  strain_score: number
  alert_dimension_score: number
  trend_dimension_score: number
  stability_dimension_score: number
  completeness_dimension_score: number
  sensitivity: number
  trigger_type: string
  calculated_at: string
  recent_alert_count: number
  prev_score?: number
  score_trend?: number
}

interface HealthRankResponse {
  data: SectionHealthRankItem[]
  total: number
  grade_count: Record<HealthGrade, number>
  line_code: string
}

interface HealthScoreDetail {
  dimension: string
  sub_dimension: string
  raw_value: number
  sub_score: number
  weight: number
  contribution: number
  explanation: string
}

interface HealthScoreIntermediate {
  sensor_id: number
  sensor_type: 'displacement' | 'crack' | 'strain'
  rate_24h: number
  rate_7d: number
  rate_30d: number
  recent_alert_count: number
  data_completeness: number
  historical_variance: number
  sensor_sub_score: number
  inputs_json: string
}

interface SectionHealthLatest {
  id: number
  section_id: number
  total_score: number
  grade: HealthGrade
  displacement_score: number
  crack_score: number
  strain_score: number
  alert_dimension_score: number
  trend_dimension_score: number
  stability_dimension_score: number
  completeness_dimension_score: number
  position_type: string
  sensitivity: number
  trigger_type: string
  calculated_at: string
}

interface SectionHealthResponse {
  score: SectionHealthLatest
  details: HealthScoreDetail[]
  intermediate: HealthScoreIntermediate[]
}

interface HealthHistoryPoint {
  bucket: string
  avg_score: number
  min_score: number
  max_score: number
  samples: number
}

interface HealthHistoryResponse {
  data: HealthHistoryPoint[]
  total: number
  interval: string
  start: string
  end: string
}