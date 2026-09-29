<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { panelApi } from '@/api/panel'
import { useDashStore } from '@/stores/dash'
import type { VisitorRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { pctClass, shortIP, tsShort, uaLabel } from '@/utils/format'

/**
 * 访问设备：sync 信标（boot.html 入口）登记的访客台账（devices 表，按 ip+设备版本去重）。
 * 交付进度（阶段权重表）属于这个模块；展示为单一访问设备列表。
 */
const dash = useDashStore()

const records = ref<VisitorRow[]>([])
const total = ref(0)
const loading = ref(false)
const kw = ref('')
const progressRows = ref<Array<Record<string, unknown>>>([])

async function load() {
  loading.value = true
  try {
    const [v, p] = await Promise.all([panelApi.visits(500), panelApi.progress()])
    records.value = v?.records ?? []
    total.value = Number(v?.total ?? 0)
    progressRows.value = Array.isArray(p?.rows) ? p.rows : []
  } catch {
    records.value = []
    progressRows.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})
watch(
  () => dash.snap.ts,
  () => {
    if (dash.snap.ts) void load()
  },
)

const progressMap = computed(() => {
  const m = new Map<string, { pct: number; stage: string }>()
  for (const r of progressRows.value) {
    const ip = String(r.client ?? r.ip ?? '')
    if (!ip) continue
    m.set(ip, { pct: Number(r.percent ?? 0), stage: String(r.stage ?? '') })
  }
  return m
})

const filtered = computed(() => {
  const k = kw.value.trim().toLowerCase()
  if (!k) return records.value
  return records.value.filter((d) =>
    [d.ip, d.device_version, d.domain, d.host, d.ua]
      .map((x) => String(x ?? '').toLowerCase())
      .some((x) => x.includes(k)),
  )
})

const withProgress = computed(() => progressMap.value.size)
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><User /></el-icon>访问设备
        <span class="chip">去重来访 {{ total }}</span>
        <span class="chip chip--ok">有交付进度 {{ withProgress }}</span>
      </h3>
      <div class="row row--wrap">
        <el-input v-model="kw" size="small" placeholder="筛选 IP / 版本 / 域 / Host / UA" clearable
          style="width: 280px" />
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="filtered" size="small" height="calc(100vh - 230px)" empty-text="…">
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
</style>
