import { defineStore } from 'pinia'
import { ref } from 'vue'
import { authApi } from '@/api/auth'
import { bus, EVT } from '@/utils/bus'

/**
 * 会话状态。
 * 凭据本身在后端（HttpOnly Cookie），这里只保存"是否已通过校验"这一视图事实。
 * 任一受保护接口 401 → 总线事件 → 立即回落登录门（契约 §5：fetch 401 = showLogin）。
 */
export const useAuthStore = defineStore('auth', () => {
  const authed = ref(false)
  const checking = ref(true)
  const error = ref('')

  bus.on(EVT.UNAUTHORIZED, () => {
    if (authed.value) authed.value = false
  })

  async function check() {
    checking.value = true
    try {
      const me = await authApi.me()
      authed.value = me?.ok === true
    } catch {
      authed.value = false
    } finally {
      checking.value = false
    }
  }

  async function login(username: string, password: string): Promise<boolean> {
    error.value = ''
    try {
      const r = await authApi.login(username, password)
      if (r?.ok === true) {
        authed.value = true
        return true
      }
      error.value = r?.error || '登录失败'
      return false
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } })?.response?.status
      error.value = status === 401 ? '账号或口令不正确' : '登录失败：无法连接后端'
      return false
    }
  }

  async function logout() {
    try {
      await authApi.logout()
    } catch {
      /* 会话可能已过期，直接回落登录门即可 */
    }
    authed.value = false
  }

  return { authed, checking, error, check, login, logout }
})
