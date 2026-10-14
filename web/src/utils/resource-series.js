// Data gaps must be explicit: uPlot spanGaps:false cannot infer missed samples.
export function resourceSeries(points = [], sampleSeconds = 5, network = false) {
  const xs = [],
    first = [],
    second = []
  let previous = null
  for (const p of points) {
    const time = Date.parse(p.time) / 1000
    if (!Number.isFinite(time) || (previous != null && time <= previous)) continue
    if (previous != null && time - previous > sampleSeconds * 2.5) {
      xs.push(previous + sampleSeconds)
      first.push(null)
      second.push(null)
    }
    xs.push(time)
    first.push(network ? (p.bytesInPerSecond ?? null) : (p.cpu ?? null))
    second.push(network ? (p.bytesOutPerSecond ?? null) : (p.memory ?? null))
    previous = time
  }
  return [xs, first, second]
}

export function chartTime(seconds, daily = false) {
  if (!Number.isFinite(seconds)) return '-'
  const date = new Date(seconds * 1000)
  const pad = (v) => String(v).padStart(2, '0')
  const clock = `${pad(date.getHours())}:${pad(date.getMinutes())}`
  return daily ? `${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${clock}` : `${clock}:${pad(date.getSeconds())}`
}

export function chartRate(bytes) {
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s']
  let value = Math.max(0, bytes),
    unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`
}
