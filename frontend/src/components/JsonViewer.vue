<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { prettyJson } from '@/utils/format'

const props = withDefaults(
  defineProps<{
    text: string
    /** 超过此长度默认折叠，避免 MB 级正文直接铺开 */
    collapseAt?: number
    maxHeight?: string
  }>(),
  { collapseAt: 4000, maxHeight: '520px' },
)

const expanded = ref(false)
const pretty = computed(() => prettyJson(props.text ?? ''))
const tooLong = computed(() => pretty.value.length > props.collapseAt)

const shown = computed(() =>
  tooLong.value && !expanded.value ? pretty.value.slice(0, props.collapseAt) + '\n…（已折叠）' : pretty.value,
)

async function copy() {
  try {
    await navigator.clipboard.writeText(pretty.value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('浏览器拒绝了剪贴板访问')
  }
}
</script>

<template>
  <div class="jv">
    <div class="jv__bar">
      <span class="jv__meta mono">
        {{ pretty.length }} 字符<template v-if="tooLong"> · 已折叠</template>
      </span>
      <div class="spacer" />
      <el-button v-if="tooLong" link size="small" @click="expanded = !expanded">
        {{ expanded ? '折叠' : '展开全部' }}
      </el-button>
      <el-button link size="small" @click="copy">复制</el-button>
    </div>
    <pre class="pre" :style="{ maxHeight }">{{ shown }}</pre>
  </div>
</template>

<style scoped>
.jv__bar {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding: 6px var(--sp-2) 8px;
}
.jv__meta {
  color: var(--text-3);
  font-size: var(--fs-xs);
}
</style>
