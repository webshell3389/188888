<script setup lang="ts">
withDefaults(
  defineProps<{
    text?: string
    /** 极简模式：行内使用（表格空态） */
    inline?: boolean
  }>(),
  { text: '暂无数据', inline: false },
)
</script>

<template>
  <div v-if="inline" class="empty-inline">
    <span class="muted">…</span>
  </div>
  <div v-else class="empty">
    <el-icon :size="30" class="empty__icon"><Box /></el-icon>
    <p>{{ text }}</p>
    <slot />
  </div>
</template>

<style scoped>
.empty {
  padding: var(--sp-10) var(--sp-4);
  text-align: center;
  color: var(--text-3);
}
.empty__icon {
  opacity: 0.5;
  margin-bottom: var(--sp-2);
}
.empty p {
  margin: 0;
  font-size: var(--fs-sm);
}
.empty-inline {
  text-align: center;
  padding: var(--sp-4);
}
</style>
