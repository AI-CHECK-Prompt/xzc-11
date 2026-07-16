<template>
  <div>
    <header class="header">
      <h1>隧道结构健康监测系统</h1>
      <div class="header-status">
        <span><span class="status-dot" :class="wsStatusClass"></span>WS: {{ wsConnected ? '已连接' : '未连接' }}</span>
        <span><span class="status-dot" :class="alertsSummary.danger > 0 ? 'danger' : 'online'"></span>告警: {{ alertsSummary.total }}</span>
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
import { useMonitorStore } from './stores/monitor'

const store = useMonitorStore()

const wsConnected = computed(() => store.wsConnected)
const wsStatusClass = computed(() => wsConnected.value ? 'online' : 'danger')
const alertsSummary = computed(() => store.alertsSummary)

onMounted(() => {
  store.connectWebSocket()
  store.fetchOverview()
})

onUnmounted(() => {
  store.disconnectWebSocket()
})
</script>