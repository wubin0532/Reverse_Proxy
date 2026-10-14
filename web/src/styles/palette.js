// 图表色板：uPlot 等 canvas 场景需要原始色值，无法读 CSS 变量。
// 这里的色值镜像 src/styles/theme.css 中的 --ap-chart-*，改动时必须同步两处。
export const chartPalette = {
  requests: '#087f8c', // --ap-chart-requests (= --ap-accent)
  resourceMemory: '#8274c6',
  resourceNetworkOut: '#39a381',
  bytesIn: '#0b8d99', // --ap-chart-bytes-in
  bytesInFill: 'rgba(11, 141, 153, 0.18)',
  bytesOut: '#b97837', // --ap-chart-bytes-out
  bytesOutFill: 'rgba(185, 120, 55, 0.15)'
}
