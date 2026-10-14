// uPlot axis size includes the label, tick and gap. Measure formatted labels,
// rather than reserving a fixed gutter that clips wider byte-rate values.
export const chartAxisFont = '12px -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", sans-serif'
export function axisGutter(values, measure, minimum = 56) {
  let widest = 0
  for (const value of values || []) {
    if (value == null) continue
    for (const line of String(value).split('\n')) widest = Math.max(widest, measure(line))
  }
  return Math.max(minimum, Math.ceil(widest) + 20)
}
export function numericAxis(format, options = {}) {
  return {
    font: chartAxisFont,
    gap: 8,
    ticks: { size: 8 },
    size: (chart, values) => {
      chart.ctx.save()
      chart.ctx.font = chartAxisFont
      const size = axisGutter(values, (label) => chart.ctx.measureText(label).width)
      chart.ctx.restore()
      return size
    },
    values: (chart, splits) => splits.map((value) => (Number.isFinite(value) ? format(value) : null)),
    ...options
  }
}
