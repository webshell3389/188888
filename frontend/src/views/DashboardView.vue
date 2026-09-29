<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useDashStore } from '@/stores/dash'
import { useUiStore } from '@/stores/ui'
import { useCommandStore } from '@/stores/command'
import { feApi, type FeActivity } from '@/api/feapi'
import StatCard from '@/components/StatCard.vue'
import EChart from '@/components/EChart.vue'
import EmptyState from '@/components/EmptyState.vue'
import { fmtInt, statusLevel, shortIP, tsShort, uaLabel, clip } from '@/utils/format'
import type { EChartsOption } from 'echarts'

const dash = useDashStore()
const ui = useUiStore()
const cmd = useCommandStore()

const stats = ref<Record<string, number>>({})
const activity = ref<FeActivity[]>([])

/** 今日新增（口径与暗色看板一致）：托管设备 first_seen 当日；台账 by /api/stats visitors_today */
function isNewToday(d: { first_seen?: string }) {
  const now = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  const td = `${now.getFullYear()}-${p(now.getMonth() + 1)}-${p(now.getDate())}`
  return String(d?.first_seen || '').startsWith(td)
}
const myNewToday = computed(() => dash.devices.filter(isNewToday).length)

async function loadFe() {
  try {
    stats.value = (await feApi.stats()) as unknown as Record<string, number>
  } catch {
    stats.value = {}
  }
  try {
    const r = await feApi.activity(20)
    activity.value = r?.items ?? []
  } catch {
    activity.value = []
  }
}

onMounted(() => {
  void loadFe()
  void cmd.refreshTargets()
})

// 快照刷新即带上兼容层（低频，跟随主轮询节拍即可）
watch(
  () => dash.snap.ts,
  () => {
    if (dash.snap.ts) void loadFe()
  },
)

/** 业务实体维度（入库解析后），不再按 capture 端点分组 */
const entityOption = computed<EChartsOption>(() => {
  const c = dash.counts
  const rows: Array<[string, number]> = [
    ['备忘录 nb', Number(c.nb || 0)],
    ['钥匙串 war', Number(c.war || 0)],
    ['照片 ph', Number(c.ph || 0)],
    ['应用清单 u', Number(c.u || 0)],
    ['结果 result', Number(c.result || 0)],
    ['心跳 heartbeat', Number(c.heartbeat || 0)],
  ]
  return {
    grid: { left: 8, right: 16, top: 20, bottom: 8, containLabel: true },
    xAxis: { type: 'value', splitLine: { lineStyle: { type: 'dashed' } } },
    yAxis: {
      type: 'category',
      data: rows.map((r) => r[0]),
      axisLine: { show: false },
      axisLabel: { fontSize: 11 },
    },
    series: [
      {
        type: 'bar',
        data: rows.map((r) => r[1]),
        barWidth: 12,
        itemStyle: { borderRadius: [0, 4, 4, 0], color: '#409eff' },
        label: { show: true, position: 'right', fontSize: 11 },
      },
    ],
  }
})

const trendOption = computed<EChartsOption>(() => {
  const buckets = new Map<string, { total: number; slow: number }>()
  for (const a of dash.access) {
    if (!a.ts || a.ts.length < 16) continue
    const k = a.ts.slice(11, 16)
    const cur = buckets.get(k) || { total: 0, slow: 0 }
    cur.total += 1
    if ((a.cost_ms || 0) > 50) cur.slow += 1
    buckets.set(k, cur)
  }
  const keys = [...buckets.keys()].sort()
  return {
    grid: { left: 8, right: 16, top: 26, bottom: 8, containLabel: true },
    legend: { top: 0, itemWidth: 10, itemHeight: 10, textStyle: { fontSize: 11 } },
    xAxis: { type: 'category', data: keys, boundaryGap: false, axisLine: { show: false } },
    yAxis: { type: 'value', splitLine: { lineStyle: { type: 'dashed' } } },
    series: [
      {
        name: '请求数',
        type: 'line',
        smooth: true,
        showSymbol: false,
        data: keys.map((k) => buckets.get(k)!.total),
        areaStyle: { opacity: 0.14 },
        itemStyle: { color: '#409eff' },
      },
      {
        name: '慢请求(>50ms)',
        type: 'line',
        smooth: true,
        showSymbol: false,
        data: keys.map((k) => buckets.get(k)!.slow),
        itemStyle: { color: '#fbbf24' },
      },
    ],
  }
})

