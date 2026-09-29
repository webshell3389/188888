<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { feApi, type FeMemo } from '@/api/feapi'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort } from '@/utils/format'

/** 备忘录：nb 回传经 packet_meta 解析入库后的实体（标题/摘要/所属设备）。 */
const items = ref<FeMemo[]>([])
const loading = ref(false)
const kw = ref('')

async function load() {
  loading.value = true
  try {
    const r = await feApi.memos(200)
    items.value = r?.items ?? []
  } catch {
    items.value = []
  } finally {
    loading.value = false
  }
}
onMounted(load)

const filtered = computed(() => {
  const k = kw.value.trim().toLowerCase()
  if (!k) return items.value
  return items.value.filter((m) =>
    [m.title, m.body, m.device_uuid, m.filename].some((x) => String(x ?? '').toLowerCase().includes(k)),
  )
})

const drawer = ref(false)
const cur = ref<FeMemo | null>(null)
function open(row: FeMemo) {
  cur.value = row
  drawer.value = true
}
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Notebook /></el-icon>备忘录
        <span class="chip">{{ filtered.length }} / {{ items.length }}</span>
      </h3>
      <div class="row">
        <el-input v-model="kw" size="small" placeholder="过滤标题 / 正文 / 设备" clearable style="width: 260px" />
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="filtered" size="small" height="calc(100vh - 250px)" empty-text="…" @row-click="open">
        <el-table-column label="标题" min-width="240">
          <template #default="{ row }">
            <span class="ellipsis">{{ row.title || '(无标题)' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="摘要" min-width="320">
          <template #default="{ row }">
            <span class="t-2 ellipsis">{{ row.body || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="device_uuid" label="设备" width="180">
          <template #default="{ row }"><span class="mono t-3">{{ row.device_uuid || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="filename" label="来源包" width="220">
          <template #default="{ row }"><span class="mono t-3 ellipsis">{{ row.filename || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="ts" label="时间" width="160">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.ts) }}</span></template>
        </el-table-column>
        <template #empty><EmptyState text="暂无备忘录（nb 端点尚无回传）" /></template>
      </el-table>
    </div>

    <el-drawer v-model="drawer" :title="cur?.title || '备忘录'" size="48%">
      <div class="body">
        <div class="meta">
          <span class="chip">设备 {{ cur?.device_uuid || '-' }}</span>
          <span class="chip mono">{{ cur?.filename || '-' }}</span>
          <span class="chip">{{ tsShort(cur?.ts) }}</span>
        </div>
        <pre class="pre">{{ cur?.body || '(空)' }}</pre>
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
</style>
