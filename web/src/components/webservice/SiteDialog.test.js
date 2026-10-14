import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, expect, it, vi } from 'vitest'
import i18n from '../../locales'
import SiteDialog from './SiteDialog.vue'
const request = vi.hoisted(() => ({ put: vi.fn(), post: vi.fn() }))
vi.mock('../../api', () => ({ default: request }))
let wrapper
function open(listen) {
  i18n.global.locale.value = 'zh-CN'
  wrapper = mount(SiteDialog, {
    global: {
      plugins: [ElementPlus, i18n],
      stubs: {
        ElDialog: {
          props: ['modelValue'],
          template: '<section v-if="modelValue"><slot/><slot name="footer"/></section>'
        }
      }
    }
  })
  wrapper.vm.open({
    id: 'existing',
    revision: 7,
    name: 'TLS test',
    listen,
    tls: true,
    certId: 'cert',
    enabled: false,
    rules: [{ id: 'rule' }],
    defaultResponse: { statusCode: 404, body: 'not found' }
  })
}
afterEach(() => {
  wrapper?.unmount()
  vi.resetAllMocks()
})
it.each(['127.0.0.1:18846', '[::]:443', ':8080'])(
  'saves split %s without changing enabled state, rules or revision',
  async (listen) => {
    request.put.mockResolvedValue({ data: {} })
    open(listen)
    await flushPromises()
    expect(wrapper.find('input[aria-label="监听地址"]').element.value).toBe(
      listen.startsWith('[') ? '::' : listen.split(':')[0]
    )
    await wrapper
      .findAll('button')
      .find((b) => b.text() === '保存')
      .trigger('click')
    await flushPromises()
    expect(request.put).toHaveBeenCalledWith(
      '/api/sites/existing',
      expect.objectContaining({
        listen,
        expectedRevision: 7,
        enabled: false,
        rules: [{ id: 'rule' }],
        defaultResponse: { statusCode: 404, body: 'not found' }
      })
    )
  }
)
it('composes a changed IPv6 address and separate port', async () => {
  request.put.mockResolvedValue({ data: {} })
  open('127.0.0.1:18846')
  await flushPromises()
  await wrapper.find('input[aria-label="监听地址"]').setValue('2001:db8::2')
  await wrapper.find('input[aria-label="监听端口"]').setValue('8443')
  await wrapper
    .findAll('button')
    .find((b) => b.text() === '保存')
    .trigger('click')
  await flushPromises()
  expect(request.put).toHaveBeenCalledWith(
    '/api/sites/existing',
    expect.objectContaining({ listen: '[2001:db8::2]:8443' })
  )
})
