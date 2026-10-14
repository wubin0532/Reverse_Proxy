import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import Protection from './Protection.vue'

const request = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: request }))
let wrapper
const policies = [
  {
    id: 'bound',
    name: 'shared',
    enabled: true,
    global: false,
    ipListMode: 'blacklist',
    ipList: ['203.0.113.1'],
    uaList: []
  },
  { id: 'all', name: 'global fixture', enabled: false, global: true, ipList: [], uaList: [] },
  { id: 'unused', name: 'not bound', enabled: true, global: false, ipList: [], uaList: [] }
]
function setup(failSites = false) {
  request.get.mockImplementation((path) => {
    if (path === '/api/protection/policies') return Promise.resolve({ data: policies })
    if (path === '/api/protection/summary')
      return Promise.resolve({ data: { policies: 3, globalPolicies: 0, activeBans: 0, rejected: 4 } })
    if (path === '/api/sites')
      return failSites
        ? Promise.reject(new Error('offline'))
        : Promise.resolve({
            data: [{ id: 's', name: 'NAS', policyId: 'bound', rules: [{ id: 'r', name: 'drive', policyId: 'bound' }] }]
          })
    if (path === '/api/forwards') return Promise.resolve({ data: [{ id: 'f', name: 'ssh', policyId: 'bound' }] })
    return Promise.resolve({ data: [] })
  })
  wrapper = mount(Protection, { global: { plugins: [ElementPlus, i18n] } })
}
afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})
describe('Protection policy scope', () => {
  it('shows bound service names and distinguishes disabled global configuration', async () => {
    setup()
    await flushPromises()
    const scopes = wrapper.findAll('.policy-scope').map((x) => x.text())
    expect(scopes[0]).toContain('NAS / drive')
    expect(scopes[0]).toContain('TCP/UDP · ssh')
    expect(scopes[1]).toMatch(/已停用|Disabled/)
    expect(scopes[1]).toMatch(/所有业务入口|All service entrances/)
    expect(scopes[2]).toMatch(/未绑定|Not bound/)
    expect(wrapper.findAll('.policy-row')).toHaveLength(3)
  })
  it('marks binding scope as unknown when its source fails', async () => {
    setup(true)
    await flushPromises()
    expect(wrapper.findAll('.policy-scope')[0].text()).toMatch(/绑定范围待核查|Binding scope unavailable/)
    expect(wrapper.findAll('.policy-row')).toHaveLength(3)
    expect(wrapper.find('.el-alert').exists()).toBe(true)
  })
})
