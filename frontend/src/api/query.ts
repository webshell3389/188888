import { get } from './http'
import type { CommandRow } from './types'

/**
 * 只读查询面 /__q/*（收敛后仅保留指令队列相关端点，供「设备控制台」使用）。
 *
 * 其余 /__q/* 历史视图（devices / timeline / packets / packet / meta / stats /
 * wallets / wallets_rules / wallet_flags / apps / keychain_items / keychain_devices /
 * memos / result_data / profiles / full_stats）已随前端全量迁移 Vue 下线，
 * 后端同步移除；对应数据由 /__dash/api、/panel/api/* 单一来源承载。
 */
export const queryApi = {
  commands(limit = 30) {
    return get<CommandRow[]>('/__q/commands', { limit })
  },

  // ── 按设备独立指令队列（Go v7+）──
  /** 各设备队列总览：{target, armed, dispatched, total, last_armed_ts} */
  deviceCommandTargets() {
    return get<DeviceQueueTarget[]>('/__q/device_command_targets')
  },
  /** 单台设备队列明细 */
  deviceCommands(target: string, limit = 100) {
    return get<DeviceCommandRow[]>('/__q/device_commands', { target, limit })
  },
}

export interface DeviceQueueTarget {
  target?: string
  armed?: number
  dispatched?: number
  total?: number
  last_armed_ts?: string
}

export interface DeviceCommandRow {
  id?: number
  target?: string
  command_id?: string
  cmd_type?: string
  params?: unknown
  once?: boolean
  state?: string // armed | dispatched | cancelled
  armed_ts?: string
  dispatched_ts?: string | null
  client_ip?: string | null
  batch_id?: string | null
}
