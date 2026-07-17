<template>
  <div>
    <div class="detail-header">
      <router-link to="/" class="back-btn">← 返回看板</router-link>
      <h2>断面健康度详情{{ section ? ' - ' + section.name : '' }}</h2>
      <div style="margin-left:auto;">
        <button
          class="btn btn-primary btn-sm"
          :disabled="recomputing"
          @click="manualRecompute"
        >{{ recomputing ? '重算中...' : '手动重算' }}</button>
      </div>
    </div>

    <div v-if="loading" class="loading">加载中...</div>

    <div v-else-if="!score" class="empty-state">
      该断面暂无健康度评分数据。请等待评分调度运行（每 1 分钟一次），或点击"手动重算"。
    </div>

    <div v-else>
      <!-- 总分卡 -->
      <div class="card" style="display:flex; gap:24px; align-items:center;">
        <div style="text-align:center; min-width:140px;">
          <div
            class="score-circle"
            :style="{
              background: `conic-gradient(${gradeColor(score.grade)} ${score.total_score * 3.6}deg, #f1f5f9 0deg)`,
            }"
          >
            <div class="score-circle-inner">
              <div class="score-value">{{ score.total_score.toFixed(1) }}</div>
              <div class="score-grade" :style="{ color: gradeColor(score.grade) }">
                {{ gradeLabel(score.grade) }}
              </div>
            </div>
          </div>
        </div>
        <div style="flex:1;">
          <div style="display:flex; flex-wrap:wrap; gap:24px; font-size:13px; color:var(--text-secondary);">
            <span>断面编号：{{ score.section_id }}</span>
            <span>位置类型：{{ positionLabel(score.position_type) }}</span>
            <span>灵敏度系数：{{ score.sensitivity.toFixed(2) }}</span>
            <span>触发方式：{{ triggerLabel(score.trigger_type) }}</span>
            <span>计算时间：{{ formatTime(score.calculated_at) }}</span>
          </div>
          <div style="margin-top:12px; font-size:13px; color:var(--text-secondary);">
            等级说明：{{ gradeMeta(score.grade).desc }}
          </div>
        </div>
      </div>

      <!-- 维度得分 -->
      <div class="card">
        <div class="card-header">
          <h2>评分维度明细</h2>
          <span style="font-size:12px; color:var(--text-secondary);">
            四个维度按 40% / 30% / 20% / 10% 加权汇总
          </span>
        </div>
        <div v-if="!details.length" class="empty-state">暂无明细数据</div>
        <table v-else class="health-table">
          <thead>
            <tr>
              <th>维度</th>
              <th>子指标</th>
              <th>原始值</th>
              <th>单项得分</th>
              <th>权重</th>
              <th>贡献</th>
              <th>说明</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(d, i) in details" :key="i">
              <td style="font-weight:600;">{{ dimensionLabel(d.dimension) }}</td>
              <td>{{ d.sub_dimension || '-' }}</td>
              <td>{{ formatRaw(d) }}</td>
              <td>
                <span :style="{ color: subScoreColor(d.sub_score), fontWeight: 600 }">
                  {{ d.sub_score.toFixed(1) }}
                </span>
              </td>
              <td>{{ (d.weight * 100).toFixed(0) }}%</td>
              <td>{{ d.contribution.toFixed(2) }}</td>
              <td style="font-size:12px; color:var(--text-secondary); max-width:280px;">
                {{ d.explanation }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 复核中间数据 -->
      <div class="card">
        <div class="card-header">
          <h2>复核中间数据（可重算）</h2>
          <span style="font-size:12px; color:var(--text-secondary);">
            每个传感器的多窗口速率、告警数、完整度、方差
          </span>
        </div>
        <div v-if="!intermediate.length" class="empty-state">暂无中间数据</div>
        <table v-else class="health-table">
          <thead>
            <tr>
              <th>传感器</th>
              <th>类型</th>
              <th>24h速率</th>
              <th>7d速率</th>
              <th>30d速率</th>
              <th>7d告警数</th>
              <th>数据完整度</th>
              <th>历史方差</th>
              <th>子项得分</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(it, i) in intermediate" :key="i">
              <td>#{{ it.sensor_id }}</td>
              <td>{{ sensorTypeLabel(it.sensor_type) }}</td>
              <td :style="{ color: rateColor(it.rate_24h, it.sensor_type) }">
                {{ it.rate_24h.toFixed(4) }}
              </td>
              <td :style="{ color: rateColor(it.rate_7d, it.sensor_type) }">
                {{ it.rate_7d.toFixed(4) }}
              </td>
              <td :style="{ color: rateColor(it.rate_30d, it.sensor_type) }">
                {{ it.rate_30d.toFixed(4) }}
              </td>
              <td>
                <span :style="{ color: it.recent_alert_count > 0 ? '#dc2626' : 'inherit' }">
                  {{ it.recent_alert_count }}
                </span>
              </td>
              <td>{{ (it.data_completeness * 100).toFixed(0) }}%</td>
              <td>{{ it.historical_variance.toFixed(4) }}</td>
              <td>{{ it.sensor_sub_score.toFixed(1) }}</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 历史曲线 -->
      <div class="card">
        <div class="card-header">
          <h2>历史健康度曲线</h2>
          <div class="time-range">
            <button
              v-for="r in historyRanges"
              :key="r.value"
              :class="{ active: selectedRange === r.value }"
              @click="changeRange(r.value)"
            >{{ r.label }}</button>
          </div>
        </div>
        <div v-if="historyLoading" class="loading">加载中...</div>
        <div v-else-if="!historyData.length" class="empty-state">暂无历史评分数据</div>
        <div v-else class="chart-container" style="height:340px;">
          <canvas ref="historyChartCanvas"></canvas>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'
import { useRoute } from 'vue-router'
import { Chart, registerables } from 'chart.js'
import * as api from '../api'
import { useHealthStore, HEALTH_GRADE_META, gradeLabel, gradeColor, gradeBg } from '../stores/health'

Chart.register(...registerables)

const route = useRoute()
const sectionId = Number(route.params.id)
const store = useHealthStore()

const loading = ref(true)
const section = ref<Section | null>(null)
const score = computed(() => store.detailMap[sectionId]?.score)
const details = computed(() => store.detailMap[sectionId]?.details || [])
const intermediate = computed(() => store.detailMap[sectionId]?.intermediate || [])

const recomputing = ref(false)

// 历史曲线
const selectedRange = ref('30d')
const historyLoading = ref(false)
const historyData = ref<HealthHistoryPoint[]>([])
const historyChartCanvas = ref<HTMLCanvasElement | null>(null)
let historyChart: Chart | null = null

const historyRanges = [
  { label: '24小时', value: '1d', hours: 24, interval: '1 hour' },
  { label: '7天', value: '7d', hours: 168, interval: '6 hours' },
  { label: '30天', value: '30d', hours: 720, interval: '1 day' },
  { label: '90天', value: '90d', hours: 2160, interval: '7 days' },
  { label: '1年', value: '365d', hours: 8760, interval: '30 days' },
]

function gradeMeta(g: HealthGrade) {
  return HEALTH_GRADE_META[g] || HEALTH_GRADE_META.normal
}

function dimensionLabel(dim: string) {
  const map: Record<string, string> = {
    alert: '告警维度 (40%)',
    trend: '趋势维度 (30%)',
    stability: '稳定度维度 (20%)',
    completeness: '完整度维度 (10%)',
  }
  return map[dim] || dim
}

function positionLabel(pos: string) {
  const map: Record<string, string> = { station: '车站', mid: '区间', shaft: '风井/竖井', cross: '横通道' }
  return map[pos] || pos
}

function triggerLabel(t: string) {
  const map: Record<string, string> = { cron: '定时任务', alert: '告警触发', manual: '手动触发' }
  return map[t] || t
}

function sensorTypeLabel(t: string) {
  const map: Record<string, string> = { crack: '裂缝计', displacement: '位移计', strain: '应变计' }
  return map[t] || t
}

function subScoreColor(s: number) {
  if (s >= 90) return '#16a34a'
  if (s >= 75) return '#0ea5e9'
  if (s >= 60) return '#eab308'
  if (s >= 40) return '#f97316'
  return '#dc2626'
}

function rateColor(rate: number, type: string) {
  const abs = Math.abs(rate)
  // 简化阈值展示（与后端阈值表一致）
  const threshold: Record<string, number> = {
    displacement: 0.5,
    crack: 0.1,
    strain: 10.0,
  }
  const t = threshold[type] || 0.5
  if (abs >= t * 2) return '#dc2626'
  if (abs >= t) return '#eab308'
  return '#16a34a'
}

function formatRaw(d: HealthScoreDetail) {
  // 简单展示：保留 4 位小数；为空时回退 explanation 解析
  if (typeof d.raw_value !== 'number') return '-'
  return d.raw_value.toFixed(4)
}

function formatTime(t: string) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

async function manualRecompute() {
  recomputing.value = true
  try {
    await store.triggerRecompute(sectionId)
    // 等待 3s 后重拉（与 throttle 30s 配合，避免重复请求）
    await new Promise(r => setTimeout(r, 2000))
    store.clearDetail(sectionId)
    await store.fetchDetail(sectionId)
  } catch (e) {
    console.error('手动重算失败:', e)
  } finally {
    recomputing.value = false
  }
}

async function changeRange(range: string) {
  selectedRange.value = range
  await loadHistory()
}

async function loadHistory() {
  const r = historyRanges.find(x => x.value === selectedRange.value)!
  const now = new Date()
  const start = new Date(now.getTime() - r.hours * 3600 * 1000)
  historyLoading.value = true
  try {
    await store.fetchHistory(sectionId, start.toISOString(), now.toISOString(), r.interval)
    const res = store.historyMap[sectionId]
    historyData.value = res?.data || []
    await nextTick()
    renderHistoryChart()
  } finally {
    historyLoading.value = false
  }
}

function renderHistoryChart() {
  if (!historyChartCanvas.value) return
  if (historyChart) {
    historyChart.destroy()
    historyChart = null
  }
  if (historyData.value.length === 0) return

  const labels = historyData.value.map(p => new Date(p.bucket).toLocaleString('zh-CN'))
  const avg = historyData.value.map(p => p.avg_score)
  const min = historyData.value.map(p => p.min_score)
  const max = historyData.value.map(p => p.max_score)

  historyChart = new Chart(historyChartCanvas.value, {
    type: 'line',
    data: {
      labels,
      datasets: [
        {
          label: '平均分',
          data: avg,
          borderColor: '#1a73e8',
          backgroundColor: 'rgba(26, 115, 232, 0.12)',
          fill: true,
          tension: 0.3,
          pointRadius: 2,
        },
        {
          label: '最高分',
          data: max,
          borderColor: 'rgba(22, 163, 74, 0.5)',
          borderDash: [4, 4],
          fill: false,
          pointRadius: 0,
          tension: 0.3,
        },
        {
          label: '最低分',
          data: min,
          borderColor: 'rgba(220, 38, 38, 0.5)',
          borderDash: [4, 4],
          fill: false,
          pointRadius: 0,
          tension: 0.3,
        },
      ],
    },
    options: {
      responsive: true,
      maintainAspectRatio: false,
      plugins: {
        legend: { position: 'top' },
        tooltip: { intersect: false, mode: 'index' },
      },
      scales: {
        y: { min: 0, max: 100, title: { display: true, text: '健康分值' } },
        x: { ticks: { maxTicksLimit: 8, font: { size: 11 } } },
      },
    },
  })
}

// 监听路由变化以重新加载
watch(() => sectionId, async (id) => {
  if (id) {
    loading.value = true
    try {
      const s = await api.getSection(id)
      section.value = s.data
    } catch {}
    await Promise.all([store.fetchDetail(id), loadHistory()])
    loading.value = false
  }
})

onMounted(async () => {
  try {
    const s = await api.getSection(sectionId)
    section.value = s.data
  } catch (e) {
    console.error('加载断面信息失败:', e)
  }
  await Promise.all([store.fetchDetail(sectionId), loadHistory()])
  loading.value = false
})

onUnmounted(() => {
  if (historyChart) {
    historyChart.destroy()
    historyChart = null
  }
})
</script>

<style scoped>
.score-circle {
  width: 130px;
  height: 130px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  margin: 0 auto;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.08);
}

.score-circle-inner {
  width: 102px;
  height: 102px;
  border-radius: 50%;
  background: white;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
}

.score-value {
  font-size: 32px;
  font-weight: 700;
  line-height: 1.1;
  color: var(--text);
}

.score-grade {
  font-size: 14px;
  font-weight: 600;
  margin-top: 4px;
}
</style>
