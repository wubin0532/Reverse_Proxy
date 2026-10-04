import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus, { ElPopconfirm } from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import { useTunnels } from '../composables/useTunnels'
import TunnelRoutes from '../components/TunnelRoutes.vue'

const mock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../api', () => ({ default: mock }))
const wrappers = []
afterEach(() => {
  wrappers.splice(0).forEach((wrapper) => wrapper.unmount())
  vi.clearAllMocks()
  document.body.innerHTML = ''
})
function setupAccounts() {
  mock.get.mockImplementation((path) =>
    Promise.resolve({ data: path.endsWith('/runtime') ? { compatible: true } : [] })
  )
  mock.post.mockResolvedValue({ data: { id: 'new' } })
  mock.put.mockResolvedValue({ data: {} })
}
describe('Tunnel management', () => {
  it('sends a write-only token without view metadata and clears it after saving', async () => {
    setupAccounts()
    const Harness = defineComponent({ setup: useTunnels, template: '<div />' })
    const wrapper = mount(Harness, { global: { plugins: [i18n] } })
    wrappers.push(wrapper)
    await flushPromises()
    wrapper.vm.editInstance({ id: 'old', source: 'imported', tokenConfigured: true, name: 'Home' })
    expect(wrapper.vm.instanceForm.token).toBe('')
    wrapper.vm.instanceForm.token = 'NEW-PRIVATE-TOKEN'
    await wrapper.vm.saveInstance()
    expect(mock.put).toHaveBeenCalledWith(
      '/api/tunnels/instances/old',
      expect.objectContaining({ token: 'NEW-PRIVATE-TOKEN', source: 'imported' })
    )
    const payload = mock.put.mock.calls[0][1]
    expect(payload).not.toHaveProperty('id')
    expect(payload).not.toHaveProperty('tokenConfigured')
    expect(wrapper.vm.instanceForm.token).toBe('')
    expect(wrapper.vm.instanceOpen).toBe(false)
  })
  it('preserves account credentials when the edit token is blank', async () => {
    setupAccounts()
    const wrapper = mount(defineComponent({ setup: useTunnels, template: '<div />' }), { global: { plugins: [i18n] } })
    wrappers.push(wrapper)
    await flushPromises()
    wrapper.vm.editAccount({ id: 'account', name: 'Home', accountId: 'a'.repeat(32), tokenConfigured: true })
    await wrapper.vm.saveAccount()
    expect(mock.put).toHaveBeenCalledWith('/api/tunnels/accounts/account', {
      name: 'Home',
      accountId: 'a'.repeat(32),
      token: ''
    })
  })
  it('disables cloud sync for token-only instances and displays cloud routes separately', async () => {
    mock.get.mockResolvedValue({
      data: {
        routes: [],
        remote: [{ hostname: 'existing.example.com', service: 'http://192.0.2.1', editable: false }],
        digest: 'digest',
        readOnly: true,
        cloudError: '',
        services: {}
      }
    })
    const wrapper = mount(TunnelRoutes, {
      attachTo: document.body,
      props: { instance: { id: 'one', name: 'Home' } },
      global: { plugins: [ElementPlus, i18n] }
    })
    wrappers.push(wrapper)
    await flushPromises()
    const button = [...document.querySelectorAll('button')].find((item) =>
      item.textContent.includes(i18n.global.t('tunnel.sync'))
    )
    expect(button).toBeTruthy()
    expect(button.disabled).toBe(true)
    expect(document.body.textContent).toContain('existing.example.com')
    expect(document.body.textContent).toContain(i18n.global.t('tunnel.readonlyTip'))
    expect(mock.put).not.toHaveBeenCalled()
  })
  it('shows changed origins and acknowledges only the explicitly confirmed hostname', async () => {
    mock.get.mockResolvedValue({
      data: {
        routes: [],
        remote: [],
        digest: 'latest-digest',
        readOnly: false,
        cloudError: '',
        conflicts: [
          {
            hostname: 'app.example.com',
            service: 'http://192.0.2.99:80',
            desiredService: 'http://192.0.2.1:80',
            removing: false,
            recoverable: true
          }
        ]
      }
    })
    mock.post.mockResolvedValue({ data: null })
    const wrapper = mount(TunnelRoutes, {
      attachTo: document.body,
      props: { instance: { id: 'one', name: 'Home' } },
      global: { plugins: [ElementPlus, i18n] }
    })
    wrappers.push(wrapper)
    await flushPromises()
    expect(document.body.textContent).toContain('http://192.0.2.99:80')
    expect(document.body.textContent).toContain('http://192.0.2.1:80')
    expect(mock.post).not.toHaveBeenCalled()
    const confirm = wrapper
      .findAllComponents(ElPopconfirm)
      .find((item) => item.props('title') === i18n.global.t('tunnel.reconcileConfirm'))
    expect(confirm).toBeTruthy()
    confirm.vm.$emit('confirm')
    await flushPromises()
    expect(mock.post).toHaveBeenCalledWith(
      '/api/tunnels/instances/one/routes/reconcile',
      { digest: 'latest-digest', hostnames: ['app.example.com'] },
      { timeout: 45000 }
    )
    expect(mock.put).not.toHaveBeenCalled()
    expect(wrapper.emitted('saved')).toHaveLength(1)
  })
  it('keeps the conflict visible when acknowledgment fails and disables shared-route recovery', async () => {
    mock.get.mockResolvedValue({
      data: {
        routes: [],
        remote: [],
        digest: 'old-digest',
        readOnly: false,
        cloudError: '',
        conflicts: [
          { hostname: 'removed.example.com', service: 'http://192.0.2.99:80', removing: true, recoverable: true }
        ]
      }
    })
    mock.post.mockRejectedValue(new Error('cloud configuration changed'))
    const wrapper = mount(TunnelRoutes, {
      attachTo: document.body,
      props: { instance: { id: 'one', name: 'Home' } },
      global: { plugins: [ElementPlus, i18n] }
    })
    wrappers.push(wrapper)
    await flushPromises()
    const confirm = wrapper
      .findAllComponents(ElPopconfirm)
      .find((item) => item.props('title') === i18n.global.t('tunnel.reconcileConfirm'))
    confirm.vm.$emit('confirm')
    await flushPromises()
    expect(wrapper.emitted('saved')).toBeUndefined()
    expect(document.body.textContent).toContain('removed.example.com')
    mock.get.mockResolvedValue({
      data: {
        routes: [],
        remote: [],
        digest: 'new-digest',
        readOnly: true,
        cloudError: 'Other connector active',
        conflicts: [
          { hostname: 'removed.example.com', service: 'http://192.0.2.99:80', removing: true, recoverable: true }
        ]
      }
    })
    const refresh = [...document.querySelectorAll('button')].find((item) =>
      item.textContent.includes(i18n.global.t('tunnel.refreshCloud'))
    )
    refresh.click()
    await flushPromises()
    const recover = [...document.querySelectorAll('button')].find((item) =>
      item.textContent.includes(i18n.global.t('tunnel.reconcile'))
    )
    expect(recover.disabled).toBe(true)
  })
})
