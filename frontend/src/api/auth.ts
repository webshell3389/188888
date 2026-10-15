import { get, post } from './http'
import type { AuthMe } from './types'

/**
 * 会话：口令登录 → 后端下发 HttpOnly Cookie（无状态 HMAC 签名 token，12h 滑动）。
 * 前端不保存任何凭据，只维护"是否已登录"的视图状态。
 */

export const authApi = {
  /** 账号 + 口令登录（username 缺省由后端按配置账号处理） */
  login(username: string, password: string) {
    return post<{ ok?: boolean; error?: string }>('/__auth/login', { username, password })
  },
  logout() {
    return post<{ ok?: boolean }>('/__auth/logout')
  },
  me() {
    return get<AuthMe>('/__auth/me')
  },
}
