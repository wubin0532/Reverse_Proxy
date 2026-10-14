import { describe, it, expect } from 'vitest'
import { axisGutter } from './chart-axis'

describe('chart axis gutters', () => {
  it('reserves the measured width plus ticks and gap for wide network labels', () => {
    const widths = { '0 B/s': 32, '120.0 MB/s': 78.5 }
    expect(axisGutter(['0 B/s', '120.0 MB/s'], (label) => widths[label])).toBe(99)
  })
  it('ignores hidden labels and uses the widest line of a multiline label', () => {
    expect(axisGutter([null, 'first\nsecond'], (line) => ({ first: 12, second: 48 })[line])).toBe(68)
    expect(axisGutter(null, () => 0)).toBe(56)
  })
})
