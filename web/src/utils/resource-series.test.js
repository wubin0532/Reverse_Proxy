import { describe, it, expect } from 'vitest'
import { resourceSeries, chartRate, chartTime } from './resource-series'
const at = (seconds) => new Date(seconds * 1000).toISOString()
describe('resource history gaps', () => {
  it('does not connect missing samples across sleep or delayed collection', () => {
    const data = resourceSeries(
      [
        { time: at(100), cpu: 10, memory: 30 },
        { time: at(105), cpu: 20, memory: 35 },
        { time: at(700), cpu: 25, memory: 40 }
      ],
      5
    )
    expect(data[0]).toEqual([100, 105, 110, 700])
    expect(data[1]).toEqual([10, 20, null, 25])
    expect(data[2]).toEqual([30, 35, null, 40])
  })
  it('uses the minute cadence for daily history and preserves unknown network rates', () => {
    const data = resourceSeries(
      [
        { time: at(100), bytesInPerSecond: null, bytesOutPerSecond: null },
        { time: at(160), bytesInPerSecond: 2048, bytesOutPerSecond: 1024 }
      ],
      60,
      true
    )
    expect(data).toEqual([
      [100, 160],
      [null, 2048],
      [null, 1024]
    ])
  })
})

it('formats small network rates without rounded duplicate labels and uses a 24-hour clock', () => {
  expect(chartRate(256)).toBe('256 B/s')
  expect(chartRate(1536)).toBe('1.5 KB/s')
  expect(chartRate(1048576)).toBe('1.0 MB/s')
  expect(chartTime(100)).toMatch(/^\d{2}:\d{2}:\d{2}$/)
  expect(chartTime(100, true)).toMatch(/^\d{2}\/\d{2} \d{2}:\d{2}$/)
})
