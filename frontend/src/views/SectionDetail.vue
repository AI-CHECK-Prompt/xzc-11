<template>
  <div>
    <div class="detail-header">
      <router-link to="/sections" class="back-btn">← 返回</router-link>
      <h2>{{ section?.name || '加载中...' }}</h2>
    </div>

    <div v-if="loading" class="loading">加载中...</div>

    <div v-else>
      <!-- 断面信息 -->
      <div class="card">
        <div style="display:flex; gap:40px; font-size:13px; color:var(--text-secondary);">
          <span>编号: {{ section?.code }}</span>
          <span>线路: {{ section?.line_code }}号线</span>
          <span>里程: {{ section?.station_km }}m</span>
          <span>描述: {{ section?.description }}</span>
        </div>
      </div>

      <!-- 活跃告警 -->
      <div v-if="activeAlerts.length > 0" class="card">
        <div class="card-header">
          <h2>活跃告警</h2>
        </div>
        <ul class="alert-list">
          <li v-for="alert in activeAlerts" :key="alert.id" class="alert-item" :class="[alert.level, alert.status]">
            <div class="alert-content">
              <div class="alert-message">{{ alert.message }}</div>
              <div class="alert-meta">
                {{ formatTime(alert.triggered_at) }}
                <span v-if="alert.status === 'resolved' && alert.resolved_at" class="resolved-tag">已解决 {{ formatTime(alert.resolved_at) }}</span>
              </div>
            </div>
          </li>
        </ul>
      </div>

      <!-- 传感器实时数据 -->
      <div class="sensor-list">
        <div v-for="sensor in sensors" :key="sensor.id" class="sensor-detail-card">
          <div class="sensor-header">
            <span>{{ sensorTypeLabel(sensor.type) }} - {{ sensor.position }}</span>
            <span style="font-size:12px; color:var(--text-secondary);">{{ sensor.code }}</span>
          </div>
          <div class="sensor-body">
            <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:12px;">
              <div>
                <span class="sensor-value">{{ getLatestValue(sensor.id)?.toFixed(3) || '--' }}</span>
                <span class="sensor-value unit"> {{ sensorUnit(sensor.type) }}</span>
              </div>
              <div v-if="getLatestData(sensor.id)" style="font-size:12px; color:var(--text-secondary);">
                {{ formatTime(getLatestData(sensor.id)!.timestamp) }}
              </div>
            </div>
            <div style="font-size:12px; color:var(--text-secondary); margin-bottom:12px;">
              24h变化速率: 
              <span :style="{ color: getRateColor(deformationRates[sensor.id]?.rate), fontWeight: 'bold' }">
                {{ formatRate(deformationRates[sensor.id]?.rate) }}
              </span>
              {{ sensorUnit(sensor.type) }}/天
            </div>
            <div>
              <button
                class="btn btn-primary btn-sm"
                @click="selectSensor(sensor)"
                :style="{ background: selectedSensorId === sensor.id ? 'var(--success)' : '' }"
              >
                {{ selectedSensorId === sensor.id ? '已选中' : '查看趋势' }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 历史趋势图 -->
      <div v-if="selectedSensorId" class="card" style="margin-top:16px;">
        <div class="card-header">
          <h2>历史趋势 - {{ selectedSensor?.code }}</h2>
          <div class="time-range">
            <button
              v-for="range in timeRanges"
              :key="range.value"
              :class="{ active: selectedRange === range.value }"
              @click="changeTimeRange(range.value)"
            >{{ range.label }}</button>
          </div>
        </div>
        <div class="chart-container">
          <canvas ref="chartCanvas"></canvas>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import { Chart, registerables } from 'chart.js'
import * as api from '../api'

Chart.register(...registerables)

const route = useRoute()
const sectionId = Number(route.params.id)

const loading = ref(true)
const section = ref<Section | null>(null)
const sensors = ref<Sensor[]>([])
const activeAlerts = ref<Alert[]>([])
const latestData = ref<Record<number, SensorData>>({})
const deformationRates = ref<Record<number, DeformationRate>>({})

const selectedSensorId = ref<number | null>(null)
const selectedSensor = ref<Sensor | null>(null)
const selectedRange = ref('24h')
const chartCanvas = ref<HTMLCanvasElement | null>(null)
let chart: Chart | null = null

const timeRanges = [
  { label: '1小时', value: '1h' },
  { label: '6小时', value: '6h' },
  { label: '24小时', value: '24h' },
  { label: '7天', value: '7d' },
  { label: '30天', value: '30d' },
]

const rangeMap: Record<string, { hours: number; interval: string }> = {
  '1h': { hours: 1, interval: '1 minute' },
  '6h': { hours: 6, interval: '5 minutes' },
  '24h': { hours: 24, interval: '15 minutes' },
  '7d': { hours: 168, interval: '1 hour' },
  '30d': { hours: 720, interval: '6 hours' },
}

function sensorTypeLabel(type: string) {
  const map: Record<string, string> = { crack: '裂缝计', displacement: '位移计', strain: '应变计' }
  return map[type] || type
}

function sensorUnit(type: string) {
  const map: Record<string, string> = { crack: 'mm', displacement: 'mm', strain: 'με' }
  return map[type] || ''
}

function getLatestValue(sensorId: number) {
  return latestData.value[sensorId]?.value
}

function getLatestData(sensorId: number) {
  return latestData.value[sensorId]
}

function getRateColor(rate: number | undefined) {
  if (rate === undefined) return 'var(--text-secondary)'
  const absRate = Math.abs(rate)
  if (absRate > 0.3) return 'var(--danger)'
  if (absRate > 0.1) return 'var(--warning)'
  return 'var(--success)'
}

function formatRate(rate: number | undefined) {
  if (rate === undefined) return '--'
  return (rate > 0 ? '+' : '') + rate.toFixed(4)
}

function formatTime(t: string) {
  return new Date(t).toLocaleString('zh-CN')
}

async function selectSensor(sensor: Sensor) {
  if (selectedSensorId.value === sensor.id) {
    selectedSensorId.value = null
    selectedSensor.value = null
    if (chart) {
      chart.destroy()
      chart = null
    }
    return
  }
  selectedSensorId.value = sensor.id
  selectedSensor.value = sensor
  await loadChartData()
}

async function changeTimeRange(range: string) {
  selectedRange.value = range
  await loadChartData()
}

async function loadChartData() {
  if (!selectedSensorId.value) return

  const range = rangeMap[selectedRange.value]
  const now = new Date()
  const start = new Date(now.getTime() - range.hours * 3600 * 1000)

  try {
    const data = await api.getSensorData(
      selectedSensorId.value,
      start.toISOString(),
      now.toISOString(),
      range.interval
    )
    await nextTick()
    renderChart(data.data || [])
  } catch (e) {
    console.error('加载历史数据失败:', e)
  }
}

function renderChart(data: SensorData[]) {
  if (!chartCanvas.value) return

  if (chart) {
    chart.destroy()
  }

  const labels = data.map(d => new Date(d.timestamp).toLocaleString('zh-CN'))
  const values = data.map(d => d.value)

  chart = new Chart(chartCanvas.value, {
    type: 'line',
    data: {
      labels,
      datasets: [{
        label: selectedSensor.value ? sensorTypeLabel(selectedSensor.value.type) + ' ' + sensorUnit(selectedSensor.value.type) : '值',
        data: values,
        borderColor: '#1a73e8',
        backgroundColor: 'rgba(26, 115, 232, 0.1)',
        fill: true,
        tension: 0.3,
        pointRadius: 0,
      }],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { display: false },
      },
      scales: {
        x: {
          ticks: { maxTicksLimit: 10, font: { size: 11 } },
        },
        y: {
          ticks: { font: { size: 11 } },
        },
      },
    },
  })
}

onMounted(async () => {
  try {
    const [sectionRes, sensorsRes, alertsRes] = await Promise.all([
      api.getSection(sectionId),
      api.getSectionSensors(sectionId),
      // 实时面板只关注当前活跃告警，后端按 status=active 过滤；
      // 客户端再防御过滤一次，避免历史脏数据混入。
      api.getSectionAlerts(sectionId, 10, 'active'),
    ])
    section.value = sectionRes.data
    sensors.value = sensorsRes.data || []
    activeAlerts.value = (alertsRes.data || []).filter(a => a.status === 'active')

    // 获取实时数据
    const realtimeRes = await api.getSectionRealtimeData(sectionId)
    if (realtimeRes.latest_data) {
      latestData.value = realtimeRes.latest_data
    }

    // 获取变形速率
    for (const sensor of sensors.value) {
      try {
        const rateRes = await api.getSensorDeformationRate(sensor.id)
        deformationRates.value[sensor.id] = rateRes.data
      } catch (e) {
        // 忽略（数据不足时）
      }
    }
  } catch (e) {
    console.error('加载断面数据失败:', e)
  } finally {
    loading.value = false
  }
})

onUnmounted(() => {
  if (chart) {
    chart.destroy()
    chart = null
  }
})
</script>