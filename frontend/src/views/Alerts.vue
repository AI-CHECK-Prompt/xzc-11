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
        <li v-for="alert in alerts" :key="alert.id" class="alert-item" :class="alertLevelClass(alert.level)">
          <span class="alert-level-badge" :class="'level-' + alertLevelClass(alert.level)">
            {{ alertLevelLabel(alert.level) }}
          </span>
          <div class="alert-content">
            <div class="alert-message">{{ alert.message }}</div>
            <div class="alert-meta">
              触发时间: {{ formatTime(alert.triggered_at) }}
              <span v-if="alert.resolved_at"> | 解决时间: {{ formatTime(alert.resolved_at) }}</span>
              <!-- 处理人：仅当告警为已解决状态且 handler 字段存在时显示。
                   - 历史数据（handler 为 null）显示 "-"
                   - 系统自动恢复（handler='system'）显示"系统"
                   - 人工解决（其他）显示具体运维账号 -->
              <span v-if="alert.status === 'resolved'">
                | 处理人: <strong>{{ formatHandler(alert.handler) }}</strong>
              </span>
            </div>
          </div>
          <div style="display:flex; align-items:center; gap:8px;">
            <span class="alert-status" :class="alert.status">
              {{ alert.status === 'active' ? '活跃' : '已解决' }}
            </span>
            <button
              v-if="alert.status === 'active'"
              class="btn btn-sm btn-primary"
              :disabled="!operator || resolvingId === alert.id"
              :title="operator ? '点击标记为已解决' : '请先在右上角填写当前操作者账号'"
              @click="handleResolve(alert)"
            >
              {{ resolvingId === alert.id ? '处理中...' : '解决' }}
            </button>
          </div>
        </li>
      </ul>

      <!-- 提示：未填写操作者时引导用户去头部填写 -->
      <div v-if="!operator && alerts.some(a => a.status === 'active')" class="hint-banner">
        提示：解决告警前请先在右上角填写当前操作者账号（用于安全例会按"处理人"统计工作量）。
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { useMonitorStore } from '../stores/monitor'
import { useUserStore } from '../stores/user'
import * as api from '../api'

const ALERT_LEVEL_META: Record<Alert['level'], { label: string; cls: string }> = {
  info:    { label: '提示',   cls: 'info' },
  warning: { label: '警告',   cls: 'warning' },
  danger:  { label: '严重',   cls: 'danger' },
}

const store = useMonitorStore()
const { activeAlerts } = storeToRefs(store)
const userStore = useUserStore()
const { currentOperator: operator } = storeToRefs(userStore)

// 防止同一告警被并发点击"解决"按钮导致重复请求
const resolvingId = ref<number | null>(null)

const alerts = computed<Alert[]>(() => activeAlerts.value)

const dangerCount = computed(() => alerts.value.filter(a => a.level === 'danger').length)
const warningCount = computed(() => alerts.value.filter(a => a.level === 'warning').length)

function alertLevelClass(level: Alert['level']) {
  return ALERT_LEVEL_META[level]?.cls ?? 'info'
}

function alertLevelLabel(level: Alert['level']) {
  return ALERT_LEVEL_META[level]?.label ?? '提示'
}

function formatTime(t: string) {
  return new Date(t).toLocaleString('zh-CN')
}

// 格式化"处理人"展示：
//   - null/undefined  -> '-'（历史数据未填写）
//   - ''              -> '-'（空字符串视为异常，提示排查）
//   - 'system'        -> '系统'（自动恢复，区分于人工）
//   - 其他            -> 原样显示
function formatHandler(h: string | null | undefined): string {
  if (h == null || h === '') return '-'
  if (h === 'system') return '系统（自动恢复）'
  return h
}

async function handleResolve(alert: Alert) {
  // 操作者校验：必须在头部填写当前运维账号才能解决告警
  if (!operator.value || !operator.value.trim()) {
    alert('请先在右上角"操作者"输入框填写当前运维账号，再解决告警。')
    return
  }
  if (resolvingId.value !== null) return
  resolvingId.value = alert.id
  try {
    await api.resolveAlert(alert.id)
    // 从 store 中移除（与后端"自动恢复"WS 推送行为保持一致）
    store.activeAlerts = store.activeAlerts.filter(a => a.id !== alert.id)
    store.fetchOverview()
  } catch (e) {
    console.error('解决告警失败:', e)
    alert('解决告警失败：' + (e as Error).message)
  } finally {
    resolvingId.value = null
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

<style scoped>
.hint-banner {
  margin-top: 12px;
  padding: 10px 14px;
  background: rgba(245, 158, 11, 0.08);
  border: 1px solid rgba(245, 158, 11, 0.3);
  border-radius: 6px;
  color: var(--warning);
  font-size: 13px;
}
</style>