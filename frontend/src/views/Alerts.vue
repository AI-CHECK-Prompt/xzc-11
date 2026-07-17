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
import { computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useMonitorStore } from '../stores/monitor'
import * as api from '../api'

const store = useMonitorStore()
const { activeAlerts } = storeToRefs(store)

// 复用全局 monitor store 中的活跃告警列表，自动接收 WS 推送的"自动恢复"事件
const alerts = computed<Alert[]>(() => activeAlerts.value)

const dangerCount = computed(() => alerts.value.filter(a => a.level === 'danger').length)
const warningCount = computed(() => alerts.value.filter(a => a.level === 'warning').length)

function formatTime(t: string) {
  return new Date(t).toLocaleString('zh-CN')
}

async function handleResolve(alert: Alert) {
  try {
    await api.resolveAlert(alert.id)
    // 从 store 中移除（与后端"自动恢复"WS 推送行为保持一致）
    store.activeAlerts = store.activeAlerts.filter(a => a.id !== alert.id)
    store.fetchOverview()
  } catch (e) {
    console.error('解决告警失败:', e)
  }
}

onMounted(async () => {
  try {
    await store.fetchAlerts()
  } catch (e) {
    console.error('获取告警列表失败:', e)
  }
})
</script>