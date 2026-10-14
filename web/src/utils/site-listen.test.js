import { describe, expect, it } from 'vitest'
import { parseSiteListen, composeSiteListen, isValidListenHost } from './site-listen'
describe('Site listen editing compatibility', () => {
  it.each(['127.0.0.1:18846', '[::]:8080', '[2001:db8::1]:443', ':8080', 'localhost:1234'])(
    'round trips %s',
    (listen) => {
      const { listenHost, listenPort } = parseSiteListen(listen)
      expect(composeSiteListen(listenHost, listenPort)).toBe(listen)
    }
  )
  it('normalizes legacy bare ports and preserves malformed values for correction', () => {
    expect(parseSiteListen('8080')).toEqual({ listenHost: '', listenPort: 8080 })
    expect(parseSiteListen('bad:value')).toEqual({ listenHost: 'bad:value', listenPort: undefined })
  })
  it('rejects combined inputs and out of range ports', () => {
    expect(isValidListenHost('127.0.0.1:8080')).toBe(false)
    expect(isValidListenHost('[::]')).toBe(false)
    expect(isValidListenHost('https://example.com')).toBe(false)
    for (const port of [0, 65536, 1.5, undefined]) expect(() => composeSiteListen('::', port)).toThrow()
  })
})
