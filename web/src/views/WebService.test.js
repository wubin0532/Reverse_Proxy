import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import WebService from './WebService.vue'

const requestMock = vi.hoisted(() => {
  // jsdom 缺少 uPlot 依赖的浏览器 API，须在模块加载前补齐
  if (!window.matchMedia) {
    window.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} })
  }
  if (!window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  return { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }
})
vi.mock('../api', () => ({ default: requestMock }))

const sites = [
  { id: 's1', name: '主站', listen: ':443', tls: true, enabled: true, status: 'listening', rules: [] },
  {
    id: 's2',
    name: '内网站',
    listen: ':8080',
    tls: false,
    enabled: true,
    status: 'listening',
    rules: [{ id: 'r1', name: '代理', type: 'reverse', enabled: true, backends: ['http://127.0.0.1'] }]
  }
]

function setupRequests(siteRows = sites) {
  requestMock.get.mockImplementation((path) => {
    if (path === '/api/sites') return Promise.resolve({ data: structuredClone(siteRows) })
    if (path === '/api/certs') return Promise.resolve({ data: [] })
    if (path === '/api/sites/stats') return Promise.resolve({ data: {} })
    return Promise.resolve({ data: [] })
  })
}

afterEach(() => vi.clearAllMocks())

describe('WebService master detail layout', () => {
  it('selects the first site and switches the visible rule panel', async () => {
    setupRequests()
    const wrapper = mount(WebService, { global: { plugins: [ElementPlus, i18n] } })
    await flushPromises()
    expect(wrapper.findAll('.site-tab')).toHaveLength(2)
    expect(wrapper.find('.site-identity h2').text()).toBe('主站')
    await wrapper.findAll('.site-tab')[1].trigger('click')
    expect(wrapper.find('.site-identity h2').text()).toBe('内网站')
    expect(wrapper.find('.rule-name b').text()).toBe('代理')
    wrapper.unmount()
  })

  it('shows the site empty state without a rule panel', async () => {
    setupRequests([])
    const wrapper = mount(WebService, { global: { plugins: [ElementPlus, i18n] } })
    await flushPromises()
    expect(wrapper.find('.rules-panel').exists()).toBe(false)
    expect(wrapper.text()).toContain(i18n.global.t('webService.empty'))
    wrapper.unmount()
  })
  it('filters rules and sends revisions for switch and keyboard reorder', async () => {
    const rows = [
      {
        ...sites[1],
        revision: 7,
        rules: [
          { ...sites[1].rules[0], revision: 3, group: 'NAS' },
          { id: 'r2', name: '其他', type: 'fileserver', enabled: false, rootDir: '/tmp', revision: 2 }
        ]
      }
    ]
    setupRequests(rows)
    requestMock.post.mockResolvedValue({})
    requestMock.put.mockResolvedValue({})
    const wrapper = mount(WebService, { global: { plugins: [ElementPlus, i18n] } })
    await flushPromises()
    expect(wrapper.findAll('.rule-row')).toHaveLength(2)
    await wrapper.find('.rule-filters input').setValue('NAS')
    expect(wrapper.findAll('.rule-row')).toHaveLength(1)
    await wrapper.find('.rule-actions .el-switch').trigger('click')
    await flushPromises()
    expect(requestMock.post).toHaveBeenCalledWith('/api/sites/s2/rules/r1/toggle', null, {
      headers: { 'If-Match': '3' }
    })
    await wrapper
      .findAll('.rule-actions button')
      .find((button) => button.text() === '↓')
      .trigger('click')
    await flushPromises()
    expect(requestMock.put).toHaveBeenCalledWith('/api/sites/s2/rules/order', {
      ruleIds: ['r2', 'r1'],
      expectedRevision: 7
    })
    wrapper.unmount()
  })
  it('shows effective inherited protection and never counts ordinary request headers', async () => {
    const rows = [
      { ...sites[1], policyId: 'site-policy', rules: [{ ...sites[1].rules[0], headers: { 'X-Business': 'value' } }] }
    ]
    setupRequests(rows)
    const base = requestMock.get.getMockImplementation()
    requestMock.get.mockImplementation((path) => {
      if (path === '/api/protection/policies')
        return Promise.resolve({
          data: [
            { id: 'site-policy', name: 'site', enabled: true, waf: { enabled: true } },
            {
              id: 'global-policy',
              name: 'all',
              enabled: true,
              global: true,
              cc: { enabled: true }
            }
          ]
        })
      if (path === '/api/protection/summary') return Promise.resolve({ data: { activeBans: 2 } })
      return base(path)
    })
    const wrapper = mount(WebService, { global: { plugins: [ElementPlus, i18n] } })
    await flushPromises()
    const badge = wrapper.find('.rule-guard').text()
    expect(badge).toMatch(/继承站点|Site policy/)
    expect(badge).toMatch(/全局 1|Global 1/)
    expect(badge).toMatch(/临时封禁 2|Temporary bans 2/)
    expect(badge).not.toMatch(/规则控制|Rule controls/)
    wrapper.unmount()
  })

  it('keeps the graph collapsed by default and rejects a previous site response', async () => {
    const old = {},
      current = {}
    old.promise = new Promise((resolve) => (old.resolve = resolve))
    current.promise = new Promise((resolve) => (current.resolve = resolve))
    setupRequests()
    const base = requestMock.get.getMockImplementation()
    requestMock.get.mockImplementation((path, ...args) => {
      if (path === '/api/sites/s1/series') return old.promise
      if (path === '/api/sites/s2/series') return current.promise
      return base(path, ...args)
    })
    const wrapper = mount(WebService, { global: { plugins: [ElementPlus, i18n], stubs: { SiteTrafficChart: true } } })
    await flushPromises()
    expect(wrapper.find('.site-chart').exists()).toBe(false)
    expect(requestMock.get.mock.calls.some(([p]) => p.endsWith('/series'))).toBe(false)
    const chartButton = wrapper.findAll('.toolbar-actions button').find((b) => /流量趋势|Traffic trend/.test(b.text()))
    await chartButton.trigger('click')
    await wrapper.findAll('.site-tab')[1].trigger('click')
    current.resolve({ data: { points: [[100, 22, 220, 2200]] } })
    await flushPromises()
    old.resolve({ data: { points: [[100, 11, 110, 1100]] } })
    await flushPromises()
    expect(wrapper.findComponent({ name: 'SiteTrafficChart' }).props('data').requests).toEqual([22])
    wrapper.unmount()
  })
})
