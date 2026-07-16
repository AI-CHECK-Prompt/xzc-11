<template>
  <div>
    <div class="card">
      <div class="card-header">
        <h2>告警中心</h2>
        <div style="display:flex; gap:12px; font-size:13px;">
          <span style="color:var(--danger);">危险: {{ dangerCount }}</span>
          <span style="color:var(--warning);">警告: {{ warningCount }}</span>
          <span>总计: {{ alerts.length }}</span>
        </div>
      </div>

      <div v-if="alerts.length === 0" class="empty-state">
        暂无告警，系统运行正常
      </div>

      <ul v-else class="alert-list">
        <li v-for="alert in alerts" :key="alert.id" class="alert-item" :class="alert.level">
          <div class="alert-content">
            <div class="alert-message">{{ alert.message }}</div>
            <div class="alert-meta">
              触发时间: {{ formatTime(alert.triggered_at) }}
              <span v-if="alert.resolved_at"> | 解决时间: {{ formatTime(alert.resolved_at) }}</span>
            </div>
          </div>
          <div style="display:flex; align-items:center; gap:8px;">
            <span class="alert-status" :class="alert.status">
              {{ alert.status === 'active' ? '活跃' : '已解决' }}
            </span>
            <button
              v-if="alert.status === 'active'"
              class="btn btn-sm btn-primary"
              @click="handleResolve(alert)"
            >解决</button>
          </div>
        </li>
      </ul>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import * as api from '../api'

const alerts = ref<Alert[]>([])

const dangerCount = computed(() => alerts.value.filter(a => a.level === 'danger').length)
const warningCount = computed(() => alerts.value.filter(a => a.level === 'warning').length)

function formatTime(t: string) {
  return new Date(t).toLocaleString('zh-CN')
}

async function handleResolve(alert: Alert) {
  try {
    await api.resolveAlert(alert.id)
    alert.status = 'resolved'
    alert.resolved_at = new Date().toISOString()
  } catch (e) {
    console.error('解决告警失败:', e)
  }
}

onMounted(async () => {
  try {
    const data = await api.getActiveAlerts()
    alerts.value = data.data || []
  } catch (e) {
    console.error('获取告警列表失败:', e)
  }
})
</script>