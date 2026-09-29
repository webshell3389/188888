<script setup lang="ts">
withDefaults(
  defineProps<{
    label: string
    value: string | number
    /** 右下角补充说明 */
    foot?: string
    /** 语义色：默认品牌蓝 */
    tone?: 'brand' | 'ok' | 'warn' | 'err' | 'muted'
    icon?: string
    /** 点击跳转（可选） */
    to?: string
  }>(),
  { foot: '', tone: 'brand', icon: '', to: '' },
)
</script>

<template>
  <component :is="to ? 'router-link' : 'div'" :to="to || undefined" class="kpi" :class="`kpi--${tone}`">
    <span class="kpi__glow" />
    <div class="kpi__label">
      <el-icon v-if="icon" :size="14"><component :is="icon" /></el-icon>
      <span>{{ label }}</span>
    </div>
    <div class="kpi__value num">{{ value }}</div>
    <div v-if="foot" class="kpi__foot">{{ foot }}</div>
  </component>
</template>

<style scoped>
.kpi {
  display: block;
  text-decoration: none;
  color: inherit;
}
.kpi--ok .kpi__value {
  color: var(--c-ok);
}
.kpi--warn .kpi__value {
  color: var(--c-warn);
}
.kpi--err .kpi__value {
  color: var(--c-err);
}
.kpi--muted .kpi__value {
  color: var(--text-2);
}
.kpi--brand .kpi__value {
  color: var(--brand-2);
}
</style>
