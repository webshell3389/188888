<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { feApi, type FePhoto } from '@/api/feapi'
import EmptyState from '@/components/EmptyState.vue'
import { fmtBytes, tsShort } from '@/utils/format'

/** 照片：ph 端点回传（packets 索引 + /api/file/{packet_id} 取原始字节）。 */
const items = ref<FePhoto[]>([])
const loading = ref(false)
const preview = ref<FePhoto | null>(null)

async function load() {
  loading.value = true
  try {
    const r = await feApi.photos(120)
    items.value = r?.items ?? []
  } catch {
    items.value = []
  } finally {
    loading.value = false
  }
}
onMounted(load)

const totalSize = computed(() => items.value.reduce((a, p) => a + (Number(p.size) || 0), 0))
</script>

<template>
  <section class="app-card">
    <div class="app-card__head">
      <h3 class="app-card__title">
        <el-icon><Picture /></el-icon>照片
        <span class="chip">{{ items.length }} 张</span>
        <span class="chip mono">{{ fmtBytes(totalSize) }}</span>
      </h3>
      <el-button size="small" :loading="loading" @click="load">刷新</el-button>
    </div>

    <div class="app-card__body">
      <EmptyState v-if="!items.length && !loading" text="暂无照片回传（ph 端点）" />

      <div v-else class="grid">
        <div v-for="(p, i) in items" :key="i" class="ph" @click="preview = p">
          <div class="ph__thumb">
            <el-image :src="p.url" fit="cover" loading="lazy" :preview-src-list="[p.url || '']">
              <template #error>
                <div class="ph__err"><el-icon :size="20"><PictureFilled /></el-icon><span>无法预览</span></div>
              </template>
            </el-image>
          </div>
          <div class="ph__meta">
            <span class="mono ellipsis">{{ p.filename || '-' }}</span>
            <span class="t-3 num">{{ fmtBytes(p.size) }} · {{ tsShort(p.ts) }}</span>
          </div>
          <a class="ph__dl" :href="p.url" target="_blank" @click.stop>下载</a>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(168px, 1fr));
  gap: var(--sp-4);
}
.ph {
  position: relative;
  border: 1px solid var(--border);
  border-radius: var(--r-md);
  overflow: hidden;
  background: var(--bg-card-alt);
  cursor: pointer;
  transition: border-color var(--dur-fast) var(--ease), transform var(--dur-fast) var(--ease);
}
.ph:hover {
  border-color: var(--border-glow);
  transform: translateY(-2px);
}
.ph__thumb {
  height: 132px;
  background: var(--bg-input);
  display: grid;
  place-items: center;
  overflow: hidden;
}
.ph__thumb :deep(.el-image) {
  width: 100%;
  height: 100%;
}
.ph__err {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  color: var(--text-3);
  font-size: var(--fs-xs);
}
.ph__meta {
  padding: 6px var(--sp-3) var(--sp-3);
  display: flex;
  flex-direction: column;
  font-size: var(--fs-xs);
}
.ph__meta span:first-child {
  color: var(--text-2);
}
.ph__dl {
  position: absolute;
  right: 6px;
  top: 6px;
  padding: 2px 8px;
  border-radius: var(--r-pill);
  background: rgba(11, 15, 23, 0.72);
  color: #e6edf7;
  font-size: var(--fs-xs);
  opacity: 0;
  transition: opacity var(--dur-fast) var(--ease);
}
.ph:hover .ph__dl {
  opacity: 1;
}
</style>
