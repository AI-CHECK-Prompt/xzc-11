import { defineStore } from 'pinia'
import { ref } from 'vue'

// 当前操作者（运维账号）store
//
// 设计要点：
//   - 持久化：localStorage key='current_operator'，
//     浏览器关闭后重开仍保留上次填写的账号。
//   - 单一来源：所有需要"当前处理人"的页面统一从这里读取，
//     避免每个组件各自读 localStorage 出现不一致。
//   - 写入：用户在顶部"操作者"输入框修改即可全局生效。
//   - 默认值：首次访问时为空字符串 ''，触发 API 调用时由后端兜底为 'unknown'。
//
// 与后端契约：当前操作者通过 axios 拦截器统一写入 X-User 请求头，
// 后端 middleware 解析后写入 gin.Context，业务 handler 通过
// model.GetCurrentUser(c) 读取。
export const useUserStore = defineStore('user', () => {
  const STORAGE_KEY = 'current_operator'

  // 从 localStorage 读初始值（SSR/隐私模式下访问会抛错，try 兜底）
  const initial = (() => {
    try {
      return localStorage.getItem(STORAGE_KEY) || ''
    } catch {
      return ''
    }
  })()

  const currentOperator = ref<string>(initial)

  function setOperator(name: string) {
    const trimmed = (name || '').trim()
    currentOperator.value = trimmed
    try {
      if (trimmed) {
        localStorage.setItem(STORAGE_KEY, trimmed)
      } else {
        localStorage.removeItem(STORAGE_KEY)
      }
    } catch {
      // 隐私模式 / 存储满 都会写失败，对核心功能不影响
    }
  }

  return { currentOperator, setOperator }
})
