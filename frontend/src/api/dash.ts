import { get, getText } from './http'
import type { DashSnapshot } from './types'

/**
 * 主数据口（契约 §1 唯一数据源）：GET /__dash/api
 * 可选参数 log_tail / acc_tail（默认后端 150/80）。
 */
export const dashApi = {
  snapshot(logTail = 150, accTail = 80) {
    return get<DashSnapshot>('/__dash/api', { log_tail: logTail, acc_tail: accTail })
  },

  /**
   * 单包全量详情（列表 body 可能已被截断，nb/war 单条可达 MB 级）。
   * 后端已防目录穿越，前端只传端点目录名 + 文件名。
   */
  file(ep: string, name: string) {
    return getText('/__dash/file', { ep, name })
  },
}
