import { get } from './http'

/**
 * Go 后端 /api/* 兼容层（收敛后）。
 *
 * 仅保留 Vue 控制台依赖的两个业务实体视图：
 *   /api/stats       → 仪表台 KPI（DashboardView）
 *   /api/data/photos → 照片实体（url 走 /api/file/{packet_id}，PhotosView）
 *
 * 其余 /api/* 历史端点（activity / list / tasks / device / logout / dispatch /
 * data/memos / data/wallets）已随前端全量迁移 Vue 下线，后端同步移除。
 */

export interface FePhoto {
  filename?: string
  url?: string
  size?: number
  device_uuid?: string
  ts?: string
}

export interface FeStats {
  total?: number
  managed?: number
  online?: number
  visitors?: number
  visitors_today?: number
  tasks?: number
  tasks_done?: number
  tasks_pending?: number
}

export const feApi = {
  stats() {
    return get<FeStats>('/api/stats')
  },
  photos(limit = 60) {
    return get<{ items?: FePhoto[] }>('/api/data/photos', { limit })
  },
}
