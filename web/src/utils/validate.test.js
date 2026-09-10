import { describe, expect, it } from 'vitest'
import { isValidHostPort, isValidIPOrCIDR, isValidIPv6, isValidListen, isValidPort } from './validate'

describe('isValidPort', () => {
  it('accepts 1-65535 integers', () => {
    expect(isValidPort('1')).toBe(true)
    expect(isValidPort('65535')).toBe(true)
    expect(isValidPort('0')).toBe(false)
    expect(isValidPort('65536')).toBe(false)
    expect(isValidPort('abc')).toBe(false)
    expect(isValidPort('')).toBe(false)
  })
})

describe('isValidListen', () => {
  it('accepts bare port, :port and host:port', () => {
    expect(isValidListen('8080')).toBe(true)
    expect(isValidListen(':8080')).toBe(true)
    expect(isValidListen('0.0.0.0:443')).toBe(true)
    expect(isValidListen('[::1]:443')).toBe(true)
  })
  it('rejects malformed addresses', () => {
    expect(isValidListen('')).toBe(false)
    expect(isValidListen(':')).toBe(false)
    expect(isValidListen('1.2.3.4:0')).toBe(false)
    expect(isValidListen('1.2.3.4:99999')).toBe(false)
    expect(isValidListen('a:b:c')).toBe(false)
    expect(isValidListen('::1:443')).toBe(false)
  })
})

describe('isValidHostPort', () => {
  it('requires a non-empty host', () => {
    expect(isValidHostPort('192.168.1.10:3389')).toBe(true)
    expect(isValidHostPort('nas.local:80')).toBe(true)
    expect(isValidHostPort('[fd00::1]:80')).toBe(true)
    expect(isValidHostPort(':3389')).toBe(false)
    expect(isValidHostPort('192.168.1.10')).toBe(false)
    expect(isValidHostPort('192.168.1.10:0')).toBe(false)
  })
})

describe('isValidIPv6', () => {
  it('accepts full, compressed and IPv4-mapped forms', () => {
    expect(isValidIPv6('::1')).toBe(true)
    expect(isValidIPv6('::')).toBe(true)
    expect(isValidIPv6('fe80::1')).toBe(true)
    expect(isValidIPv6('2001:0db8:0000:0000:0000:ff00:0042:8329')).toBe(true)
    expect(isValidIPv6('::ffff:192.168.0.1')).toBe(true)
  })
  it('rejects invalid forms', () => {
    expect(isValidIPv6('')).toBe(false)
    expect(isValidIPv6('12345::')).toBe(false)
    expect(isValidIPv6('1::2::3')).toBe(false)
    expect(isValidIPv6('1:2:3:4:5:6:7:8:9')).toBe(false)
    expect(isValidIPv6('fe80::192.168.999.1')).toBe(false)
  })
})

describe('isValidIPOrCIDR', () => {
  it('accepts IPs and CIDRs', () => {
    expect(isValidIPOrCIDR('10.0.0.5')).toBe(true)
    expect(isValidIPOrCIDR('192.168.1.0/24')).toBe(true)
    expect(isValidIPOrCIDR('0.0.0.0/0')).toBe(true)
    expect(isValidIPOrCIDR('fe80::1')).toBe(true)
    expect(isValidIPOrCIDR('fe80::/10')).toBe(true)
  })
  it('rejects bad values', () => {
    expect(isValidIPOrCIDR('')).toBe(false)
    expect(isValidIPOrCIDR('999.1.1.1')).toBe(false)
    expect(isValidIPOrCIDR('01.2.3.4')).toBe(false)
    expect(isValidIPOrCIDR('10.0.0.1/33')).toBe(false)
    expect(isValidIPOrCIDR('fe80::/129')).toBe(false)
    expect(isValidIPOrCIDR('10.0.0.1/24/1')).toBe(false)
  })
})
