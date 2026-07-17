<template>
  <div>
    <div class="card-header" style="background:var(--card-bg); padding:20px; border-radius:8px 8px 0 0; margin-bottom:0;">
      <h2>监测断面列表</h2>
      <span style="color:var(--text-secondary);">共 {{ sections.length }} 个断面</span>
    </div>
    <div class="section-grid">
      <router-link
        v-for="section in sections"
        :key="section.id"
        :to="`/sections/${section.id}`"
        class="section-card"
        style="text-decoration:none; color: inherit;"
      >
        <div class="section-name">{{ section.name }}</div>
        <div style="font-size:12px; color: var(--text-secondary); margin-bottom:10px;">
          {{ section.code }} | 里程: {{ section.station_km }}m
        </div>
        <div style="font-size:12px; color: var(--text-secondary); margin-bottom:10px;">
          {{ section.description }}
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
    <div v-if="sections.length === 0" class="loading">加载中...</div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import * as api from '../api'
import { useMonitorStore } from '../stores/monitor'

const sections = ref<Section[]>([])
const store = useMonitorStore()

// 与详情页同口径（status='active'），缺失 key 视为 0
function getActiveAlertCount(sectionId: number): number {
  return store.sectionActiveAlertCounts[sectionId] || 0
}

onMounted(async () => {
  try {
    const [sectionsRes, _countsRes] = await Promise.all([
      api.getSections(),
      // 详情页 /sections/:id/alerts?status=active 同口径，用于卡片右下角"当前告警数"红标
      api.getSectionActiveAlertCounts().then((data) => {
        const converted: Record<number, number> = {}
        for (const [k, v] of Object.entries(data.data || {})) {
          converted[Number(k)] = v
        }
        store.sectionActiveAlertCounts = converted
      }),
    ])
    sections.value = sectionsRes.data || []
  } catch (e) {
    console.error('获取断面列表失败:', e)
  }
})
</script>