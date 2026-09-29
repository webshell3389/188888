<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { panelApi } from '@/api/panel'
import { useDashStore } from '@/stores/dash'
import { useCommandStore } from '@/stores/command'
import type { ManagedDevice } from '@/api/types'
import CommandDialog from '@/components/CommandDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import { shortIP, tsShort } from '@/utils/format'
import { ElMessage } from 'element-plus'

/**
 * 我的设备：/a + /beacon 收包解析出的受控设备（每台 = 一个 uuid 一行）。
 * 不含交付进度（那是"访问统计"模块的维度）；只展示受控设备的资产画像。
 * 排版：设备身份集中到主列（三层），资产 chip 分组，避免列过多挤压。
 */
const dash = useDashStore()
const cmd = useCommandStore()

const devices = ref<ManagedDevice[]>([])
const loading = ref(false)
const kw = ref('')
const selected = ref<ManagedDevice[]>([])

async function load() {
  loading.value = true
  try {
    const r = await panelApi.devices()
    devices.value = r?.devices ?? []
  } catch {
    devices.value = []
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
  void cmd.refreshTargets()
})
watch(
  () => dash.snap.ts,
  () => {
    if (dash.snap.ts) void load()
  },
)

const filtered = computed(() => {
  const k = kw.value.trim().toLowerCase()
  if (!k) return devices.value
  return devices.value.filter((d) =>
    [d.uuid, d.ip, d.deviceName, d.machine, d.ios, d.build]
      .map((x) => String(x ?? '').toLowerCase())
      .some((x) => x.includes(k)),
  )
})

const queueMap = computed(() => {
  const m = new Map<string, { armed: number; dispatched: number }>()
  for (const t of cmd.targets) {
    m.set(String(t.target ?? ''), {
      armed: Number(t.armed ?? 0),
      dispatched: Number(t.dispatched ?? 0),
    })
  }
  return m
})

function queueOf(d: ManagedDevice) {
  return queueMap.value.get(String(d.ip ?? '')) ?? queueMap.value.get(String(d.uuid ?? ''))
}

// ── 指令弹窗 ──
const dialog = ref(false)
const dialogTargets = ref<string[]>([])

function armOne(row: ManagedDevice) {
  if (!row.ip) {
    ElMessage.warning('该设备无 IP，无法作为下发目标')
    return
  }
  dialogTargets.value = [row.ip]
  dialog.value = true
}

function armSelected() {
  const ips = selected.value.map((d) => d.ip).filter((x): x is string => !!x)
  if (!ips.length) {
    ElMessage.warning('请先勾选设备')
    return
  }
  dialogTargets.value = ips
  dialog.value = true
}

// ── 详情抽屉：默认直显 row 资产详情（零请求、秒开）──
const drawer = ref(false)
const current = ref<ManagedDevice | null>(null)

function openDevice(row: ManagedDevice) {
  current.value = row
  drawer.value = true
}

