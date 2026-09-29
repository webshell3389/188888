<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Lock, Key, Cpu } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const pw = ref('')
const busy = ref(false)

async function submit() {
  if (!pw.value) {
    ElMessage.warning('请输入管理口令')
    return
  }
  busy.value = true
  try {
    const ok = await auth.login(pw.value)
    if (!ok) ElMessage.error(auth.error || '登录失败')
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="login">
    <!-- 左：品牌/说明区 -->
    <aside class="login__brand">
      <div class="brand__glow" />
      <div class="brand__inner">
        <div class="brand__logo">
          <el-icon :size="26"><Cpu /></el-icon>
        </div>
        <h1>Mock C2 控制台</h1>
        <p class="brand__sub">受授权靶场 · 交付链与遥测运维面</p>

        <ul class="brand__list">
          <li><span>01</span>单一数据源：<code>/__dash/api</code></li>
          <li><span>02</span>只读查询面：<code>/__q/*</code> · <code>/panel/api/*</code></li>
          <li><span>03</span>指令只做武装 / 清空 / peek，分发由设备 beacon 领走</li>
          <li><span>04</span>会话为后端 HttpOnly Cookie（12h 滑动）</li>
        </ul>
      </div>
      <div class="brand__foot">LAN RANGE · AUTHORIZED USE ONLY</div>
    </aside>

    <!-- 右：表单区 -->
    <main class="login__form">
      <div class="card">
        <h2>登录</h2>
        <p class="card__sub">请输入后端管理口令（<code>admin_password</code>）</p>

        <el-form @submit.prevent="submit">
          <el-form-item>
            <el-input
              v-model="pw"
              type="password"
              size="large"
              show-password
              placeholder="管理口令"
              :prefix-icon="Lock"
              autofocus
              @keyup.enter="submit"
            />
          </el-form-item>
          <el-button type="primary" size="large" :loading="busy" style="width: 100%" @click="submit">
            <el-icon v-if="!busy"><Key /></el-icon>
            <span>{{ busy ? '校验中…' : '进入控制台' }}</span>
          </el-button>
        </el-form>

        <p v-if="auth.error" class="card__err">{{ auth.error }}</p>

        <div class="card__hint">
          口令错误会在后端 <code>control.jsonl</code> 留痕（<code>auth_fail</code>），可用于爆破观测演练。
        </div>
      </div>
    </main>
  </div>
</template>

<style scoped>
.login {
  height: 100%;
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  background: var(--bg-app);
}

/* ── 品牌侧 ─────────────────────────────────── */
.login__brand {
  position: relative;
  overflow: hidden;
  padding: var(--sp-10);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  background: linear-gradient(160deg, #0f1626 0%, #121a2b 45%, #0d1420 100%);
  color: #e6edf7;
  border-right: 1px solid var(--border);
}
[data-theme='light'] .login__brand {
  background: linear-gradient(160deg, #123a63 0%, #186ec8 55%, #1f5fa8 100%);
}
.brand__glow {
  position: absolute;
  width: 520px;
  height: 520px;
  right: -180px;
  top: -160px;
  border-radius: 50%;
  background: radial-gradient(circle, rgba(64, 158, 255, 0.28), transparent 65%);
  pointer-events: none;
}
.brand__inner {
  position: relative;
  z-index: 1;
}
.brand__logo {
  width: 46px;
  height: 46px;
  border-radius: var(--r-lg);
  display: grid;
  place-items: center;
  background: rgba(64, 158, 255, 0.16);
  border: 1px solid rgba(64, 158, 255, 0.4);
  color: #6cb6ff;
  margin-bottom: var(--sp-5);
}
.login__brand h1 {
  margin: 0;
  font-size: 28px;
  letter-spacing: -0.5px;
  font-weight: 650;
}
.brand__sub {
  margin: 6px 0 0;
  color: #93a6c0;
  font-size: var(--fs-md);
}
.brand__list {
  margin: var(--sp-8) 0 0;
  padding: 0;
  list-style: none;
  display: grid;
  gap: var(--sp-3);
  max-width: 460px;
}
.brand__list li {
  display: flex;
  gap: var(--sp-3);
  align-items: flex-start;
  font-size: var(--fs-md);
  color: #b9c8dc;
  line-height: 1.7;
}
.brand__list span {
  font-family: var(--font-mono);
  color: #6cb6ff;
  font-size: var(--fs-sm);
  padding-top: 2px;
}
.brand__list code {
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
  padding: 1px 6px;
  border-radius: var(--r-sm);
  background: rgba(255, 255, 255, 0.08);
  color: #dce7f5;
}
.brand__foot {
  position: relative;
  z-index: 1;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  letter-spacing: 2px;
  color: #64748b;
}

/* ── 表单侧 ─────────────────────────────────── */
.login__form {
  display: grid;
  place-items: center;
  padding: var(--sp-6);
}
.card {
  width: min(380px, 100%);
}
.card h2 {
  margin: 0 0 4px;
  font-size: var(--fs-xl);
  font-weight: 650;
}
.card__sub {
  margin: 0 0 var(--sp-6);
  color: var(--text-3);
  font-size: var(--fs-sm);
}
.card__sub code,
.card__hint code {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  padding: 1px 5px;
  border-radius: var(--r-sm);
  background: var(--bg-hover);
}
.card__err {
  margin: var(--sp-4) 0 0;
  color: var(--c-err);
  font-size: var(--fs-sm);
}
.card__hint {
  margin-top: var(--sp-6);
  padding-top: var(--sp-4);
  border-top: 1px solid var(--border);
  color: var(--text-3);
  font-size: var(--fs-xs);
  line-height: 1.7;
}

@media (max-width: 900px) {
  .login {
    grid-template-columns: 1fr;
  }
  .login__brand {
    display: none;
  }
}
</style>
