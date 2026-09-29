<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useDashStore } from '@/stores/dash'
import EmptyState from '@/components/EmptyState.vue'
import { tsTime } from '@/utils/format'

/**
 * 样本端 /log.html 同步打点（exploit 链在设备上的实时 stdout）。
 *
 * 数据源：/__dash/api 的 log 窗口（console.jsonl tail，默认 150 条）。
 * 前端做**累积缓冲**：页面开着期间新到达的打点会合并保留，避免窗口滚动导致旧行丢失。
 * 后端若后续开放 probe_log 表的只读面（/__panel/api/logs），此页可无缝切全量。
 */
const dash = useDashStore()

const kw = ref('')
const autoScroll = ref(true)
const bodyRef = ref<HTMLDivElement | null>(null)

/** 累积缓冲：以 (ts,id,text) 为键合并，跨轮询保留 */
const buffer = ref<Array<{ ts: string; channel?: string; id?: string | number; text?: string }>>([])

watch(
  () => dash.logs,
  (logs) => {
    const seen = new Set(buffer.value.map((b) => `${b.ts}|${b.id}|${b.text}`))
    for (const l of logs ?? []) {
      const k = `${l.ts}|${l.id}|${l.text}`
      if (!seen.has(k)) {
        seen.add(k)
        buffer.value.push(l)
      }
    }
    // 上限保护：最多保留 5000 行
    if (buffer.value.length > 5000) buffer.value = buffer.value.slice(-5000)
  },
  { immediate: true, deep: true },
)

const filtered = computed(() => {
  const k = kw.value.trim().toLowerCase()
  if (!k) return buffer.value
  return buffer.value.filter((l) => String(l.text ?? '').toLowerCase().includes(k))
})

watch(
  () => filtered.value.length,
  async () => {
    if (!autoScroll.value) return
    await nextTick()
    const el = bodyRef.value
    if (el) el.scrollTop = el.scrollHeight
  },
)

const levelOf = (text?: string) => {
  const t = String(text ?? '').toLowerCase()
  if (/(fail|error|fatal|panic)/.test(t)) return 'err'
  if (/(complete|success|ok)/.test(t)) return 'ok'
  if (/(warn|retry|skip)/.test(t)) return 'warn'
  return 'plain'
}

function clearBuffer() {
  buffer.value = []
}
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><ChatLineSquare /></el-icon>日志
        <span class="chip">{{ filtered.length }} / {{ buffer.length }}（累积）</span>
        <span class="chip">窗口 {{ dash.logs.length }}</span>
      </h3>
      <div class="row">
        <el-checkbox v-model="autoScroll" size="small">自动贴底</el-checkbox>
        <el-button size="small" @click="clearBuffer">清空缓冲</el-button>
        <el-input v-model="kw" size="small" placeholder="过滤日志文本" clearable style="width: 240px" />
      </div>
    </div>

    <div class="app-card__body app-card__body--flush">
      <EmptyState v-if="!filtered.length" text="暂无打点（样本端尚未写 /log.html）" />
      <div v-else ref="bodyRef" class="log">
        <div v-for="(l, i) in filtered" :key="i" class="log__row" :class="`log__row--${levelOf(l.text)}`">
          <span class="log__ts mono">{{ tsTime(l.ts) }}</span>
          <span class="log__ch mono">{{ l.channel || '-' }}</span>
          <span class="log__id mono num">{{ l.id ?? '-' }}</span>
          <span class="log__text mono">{{ l.text }}</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.log {
  height: calc(100vh - 240px);
  overflow: auto;
  padding: var(--sp-3) 0;
}
.log__row {
  display: grid;
  grid-template-columns: 84px 40px 46px 1fr;
  gap: var(--sp-3);
  padding: 2px var(--sp-5);
  font-size: var(--fs-sm);
  line-height: 1.65;
  border-left: 2px solid transparent;
}
.log__row:hover {
  background: var(--bg-hover);
}
.log__row--ok {
  border-left-color: rgba(52, 211, 153, 0.5);
}
.log__row--warn {
  border-left-color: rgba(251, 191, 36, 0.5);
}
.log__row--err {
  border-left-color: rgba(248, 113, 113, 0.6);
  background: rgba(248, 113, 113, 0.05);
}
.log__ts,
.log__ch,
.log__id {
  color: var(--text-3);
}
.log__ch,
.log__id {
  text-align: right;
}
.log__text {
  white-space: pre-wrap;
  word-break: break-all;
}
.log__row--err .log__text {
  color: var(--c-err);
}
.log__row--ok .log__text {
  color: var(--c-ok);
}
</style>
