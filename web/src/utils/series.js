// 站点分钟级流量序列（GET /api/sites/{id}/series）→ uPlot 数据列
// points 元素为 [minuteUnix, requests, bytesIn, bytesOut]
export function seriesToChartData(view) {
  const xs = []
  const requests = []
  const bytesIn = []
  const bytesOut = []
  for (const p of view?.points || []) {
    if (!Array.isArray(p) || p.length < 4) continue
    xs.push(p[0])
    requests.push(p[1] || 0)
    bytesIn.push(p[2] || 0)
    bytesOut.push(p[3] || 0)
  }
  return { xs, requests, bytesIn, bytesOut }
}

export function hasAnyTraffic(data) {
  if (!data || !data.xs.length) return false
  const nonZero = (arr) => arr.some((v) => v > 0)
  return nonZero(data.requests) || nonZero(data.bytesIn) || nonZero(data.bytesOut)
}

// 按请求数降序取前 n 个站点（用于仪表盘默认选中的站点）
export function topSitesByRequests(sites = [], n = 5) {
  return [...sites].sort((a, b) => (b.requests || 0) - (a.requests || 0)).slice(0, n)
}
