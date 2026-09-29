<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import * as echarts from 'echarts/core'
import { BarChart, GaugeChart, LineChart, PieChart } from 'echarts/charts'
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
  DatasetComponent,
  TitleComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import type { EChartsOption } from 'echarts'

echarts.use([
  BarChart,
  LineChart,
  PieChart,
  GaugeChart,
  GridComponent,
  LegendComponent,
  TooltipComponent,
  DatasetComponent,
  TitleComponent,
  CanvasRenderer,
])

const props = withDefaults(
  defineProps<{
    option: EChartsOption
    height?: string
    /** 主题切换后需重建实例（echarts 无 CSS 变量感知） */
    themeKey?: string | number
  }>(),
  { height: '260px', themeKey: '' },
)

const el = ref<HTMLDivElement | null>(null)
const chart = shallowRef<echarts.ECharts | null>(null)
let ro: ResizeObserver | null = null

function baseStyle() {
  const cs = getComputedStyle(document.documentElement)
  return {
    text: cs.getPropertyValue('--text-2').trim() || '#9fb0c8',
    line: cs.getPropertyValue('--border').trim() || '#1e2a3d',
  }
}

function render() {
  if (!el.value) return
  if (!chart.value) {
    chart.value = echarts.init(el.value, undefined, { renderer: 'canvas' })
  }
  const s = baseStyle()
  const merged: EChartsOption = {
    textStyle: { color: s.text, fontFamily: 'inherit' },
    grid: { left: 44, right: 16, top: 28, bottom: 28, containLabel: true },
    tooltip: {
      backgroundColor: 'rgba(11,15,23,.92)',
      borderColor: s.line,
      textStyle: { color: '#e6edf7', fontSize: 12 },
    },
    ...props.option,
  }
  chart.value.setOption(merged, true)
}

onMounted(() => {
  render()
  ro = new ResizeObserver(() => chart.value?.resize())
  if (el.value) ro.observe(el.value)
})

// 主题切换 → 重建实例，保证配色跟随
watch(
  () => props.themeKey,
  () => {
    chart.value?.dispose()
    chart.value = null
    render()
  },
)

watch(
  () => props.option,
  () => render(),
  { deep: true },
)

onBeforeUnmount(() => {
  ro?.disconnect()
  chart.value?.dispose()
  chart.value = null
})
</script>

<template>
  <div ref="el" class="echart" :style="{ height }" />
</template>

<style scoped>
.echart {
  width: 100%;
}
</style>
