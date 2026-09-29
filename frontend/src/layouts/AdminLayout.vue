<script setup lang="ts">
import { computed, onMounted, onUnmounted } from 'vue'
import Sidebar from './Sidebar.vue'
import Topbar from './Topbar.vue'
import { useDashStore } from '@/stores/dash'
import { useUiStore } from '@/stores/ui'

const dash = useDashStore()
const ui = useUiStore()

const sidebarWidth = computed(() =>
  ui.collapsed ? 'var(--sidebar-w-collapsed)' : 'var(--sidebar-w)',
)

onMounted(() => {
  dash.start()
})
onUnmounted(() => {
  dash.stop()
})
</script>

<template>
  <div class="shell">
    <aside class="shell__side" :style="{ width: sidebarWidth }">
      <Sidebar :collapsed="ui.collapsed" />
    </aside>

    <div class="shell__main">
      <Topbar />

      <!-- 错误条：5xx / 网络失败；人工关闭前不重复弹窗打扰 -->
      <div v-if="ui.errorBar" class="shell__err">
        <el-icon><WarnTriangleFilled /></el-icon>
        <span>{{ ui.errorBar.text }}</span>
        <el-button link size="small" @click="ui.clearError()">关闭</el-button>
      </div>

      <main class="shell__view">
        <router-view v-slot="{ Component }">
          <transition name="fade-slide" mode="out-in">
            <component :is="Component" />
          </transition>
        </router-view>
      </main>
    </div>
  </div>
</template>

<style scoped>
.shell {
  height: 100%;
  display: flex;
  overflow: hidden;
}
.shell__side {
  flex: none;
  transition: width var(--dur) var(--ease);
  overflow: hidden;
}
.shell__main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.shell__err {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding: 8px var(--sp-5);
  background: rgba(248, 113, 113, 0.12);
  border-bottom: 1px solid rgba(248, 113, 113, 0.3);
  color: var(--c-err);
  font-size: var(--fs-sm);
}
.shell__err span {
  flex: 1;
}
.shell__view {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: var(--sp-5);
}
</style>