// ── UUID 折叠：显示前 5 后 4，tooltip 全文，点击复制 ──
function shortUuid(u?: string) {
  if (!u) return ''
  return u.length <= 10 ? u : `${u.slice(0, 5)}…${u.slice(-4)}`
}
async function copyUuid(u?: string) {
  if (!u) return
  try {
    await navigator.clipboard.writeText(u)
    ElMessage.success('UUID 已复制')
  } catch {
    ElMessage.warning('复制失败，请手动复制')
  }
}
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Monitor /></el-icon>我的设备
        <span class="chip">{{ filtered.length }} / {{ devices.length }}</span>
        <span v-if="selected.length" class="chip chip--brand">已选 {{ selected.length }}</span>
      </h3>
      <div class="row row--wrap">
        <el-button v-if="selected.length" type="primary" size="small" @click="armSelected">
          <el-icon><Promotion /></el-icon>
          <span style="margin-left: 4px">批量下发（{{ selected.length }}）</span>
        </el-button>
        <el-input v-model="kw" size="small" placeholder="筛选 uuid / IP / 设备名 / 机型 / iOS" clearable
          style="width: 260px" />
        <el-button size="small" :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <el-table
        :data="filtered"
        size="small"
        height="calc(100vh - 250px)"
        empty-text="…"
        @selection-change="(v: ManagedDevice[]) => (selected = v)"
      >
        <el-table-column type="selection" width="42" />

        <el-table-column label="UUID" width="120">
          <template #default="{ row }">
            <el-tooltip v-if="row.uuid" placement="top" :content="row.uuid">
              <span class="mono uuid-link" title="点击复制完整 UUID" @click="copyUuid(row.uuid)">
                {{ shortUuid(row.uuid) }}
              </span>
            </el-tooltip>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="设备名" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="dev-name-cell">
              <span class="dot" :class="row.online ? 'dot--ok' : 'dot--off'" />
              <span class="dev-name">{{ row.deviceName || row.machine || '未知设备' }}</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="iOS 版本" width="120">
          <template #default="{ row }">
            <span v-if="row.ios" class="chip chip--brand">iOS {{ row.ios }}</span>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="钱包" width="90" align="center">
          <template #default="{ row }">
            <template v-if="(row.wallets ?? []).length">
              <el-tooltip placement="top">
                <template #content>
                  <div class="wallet-tip">
                    <div v-for="w in row.wallets" :key="w.rule" class="wallet-tip__row">
                      <span>{{ w.display || w.rule }}</span>
                      <b v-if="w.count"> ×{{ w.count }}</b>
                    </div>
                  </div>
                </template>
                <span class="wallet-yes">有钱包 · {{ row.wallets.length }}</span>
              </el-tooltip>
            </template>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="备忘录" width="80" align="center">
          <template #default="{ row }"><b class="n">{{ row.notes_count ?? 0 }}</b></template>
        </el-table-column>

        <el-table-column label="应用" width="70" align="center">
          <template #default="{ row }"><b class="n">{{ row.apps_count ?? 0 }}</b></template>
        </el-table-column>

        <el-table-column label="IP" width="150">
          <template #default="{ row }"><span class="mono ellipsis" :title="row.ip">{{ shortIP(row.ip) }}</span></template>
        </el-table-column>

        <el-table-column label="指令队列" width="110">
          <template #default="{ row }">
            <span v-if="queueOf(row)?.armed" class="chip chip--warn">待发 {{ queueOf(row)?.armed }}</span>
            <span v-else-if="queueOf(row)?.dispatched" class="chip chip--ok">已发 {{ queueOf(row)?.dispatched }}</span>
            <span v-else class="t-3" style="font-size: var(--fs-xs)">—</span>
          </template>
        </el-table-column>

        <el-table-column label="首次上线" width="150">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.first_seen) }}</span></template>
        </el-table-column>

        <el-table-column label="最后上线" width="150">
          <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.last_seen) }}</span></template>
        </el-table-column>

        <el-table-column label="操作" width="120" fixed="right">
          <template #default="{ row }">
            <el-button link size="small" type="primary" @click="armOne(row)">指令</el-button>
            <el-button link size="small" @click="openDevice(row)">详情</el-button>
          </template>
        </el-table-column>

        <template #empty><EmptyState text="暂无受控设备（/a /beacon 尚未回传）" /></template>
      </el-table>
    </div>

    <CommandDialog v-model="dialog" :targets="dialogTargets" @done="cmd.refreshTargets()" />

    <el-drawer v-model="drawer" :title="`设备详情 · ${current?.deviceName || current?.machine || current?.uuid || ''}`"
      size="46%">
      <div class="tl">
        <!-- 基本信息 + 资产：直接来自表格 row，打开即显 -->
        <dl class="detail-grid">
          <div class="detail-item">
            <dt>UUID</dt>
            <dd>
              <template v-if="current?.uuid">
                <span class="mono t-2">{{ current.uuid }}</span>
                <el-button link size="small" type="primary" @click="copyUuid(current.uuid)">复制</el-button>
              </template>
              <span v-else class="t-3">-</span>
            </dd>
          </div>
          <div class="detail-item"><dt>设备名</dt><dd>{{ current?.deviceName || current?.machine || '-' }}</dd></div>
          <div class="detail-item"><dt>机型</dt><dd>{{ current?.machine || '-' }}</dd></div>
          <div class="detail-item"><dt>iOS</dt>
            <dd><span v-if="current?.ios" class="mono">iOS {{ current.ios }}</span><span v-else class="t-3">-</span></dd>
          </div>
          <div class="detail-item"><dt>IP</dt><dd><span class="mono">{{ current?.ip || '-' }}</span></dd></div>
          <div class="detail-item"><dt>状态</dt>
            <dd><span class="chip" :class="current?.online ? 'chip--ok' : 'chip--muted'">{{ current?.online ? '在线'
                : '离线' }}</span></dd>
          </div>
          <div class="detail-item"><dt>首次上线</dt><dd><span class="mono t-2">{{ tsShort(current?.first_seen) }}</span></dd>
          </div>
          <div class="detail-item"><dt>最后上线</dt><dd><span class="mono t-2">{{ tsShort(current?.last_seen) }}</span></dd>
          </div>
          <div class="detail-item"><dt>钱包</dt>
            <dd>
              <div class="asset-chips">
                <template v-if="(current?.wallets ?? []).length">
                  <span v-for="w in current!.wallets" :key="w.rule" class="chip chip--violet">
                    {{ w.display || w.rule }}<template v-if="w.count"> ×{{ w.count }}</template>
                  </span>
                </template>
                <span v-else class="t-3">无</span>
              </div>
            </dd>
          </div>
          <div class="detail-item"><dt>资产统计</dt>
            <dd>
              备忘录 <b class="n">{{ current?.notes_count ?? 0 }}</b> ·
              应用 <b class="n">{{ current?.apps_count ?? 0 }}</b> ·
              钥匙串 <b class="n">{{ current?.war_count ?? 0 }}</b>
            </dd>
          </div>
          <div v-if="queueOf(current ?? ({} as ManagedDevice))" class="detail-item"><dt>指令队列</dt>
            <dd>
              <span v-if="queueOf(current!)?.armed" class="chip chip--warn">待发 {{ queueOf(current!)?.armed }}</span>
              <span v-else class="chip chip--ok">已发 {{ queueOf(current!)?.dispatched }}</span>
            </dd>
          </div>
          <div class="detail-item"><dt>操作</dt>
            <dd>
              <el-button size="small" type="primary" plain @click="armOne(current!)">下发指令</el-button>
            </dd>
          </div>
        </dl>
      </div>
    </el-drawer>
  </section>
