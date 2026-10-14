import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../../locales'
import BackendPoolManager from './BackendPoolManager.vue'
const request = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../../api', () => ({ default: request }))
const pool = {
  id: 'p',
  revision: 5,
  name: 'shared',
  maxConcurrent: 10,
  nodes: [{ url: 'http://localhost:8080', enabled: true, weight: 2, maxConcurrent: 0 }],
  healthCheck: { enabled: false, type: 'tcp', path: '/', intervalSeconds: 10, timeoutSeconds: 3, rise: 1, fall: 2 }
}
let wrapper
function create() {
  i18n.global.locale.value = 'zh-CN'
  wrapper = mount(BackendPoolManager, {
    global: {
      plugins: [ElementPlus, i18n],
      stubs: {
        ElDialog: {
          template: '<section v-if="modelValue"><slot/><slot name="footer"/></section>',
          props: ['modelValue']
        }
      }
    }
  })
  wrapper.vm.open()
  return wrapper
}
afterEach(() => {
  wrapper?.unmount()
  vi.resetAllMocks()
})
describe('Backend pool drafts', () => {
  it('preserves nodes and expected revision when editing a reactive pool', async () => {
    request.get.mockResolvedValue({ data: [structuredClone(pool)] })
    request.put.mockResolvedValue({ data: {} })
    create()
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((b) => b.text() === '编辑')
      .trigger('click')
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((b) => b.text() === '保存')
      .trigger('click')
    await flushPromises()
    expect(request.put).toHaveBeenCalledWith(
      '/api/backend-pools/p',
      expect.objectContaining({ revision: 5, nodes: pool.nodes })
    )
  })
  it('keeps a conflicted draft open and blocks repeated overwrites', async () => {
    request.get.mockResolvedValue({ data: [structuredClone(pool)] })
    request.put.mockRejectedValue({ response: { status: 409 } })
    create()
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((b) => b.text() === '编辑')
      .trigger('click')
    await flushPromises()
    await wrapper
      .findAll('button')
      .find((b) => b.text() === '保存')
      .trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('草稿已保留')
    expect(
      wrapper
        .findAll('button')
        .find((b) => b.text() === '保存')
        .attributes('disabled')
    ).toBeDefined()
  })
})
