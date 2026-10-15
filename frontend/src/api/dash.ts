import { get, getText } from './http'
import type { DashSnapshot } from './types'

/**
 * 主数据口（契约 §1 唯一数据源）：GET /__dash/api
 * 可选参数 log_tail / acc_tail（默认后端 150/80）。
 *
 * timeout 放宽到 40s：后端快照有 15s 缓存，但冷缓存要跑十余条聚合查询，
 * DB 抖动时 25s 默认值会在"后端其实能返回"的情况下误判成网络中断。
 */
export const dashApi = {
  snapshot(logTail = 150, accTail = 80) {
    return get<DashSnapshot>('/__dash/api', { log_tail: logTail, acc_tail: accTail }, { timeout: 40_000 })
  },

  /**
   * 单包全量详情（列表 body 可能已被截断，nb/war 单条可达 MB 级）。
   * 后端已防目录穿越，前端只传端点目录名 + 文件名。
   */
  file(ep: string, name: string) {
    return getText('/__dash/file', { ep, name })
  },
}
