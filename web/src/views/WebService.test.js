import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import WebService from './WebService.vue'

const requestMock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: requestMock }))

const sites = [
  { id: 's1', name: '主站', listen: ':443', tls: true, enabled: true, status: 'listening', rules: [] },
  { id: 's2', name: '内网站', listen: ':8080', tls: false, enabled: true, status: 'listening', rules: [{ id: 'r1', name: '代理', type: 'reverse', enabled: true, backends: ['http://127.0.0.1'] }] }
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
})
