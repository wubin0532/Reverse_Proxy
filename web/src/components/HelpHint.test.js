import { mount, flushPromises } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, expect, it, vi } from 'vitest'
import HelpHint from './HelpHint.vue'
let wrapper
afterEach(() => {
  wrapper?.unmount()
  document.body.innerHTML = ''
})
it('offers a focusable explanation without submitting the parent form', async () => {
  const submit = vi.fn()
  wrapper = mount(
    {
      components: { HelpHint },
      template: '<form @submit.prevent="submit"><HelpHint label="TLS" text="请选择匹配域名的证书" /></form>',
      setup: () => ({ submit })
    },
    { attachTo: document.body, global: { plugins: [ElementPlus] } }
  )
  const button = wrapper.find('button')
  expect(button.attributes('type')).toBe('button')
  expect(button.attributes('aria-description')).toContain('请选择匹配域名的证书')
  await button.trigger('click')
  await flushPromises()
  await vi.waitFor(() =>
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain('请选择匹配域名的证书')
  )
  expect(submit).not.toHaveBeenCalled()
})

it('shows the explanation when the question mark is hovered', async () => {
  wrapper = mount(HelpHint, {
    props: { label: 'CORS', text: '允许指定来源的跨域读取' },
    attachTo: document.body,
    global: { plugins: [ElementPlus] }
  })
  await wrapper.find('button').trigger('mouseenter')
  await vi.waitFor(() =>
    expect(document.querySelector('[role="tooltip"]')?.textContent).toContain('允许指定来源的跨域读取')
  )
})
