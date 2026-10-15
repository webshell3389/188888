<script setup lang="ts">
import { onMounted } from 'vue'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { bus, EVT } from '@/utils/bus'
import { getApiBase } from '@/api/http'
import LoginView from '@/views/LoginView.vue'

const ui = useUiStore()
const auth = useAuthStore()

onMounted(async () => {
  ui.applyTheme()
  bus.on(EVT.NETWORK_ERROR, (_p) => {
    // 全局兜底（dash 轮询有自己的细分提示，此处覆盖其余接口）。
    // axios 超时的 err.message 形如 "timeout of 25000ms exceeded"，据此区分：
    // 后端卡住（多半等 DB）≠ 服务未启动，避免误导排查方向。
    const payload = _p as { message?: string } | undefined
    const raw = String(payload?.message || '')
    const hint = raw.includes('timeout')
      ? '后端响应超时 —— 服务在运行，大概率卡在数据库'
      : '网络中断或服务未启动'
    ui.setError('network', `后端请求失败：${getApiBase() || location.origin}（${hint}）`)
  })
  await auth.check()
})
</script>

<template>
  <!-- 校验中优先显示 loading；checking 初始为 true，把登录门放前面会先闪登录页 -->
  <div v-if="auth.checking" class="boot">
    <div class="boot__ring" />
    <span>正在校验会话…</span>
  </div>
  <!-- 登录门：未通过会话校验时，任何视图都不渲染（契约 §5：fetch 401 → showLogin） -->
  <LoginView v-else-if="!auth.authed" />
  <router-view v-else />
</template>

<style scoped>
.boot {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  color: var(--text-3);
}
.boot__ring {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  border: 2px solid var(--border-strong);
  border-top-color: var(--brand);
  animation: spin 0.8s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
