import { describe, it, expect, vi, afterEach } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { createI18n } from 'vue-i18n'
import Dashboard from './Dashboard.vue'
import zh from '../locales/zh-CN'
const request = vi.hoisted(() => {
  window.matchMedia ||= () => ({ matches: false, addEventListener() {}, removeEventListener() {} })
  window.ResizeObserver ||= class {
    observe() {}
    disconnect() {}
  }
  return { get: vi.fn() }
})
vi.mock('../api', () => ({ default: request }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
let wrapper
afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})
describe('Dashboard independent module loading', () => {
  it('keeps successful dashboard data when system info fails and does not fabricate unsupported CPU', async () => {
    request.get.mockImplementation((path) => {
      if (path === '/api/system/info') return Promise.reject(new Error('fixture unavailable'))
      if (path === '/api/dashboard')
        return Promise.resolve({
          data: {
            version: 'fixture-version',
            adminHttps: true,
            sites: [],
            issues: [],
            stats: {},
            firewall: { openwrt: false, rules: [] },
            recentErrors: [],
            lastUpdateEntries: []
          }
        })
      if (path === '/api/metrics')
        return Promise.resolve({
          data: {
            hostCpu: null,
            memoryPercent: null,
            processHeap: 1024,
            goroutines: 3,
            bytesInPerSecond: null,
            bytesOutPerSecond: null
          }
        })
      if (path === '/api/metrics/history') return Promise.resolve({ data: { points: [] } })
      if (path === '/api/dashboard/workspace')
        return Promise.resolve({ data: { ddns: [], certs: [], protection: { rejected: 7, activeBans: 1 } } })
      return Promise.resolve({ data: {} })
    })
    wrapper = mount(Dashboard, {
      global: {
        plugins: [ElementPlus, createI18n({ legacy: false, locale: 'zh-CN', messages: { 'zh-CN': zh } })],
        stubs: { ResourceChart: true, SiteTrafficChart: true, HealthBadge: true }
      }
    })
    await flushPromises()
    expect(wrapper.text()).toContain('fixture-version')
    expect(wrapper.text()).toContain('部分卡片加载失败')
    expect(wrapper.text()).toContain('不可用／待采样')
    expect(wrapper.text()).not.toContain('0.0%')
    expect(wrapper.text()).toContain('7')
    const headings = wrapper.findAll('h1,h2').map((h) => h.text())
    expect(headings[0]).toBe('概览')
    expect(headings.indexOf('资源趋势')).toBeLessThan(headings.indexOf('服务状态'))
  })
})
