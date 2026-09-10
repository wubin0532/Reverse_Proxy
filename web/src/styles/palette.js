// 图表色板：uPlot 等 canvas 场景需要原始色值，无法读 CSS 变量。
// 这里的色值镜像 src/styles/theme.css 中的 --ap-chart-*，改动时必须同步两处。
export const chartPalette = {
  requests: '#14998b', // --ap-chart-requests (= --ap-accent)
  bytesIn: '#3b7dd8', // --ap-chart-bytes-in
  bytesInFill: 'rgba(59, 125, 216, 0.18)',
  bytesOut: '#d8863b', // --ap-chart-bytes-out
  bytesOutFill: 'rgba(216, 134, 59, 0.15)'
}
