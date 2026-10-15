<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { panelApi } from '@/api/panel'
import type { Bip39Row, NoteRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort } from '@/utils/format'
import { ElMessage } from 'element-plus'

/**
 * 备忘录：默认只显示「含 BIP39 助记词」的条目（读后端命中记录表 memo_bip39），
 * 避免把 6~7 万条全部堆出来；可切到「全部」查看原始备忘录。
 * 助记词后端做批量模糊匹配 + BIP39 校验，前端只负责展示与筛选。
 */
const mode = ref<'bip39' | 'all'>('bip39')

// ── 助记词命中 ──
const bRows = ref<Bip39Row[]>([])
const bTotal = ref(0)
const bPage = ref(1)
const bPer = ref(50)
const bLoading = ref(false)
const onlyValid = ref(false)
const scanning = ref(false)

async function loadBip39() {
  bLoading.value = true
  try {
    const r = await panelApi.memosBip39(bPage.value, bPer.value, onlyValid.value)
    bRows.value = r?.rows ?? []
    bTotal.value = Number(r?.total ?? 0)
  } catch {
    bRows.value = []
    bTotal.value = 0
  } finally {
    bLoading.value = false
  }
}

async function scan() {
  scanning.value = true
  try {
    const st = await panelApi.memosBip39Scan()
    ElMessage.info(st?.running ? '已有扫描在进行，等待完成…' : '扫描已启动（后台进行，不影响其它操作）')
    // 轮询到扫描结束（最多 5 分钟）
    for (let i = 0; i < 300; i++) {
      await new Promise((r) => setTimeout(r, 1000))
      const s = await panelApi.memosBip39ScanStatus()
      if (!s?.running) break
    }
    await loadBip39()
    ElMessage.success('扫描完成')
  } catch {
    ElMessage.error('扫描失败')
  } finally {
    scanning.value = false
  }
}

function onBPage(p: number) {
  bPage.value = p
  void loadBip39()
}
function onBSize(s: number) {
  bPer.value = s
  bPage.value = 1
  void loadBip39()
}

// ── 全部备忘录 ──
const rows = ref<NoteRow[]>([])
const total = ref(0)
const page = ref(1)
const per = ref(50)
const kw = ref('')
const loading = ref(false)

