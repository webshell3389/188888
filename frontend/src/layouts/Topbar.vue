<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh, Moon, Sunny, SwitchButton, Fold, Expand, Timer } from '@element-plus/icons-vue'
import { useDashStore } from '@/stores/dash'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { tsShort } from '@/utils/format'

const route = useRoute()
const dash = useDashStore()
const ui = useUiStore()
const auth = useAuthStore()

const title = computed(() => (route.meta?.title as string) || '企业数据安全运维')
const desc = computed(() => (route.meta?.desc as string) || '')

const intervals = [
  { label: '1s', value: 1000 },
  { label: '2s', value: 2000 },
  { label: '5s', value: 5000 },
  { label: '10s', value: 10000 },
]

async function manualRefresh() {
  await dash.refresh()
  ElMessage.success('已刷新')
}

async function doLogout() {
  try {
    await ElMessageBox.confirm('退出后需要重新输入管理口令。', '确认退出', {
      type: 'warning',
      confirmButtonText: '退出',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await auth.logout()
}
</script>

<template>
  <header class="top">
    <el-icon class="top__fold" :size="18" @click="ui.collapsed = !ui.collapsed">
      <component :is="ui.collapsed ? Expand : Fold" />
    </el-icon>

    <div class="top__title">
      <b>{{ title }}</b>
      <span v-if="desc">{{ desc }}</span>
    </div>

    <div class="spacer" />

    <!-- 数据时间 + 轮询控制 -->
    <div class="top__meta">
      <span class="chip" :class="dash.loading ? 'chip--brand' : ''">
        <el-icon><Timer /></el-icon>
        数据时间 {{ tsShort(dash.snap.ts) }}
      </span>
    </div>

    <el-select v-model="ui.refreshMs" size="small" class="top__sel">
      <el-option v-for="i in intervals" :key="i.value" :label="i.label" :value="i.value" />
    </el-select>

    <el-switch
      v-model="ui.autoRefresh"
      size="small"
      inline-prompt
      active-text="自动"
      inactive-text="暂停"
      class="top__switch"
    />

    <el-tooltip content="立即刷新" placement="bottom">
      <el-button size="small" :icon="Refresh" :loading="dash.loading" circle @click="manualRefresh" />
    </el-tooltip>

    <el-tooltip :content="ui.isDark ? '切换浅色' : '切换深色'" placement="bottom">
      <el-button size="small" circle @click="ui.toggleTheme()">
        <el-icon><component :is="ui.isDark ? Sunny : Moon" /></el-icon>
      </el-button>
    </el-tooltip>

    <el-tooltip content="退出登录" placement="bottom">
      <el-button size="small" :icon="SwitchButton" circle @click="doLogout" />
    </el-tooltip>
  </header>
</template>

<style scoped>
.top {
  height: var(--topbar-h);
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  padding: 0 var(--sp-5);
  background: var(--bg-topbar);
  backdrop-filter: blur(10px);
  border-bottom: 1px solid var(--border);
}
.top__fold {
  color: var(--text-3);
  cursor: pointer;
  padding: 4px;
  border-radius: var(--r-sm);
}
.top__fold:hover {
  color: var(--text-1);
  background: var(--bg-hover);
}
.top__title {
  display: flex;
  align-items: baseline;
  gap: var(--sp-3);
  overflow: hidden;
}
.top__title b {
  font-size: var(--fs-lg);
  font-weight: 600;
}
.top__title span {
  font-size: var(--fs-sm);
  color: var(--text-3);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.top__meta {
  display: flex;
  gap: var(--sp-2);
}
.top__sel {
  width: 78px;
}
.top__switch {
  flex: none;
}
@media (max-width: 1100px) {
  .top__title span {
    display: none;
  }
  .top__meta {
    display: none;
  }
}
</style>
