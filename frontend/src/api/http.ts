import axios, { type AxiosRequestConfig } from 'axios'
import { bus, EVT } from '@/utils/bus'

/**
 * 统一 HTTP 层。
 *
 * - 同源相对地址：dev 由 Vite proxy 转发到后端，prod 由后端直接托管静态资源 → 零 CORS 问题。
 * - 凭据：会话 Cookie（HttpOnly, SameSite=Lax）由浏览器自动携带，前端不碰 token。
 * - 契约红线：只允许 /__dash、/__q、/__auth、/__control、/panel 前缀（见 vite.config.ts 代理表）；
 *   回传端点（/beacon /war /p /result /event /u /nb /ph /a）**永不**由此层发起。
 */

const USE_MOCK = import.meta.env.VITE_USE_MOCK === '1'

const http = axios.create({
  baseURL: '',
  timeout: 25_000,
  withCredentials: true,
  headers: { Accept: 'application/json' },
})

/** 受保护前缀：命中即认为需要会话（401 时弹登录门） */
const GUARDED = ['/__dash', '/__q', '/__control', '/panel', '/__stats']

const isGuarded = (url = '') => GUARDED.some((p) => url.startsWith(p))

http.interceptors.response.use(
  (res) => res,
  (err) => {
    const status = err?.response?.status as number | undefined
    const url = err?.config?.url || ''

    if (status === 401 && isGuarded(url)) {
      bus.emit(EVT.UNAUTHORIZED, { url })
    } else if (status && status >= 500) {
      bus.emit(EVT.SERVER_ERROR, { url, status })
    } else if (!status) {
      bus.emit(EVT.NETWORK_ERROR, { url, message: err?.message })
    }
    return Promise.reject(err)
  },
)

/** 单次请求取消令牌（表格切换/抽屉关闭时避免竞态覆盖） */
export function newAbort() {
  return new AbortController()
}

export async function get<T>(url: string, params?: Record<string, unknown>, config?: AxiosRequestConfig): Promise<T> {
  if (USE_MOCK) return (await mockResolve(url, params)) as T
  const res = await http.get<T>(url, { params, ...config })
  return res.data
}

export async function getText(url: string, params?: Record<string, unknown>): Promise<string> {
  if (USE_MOCK) return String((await mockResolve(url, params)) ?? '')
  const res = await http.get<string>(url, { params, transformResponse: [(d) => d] })
  return res.data
}

export async function post<T>(url: string, body?: unknown, config?: AxiosRequestConfig): Promise<T> {
  const res = await http.post<T>(url, body, {
    headers: { 'Content-Type': 'application/json' },
    ...config,
  })
  return res.data
}

/** 触发浏览器下载（走后端 /panel/download，不经前端代理样本路径） */
export function downloadUrl(ep: string, name: string) {
  return `/panel/download?ep=${encodeURIComponent(ep)}&name=${encodeURIComponent(name)}`
}

// ── 离线 mock（VITE_USE_MOCK=1，仅用于无后端时的 UI 联调） ──────────────
async function mockResolve(url: string, params?: Record<string, unknown>): Promise<unknown> {
  const { buildMockSnapshot } = await import('./mock')
  const snap = buildMockSnapshot()
  switch (url) {
    case '/__dash/api':
      return snap
    case '/__auth/me':
      return { ok: true, user: 'mock' }
    case '/__q/commands':
      return []
    case '/panel/api/commands':
      return { current: {}, history: [] }
    case '/panel/api/progress':
      return { checkpoints: [], rows: [] }
    case '/__dash/file':
      return JSON.stringify({ mock: true, params }, null, 2)
    default:
      if (url.startsWith('/panel/api/')) return { rows: [], total: 0, page: 1 }
      return {}
  }
}

export default http
