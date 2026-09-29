<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { panelApi } from '@/api/panel'
import type { ResultDetail, ResultRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { clip, fmtInt, tsShort } from '@/utils/format'

/** 指令结果：result 端点 data（base64）解码落库后的实体（JSON 存正文，二进制存摘要）。 */
const rows = ref<ResultRow[]>([])
const total = ref(0)
const page = ref(1)
const per = 40
const kw = ref('')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const r = await panelApi.results(kw.value, page.value, per)
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
  void load()
}

onMounted(load)

const drawer = ref(false)
const cur = ref<ResultDetail | null>(null)
const detailLoading = ref(false)

async function open(row: ResultRow) {
  drawer.value = true
  detailLoading.value = true
  cur.value = null
  try {
    cur.value = await panelApi.resultDetail(row.id ?? '')
  } catch {
    cur.value = null
  } finally {
    detailLoading.value = false
  }
}
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Document /></el-icon>指令结果
        <span class="chip">共 {{ fmtInt(total) }}</span>
      </h3>
      <div class="row">
        <el-input v-model="kw" size="small" placeholder="过滤 filename / category / command_id / uuid" clearable
          style="width: 300px" @keyup.enter="search" />
        <el-button size="small" type="primary" @click="search">查询</el-button>
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="rows" size="small" height="calc(100vh - 250px)" empty-text="…" @row-click="open">
        <el-table-column prop="filename" label="文件名" min-width="200">
          <template #default="{ row }"><span class="mono t-2 break-all">{{ row.filename || '-' }}</span></template>
        </el-table-column>
        <el-table-column label="类型" width="120">
          <template #default="{ row }">
            <span class="chip" :class="row.data_kind === 'json' ? 'chip--brand' : ''">
              {{ row.category || row.data_kind || '-' }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="command_id" label="command_id" width="200">
          <template #default="{ row }"><span class="mono t-3 ellipsis">{{ row.command_id || '-' }}</span></template>
        </el-table-column>
        <el-table-column prop="uuid" label="设备 uuid" width="170">
          <template #default="{ row }"><span class="mono t-3">{{ clip(row.uuid, 22) }}</span></template>
        </el-table-column>
        <el-table-column prop="data_size" label="大小" width="100" align="right">
          <template #default="{ row }"><span class="mono t-3">{{ fmtInt(row.data_size) }}</span></template>
        </el-table-column>
        <el-table-column prop="recv_ts" label="回传时间" width="160">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.recv_ts) }}</span></template>
        </el-table-column>
        <template #empty><EmptyState text="暂无指令结果（result 端点尚无回传）" /></template>
      </el-table>
    </div>

    <div class="pager">
      <el-pagination v-model:current-page="page" :page-size="per" :total="total" layout="prev, pager, next, total"
        small background @current-change="load" />
    </div>

    <el-drawer v-model="drawer" :title="cur?.filename || '指令结果'" size="56%">
      <div v-loading="detailLoading" class="body">
        <div class="meta">
          <span class="chip">{{ cur?.category || '-' }}</span>
          <span class="chip mono">{{ cur?.command_id || '-' }}</span>
          <span class="chip mono">{{ clip(cur?.uuid, 20) }}</span>
          <span class="chip">{{ tsShort(cur?.recv_ts) }}</span>
          <span class="chip">kind={{ cur?.data_kind }} · {{ fmtInt(cur?.data_size) }} 字节</span>
        </div>
        <pre v-if="cur" class="pre">{{ cur.data_json || '(空)' }}</pre>
      </div>
    </el-drawer>
  </section>
</template>

<style scoped>
.pager {
  padding: var(--sp-3) var(--sp-5);
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: flex-end;
}
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
