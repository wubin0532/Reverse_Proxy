import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../../locales'
import RuleEditor from './RuleEditor.vue'

const request = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn() }))
vi.mock('../../api', () => ({ default: request }))
const deferred = () => {
  let resolve, reject
  const promise = new Promise((ok, fail) => {
    resolve = ok
    reject = fail
  })
  return { promise, resolve, reject }
}
const rule = {
  id: 'r',
  name: 'fixture',
  type: 'reverse',
  enabled: true,
  backends: ['http://127.0.0.1:8080'],
  revision: 2
}
let wrapper
function create() {
  wrapper = mount(RuleEditor, {
    props: { site: { id: 's', revision: 6 } },
    global: {
      plugins: [ElementPlus, i18n],
      stubs: {
        ElDialog: { template: '<div v-if="modelValue"><slot/><slot name="footer"/></div>', props: ['modelValue'] }
      }
    }
  })
  return wrapper
}
const confirm = () => wrapper.findAll('button').find((b) => b.text() === i18n.global.t('webService.confirmOk'))
afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})

describe('Rule editor health drafts', () => {
  it('ignores a late health response from a previous opening of the same rule', async () => {
    const first = deferred(),
      second = deferred()
    let healthCalls = 0
    request.get.mockImplementation((path) =>
      !path.endsWith('/health') ? Promise.resolve({ data: [] }) : ++healthCalls === 1 ? first.promise : second.promise
    )
    request.put.mockResolvedValue({ data: {} })
    create().vm.open(rule, 0)
    await flushPromises()
    wrapper.vm.open({ ...rule, revision: 9 }, 0)
    await flushPromises()
    second.resolve({
      data: {
        conf: {
          enabled: true,
          type: 'http',
          path: '/current',
          intervalSeconds: 30,
          timeoutSeconds: 4,
          rise: 2,
          fall: 3
        }
      }
    })
    await flushPromises()
    first.resolve({ data: { conf: { enabled: true, type: 'tcp', intervalSeconds: 90, timeoutSeconds: 3 } } })
    await flushPromises()
    await confirm().trigger('click')
    await flushPromises()
    expect(request.put).toHaveBeenCalledTimes(1)
    expect(request.put.mock.calls[0][1]).toMatchObject({
      expectedRevision: 9,
      healthCheck: { type: 'http', path: '/current', intervalSeconds: 30 }
    })
    expect(request.put.mock.calls[0][0]).toBe('/api/sites/s/rules/r')
  })

  it('keeps saving disabled on load failure and allows an explicit retry', async () => {
    let healthCalls = 0
    request.get.mockImplementation((path) => {
      if (!path.endsWith('/health')) return Promise.resolve({ data: [] })
      if (++healthCalls === 1) return Promise.reject(new Error('offline'))
      return Promise.resolve({
        data: { conf: { enabled: false, type: 'tcp', intervalSeconds: 10, timeoutSeconds: 3 } }
      })
    })
    request.put.mockResolvedValue({ data: {} })
    create().vm.open(rule, 0)
    await flushPromises()
    expect(confirm().attributes('disabled')).toBeDefined()
    const retry = wrapper.find('.health-retry')
    expect(retry.exists()).toBe(true)
    await retry.trigger('click')
    await flushPromises()
    expect(confirm().attributes('disabled')).toBeUndefined()
    await confirm().trigger('click')
    await flushPromises()
    expect(request.get.mock.calls.filter(([path]) => path.endsWith('/health'))).toHaveLength(2)
    expect(request.put).toHaveBeenCalledTimes(1)
    expect(request.put.mock.calls[0][1].healthCheck.enabled).toBe(false)
  })
})

describe('Rule editor discovery and save lifetime', () => {
  it('preserves the disabled discovery draft when saving', async () => {
    request.get.mockResolvedValue({ data: [] })
    request.post.mockResolvedValue({ data: {} })
    create().vm.open({ name: 'discovered', type: 'reverse', enabled: false, backends: ['http://127.0.0.1:8080'] })
    await flushPromises()
    await confirm().trigger('click')
    await flushPromises()
    expect(request.post).toHaveBeenCalledWith(
      '/api/sites/s/rules',
      expect.objectContaining({ name: 'discovered', enabled: false, expectedRevision: 6 })
    )
  })

  it('does not close or mark a newer draft busy when an old save completes', async () => {
    const pending = deferred()
    request.get.mockImplementation((path) =>
      Promise.resolve({ data: path.endsWith('/health') ? { conf: { enabled: false } } : [] })
    )
    request.put.mockReturnValue(pending.promise)
    create().vm.open(rule, 0)
    await flushPromises()
    await confirm().trigger('click')
    wrapper.vm.open({ name: 'second draft', type: 'reverse', backends: ['http://127.0.0.1:8081'] })
    await flushPromises()
    pending.resolve({ data: {} })
    await flushPromises()
    expect(confirm()).toBeDefined()
    expect(confirm().attributes('disabled')).toBeUndefined()
    expect(wrapper.findAll('input').some((input) => input.element.value === 'second draft')).toBe(true)
    expect(request.put).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
})
