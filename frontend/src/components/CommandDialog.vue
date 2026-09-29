<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useCommandStore } from '@/stores/command'
import { COMMAND_PARAM_HINT, type CommandType } from '@/api/types'

/**
 * 指令下发弹窗（设备列表批量入口 / 单台快捷入口 / 设备控制台共用）。
 *
 * 批量异步：targets 多台时一次请求写入各设备独立队列，各自被自己的 beacon 领走，
 * 一台不在线不会阻塞其它设备。后端 v7+ 生效；老后端降级为全局单队列（UI 明示）。
 */
const props = defineProps<{
  modelValue: boolean
  /** 目标设备身份（uuid / ip）；多台 = 批量 */
  targets?: string[]
}>()
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'done'): void }>()

const cmd = useCommandStore()
const type = ref<CommandType>('basic_info')
const paramsText = ref(COMMAND_PARAM_HINT.basic_info)
const once = ref(true)

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    type.value = 'basic_info'
    paramsText.value = COMMAND_PARAM_HINT.basic_info
    once.value = true
    void cmd.peek()
  },
)

const list = computed(() => (props.targets ?? []).filter(Boolean))
const isBatch = computed(() => list.value.length > 1)
const label = computed(() =>
  list.value.length === 0
    ? '未指定设备 · 全局队列'
    : isBatch.value
      ? `${list.value.length} 台设备（批量异步）`
      : list.value[0],
)

function onTypeChange(v: CommandType) {
  paramsText.value = COMMAND_PARAM_HINT[v] || '{}'
}

async function submit() {
  const ok = isBatch.value
    ? (await cmd.armBatch(list.value, type.value, paramsText.value, once.value)) > 0
    : await cmd.arm(type.value, paramsText.value, once.value, list.value[0])
  if (!ok) return
  emit('done')
  emit('update:modelValue', false)
}
</script>

<template>
  <el-dialog
    :model-value="modelValue"
    :title="`下发指令 · ${label}`"
    width="600"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <el-alert v-if="!list.length" type="warning" :closable="false" show-icon class="tip">
      <template #title>未指定目标设备</template>
      将写入全局队列：由下一个到达的任意 beacon 领走，不保证是某台指定设备。
    </el-alert>
    <el-alert v-else-if="cmd.perDevice === false" type="warning" :closable="false" show-icon class="tip">
      <template #title>当前后端不支持按设备队列</template>
      需要 Go 版（device_commands 表）才能做到"每台独立、批量异步"；
      本次将退化为全局单队列：任意一台领走即清。
    </el-alert>
    <el-alert v-else-if="isBatch" type="success" :closable="false" show-icon class="tip">
      <template #title>批量异步 · {{ list.length }} 台</template>
      每台各写一条独立队列，各自领取；单台不在线不会阻塞其它设备。
    </el-alert>

    <el-form label-position="top" size="small" class="form">
      <el-form-item v-if="isBatch" label="目标设备">
        <div class="chips">
          <span v-for="t in list" :key="t" class="chip chip--brand mono">{{ t }}</span>
        </div>
      </el-form-item>
      <el-form-item label="指令类型（白名单，与样本端命令循环一一对应）">
        <el-select v-model="type" style="width: 100%" @change="onTypeChange">
          <el-option v-for="t in cmd.types" :key="t" :label="t" :value="t" />
        </el-select>
      </el-form-item>
      <el-form-item label="params（JSON）">
        <el-input v-model="paramsText" type="textarea" :rows="4" class="mono" />
      </el-form-item>
      <el-form-item>
        <el-checkbox v-model="once">once（单发即清）</el-checkbox>
      </el-form-item>
    </el-form>

    <div class="foot">
      <span class="t-3" style="font-size: var(--fs-xs)">
        exit 不在可下发列表内：会终止设备端 c2_agent 的 beacon 循环且不可恢复
      </span>
    </div>

    <template #footer>
      <el-button @click="emit('update:modelValue', false)">取消</el-button>
      <el-button type="primary" :loading="cmd.busy" @click="submit">
        {{ isBatch ? `批量武装 ${list.length} 台` : '武装' }}
      </el-button>
    </template>
  </el-dialog>
</template>

<style scoped>
.tip {
  margin-bottom: var(--sp-4);
  background: transparent;
  border: 1px solid var(--border);
}
.form {
  margin-top: var(--sp-2);
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  max-height: 92px;
  overflow: auto;
}
.foot {
  padding-top: var(--sp-2);
}
</style>