</template>

<style scoped>
.dev-name-cell {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.dev-name {
  font-weight: 600;
  font-size: var(--fs-md);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 在线状态点 */
.dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: 50%;
}
.dot--ok {
  background: var(--c-ok);
  box-shadow: 0 0 6px rgba(52, 211, 153, .6);
}
.dot--off {
  background: var(--text-3);
  opacity: .55;
}

/* UUID 短显示 */
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

/* 钱包有/无 */
.wallet-yes {
  display: inline-flex;
  padding: 1px 8px;
  border-radius: var(--r-pill);
  font-size: var(--fs-xs);
  background: rgba(167, 139, 250, .13);
  border: 1px solid rgba(167, 139, 250, .32);
  color: #a78bfa;
  cursor: default;
  white-space: nowrap;
}
.asset-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.n {
  color: var(--text-1);
  font-weight: 600;
}

/* 抽屉详情网格 */
.tl {
  display: flex;
  flex-direction: column;
  height: 100%;
  padding: var(--sp-4) var(--sp-5);
}
.detail-grid {
  margin: 0 0 var(--sp-4);
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 18px;
}
.detail-item dt {
  font-size: var(--fs-xs);
  color: var(--text-3);
  margin-bottom: 2px;
}
.detail-item dd {
  margin: 0;
  font-size: var(--fs-sm);
  color: var(--text-1);
  word-break: break-all;
}
</style>
