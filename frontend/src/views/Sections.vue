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
      </router-link>
    </div>
    <div v-if="sections.length === 0" class="loading">加载中...</div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import * as api from '../api'

const sections = ref<Section[]>([])

onMounted(async () => {
  try {
    const data = await api.getSections()
    sections.value = data.data || []
  } catch (e) {
    console.error('获取断面列表失败:', e)
  }
})
</script>