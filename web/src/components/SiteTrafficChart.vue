<template>
  <div class="traffic-chart">
    <div v-show="!empty" ref="host" class="chart-host" />
    <el-empty v-if="empty" :description="$t('chart.empty')" :image-size="60" />
  </div>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { useI18n } from 'vue-i18n'
import { hasAnyTraffic } from '../utils/series'
import { formatBytes } from '../utils/format'
import { chartPalette } from '../styles/palette'

const props = defineProps({
  data: { type: Object, default: null } // seriesToChartData() 的输出
})

const { t } = useI18n()
const host = ref(null)
const empty = computed(() => !hasAnyTraffic(props.data))
let chart = null
let observer = null

function buildOptions(width) {
  return {
    width,
    height: 220,
    padding: [10, 8, 0, 0],
    legend: { show: true },
    scales: {
      x: { time: true },
      y: { range: (u, min, max) => [0, Math.max(1, max)] },
      bytes: { range: (u, min, max) => [0, Math.max(1, max)] }
    },
    axes: [
      { size: 60 },
      { scale: 'y', size: 50, grid: { show: true } },
      {
        scale: 'bytes',
        side: 1,
        size: 60,
        grid: { show: false },
        values: (u, splits) => splits.map((v) => formatBytes(v))
      }
    ],
    series: [
      {},
      {
        label: t('chart.requestsPerMin'),
        scale: 'y',
        stroke: chartPalette.requests,
        width: 2,
        points: { show: false }
      },
      {
        label: t('chart.bytesIn'),
        scale: 'bytes',
        stroke: chartPalette.bytesIn,
        width: 1,
        fill: chartPalette.bytesInFill,
        points: { show: false }
      },
      {
        label: t('chart.bytesOut'),
        scale: 'bytes',
        stroke: chartPalette.bytesOut,
        width: 1,
        fill: chartPalette.bytesOutFill,
        points: { show: false }
      }
    ]
  }
}

function toUplotData() {
  const d = props.data
  return [d.xs, d.requests, d.bytesIn, d.bytesOut]
}

function render() {
  if (empty.value || !host.value) {
    destroy()
    return
  }
  const width = host.value.clientWidth || 600
  if (chart) {
    chart.setData(toUplotData())
    chart.setSize({ width, height: 220 })
  } else {
    chart = new uPlot(buildOptions(width), toUplotData(), host.value)
  }
}

function destroy() {
  chart?.destroy()
  chart = null
}

watch(() => props.data, render, { deep: true })
onMounted(() => {
  render()
  observer = new ResizeObserver(() => {
    if (chart && host.value) chart.setSize({ width: host.value.clientWidth || 600, height: 220 })
  })
  if (host.value) observer.observe(host.value)
})
onBeforeUnmount(() => {
  observer?.disconnect()
  destroy()
})
</script>

<style scoped>
.chart-host {
  width: 100%;
  min-height: 220px;
}
:deep(.u-legend) {
  font-size: 12px;
}
</style>