async function loadAll() {
  loading.value = true
  try {
    const r = await panelApi.notes(kw.value.trim(), page.value, per.value)
    rows.value = r?.rows ?? []
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
  void loadAll()
}
function onPage(p: number) {
  page.value = p
  void loadAll()
}
function onSize(s: number) {
  per.value = s
  page.value = 1
  void loadAll()
}

function switchMode(m: 'bip39' | 'all') {
  mode.value = m
  if (m === 'bip39') void loadBip39()
  else void loadAll()
}

onMounted(loadBip39)

// 抽屉（全部模式用）
const drawer = ref(false)
const cur = ref<NoteRow | null>(null)
function open(row: NoteRow) {
  cur.value = row
  drawer.value = true
}
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Key /></el-icon>助记词 / 备忘录
        <span class="chip" v-if="mode === 'bip39'">{{ bTotal }} 命中</span>
        <span class="chip" v-else>{{ total }} 条</span>
      </h3>
      <div class="row row--wrap">
        <el-radio-group :model-value="mode" size="small" @change="(v: any) => switchMode(v)">
          <el-radio-button value="bip39">助记词命中</el-radio-button>
          <el-radio-button value="all">全部备忘录</el-radio-button>
        </el-radio-group>

        <template v-if="mode === 'bip39'">
          <el-checkbox v-model="onlyValid" size="small" @change="() => { bPage = 1; loadBip39() }">
            仅校验通过
          </el-checkbox>
          <el-button size="small" :loading="bLoading" @click="loadBip39">刷新</el-button>
          <el-button size="small" type="primary" :loading="scanning" @click="scan">
            <el-icon><Search /></el-icon>
            <span style="margin-left: 4px">重新扫描</span>
          </el-button>
        </template>

        <template v-else>
          <el-input v-model="kw" size="small" placeholder="搜索标题 / 摘要 / 设备" clearable style="width: 240px"
            @keyup.enter="search" @clear="search" />
          <el-button size="small" type="primary" @click="search">搜索</el-button>
          <el-button size="small" :loading="loading" @click="loadAll">刷新</el-button>
        </template>
      </div>
    </div>

    <!-- 助记词命中 -->
    <div v-if="mode === 'bip39'" class="app-card__body app-card__body--flush">
      <el-table :data="bRows" size="small" height="calc(100vh - 310px)" empty-text="…">
        <el-table-column label="校验" width="80" align="center">
          <template #default="{ row }">
            <span class="chip" :class="row.valid ? 'chip--ok' : 'chip--warn'">{{ row.valid ? '通过' : '未过' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="助记词（匹配/纠正后）" min-width="360">
          <template #default="{ row }">
            <el-tooltip placement="top" :content="row.raw || ''" :disabled="!row.raw">
              <span class="mono phrase">{{ row.phrase || '-' }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="词数" prop="words" width="70" align="center" />
        <el-table-column label="纠正 / 噪声" width="110" align="center">
          <template #default="{ row }">
            <span class="t-3">{{ row.corrected ?? 0 }} / {{ row.unknown ?? 0 }}</span>
          </template>
        </el-table-column>
        <el-table-column label="校验说明" prop="reason" min-width="120">
          <template #default="{ row }"><span class="t-3">{{ row.reason || '—' }}</span></template>
        </el-table-column>
        <el-table-column prop="uuid" label="设备" width="170">
          <template #default="{ row }"><span class="mono t-3">{{ row.uuid || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="来源" width="80" align="center">
          <template #default="{ row }"><span class="chip">{{ row.field === 'title' ? '标题' : '正文' }}</span></template>
        </el-table-column>
        <template #empty><EmptyState text="暂无命中（点「重新扫描」或 wait 启动扫描完成）" /></template>
      </el-table>
    </div>

    <!-- 全部备忘录 -->
    <div v-else class="app-card__body app-card__body--flush">
      <el-table :data="rows" size="small" height="calc(100vh - 310px)" empty-text="…" @row-click="open">
        <el-table-column label="标题" min-width="240">
          <template #default="{ row }"><span class="ellipsis">{{ row.title || '(无标题)' }}</span></template>
        </el-table-column>
        <el-table-column label="摘要" min-width="320">
          <template #default="{ row }"><span class="t-2 ellipsis">{{ row.snippet || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="uuid" label="设备" width="180">
          <template #default="{ row }"><span class="mono t-3">{{ row.uuid || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="recv_ts" label="时间" width="170">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.recv_ts) }}</span></template>
        </el-table-column>
        <template #empty><EmptyState text="暂无备忘录（nb 端点尚无回传）" /></template>
      </el-table>
    </div>

    <div v-if="mode === 'bip39'" class="pager">
      <el-pagination background layout="total, sizes, prev, pager, next, jumper" :total="bTotal"
        :current-page="bPage" :page-size="bPer" :page-sizes="[20, 50, 100, 200]" @current-change="onBPage"
        @size-change="onBSize" />
    </div>
    <div v-else class="pager">
      <el-pagination background layout="total, sizes, prev, pager, next, jumper" :total="total"
        :current-page="page" :page-size="per" :page-sizes="[20, 50, 100, 200]" @current-change="onPage"
        @size-change="onSize" />
    </div>

    <el-drawer v-model="drawer" :title="cur?.title || '备忘录'" size="48%">
      <div class="body">
        <div class="meta">
          <span class="chip">设备 {{ cur?.uuid || '-' }}</span>
          <span class="chip">{{ tsShort(cur?.recv_ts) }}</span>
        </div>
        <pre class="pre">{{ cur?.snippet || '(空)' }}</pre>
      </div>
    </el-drawer>
  </section>
</template>

<style scoped>
.body {
  padding: var(--sp-4) var(--sp-5);
}
.meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
  margin-bottom: var(--sp-4);
}
.pager {
  display: flex;
  justify-content: flex-end;
  padding: var(--sp-3) var(--sp-4);
}
.phrase {
  word-break: break-all;
  line-height: 1.5;
}
</style>
