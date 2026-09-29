import { get } from './http'

/**
 * Go 后端 /api/* 兼容层：入库解析后的**业务实体**视图（不是 capture 原始端点分组）。
 *
 * 这层是"入库解析"语义的落点：
 *   /api/data/memos    → packet_meta 解析 nb 回传 → 备忘录实体
 *   /api/data/photos   → packets(endpoint='ph')  → 照片实体（url 走 /api/file/{packet_id}）
 *   /api/data/wallets  → war keychain items      → 钱包/钥匙串实体（按设备分组）
 *   /api/activity      → 各端点语义化活动流
 *   /api/list?type=    → visitor / managed 设备清单
 */

export interface FePhoto {
  filename?: string
  url?: string
  size?: number
  device_uuid?: string
  ts?: string
}

export interface FeMemo {
  title?: string
  device_uuid?: string
  filename?: string
  locked?: boolean
  body?: string
  ts?: string
}

export interface FeWalletEntry {
  account?: string
  service?: string
  access_group?: string
  label?: string
  data_hex?: string
  data_hex_full?: string
  status?: string // ok(可解密) / enc
  rowid?: number | string
}

export interface FeWallet {
  wallet_key?: string
  display_name?: string
  count?: number
  entries?: FeWalletEntry[]
}

export interface FeWalletDevice {
  device_uuid?: string
  wallets?: FeWallet[]
}

export interface FeActivity {
  stage?: string
  device_uuid?: string
  label?: string
  ts?: string
}

export interface FeTask {
  task_id?: string
  device_uuid?: string
  cmd_type?: string
  status?: string // PENDING / COMPLETED
  created_at?: string | null
}

export interface FeStats {
  total?: number
  managed?: number
  online?: number
  visitors?: number
  tasks?: number
  tasks_done?: number
  tasks_pending?: number
}

export interface FeDevice {
  uuid?: string
  ip?: string
  [k: string]: unknown
}

export const feApi = {
  stats() {
    return get<FeStats>('/api/stats')
  },
  activity(limit = 20) {
    return get<{ items?: FeActivity[] }>('/api/activity', { limit })
  },
  list(type: 'visitor' | 'managed' = 'visitor') {
    return get<{ items?: FeDevice[] }>('/api/list', { type })
  },
  photos(limit = 60) {
    return get<{ items?: FePhoto[] }>('/api/data/photos', { limit })
  },
  memos(limit = 60) {
    return get<{ items?: FeMemo[] }>('/api/data/memos', { limit })
  },
  wallets(limit = 30) {
    return get<{ items?: FeWalletDevice[] }>('/api/data/wallets', { limit })
  },
  tasks(limit = 100) {
    return get<{ items?: FeTask[] }>('/api/tasks', { limit })
  },
  device(uuid: string) {
    return get<Record<string, unknown>>('/api/device/' + encodeURIComponent(uuid))
  },
  /** 原始包字节流（照片预览/下载）：/api/file/{packet_id} */
  fileUrl(packetId: number | string) {
    return `/api/file/${encodeURIComponent(String(packetId))}`
  },
}
