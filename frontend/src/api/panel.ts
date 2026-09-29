import { get, post } from './http'
import type {
  AppRow,
  CommandState,
  KeychainDevice,
  KeychainItemRow,
  KeychainWalletStat,
  ManagedDevice,
  NoteRow,
  Paged,
  ProgressResp,
  ResultDetail,
  ResultRow,
  StatsResp,
  VisitorRow,
  WarRow,
} from './types'

/**
 * 运维面板 /panel/api/* —— DB 聚合视图（设备台账 / 资产钻取 / 战果 / 备忘录 / 访问审计）。
 * 全部只读（除 POST /panel/api/commands，等价 /__control 武装）。
 */
export const panelApi = {
  overview() {
    return get<StatsResp>('/panel/api/overview')
  },
  devices() {
    return get<{ devices?: ManagedDevice[] }>('/panel/api/devices')
  },
  progress() {
    return get<ProgressResp>('/panel/api/progress')
  },
  deviceAssets(uuid: string) {
    return get<Record<string, unknown>>('/panel/api/device/' + encodeURIComponent(uuid) + '/assets')
  },
  apps(kw = '', page = 1, per = 40) {
    return get<Paged<AppRow>>('/panel/api/apps', { kw, page, per })
  },
  war(kw = '', page = 1, per = 40) {
    return get<Paged<WarRow>>('/panel/api/war', { kw, page, per })
  },
  notes(kw = '', page = 1, per = 40) {
    return get<Paged<NoteRow>>('/panel/api/notes', { kw, page, per })
  },
  /** keychain 钱包命中条目：device 限定设备、wallet 限定钱包规则 */
  keychain(kw = '', tbl = '', wallet = '', page = 1, per = 40, device = '') {
    return get<Paged<KeychainItemRow>>('/panel/api/war', { kw, tbl, wallet, page, per, device })
  },
  keychainDevices() {
    return get<{ devices?: KeychainDevice[] }>('/panel/api/keychain/devices')
  },
  keychainItem(id: number | string) {
    return get<KeychainItemRow>('/panel/api/keychain/item/' + encodeURIComponent(String(id)))
  },
  keychainWallets() {
    return get<{ wallets?: KeychainWalletStat[] }>('/panel/api/keychain/wallets')
  },
  results(kw = '', page = 1, per = 40) {
    return get<Paged<ResultRow>>('/panel/api/results', { kw, page, per })
  },
  resultDetail(id: number | string) {
    return get<ResultDetail>('/panel/api/result/' + encodeURIComponent(String(id)))
  },
  profiles() {
    return get<{ profiles?: Array<Record<string, unknown>> }>('/panel/api/profiles')
  },
  commands() {
    return get<CommandState>('/panel/api/commands')
  },
  armCommand(payload: { type: string; params?: unknown; once?: boolean; command_id?: string }) {
    return post<{ ok?: boolean; command?: unknown; current?: Record<string, unknown> }>(
      '/panel/api/commands',
      payload,
    )
  },
  access(n = 120) {
    return get<{ rows?: Array<Record<string, unknown>> }>('/panel/api/access', { n })
  },
  visits(n = 200) {
    return get<{ records?: VisitorRow[]; total?: number }>('/panel/api/visits', { n })
  },
}
