<script setup lang="ts">
import { onMounted } from 'vue'
import { useUiStore } from '@/stores/ui'
import { useAuthStore } from '@/stores/auth'
import { bus, EVT } from '@/utils/bus'
import LoginView from '@/views/LoginView.vue'

const ui = useUiStore()
const auth = useAuthStore()

onMounted(async () => {
  ui.applyTheme()
  bus.on(EVT.NETWORK_ERROR, () => {
    ui.setError('network', '无法连接后端（检查 9001 端口是否存活）')
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
