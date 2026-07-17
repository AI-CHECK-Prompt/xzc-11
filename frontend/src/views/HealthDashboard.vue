<template>
  <div class="health-dashboard-card">
    <div class="card-header" style="border-bottom:none; padding-bottom:8px;">
      <div style="display:flex; align-items:center; gap:12px;">
        <h2>断面健康度看板</h2>
        <div class="health-line-tabs">
          <button
            v-for="ln in availableLines"
            :key="ln"
            :class="{ active: store.lineCode === ln }"
            @click="switchLine(ln)"
          >{{ ln }}号线</button>
        </div>
        <span
          v-if="store.rankUpdatedAt"
          style="font-size:12px; color: var(--text-secondary);"
        >最近更新：{{ formatTime(store.rankUpdatedAt) }}</span>
      </div>
      <button
        class="btn btn-primary btn-sm"
        :disabled="store.rankLoading"
        @click="refresh"
      >{{ store.rankLoading ? '刷新中...' : '刷新' }}</button>
    </div>

    <!-- 等级概览 -->
    <div class="health-grade-summary">
      <div
        v-for="g in gradeList"
        :key="g.key"
        class="health-grade-pill"
        :style="{ borderColor: g.color, background: g.bg }"
      >
        <div class="grade-label" :style="{ color: g.color }">{{ g.label }}</div>
        <div class="grade-count" :style="{ color: g.color }">{{ store.gradeCount[g.key] || 0 }}</div>
        <div class="grade-bar">
          <span :style="{ width: pct(g.key) + '%', background: g.color }"></span>
        </div>
      </div>
    </div>

    <!-- 表格 -->
    <div v-if="store.rankLoading && store.rankItems.length === 0" class="loading">加载中...</div>
    <div v-else-if="store.rankError" class="empty-state">
      加载失败：{{ store.rankError }}
    </div>
    <div v-else-if="store.rankItems.length === 0" class="empty-state">
      暂无健康度评分数据，请等待评分调度运行（每 1 分钟一次）
    </div>
    <div v-else style="overflow-x:auto; max-height:520px; overflow-y:auto;">
      <table class="health-table">
        <thead>
          <tr>
            <th style="width:50px;">排名</th>
            <th>断面</th>
            <th style="width:90px;">里程(m)</th>
            <th style="width:90px;">位置类型</th>
            <th style="width:240px;">健康分值</th>
            <th style="width:90px;">等级</th>
            <th style="width:90px;">7d告警数</th>
            <th style="width:120px;">趋势</th>
            <th style="width:140px;">计算时间</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(it, idx) in store.sortedByScore"
            :key="it.section_id"
            :class="{ 'danger-row': isHighRisk(it.grade) }"
            @click="goDetail(it.section_id)"
          >
            <td>{{ idx + 1 }}</td>
            <td>
              <div style="font-weight:600;">{{ it.section_name }}</div>
              <div style="font-size:11px; color:var(--text-secondary);">{{ it.section_code }}</div>
            </td>
            <td>{{ stationKmLabel(it) }}</td>
            <td>{{ positionLabel(it.position_type) }}</td>
            <td>
              <div class="score-bar">
                <div
                  class="score-bar-fill"
                  :style="{ width: it.total_score + '%', background: gradeColor(it.grade) }"
                ></div>
                <div class="score-bar-text">{{ it.total_score.toFixed(1) }}</div>
              </div>
            </td>
            <td>
              <span
                class="grade-tag"
                :style="{ color: gradeColor(it.grade), background: gradeBg(it.grade) }"
              >{{ gradeLabel(it.grade) }}</span>
            </td>
            <td>
              <span
                v-if="it.recent_alert_count > 0"
                :style="{ color: it.recent_alert_count >= 3 ? '#dc2626' : '#eab308', fontWeight: 600 }"
              >{{ it.recent_alert_count }}</span>
              <span v-else style="color: var(--text-secondary);">0</span>
            </td>
            <td>
              <span :class="trendClass(it.score_trend)">
                <template v-if="(it.score_trend ?? 0) > 0.5">↑ {{ it.score_trend!.toFixed(1) }}</template>
                <template v-else-if="(it.score_trend ?? 0) < -0.5">↓ {{ Math.abs(it.score_trend!).toFixed(1) }}</template>
                <template v-else>—</template>
              </span>
            </td>
            <td style="font-size:12px; color: var(--text-secondary);">
              {{ formatTime(it.calculated_at) }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useHealthStore, HEALTH_GRADE_META, gradeLabel, gradeColor, gradeBg } from '../stores/health'

const store = useHealthStore()
const router = useRouter()

const availableLines = ['1', '2', '3', '4']

// 排序：先按风险等级升序，再按分值升序（差→好），保证表格从差到好
const gradeList = computed(() => {
  return (['danger', 'degraded', 'attention', 'normal', 'excellent'] as HealthGrade[]).map(k => ({
    key: k,
    label: HEALTH_GRADE_META[k].label,
    color: HEALTH_GRADE_META[k].color,
    bg: HEALTH_GRADE_META[k].bg,
  }))
})

function pct(g: HealthGrade) {
  const total = store.rankItems.length
  if (total === 0) return 0
  return Math.round(((store.gradeCount[g] || 0) / total) * 100)
}

function isHighRisk(g: HealthGrade) {
  return g === 'danger' || g === 'degraded'
}

function trendClass(trend?: number) {
  if (trend === undefined || Math.abs(trend) < 0.5) return 'score-trend flat'
  return trend > 0 ? 'score-trend up' : 'score-trend down'
}

function positionLabel(pos: string) {
  const map: Record<string, string> = {
    station: '车站',
    mid: '区间',
    shaft: '风井/竖井',
    cross: '横通道',
  }
  return map[pos] || pos
}

function stationKmLabel(it: SectionHealthRankItem) {
  // 站名无法直接从当前数据中拿到，仅展示里程数字
  return (it as any).station_km ?? '-'
}

function formatTime(t: string) {
  if (!t) return '-'
  return new Date(t).toLocaleString('zh-CN')
}

async function refresh() {
  await store.fetchRank()
}

async function switchLine(line: string) {
  if (line === store.lineCode) return
  await store.fetchRank(line)
}

function goDetail(sectionId: number) {
  router.push(`/sections/${sectionId}/health`)
}

onMounted(async () => {
  if (store.rankItems.length === 0) {
    await store.fetchRank()
  }
})
</script>
