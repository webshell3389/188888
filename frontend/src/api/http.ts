import axios, { type AxiosRequestConfig } from 'axios'
import { bus, EVT } from '@/utils/bus'

/**
 * 统一 HTTP 层。
 *
 * - 默认同源相对地址（baseURL=''）：prod 由后端直接托管静态资源，零 CORS 问题。
 * - 可在「设置 → 后端入口」人工覆盖后端地址（持久化 localStorage），
 *   用于前端独立部署 / 换端口 / 临时指向演练实例。
 * - 凭据：会话 Cookie（HttpOnly, SameSite=Lax）由浏览器自动携带，前端不碰 token。
 * - 契约红线：只发起 /__dash /__q /__auth /__control /__stats /__metrics /panel /api 前缀；
 *   回传端点（/beacon /war /p /result /event /u /nb /ph /a）**永不**由此层发起。
 */

const API_BASE_KEY = 'mockc2.api_base'

function readApiBase(): string {
  try {
    return localStorage.getItem(API_BASE_KEY) || ''
  } catch {
    return ''
  }
}

const USE_MOCK = import.meta.env.VITE_USE_MOCK === '1'

const http = axios.create({
  baseURL: readApiBase(),
  timeout: 35_000, // 重型聚合接口（设备台账等）后端 SWR 重算偶发 20s+，25s 会误判成网络中断
  withCredentials: true,
  headers: { Accept: 'application/json' },
})

/** 当前生效的后端地址（''=同源） */
export function getApiBase(): string {
  return http.defaults.baseURL || ''
}

/** 人工设置后端入口地址；传空串恢复同源。即时生效并持久化。 */
export function setApiBase(url: string): void {
  const v = (url || '').trim().replace(/\/+$/, '')
  http.defaults.baseURL = v
  try {
    if (v) localStorage.setItem(API_BASE_KEY, v)
    else localStorage.removeItem(API_BASE_KEY)
  } catch {
    /* localStorage 不可用时仅当前会话生效 */
  }
}

/** 受保护前缀：与后端 protectedPrefixes 镜像，命中即 401 弹登录门 */
const GUARDED = [
  '/__dash', '/__q', '/__control', '/__metrics',
  '/panel', '/api/data', '/api/file', '/api/stats',
]

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

/**
 * 后端存活探测：GET 静态根 `/`（纯静态、不走 DB）。
 * 用于网络失败时区分「后端进程活着但接口卡（多半是 DB）」vs「后端不可达」。
 */
export async function probeBackend(): Promise<boolean> {
  const base = getApiBase() || location.origin
  try {
    const ctl = new AbortController()
    const timer = window.setTimeout(() => ctl.abort(), 2500)
    await fetch(`${base}/`, { signal: ctl.signal, cache: 'no-store' })
    window.clearTimeout(timer)
    return true
  } catch {
    return false
  }
}

/** 网络层失败细分文案：超时（后端在但慢/卡 DB）vs 连接失败（服务未启动/端口不通） */
export function describeNetworkError(err: unknown): string {
  const e = err as { code?: string; config?: { timeout?: number } } | undefined
  const code = e?.code || ''
  if (code === 'ECONNABORTED') {
    const sec = Math.round((e?.config?.timeout || 25_000) / 1000)
    return `后端接口超时（>${sec}s 未返回）—— 后端在运行，大概率卡在数据库，请检查 DB 连接`
  }
  return '网络中断或服务未启动'
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
