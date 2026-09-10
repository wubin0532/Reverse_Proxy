import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import Notifications from './Notifications.vue'

const requestMock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: requestMock }))

afterEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
})

describe('Notifications channel actions', () => {
  it('confirms before deleting a configured Telegram channel', async () => {
    requestMock.get.mockImplementation((path) =>
      Promise.resolve({
        data: path.includes('/settings')
          ? {
              types: ['site'],
              telegram: { enabled: true, configured: true, botTokenConfigured: true, chatId: '123', messageThreadId: 0 }
            }
          : []
      })
    )
    requestMock.delete.mockResolvedValue({
      data: {
        types: ['site'],
        telegram: { enabled: false, configured: false, botTokenConfigured: false, chatId: '', messageThreadId: 0 }
      }
    })
    const wrapper = mount(Notifications, { attachTo: document.body, global: { plugins: [ElementPlus, i18n] } })
    await flushPromises()
    const deleteButton = wrapper
      .findAll('button')
      .find((button) => button.text().includes(i18n.global.t('notifications.deleteChannel')))
    expect(deleteButton).toBeTruthy()
    await deleteButton.trigger('click')
    await new Promise((resolve) => setTimeout(resolve, 250))
    await flushPromises()
    expect(document.body.textContent).toContain(i18n.global.t('notifications.deleteConfirm'))
    const confirm = document.querySelector('.el-popconfirm__action .el-button--danger')
    expect(confirm).toBeTruthy()
    confirm.click()
    await flushPromises()
    expect(requestMock.delete).toHaveBeenCalledWith('/api/notifications/channels/telegram')
    expect(wrapper.text()).not.toContain('123')
    wrapper.unmount()
  })
})
