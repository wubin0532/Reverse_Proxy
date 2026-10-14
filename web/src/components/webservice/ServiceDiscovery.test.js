import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../../locales'
import ServiceDiscovery from './ServiceDiscovery.vue'
const request = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../../api', () => ({ default: request }))
let wrapper
function create() {
  wrapper = mount(ServiceDiscovery, {
    global: {
      plugins: [ElementPlus, i18n],
      stubs: {
        ElDialog: { template: '<div v-if="modelValue"><slot/><slot name="footer"/></div>', props: ['modelValue'] }
      }
    }
  })
  wrapper.vm.open()
  return wrapper
}
async function start() {
  await flushPromises()
  await wrapper.find('textarea').setValue('127.0.0.1')
  await wrapper
    .findAll('button')
    .find((b) => /开始发现|Start discovery/.test(b.text()))
    .trigger('click')
  await flushPromises()
}
afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})
describe('Service discovery drafts and job lifetime', () => {
  it('creates a disabled draft and labels TCP results without claiming HTTP health', async () => {
    request.post.mockResolvedValue({
      data: {
        id: 'job',
        status: 'completed',
        total: 1,
        completed: 1,
        results: [{ address: '127.0.0.1:8080', latencyMs: 1 }]
      }
    })
    create()
    await start()
    expect(wrapper.text()).toMatch(/TCP 可连接|TCP reachable/)
    await wrapper
      .findAll('button')
      .find((b) => /创建规则草稿|Create rule draft/.test(b.text()))
      .trigger('click')
    expect(wrapper.emitted('draft')[0][0]).toEqual({
      name: '127.0.0.1:8080',
      type: 'reverse',
      enabled: false,
      backends: ['http://127.0.0.1:8080']
    })
  })
  it('cancels a late start response after the dialog was closed', async () => {
    let resolve
    const pending = new Promise((ok) => {
      resolve = ok
    })
    request.post.mockImplementation((path) =>
      path === '/api/sites/discovery' ? pending : Promise.resolve({ data: null })
    )
    create()
    await start()
    await wrapper
      .findAll('button')
      .find((b) => /^(关闭|Close)$/.test(b.text()))
      .trigger('click')
    resolve({ data: { id: 'late', status: 'running', results: [], total: 1, completed: 0 } })
    await flushPromises()
    expect(request.post).toHaveBeenCalledWith('/api/sites/discovery/late/cancel')
    wrapper.vm.open()
    await flushPromises()
    expect(wrapper.text()).not.toContain('late')
  })
})
