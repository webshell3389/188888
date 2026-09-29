import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

export type ThemeName = 'dark' | 'light'
export type Density = 'comfortable' | 'compact'

const KEY = 'mockc2.ui.v1'

interface UiState {
  theme: ThemeName
  collapsed: boolean
  density: Density
  refreshMs: number
  autoRefresh: boolean
}

function load(): UiState {
  const base: UiState = {
    theme: 'dark',
    collapsed: false,
    density: 'comfortable',
    refreshMs: 2000,
    autoRefresh: true,
  }
  try {
    const raw = localStorage.getItem(KEY)
    if (raw) Object.assign(base, JSON.parse(raw))
  } catch {
    /* 忽略损坏的本地配置 */
  }
  return base
}

/** 视图偏好：主题 / 侧栏 / 密度 / 轮询节奏 + 全局错误条 */
export const useUiStore = defineStore('ui', () => {
  const init = load()
  const theme = ref<ThemeName>(init.theme)
  const collapsed = ref(init.collapsed)
  const density = ref<Density>(init.density)
  const refreshMs = ref(init.refreshMs)
  const autoRefresh = ref(init.autoRefresh)

  /** 顶栏错误条：服务端 5xx / 网络失败时展示，人工关闭前不重复打扰 */
  const errorBar = ref<{ kind: 'server' | 'network' | 'client'; text: string } | null>(null)

  const isDark = computed(() => theme.value === 'dark')

  function applyTheme() {
    const el = document.documentElement
    el.setAttribute('data-theme', theme.value)
    el.setAttribute('data-density', density.value)
  }

  function toggleTheme() {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
  }

  function setError(kind: 'server' | 'network' | 'client', text: string) {
    errorBar.value = { kind, text }
  }
  function clearError() {
    errorBar.value = null
  }

  watch(
    [theme, collapsed, density, refreshMs, autoRefresh],
    () => {
      applyTheme()
      try {
        localStorage.setItem(
          KEY,
          JSON.stringify({
            theme: theme.value,
            collapsed: collapsed.value,
            density: density.value,
            refreshMs: refreshMs.value,
            autoRefresh: autoRefresh.value,
          }),
        )
      } catch {
        /* 隐私模式下 localStorage 可能不可写，忽略 */
      }
    },
    { immediate: true },
  )

  return {
    theme,
    collapsed,
    density,
    refreshMs,
    autoRefresh,
    errorBar,
    isDark,
    toggleTheme,
    applyTheme,
    setError,
    clearError,
  }
})
