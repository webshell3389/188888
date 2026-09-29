<script setup lang="ts">
import { ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { dashApi } from '@/api/dash'
import { downloadUrl } from '@/api/http'
import { fmtBytes } from '@/utils/format'
import JsonViewer from '@/components/JsonViewer.vue'

/**
 * 原始包抽屉：列表里的 body 可能被后端截断（nb 单条实测 2.4MB），
 * 全量内容一律走 /__dash/file?ep=&name=（后端已防目录穿越）。
 */
const props = defineProps<{ modelValue: boolean; ep: string; file: string; size?: number }>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>()

const text = ref('')
const loading = ref(false)

watch(
  () => [props.modelValue, props.ep, props.file],
  async ([open]) => {
    if (!open) return
    if (!props.ep || !props.file) {
      text.value = ''
      return
    }
    loading.value = true
    text.value = ''
    try {
      text.value = await dashApi.file(props.ep, props.file)
    } catch (e: unknown) {
      const status = (e as { response?: { status?: number } })?.response?.status
      text.value = status === 404 ? '(not found)' : '(读取失败)'
      ElMessage.error('读取原始包失败')
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)

const dl = () => downloadUrl(props.ep, props.file)
</script>

<template>
  <el-drawer
    :model-value="modelValue"
    :title="`/${ep}/${file}`"
    size="56%"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="pd">
      <div class="pd__bar">
        <span class="chip">端点 /{{ ep }}</span>
        <span class="chip">{{ file }}</span>
        <span v-if="size != null" class="chip">列表记录大小 {{ fmtBytes(size) }}</span>
        <div class="spacer" />
        <el-button size="small" @click="dl">下载原始包</el-button>
      </div>
      <div v-loading="loading" class="pd__body">
        <JsonViewer :text="text" />
      </div>
    </div>
  </el-drawer>
</template>

<style scoped>
.pd {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.pd__bar {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding: var(--sp-3) var(--sp-5);
  border-bottom: 1px solid var(--border);
}
.pd__body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: var(--sp-4) var(--sp-5);
}
</style>
