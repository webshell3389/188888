import { get, post } from './http'
import type { ControlPeek } from './types'

/**
 * 指令控制台（契约 §6）+
 * 按设备独立队列（Go v7+ device_commands 表）：批量异步下发，单台不阻塞全局。
 *
 * 前端角色只有三个：武装 / 清空 / peek。
 * 指令的"分发"由各设备自己的 beacon 领走 —— 前端绝不直接调 /beacon（污染交付链与台账）。
 *
 * 能力降级：老后端（Python / Go v6-）没有 /__q/device_command_targets，
 * 前端探测 404 后回落"全局单队列"语义，并在 UI 明示。
 */
export const controlApi = {
  peek() {
    return get<ControlPeek>('/__control')
  },
  /** 全局队列（老语义）：下一个到达的任意 beacon 领走 */
  arm(type: string, params: Record<string, unknown> = {}, once = true) {
    return post<unknown>('/__control', { type, params, once })
  },
  /**
   * 批量按设备武装：每台各写一条独立队列记录（异步，互不阻塞）。
   * 后端 v7+ 支持；老后端会忽略 targets 退化为全局队列。
   */
  armTargets(targets: string[], type: string, params: Record<string, unknown> = {}, once = true) {
    return post<{ ok?: boolean; batch_id?: string; count?: number; commands?: unknown[] }>('/__control', {
      type,
      params,
      once,
      targets,
    })
  },
  clear() {
    return post<unknown>('/__control', { command: null })
  },
  /** 只取消某台设备的待发指令，不影响其它设备 */
  clearFor(target: string) {
    return post<{ ok?: boolean; cleared_target?: string }>('/__control', { command: null, target })
  },
}
