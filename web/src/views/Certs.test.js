import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import Certs from './Certs.vue'
const api = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: api }))
let wrapper
const fixture = {
  id: 'import',
  name: 'fixture cert',
  source: 'uploaded',
  keyType: 'P256',
  enabled: true,
  domains: ['home.example.com'],
  status: 'ok',
  remainingDays: 89,
  usingSites: []
}
function setup() {
  i18n.global.locale.value = 'zh-CN'
  api.get.mockImplementation((path) => Promise.resolve({ data: path === '/api/certs' ? [fixture] : [] }))
  wrapper = mount(Certs, {
    attachTo: document.body,
    global: {
      plugins: [ElementPlus, i18n],
      stubs: { teleport: true, ElSelect: true, ElOption: true, ElDropdown: true }
    }
  })
}
afterEach(() => {
  wrapper?.unmount()
  document.body.innerHTML = ''
  vi.clearAllMocks()
})
describe('PEM import UI contract', () => {
  it('replaces the existing ID without requiring a DNS provider or ACME email', async () => {
    setup()
    api.post.mockResolvedValue({ data: fixture })
    await flushPromises()
    const action = wrapper
      .findAll('.mobile-cert button')
      .find((b) => b.text() === i18n.global.t('workspace.certs.replace'))
    await action.trigger('click')
    await flushPromises()
    const textareas = wrapper.findAll('.el-dialog textarea')
    await textareas[0].setValue('')
    await textareas[1].setValue('fixture-cert-pem')
    await textareas[2].setValue('fixture-key-pem')
    const save = wrapper.findAll('.el-dialog button').find((b) => b.text() === i18n.global.t('common.save'))
    await save.trigger('click')
    await flushPromises()
    expect(api.post).toHaveBeenCalledWith('/api/certs/import', {
      id: 'import',
      name: 'fixture cert',
      enabled: true,
      domains: [],
      certPEM: 'fixture-cert-pem',
      keyPEM: 'fixture-key-pem'
    })
    expect(api.put).not.toHaveBeenCalled()
  })
  it('keeps the replacement form open after a rejected import', async () => {
    setup()
    api.post.mockRejectedValue(new Error('pair mismatch'))
    await flushPromises()
    await wrapper
      .findAll('.mobile-cert button')
      .find((b) => b.text() === i18n.global.t('workspace.certs.replace'))
      .trigger('click')
    await flushPromises()
    const textareas = wrapper.findAll('.el-dialog textarea')
    await textareas[1].setValue('invalid cert')
    await textareas[2].setValue('invalid key')
    await wrapper
      .findAll('.el-dialog button')
      .find((b) => b.text() === i18n.global.t('common.save'))
      .trigger('click')
    await flushPromises()
    expect(wrapper.find('.el-dialog').isVisible()).toBe(true)
    expect(wrapper.findAll('.el-dialog textarea')[2].element.value).toBe('invalid key')
  })
  it('does not apply a delayed file read after the editor was reopened', async () => {
    setup()
    await flushPromises()
    const replace = () =>
      wrapper.findAll('.mobile-cert button').find((b) => b.text() === i18n.global.t('workspace.certs.replace'))
    await replace().trigger('click')
    await flushPromises()
    let resolve
    const pending = new Promise((done) => {
      resolve = done
    })
    const input = wrapper.find('.el-dialog input[type="file"]')
    Object.defineProperty(input.element, 'files', { value: [{ size: 10, text: () => pending }], configurable: true })
    await input.trigger('change')
    await wrapper
      .findAll('.el-dialog button')
      .find((b) => b.text() === i18n.global.t('common.cancel'))
      .trigger('click')
    await flushPromises()
    await replace().trigger('click')
    await flushPromises()
    resolve('old-file-private-material')
    await flushPromises()
    expect(wrapper.findAll('.el-dialog textarea')[1].element.value).toBe('')
  })
})

it('opens ACME advanced parameters and exposes the EAB explanation', async () => {
  setup()
  await flushPromises()
  await wrapper
    .findAll('button')
    .find((b) => b.text().includes(i18n.global.t('certs.add')))
    .trigger('click')
  await flushPromises()
  await wrapper
    .findAll('.el-radio')
    .find((e) => e.text() === i18n.global.t('workspace.certs.acme'))
    .trigger('click')
  await flushPromises()
  const advanced = wrapper.findAll('.el-collapse-item__header').find((e) => e.text().includes('高级签发参数'))
  expect(advanced).toBeDefined()
  await advanced.trigger('click')
  await flushPromises()
  expect(wrapper.find('.field-help[aria-label="EAB help"]').attributes('aria-description')).toContain('外部账户绑定')
})
