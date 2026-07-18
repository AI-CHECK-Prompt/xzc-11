<template>
  <div>
    <header class="header">
      <h1>隧道结构健康监测系统</h1>
      <div class="header-status">
        <span><span class="status-dot" :class="wsStatusClass"></span>WS: {{ wsConnected ? '已连接' : '未连接' }}</span>
        <span><span class="status-dot" :class="alertsSummary.danger > 0 ? 'danger' : 'online'"></span>告警: {{ alertsSummary.total }}</span>
        <!-- 当前操作者：解决告警时通过 X-User 头传给后端，
             安全例会按"处理人"统计每位运维的告警处置工作量。
             持久化在 localStorage，刷新后保留上次填写。 -->
        <label class="operator-input" :title="operator ? `当前操作者：${operator}` : '请填写当前操作者账号'">
          <span class="operator-label">操作者:</span>
          <input
            type="text"
            class="operator-field"
            :value="operator"
            @input="onOperatorInput"
            placeholder="如 zhang.san / 张三"
            maxlength="64"
          />
        </label>
      </div>
    </header>
    <div class="main-layout">
      <nav class="sidebar">
        <ul class="sidebar-menu">
          <li><router-link to="/" exact-active-class="active"><span class="icon">📊</span>仪表盘</router-link></li>
          <li><router-link to="/sections" active-class="active"><span class="icon">🏗️</span>监测断面</router-link></li>
          <li><router-link to="/alerts" active-class="active"><span class="icon">🔔</span>告警中心</router-link></li>
        </ul>
      </nav>
      <main class="content">
        <router-view />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useMonitorStore } from './stores/monitor'
import { useUserStore } from './stores/user'

const store = useMonitorStore()
const userStore = useUserStore()
const { currentOperator: operator } = storeToRefs(userStore)

const wsConnected = computed(() => store.wsConnected)
const wsStatusClass = computed(() => wsConnected.value ? 'online' : 'danger')
const alertsSummary = computed(() => store.alertsSummary)

// 顶部输入框直接绑到 userStore，修改即时生效并写入 localStorage。
// 不做防抖——单次输入字符数有限，且 store.set 内部已做 trim。
function onOperatorInput(e: Event) {
  const v = (e.target as HTMLInputElement).value
  userStore.setOperator(v)
}

onMounted(() => {
  store.connectWebSocket()
  store.fetchOverview()
})

onUnmounted(() => {
  store.disconnectWebSocket()
})
</script>

<style scoped>
.operator-input {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--text-secondary);
  /* 让输入框与左右的状态徽章对齐 */
  padding: 0 6px;
  border-left: 1px solid var(--border, #e5e7eb);
  margin-left: 4px;
}
.operator-label {
  white-space: nowrap;
}
.operator-field {
  width: 140px;
  padding: 3px 8px;
  border: 1px solid var(--border, #d1d5db);
  border-radius: 4px;
  font-size: 13px;
  background: var(--bg-input, #fff);
  color: var(--text-primary, #111);
  outline: none;
  transition: border-color 0.15s;
}
.operator-field:focus {
  border-color: var(--primary, #1a73e8);
}
.operator-field::placeholder {
  color: var(--text-secondary, #9ca3af);
  opacity: 0.7;
}
</style>