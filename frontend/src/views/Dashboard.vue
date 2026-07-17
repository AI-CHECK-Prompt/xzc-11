<template>
  <div>
    <div class="stats-grid">
      <div class="stat-card">
        <div class="stat-icon blue">🏗️</div>
        <div>
          <div class="stat-value">{{ overview?.total_sections || 0 }}</div>
          <div class="stat-label">监测断面</div>
        </div>
      </div>
      <div class="stat-card">
        <div class="stat-icon red">🔴</div>
        <div>
          <div class="stat-value">{{ overview?.danger_alerts || 0 }}</div>
          <div class="stat-label">危险告警</div>
        </div>
      </div>
      <div class="stat-card">
        <div class="stat-icon orange">🟡</div>
        <div>
          <div class="stat-value">{{ overview?.warning_alerts || 0 }}</div>
          <div class="stat-label">警告告警</div>
        </div>
      </div>
      <div class="stat-card" :class="{ 'stat-card-warn': (overview?.offline_sensors || 0) > 0 }">
        <div class="stat-icon" :class="(overview?.offline_sensors || 0) > 0 ? 'red' : 'green'">
          {{ (overview?.offline_sensors || 0) > 0 ? '⚠️' : '✅' }}
        </div>
        <div>
          <div class="stat-value">{{ overview?.offline_sensors || 0 }}</div>
          <div class="stat-label">离线传感器</div>
        </div>
      </div>
      <div class="stat-card">
        <div class="stat-icon green">📊</div>
        <div>
          <div class="stat-value">{{ normalSections }}</div>
          <div class="stat-label">正常断面</div>
        </div>
      </div>
    </div>

    <!-- 断面健康度看板（按线路展示所有断面的健康分值排名/趋势/告警数） -->
    <HealthDashboard />

    <div class="card">
      <div class="card-header">
        <h2>最近告警</h2>
        <router-link to="/alerts" style="font-size:13px; color: var(--primary); text-decoration:none;">查看全部</router-link>
      </div>
      <div v-if="store.activeAlerts.length === 0" class="empty-state">
        暂无告警，系统运行正常
      </div>
      <ul v-else class="alert-list">
        <li v-for="alert in store.activeAlerts.slice(0, 10)" :key="alert.id" class="alert-item" :class="alert.level">
          <div class="alert-content">
            <div class="alert-message">{{ alert.message }}</div>
            <div class="alert-meta">{{ formatTime(alert.triggered_at) }}</div>
          </div>
          <span class="alert-status" :class="alert.status">
            {{ alert.status === 'active' ? '活跃' : '已解决' }}
          </span>
        </li>
      </ul>
    </div>

    <div class="card">
      <div class="card-header">
        <h2>监测断面概览</h2>
        <router-link to="/sections" style="font-size:13px; color: var(--primary); text-decoration:none;">查看全部断面</router-link>
      </div>
      <div v-if="store.sections.length === 0" class="loading">加载中...</div>
      <div v-else class="section-grid">
        <router-link
          v-for="section in store.sections"
          :key="section.id"
          :to="`/sections/${section.id}`"
          class="section-card"
          style="text-decoration:none; color: inherit;"
        >
          <div class="section-name">{{ section.name }}</div>
          <div style="font-size:12px; color: var(--text-secondary); margin-bottom:8px;">
            {{ section.code }} | 里程: {{ section.station_km }}m
          </div>
          <div class="section-sensors">
            <span class="sensor-badge">裂缝计</span>
            <span class="sensor-badge">位移计</span>
            <span class="sensor-badge">应变计</span>
          </div>
          <!--
            当前告警数：右下角小红标
            数据源与详情页"活跃告警"列表完全一致（status='active'），
            不会出现"卡片 3 条/详情 1 条"或"卡片 0 条/详情 2 条"这种偏差。
            0 条时灰色不抢眼，>0 条红色高亮。
          -->
          <div
            v-if="getActiveAlertCount(section.id) > 0"
            class="section-alert-badge danger"
            :title="`该断面当前活跃告警 ${getActiveAlertCount(section.id)} 条`"
          >
            <span class="dot"></span>
            {{ getActiveAlertCount(section.id) }}条告警
          </div>
          <div
            v-else
            class="section-alert-badge ok"
            title="该断面当前无活跃告警"
          >
            <span class="dot"></span>
            0条告警
          </div>
        </router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useMonitorStore } from '../stores/monitor'
import HealthDashboard from './HealthDashboard.vue'

const store = useMonitorStore()

const overview = computed(() => store.overview)

const normalSections = computed(() => {
  if (!overview.value) return 0
  return overview.value.total_sections - overview.value.total_alerts
})

function formatTime(t: string) {
  return new Date(t).toLocaleString('zh-CN')
}

// 取某断面的"当前活跃告警数"
// 与详情页 /sections/:id/alerts?status=active 同口径（status='active'），
// 缺失 key 视为 0（该断面当前无活跃告警）。
function getActiveAlertCount(sectionId: number): number {
  return store.sectionActiveAlertCounts[sectionId] || 0
}

onMounted(() => {
  store.fetchOverview()
  store.fetchSections()
  store.fetchAlerts()
  // 加载每断面的"当前活跃告警数"，供卡片右下角徽标使用
  store.fetchSectionActiveAlertCounts()
})
</script>