const statusOption = computed<EChartsOption>(() => {
  const buckets: Record<string, number> = {}
  for (const a of dash.access) {
    const lv = statusLevel(a.status, a.path)
    const key = lv === 'err' ? '≥500' : lv === 'warn' ? '4xx' : lv === 'muted' ? '预期404' : '2xx/3xx'
    buckets[key] = (buckets[key] || 0) + 1
  }
  const colorMap: Record<string, string> = {
    '≥500': '#f87171',
    '4xx': '#fbbf24',
    预期404: '#64748b',
    '2xx/3xx': '#34d399',
  }
  return {
    tooltip: { trigger: 'item' },
    legend: { bottom: 0, itemWidth: 10, itemHeight: 10, textStyle: { fontSize: 11 } },
    series: [
      {
        type: 'pie',
        radius: ['46%', '68%'],
        center: ['50%', '46%'],
        label: { show: false },
        data: Object.entries(buckets).map(([name, value]) => ({
          name,
          value,
          itemStyle: { color: colorMap[name] || '#409eff' },
        })),
      },
    ],
  }
})
</script>

<template>
  <div class="db">
    <!-- KPI：业务实体视角 -->
    <div class="grid kpi-grid">
      <StatCard label="我的设备" :value="fmtInt(dash.devices.length)" icon="Monitor" to="/devices"
        :foot="`/a /beacon 受控设备${myNewToday ? ` · 今日 +${myNewToday}` : ''}`" />
      <StatCard label="访问设备" :value="fmtInt(stats.visitors ?? 0)" icon="User" tone="muted" to="/access"
        :foot="(stats.visitors_today ?? 0) > 0 ? `访客台账 · 今日 +${stats.visitors_today}` : '访客台账 · 交付进度'" />
      <StatCard label="在线设备" :value="fmtInt(stats.online ?? 0)" icon="Connection" tone="ok" to="/devices"
        foot="10min beacon 窗口 · 在线判定" />
      <StatCard label="待发指令" :value="fmtInt(cmd.pendingTotal)" icon="Promotion" to="/console"
        :tone="cmd.pendingTotal ? 'warn' : 'muted'" foot="按设备独立队列" />
      <StatCard label="备忘录" :value="fmtInt(dash.counts.nb ?? 0)" icon="Notebook" to="/memos" tone="ok" />
      <StatCard label="钥匙串战果" :value="fmtInt(dash.counts.war ?? 0)" icon="Coin" to="/keychain" tone="warn" />
      <StatCard label="照片" :value="fmtInt(dash.counts.ph ?? 0)" icon="Picture" to="/photos" />
      <StatCard label="应用清单" :value="fmtInt(dash.counts.u ?? 0)" icon="Grid" to="/apps" />
      <StatCard label="探针日志" :value="fmtInt(dash.logs.length)" icon="ChatLineSquare" to="/logs" tone="muted" />
    </div>

    <div class="grid chart-grid">
      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><TrendCharts /></el-icon>访问趋势（按分钟）</h3>
          <span class="t-3" style="font-size: var(--fs-xs)">窗口内 {{ dash.access.length }} 条</span>
        </div>
        <div class="app-card__body"><EChart :option="trendOption" :theme-key="ui.theme" height="236px" /></div>
      </section>

      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><PieChart /></el-icon>响应状态分布</h3>
        </div>
        <div class="app-card__body"><EChart :option="statusOption" :theme-key="ui.theme" height="236px" /></div>
      </section>

      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><Box /></el-icon>业务实体回传量</h3>
          <span class="t-3" style="font-size: var(--fs-xs)">入库解析后的语义维度</span>
        </div>
        <div class="app-card__body"><EChart :option="entityOption" :theme-key="ui.theme" height="236px" /></div>
      </section>
    </div>

    <div class="grid detail-grid">
      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><Clock /></el-icon>活动流（语义化）</h3>
          <router-link to="/logs" class="t-3" style="font-size: var(--fs-xs)">日志 →</router-link>
        </div>
        <div class="app-card__body app-card__body--flush">
          <el-table :data="activity" size="small" height="300" empty-text="…">
            <el-table-column prop="ts" label="时间" width="150">
              <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.ts) }}</span></template>
            </el-table-column>
            <el-table-column prop="label" label="事件" min-width="180" />
            <el-table-column prop="device_uuid" label="设备" width="160">
              <template #default="{ row }">
                <span class="mono t-2">{{ clip(row.device_uuid || '-', 22) }}</span>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </section>

      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title"><el-icon><Monitor /></el-icon>最近设备</h3>
          <router-link to="/devices" class="t-3" style="font-size: var(--fs-xs)">全部 →</router-link>
        </div>
        <div class="app-card__body app-card__body--flush">
          <el-table :data="dash.devices.slice(0, 40)" size="small" height="300" empty-text="…">
            <el-table-column prop="ip" label="IP" width="140">
              <template #default="{ row }"><span class="mono ellipsis" :title="row.ip">{{ shortIP(row.ip) }}</span></template>
            </el-table-column>
            <el-table-column label="设备" width="120">
              <template #default="{ row }"><span class="chip chip--brand">{{ uaLabel(row.ua) }}</span></template>
            </el-table-column>
            <el-table-column prop="deviceVersion" label="版本" width="110" />
            <el-table-column label="域" min-width="160">
              <template #default="{ row }">
                <span class="mono t-3 ellipsis">{{ row.domain || '-' }}</span>
              </template>
            </el-table-column>
            <el-table-column label="最后出现" width="150">
              <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.ts) }}</span></template>
            </el-table-column>
          </el-table>
        </div>
      </section>
    </div>

    <!-- 队列概览（批量异步） -->
    <section v-if="cmd.targets.length" class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title">
          <el-icon><Promotion /></el-icon>指令队列（按设备）
          <span class="chip chip--warn">待发 {{ cmd.pendingTotal }}</span>
          <span class="chip chip--ok">已发 {{ cmd.dispatchedTotal }}</span>
        </h3>
        <router-link to="/console" class="t-3" style="font-size: var(--fs-xs)">设备控制台 →</router-link>
      </div>
      <div class="app-card__body app-card__body--flush">
        <el-table :data="cmd.targets" size="small" height="220">
          <el-table-column prop="target" label="设备" min-width="180">
            <template #default="{ row }"><span class="mono">{{ row.target }}</span></template>
          </el-table-column>
          <el-table-column prop="armed" label="待发" width="90" align="right">
            <template #default="{ row }"><b class="num s-warn">{{ row.armed }}</b></template>
          </el-table-column>
          <el-table-column prop="dispatched" label="已发" width="90" align="right">
            <template #default="{ row }"><span class="num s-ok">{{ row.dispatched }}</span></template>
          </el-table-column>
          <el-table-column prop="last_armed_ts" label="最后武装" width="170">
            <template #default="{ row }">
              <span class="mono t-3">{{ tsShort(row.last_armed_ts) }}</span>
            </template>
          </el-table-column>
        </el-table>
      </div>
    </section>

    <section v-else-if="cmd.perDevice === false" class="app-card">
      <div class="app-card__body">
        <EmptyState text="当前后端不支持按设备独立队列（需 Go 版 device_commands 表）" />
      </div>
    </section>
  </div>
</template>

<style scoped>
.db {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
.kpi-grid {
  grid-template-columns: repeat(auto-fit, minmax(178px, 1fr));
}
.chart-grid {
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
}
.detail-grid {
  grid-template-columns: repeat(auto-fit, minmax(420px, 1fr));
}
</style>
