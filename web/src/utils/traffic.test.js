import { describe, expect, it } from 'vitest'
import { calculateTrafficRates } from './traffic'

describe('calculateTrafficRates', () => {
  it('calculates independent site and rule byte rates', () => {
    const previous = { s1: { bytesIn: 100, bytesOut: 300, rules: { r1: { bytesIn: 40, bytesOut: 90 } } } }
    const current = { s1: { bytesIn: 160, bytesOut: 420, rules: { r1: { bytesIn: 50, bytesOut: 130 } } } }
    expect(calculateTrafficRates(current, previous, 2)).toEqual({
      s1: { bytesIn: 30, bytesOut: 60, rules: { r1: { bytesIn: 5, bytesOut: 20 } } }
    })
  })

  it('returns zero for the first sample and after counters reset', () => {
    const current = { s1: { bytesIn: 10, bytesOut: 20, rules: { r1: { bytesIn: 5, bytesOut: 8 } } } }
    expect(calculateTrafficRates(current, {}, 0).s1.bytesIn).toBe(0)
    const reset = calculateTrafficRates(
      current,
      { s1: { bytesIn: 100, bytesOut: 200, rules: { r1: { bytesIn: 50, bytesOut: 80 } } } },
      3
    )
    expect(reset.s1.bytesOut).toBe(0)
    expect(reset.s1.rules.r1.bytesIn).toBe(0)
  })
})
