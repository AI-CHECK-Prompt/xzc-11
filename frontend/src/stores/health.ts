import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import * as api from '../api'

/**
 * 断面健康度综合评估状态管理
 * - 看板排名缓存（默认当前线路）
 * - 详情/历史数据按需加载
 * - 等级 → 颜色/标签 映射集中管理
 */
export const useHealthStore = defineStore('health', () => {
  // 当前选中线路
  const lineCode = ref('3')

  // 看板数据
  const rankItems = ref<SectionHealthRankItem[]>([])
  const gradeCount = ref<Record<HealthGrade, number>>({
    excellent: 0,
    normal: 0,
    attention: 0,
    degraded: 0,
    danger: 0,
  })
  const rankLoading = ref(false)
  const rankError = ref<string | null>(null)
  const rankUpdatedAt = ref<string | null>(null)

  // 详情（按 sectionId 缓存）
  const detailMap = ref<Record<number, SectionHealthResponse | null>>({})
  const detailLoading = ref<Record<number, boolean>>({})

  // 历史曲线（按 sectionId 缓存）
  const historyMap = ref<Record<number, HealthHistoryResponse | null>>({})
  const historyLoading = ref<Record<number, boolean>>({})

  // 计算属性：按总分升序（越差越靠前），便于快速定位风险断面
  const sortedByScore = computed(() =>
    [...rankItems.value].sort((a, b) => a.total_score - b.total_score)
  )

  // 计算属性：按总分降序的"健康 Top 5"
  const topHealthy = computed(() =>
    [...rankItems.value]
      .sort((a, b) => b.total_score - a.total_score)
      .slice(0, 5)
  )

  // 计算属性：风险 Top 5（低分优先）
  const topRisk = computed(() =>
    sortedByScore.value.slice(0, 5)
  )

  // 拉取看板排名
  async function fetchRank(targetLine?: string) {
    if (targetLine) lineCode.value = targetLine
    rankLoading.value = true
    rankError.value = null
    try {
      const res = await api.getHealthRank(lineCode.value)
      rankItems.value = res.data || []
      gradeCount.value = res.grade_count || {
        excellent: 0,
        normal: 0,
        attention: 0,
        degraded: 0,
        danger: 0,
      }
      rankUpdatedAt.value = new Date().toISOString()
    } catch (e: any) {
      rankError.value = e?.message || '获取健康度排名失败'
      console.error('【健康度-看板】获取排名失败:', e)
    } finally {
      rankLoading.value = false
    }
  }

  // 拉取单断面最新健康度详情
  async function fetchDetail(sectionId: number) {
    detailLoading.value[sectionId] = true
    try {
      const res = await api.getSectionHealth(sectionId)
      detailMap.value[sectionId] = res
    } catch (e) {
      // 404 时清除缓存并提示
      detailMap.value[sectionId] = null
      console.error(`【健康度-详情】断面${sectionId}获取失败:`, e)
    } finally {
      detailLoading.value[sectionId] = false
    }
  }

  // 拉取单断面历史健康度曲线
  async function fetchHistory(
    sectionId: number,
    start: string,
    end: string,
    interval = '1 day'
  ) {
    historyLoading.value[sectionId] = true
    try {
      const res = await api.getSectionHealthHistory(sectionId, start, end, interval)
      historyMap.value[sectionId] = res
    } catch (e) {
      historyMap.value[sectionId] = null
      console.error(`【健康度-历史】断面${sectionId}获取失败:`, e)
    } finally {
      historyLoading.value[sectionId] = false
    }
  }

  // 手动触发重算（管理按钮）
  async function triggerRecompute(sectionId: number) {
    try {
      await api.recomputeSectionHealth(sectionId)
    } catch (e) {
      console.error(`【健康度-重算】断面${sectionId}触发失败:`, e)
      throw e
    }
  }

  // 清除详情缓存（便于测试重算后立刻刷新）
  function clearDetail(sectionId: number) {
    delete detailMap.value[sectionId]
    delete detailLoading.value[sectionId]
  }

  return {
    // state
    lineCode,
    rankItems,
    gradeCount,
    rankLoading,
    rankError,
    rankUpdatedAt,
    detailMap,
    detailLoading,
    historyMap,
    historyLoading,
    // getters
    sortedByScore,
    topHealthy,
    topRisk,
    // actions
    fetchRank,
    fetchDetail,
    fetchHistory,
    triggerRecompute,
    clearDetail,
  }
})

// 等级显示辅助（不放在 store 里以便组件直接 import）
export const HEALTH_GRADE_META: Record<
  HealthGrade,
  { label: string; color: string; bg: string; desc: string }
> = {
  excellent: { label: '优良', color: '#16a34a', bg: '#dcfce7', desc: '各项指标稳定，无风险' },
  normal:    { label: '正常', color: '#0ea5e9', bg: '#e0f2fe', desc: '在可控范围内' },
  attention: { label: '关注', color: '#eab308', bg: '#fef9c3', desc: '出现波动，建议加强巡检' },
  degraded:  { label: '劣化', color: '#f97316', bg: '#ffedd5', desc: '趋势恶化，需要处置' },
  danger:    { label: '危险', color: '#dc2626', bg: '#fee2e2', desc: '立即响应/紧急处置' },
}

export function gradeLabel(g: HealthGrade | string) {
  return HEALTH_GRADE_META[g as HealthGrade]?.label || g
}

export function gradeColor(g: HealthGrade | string) {
  return HEALTH_GRADE_META[g as HealthGrade]?.color || '#64748b'
}

export function gradeBg(g: HealthGrade | string) {
  return HEALTH_GRADE_META[g as HealthGrade]?.bg || '#f1f5f9'
}
