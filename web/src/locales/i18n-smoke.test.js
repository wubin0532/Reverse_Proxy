import { mount } from '@vue/test-utils'
import { createRouter, createMemoryHistory } from 'vue-router'
import { describe, expect, it } from 'vitest'
import i18n from '../locales'

describe('i18n smoke', () => {
  it('translates without runtime compile errors', () => {
    i18n.global.locale.value = 'zh-CN'
    expect(i18n.global.t('app.title')).toBe('andey-proxy 管理后台')
    expect(i18n.global.t('certs.emailPlaceholder')).toBe('ACME 账号邮箱，如 admin@example.com')
    expect(i18n.global.t('webService.redirectUrlPlaceholder')).toContain('{path}')
    expect(i18n.global.t('dashboard.issuesToHandle', { n: 3 })).toBe('3 项需要处理')
    i18n.global.locale.value = 'en-US'
    expect(i18n.global.t('app.title')).toBe('andey-proxy Admin')
    i18n.global.locale.value = 'zh-CN'
  })

  it('mounts a component using $t', () => {
    i18n.global.locale.value = 'zh-CN'
    const Comp = { template: '<p>{{ $t("login.subtitle") }}</p>' }
    const wrapper = mount(Comp, { global: { plugins: [i18n] } })
    expect(wrapper.text()).toBe('安全管理反向代理与网络入口')
  })
})
