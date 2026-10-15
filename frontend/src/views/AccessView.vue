<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { panelApi } from '@/api/panel'
import type { VisitorRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { pctClass, shortIP, tsShort, uaLabel } from '@/utils/format'

/**
 * 访问设备：sync 信标（boot.html 入口）登记的访客台账（devices 表，按 ip+设备版本去重）。
 * 交付进度（阶段权重表）属于这个模块；展示为单一访问设备列表。
 *
 * 分页纪律（性能契约）：
 *  - 服务端分页，每页 20 条；筛选（kw）下推后端。
 *    翻页/筛选只拉一页，禁止一次性回传全量（旧版 visits(500) 全加载是页面卡顿主因）。
 *  - dash 轮询触发的自动刷新只重拉当前页；页码越界自动回退最后一页。
 */
const records = ref<VisitorRow[]>([])
/** 当前筛选条件下的总行数（分页总数） */
const totalRows = ref(0)
/** 去重来访数（全量口径，头部 chip） */
const totalVisits = ref(0)
const loading = ref(false)
const kw = ref('')
const page = ref(1)
const per = ref(20)

// 竞态序号：翻页/轮询并发时，过期响应直接丢弃
let loadSeq = 0
let activeLoad: AbortController | undefined

async function load() {
  if (loading.value) {
    activeLoad?.abort()
  }
  const my = ++loadSeq
  activeLoad?.abort()
  const controller = new AbortController()
  activeLoad = controller
  loading.value = true
  try {
    const v = await panelApi.visits(kw.value.trim(), page.value, per.value, controller.signal)
    if (my !== loadSeq) return
    records.value = v?.records ?? []
    totalRows.value = Number(v?.total ?? 0)
    totalVisits.value = Number(v?.total_visits ?? v?.total ?? 0)
    // 删除/筛选后页码越界：回退最后一页（page watcher 触发重拉）
    const maxPage = Math.max(1, Math.ceil(totalRows.value / per.value))
    if (page.value > maxPage) page.value = maxPage
  } catch (error: unknown) {
    if (my === loadSeq && (error as { code?: string })?.code !== 'ERR_CANCELED') {
      records.value = []
    }
  } finally {
    if (my === loadSeq) {
      loading.value = false
      activeLoad = undefined
    }
  }
}

let refreshTimer: number | undefined

onMounted(() => {
  void load()
  refreshTimer = window.setInterval(() => {
    if (!document.hidden && !loading.value) void load()
  }, 15000)
})
onUnmounted(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
  activeLoad?.abort()
  activeLoad = undefined
})

// 筛选变化（300ms 防抖）：回第 1 页。page 未变时显式重拉，避免漏触发。
let kwTimer: number | undefined
watch(kw, () => {
  window.clearTimeout(kwTimer)
  kwTimer = window.setTimeout(() => {
    if (page.value === 1) void load()
    else page.value = 1 // [page] watcher 触发 load
  }, 300)
})

// 翻页 / 每页条数：ref 变化统一触发（Vue 同 tick 批处理，不会双发）
watch([page, per], () => {
  void load()
})

const progressMap = computed(() => {
  const m = new Map<string, { pct: number; stage: string }>()
  for (const row of records.value) {
    const ip = String(row.ip || '')
    if (!ip) continue
    m.set(ip, { pct: Number(row.progress ?? 0), stage: String(row.stage ?? '') })
  }
  return m
})

const withProgress = computed(() => progressMap.value.size)
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><User /></el-icon>访问设备
        <span class="chip">去重来访 {{ totalVisits }}</span>
        <span class="chip">共 {{ totalRows }} 条</span>
        <span class="chip chip--ok">有交付进度 {{ withProgress }}</span>
      </h3>
      <div class="row row--wrap">
        <el-input v-model="kw" size="small" placeholder="筛选 IP / 版本 / 域 / Host / UA" clearable
          style="width: 280px" />
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="records" size="small" height="calc(100vh - 285px)" empty-text="…">
        <el-table-column prop="ip" label="IP" width="170">
          <template #default="{ row }"><span class="mono ellipsis" :title="row.ip">{{ shortIP(row.ip) }}</span></template>
        </el-table-column>
        <el-table-column label="设备" width="140">
          <template #default="{ row }"><span class="chip chip--brand">{{ uaLabel(String(row.ua)) }}</span></template>
        </el-table-column>
        <el-table-column label="版本" width="130">
          <template #default="{ row }"><span class="ellipsis">{{ row.device_version || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="交付进度" min-width="240">
          <template #default="{ row }">
            <div v-if="progressMap.get(String(row.ip))" class="prog">
              <div class="bar">
                <div
                  class="bar__fill"
                  :class="pctClass(progressMap.get(String(row.ip))?.pct)"
                  :style="{ width: (progressMap.get(String(row.ip))?.pct || 0) + '%' }"
                />
              </div>
              <span class="pct">{{ progressMap.get(String(row.ip))?.pct || 0 }}%</span>
              <span class="chip">{{ progressMap.get(String(row.ip))?.stage || '-' }}</span>
            </div>
            <span v-else class="t-3">—</span>
          </template>
        </el-table-column>
        <el-table-column prop="domain" label="域" min-width="200">
          <template #default="{ row }"><span class="mono t-3 ellipsis">{{ row.domain || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="host" label="Host" min-width="160">
          <template #default="{ row }"><span class="mono t-3 ellipsis">{{ row.host || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="ts" label="最后来访" width="170">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(String(row.ts)) }}</span></template>
        </el-table-column>
        <template #empty><EmptyState text="暂无访问设备（boot.html 入口尚未有来访）" /></template>
      </el-table>

      <div class="pager">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="per"
          :total="totalRows"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          background
          size="small"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.prog {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
}
.prog .bar {
  flex: 1;
  min-width: 90px;
  height: 8px;
}
.pct {
  min-width: 40px;
  text-align: right;
  font-size: var(--fs-sm);
  color: var(--text-2);
}
/* 分页条：右侧对齐，与表格分离 */
.pager {
  display: flex;
  justify-content: flex-end;
  padding: 8px 12px;
}
</style>
