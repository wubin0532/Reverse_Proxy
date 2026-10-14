<template>
  <div ref="host" class="resource-chart" :aria-label="label" role="img" />
  <el-empty v-if="!points.length" :description="label" :image-size="48" />
</template>
<script setup>
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import uPlot from 'uplot'
import 'uplot/dist/uPlot.min.css'
import { chartPalette } from '../styles/palette'
import { resourceSeries, chartTime, chartRate } from '../utils/resource-series'
import { numericAxis, chartAxisFont } from '../utils/chart-axis'
import { useI18n } from 'vue-i18n'
const { t, locale } = useI18n()
const props = defineProps({
  points: { type: Array, default: () => [] },
  label: { type: String, default: 'CPU / Memory %' },
  sampleSeconds: { type: Number, default: 5 },
  network: { type: Boolean, default: false }
})
const host = ref()
let chart, observer
function render() {
  if (!host.value) return
  if (!props.points.length) {
    chart?.destroy()
    chart = null
    return
  }
  const data = resourceSeries(props.points, props.sampleSeconds, props.network)
  if (chart) {
    chart.setData(data)
    return
  }
  chart = new uPlot(
    {
      width: Math.max(200, host.value.clientWidth),
      height: 240,
      padding: [12, 40, 6, 12],
      scales: {
        x: { time: true },
        y: props.network ? { range: (u, min, max) => [0, Math.max(1024, max || 0) * 1.1] } : { range: [0, 100] }
      },
      axes: [
        {
          font: chartAxisFont,
          space: 110,
          values: (u, v) => v.map((x) => (Number.isFinite(x) ? chartTime(x, props.sampleSeconds >= 60) : null))
        },
        numericAxis((x) => (props.network ? chartRate(x) : x + '%'))
      ],
      series: [
        { label: t('logs.colTime'), value: (u, v) => chartTime(v, props.sampleSeconds >= 60) },
        {
          label: props.network ? t('dashboard.trafficIn') : 'CPU %',
          stroke: chartPalette.requests,
          width: 2,
          spanGaps: false
        },
        {
          label: props.network ? t('dashboard.trafficOut') : t('workspace.dashboard.hostMemory') + ' %',
          stroke: props.network ? chartPalette.resourceNetworkOut : chartPalette.resourceMemory,
          width: 2,
          spanGaps: false
        }
      ]
    },
    data,
    host.value
  )
}
watch(() => [props.points, props.sampleSeconds], render)
watch(locale, () => {
  chart?.destroy()
  chart = null
  render()
})
onMounted(() => {
  render()
  observer = new ResizeObserver(() => {
    if (chart && host.value) chart.setSize({ width: Math.max(200, host.value.clientWidth), height: 240 })
  })
  observer.observe(host.value)
})
onBeforeUnmount(() => {
  observer?.disconnect()
  chart?.destroy()
})
</script>
<style scoped>
.resource-chart {
  width: 100%;
  min-width: 0;
  overflow: visible;
}
</style>
