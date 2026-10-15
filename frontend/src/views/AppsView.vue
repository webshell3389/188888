<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { panelApi } from '@/api/panel'
import type { AppRow } from '@/api/types'
import EmptyState from '@/components/EmptyState.vue'
import { clip, fmtInt, tsShort } from '@/utils/format'

/**
 * 应用资产：u 端点回传的已安装应用清单（bundleId / 名称 / lhu）。
 * 行内「设备 UUID」= 设备列表的设备标识（lhu），点击可跳转「我的设备」并自动筛选；
 * 「App UUID」= 单个应用实例自己的 uuid（app_uuid），与设备标识无关，仅展示。
 */
const route = useRoute()
const router = useRouter()

const rows = ref<AppRow[]>([])
const total = ref(0)
const page = ref(1)
const per = 40
const kw = ref('')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const r = await panelApi.apps(kw.value, page.value, per)
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

/** 设备 UUID → 跳转「我的设备」自动筛选该设备 */
function gotoDevice(lhu: string) {
  if (!lhu) return
  void router.push({ path: '/devices', query: { kw: lhu } })
}

onMounted(() => {
  // 支持从其它模块带 ?kw= 跳入（如设备列表反向跳转）
  const q = String(route.query.kw ?? '')
  if (q) kw.value = q
  void load()
})
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Grid /></el-icon>应用资产
        <span class="chip">共 {{ fmtInt(total) }}</span>
      </h3>
      <div class="row">
        <el-input v-model="kw" size="small" placeholder="过滤 bundleId / 名称 / 设备 UUID" clearable
          style="width: 280px" @keyup.enter="search" />
        <el-button size="small" type="primary" @click="search">查询</el-button>
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table :data="rows" size="small" height="calc(100vh - 250px)" empty-text="…">
        <el-table-column prop="name" label="应用名称" min-width="180" />
        <el-table-column prop="bundleId" label="bundleId" min-width="260">
          <template #default="{ row }">
            <span class="mono t-2 break-all">{{ row.bundleId || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column prop="lhu" label="设备 UUID" width="170">
          <template #default="{ row }">
            <span v-if="row.lhu" class="mono uuid-link" title="点击跳转该设备" @click="gotoDevice(String(row.lhu))">
              {{ clip(String(row.lhu), 16) }}
            </span>
            <span v-else class="t-3">-</span>
          </template>
        </el-table-column>
        <el-table-column prop="uuid" label="App UUID" width="160">
          <template #default="{ row }"><span class="mono t-3">{{ clip(row.uuid, 20) }}</span></template>
        </el-table-column>
        <el-table-column prop="ts" label="回传时间" width="160">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(String(row.ts)) }}</span></template>
        </el-table-column>
        <el-table-column prop="file" label="来源包" width="220">
          <template #default="{ row }"><span class="mono t-3 ellipsis">{{ row.file || '-' }}</span></template>
        </el-table-column>
        <template #empty><EmptyState text="暂无应用清单（u 端点尚无回传）" /></template>
      </el-table>
    </div>

    <div class="pager">
      <el-pagination v-model:current-page="page" :page-size="per" :total="total" layout="prev, pager, next, total"
        small background @current-change="load" />
    </div>
  </section>
</template>

<style scoped>
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
.pager {
  padding: var(--sp-3) var(--sp-5);
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: flex-end;
}
</style>
