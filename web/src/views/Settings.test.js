import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, expect, it, vi } from 'vitest'
import i18n from '../locales'
import Settings from './Settings.vue'
let wrapper
const openAccountSecurity = vi.fn()
function create() {
  i18n.global.locale.value = 'zh-CN'
  wrapper = mount(Settings, {
    global: {
      plugins: [ElementPlus, i18n],
      provide: { openAccountSecurity },
      stubs: { SystemMaintenance: { template: '<section data-test="maintenance">升级与备份操作</section>' } }
    }
  })
}
afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})
it('removes overview-only status panels and preserves account management', async () => {
  create()
  expect(wrapper.find('h1').text()).toBe('系统设置')
  expect(wrapper.text()).not.toContain('系统运行信息')
  expect(wrapper.text()).not.toContain('防火墙自动放行')
  expect(wrapper.text()).not.toContain('平台架构')
  await wrapper
    .findAll('button')
    .find((b) => b.text() === '打开账户安全')
    .trigger('click')
  expect(openAccountSecurity).toHaveBeenCalledOnce()
})
it('keeps update and backup operations accessible from the maintenance tab', async () => {
  create()
  expect(wrapper.find('[data-test="maintenance"]').exists()).toBe(false)
  await wrapper
    .findAll('[role="tab"]')
    .find((e) => e.text() === '升级与备份')
    .trigger('click')
  await flushPromises()
  expect(wrapper.find('[data-test="maintenance"]').isVisible()).toBe(true)
})
