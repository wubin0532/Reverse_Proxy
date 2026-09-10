import { describe, expect, it } from 'vitest'
import { hasAnyTraffic, seriesToChartData, topSitesByRequests } from './series'

describe('seriesToChartData', () => {
  it('splits points into column arrays', () => {
    const view = {
      siteId: 's1',
      step: 60,
      from: 1000,
      to: 1120,
      points: [
        [1000, 3, 100, 200],
        [1060, 0, 0, 0],
        [1120, 5, 300, 400]
      ]
    }
    expect(seriesToChartData(view)).toEqual({
      xs: [1000, 1060, 1120],
      requests: [3, 0, 5],
      bytesIn: [100, 0, 300],
      bytesOut: [200, 0, 400]
    })
  })

  it('handles empty and malformed input', () => {
    expect(seriesToChartData(null)).toEqual({ xs: [], requests: [], bytesIn: [], bytesOut: [] })
    expect(seriesToChartData({})).toEqual({ xs: [], requests: [], bytesIn: [], bytesOut: [] })
    expect(seriesToChartData({ points: [[1, 2], 'bad', [3, 4, 5, 6]] })).toEqual({
      xs: [3],
      requests: [4],
      bytesIn: [5],
      bytesOut: [6]
    })
  })
})

describe('hasAnyTraffic', () => {
  it('is false for empty or all-zero series', () => {
    expect(hasAnyTraffic(null)).toBe(false)
    expect(hasAnyTraffic({ xs: [1], requests: [0], bytesIn: [0], bytesOut: [0] })).toBe(false)
  })

  it('is true when any column has traffic', () => {
    expect(hasAnyTraffic({ xs: [1], requests: [0], bytesIn: [7], bytesOut: [0] })).toBe(true)
    expect(hasAnyTraffic({ xs: [1, 2], requests: [0, 1], bytesIn: [0, 0], bytesOut: [0, 0] })).toBe(true)
  })
})

describe('topSitesByRequests', () => {
  it('sorts by requests desc and limits', () => {
    const sites = [{ id: 'a', requests: 5 }, { id: 'b', requests: 50 }, { id: 'c' }, { id: 'd', requests: 10 }]
    expect(topSitesByRequests(sites, 2).map((s) => s.id)).toEqual(['b', 'd'])
    expect(topSitesByRequests(sites).map((s) => s.id)).toEqual(['b', 'd', 'a', 'c'])
  })
})
