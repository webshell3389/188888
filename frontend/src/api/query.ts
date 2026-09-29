import { get } from './http'
import type { CommandRow, DeviceRow } from './types'

/**
 * 只读查询面 /__q/*（SQL 直读 DB）：主接口解析不可靠或需要精确检索时使用。
 */
export const queryApi = {
  devices() {
    return get<DeviceRow[] | { rows?: DeviceRow[] }>('/__q/devices')
  },
  timeline(ip: string, limit = 200) {
    return get<unknown[]>('/__q/timeline/' + encodeURIComponent(ip), { limit })
  },
  packets(endpoint: string, limit = 200, sinceEpoch?: number) {
    return get<unknown[]>('/__q/packets/' + encodeURIComponent(endpoint), {
      limit,
      since_epoch: sinceEpoch,
    })
  },
  packet(packetId: number | string) {
    return get<Record<string, unknown>>('/__q/packet/' + encodeURIComponent(String(packetId)))
  },
  meta(field: string, valueLike = '', limit = 200) {
    return get<unknown[]>('/__q/meta', { field, value_like: valueLike, limit })
  },
  commands(limit = 30) {
    return get<CommandRow[]>('/__q/commands', { limit })
  },
  stats() {
    return get<Record<string, unknown>>('/__q/stats')
  },
  wallets(limit = 20) {
    return get<unknown[]>('/__q/wallets', { limit })
  },
  walletRules() {
    return get<Array<{ rule_name?: string; patterns?: string }>>('/__q/wallets_rules')
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
