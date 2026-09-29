<script setup lang="ts">
import { computed } from 'vue'
import { navGroups } from '@/router'
import { useDashStore } from '@/stores/dash'
import { useCommandStore } from '@/stores/command'

defineProps<{ collapsed: boolean }>()

const dash = useDashStore()
const cmd = useCommandStore()

/** 分组角标：只取快照里的动态计数，没有则不显示 */
function badgeOf(path: string): number | null {
  const c = dash.counts
  switch (path) {
    case '/devices':
      return dash.devices.length
    case '/console':
      return cmd.pendingTotal || null
    case '/memos':
      return c.nb ?? null
    case '/keychain':
      return dash.wallets.length || null
    case '/photos':
      return c.ph ?? null
    case '/apps':
      return c.u ?? null
    case '/logs':
      return dash.logs.length || null
    default:
      return null
  }
}

const groups = computed(() =>
  navGroups.map((g) => ({
    ...g,
    items: g.items.map((it) => ({ ...it, badge: badgeOf(it.path) })),
  })),
)
</script>

<template>
  <nav class="side">
    <div class="side__brand">
      <div class="side__logo"><el-icon :size="18"><Cpu /></el-icon></div>
      <div v-show="!collapsed" class="side__name">
        <b>Mock C2</b>
        <span>运维控制台</span>
      </div>
    </div>

    <div class="side__nav">
      <template v-for="g in groups" :key="g.name">
        <div v-show="!collapsed" class="side__group">{{ g.name }}</div>
        <div v-show="collapsed" class="side__sep" />
        <router-link
          v-for="it in g.items"
          :key="it.path"
          :to="it.path"
          class="side__item"
          active-class="is-active"
          :title="collapsed ? it.title : ''"
        >
          <el-icon :size="16"><component :is="it.icon" /></el-icon>
          <span v-show="!collapsed" class="side__label">{{ it.title }}</span>
          <span v-show="!collapsed && it.badge" class="side__badge num">{{ it.badge }}</span>
        </router-link>
      </template>
    </div>

    <div class="side__foot">
      <div v-show="!collapsed" class="side__ver">v1.1 · 契约 v2.0</div>
    </div>
  </nav>
</template>

<style scoped>
.side {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--bg-sidebar);
  border-right: 1px solid var(--border);
}
.side__brand {
  height: var(--topbar-h);
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  padding: 0 var(--sp-4);
  border-bottom: 1px solid var(--border);
  flex: none;
}
.side__logo {
  width: 28px;
  height: 28px;
  flex: none;
  border-radius: var(--r-md);
  display: grid;
  place-items: center;
  color: var(--brand-2);
  background: var(--brand-soft);
  border: 1px solid var(--border-glow);
}
.side__name {
  display: flex;
  flex-direction: column;
  line-height: 1.25;
  overflow: hidden;
}
.side__name b {
  font-size: var(--fs-lg);
  letter-spacing: -0.2px;
}
.side__name span {
  font-size: var(--fs-xs);
  color: var(--text-3);
}

.side__nav {
  flex: 1;
  padding: var(--sp-3) var(--sp-2);
  overflow-y: auto;
}
.side__group {
  padding: var(--sp-3) var(--sp-3) 6px;
  font-size: var(--fs-xs);
  letter-spacing: 1px;
  color: var(--text-3);
  text-transform: uppercase;
}
.side__sep {
  height: 1px;
  margin: var(--sp-2) var(--sp-3);
  background: var(--border);
}
.side__item {
  display: flex;
  align-items: center;
  gap: var(--sp-3);
  padding: 9px var(--sp-3);
  border-radius: var(--r-md);
  color: var(--text-2);
  font-size: var(--fs-md);
  transition: background var(--dur-fast) var(--ease), color var(--dur-fast) var(--ease);
  white-space: nowrap;
}
.side__item:hover {
  background: var(--bg-hover);
  color: var(--text-1);
}
.side__item.is-active {
  background: var(--bg-active);
  color: var(--brand-2);
  box-shadow: inset 2px 0 0 var(--brand);
}
.side__label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
}
.side__badge {
  font-size: var(--fs-xs);
  padding: 0 6px;
  border-radius: var(--r-pill);
  background: var(--bg-hover);
  color: var(--text-3);
  border: 1px solid var(--border);
}
.side__foot {
  flex: none;
  padding: var(--sp-3);
  border-top: 1px solid var(--border);
}
.side__ver {
  font-size: var(--fs-xs);
  color: var(--text-3);
  font-family: var(--font-mono);
  text-align: center;
}
</style>
