import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import i18n from '../locales'
import TCPTrap from './TCPTrap.vue'

const request = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../api', () => ({ default: request }))
let wrapper
const state = () => ({
  config: { enabled: false, listenHost: '0.0.0.0', ports: [18870], allowIps: ['192.0.2.0/24'], banSeconds: 3600 },
  running: false,
  listening: [],
  captured: 0,
  exempted: 0
})
beforeEach(() => {
  request.get.mockResolvedValue({ data: state() })
  request.put.mockResolvedValue({ data: { ...state(), running: true, listening: ['0.0.0.0:18870'] } })
})
afterEach(() => {
  wrapper?.unmount()
  vi.clearAllMocks()
})
async function page() {
  wrapper = mount(TCPTrap, { global: { plugins: [ElementPlus, i18n] } })
  await flushPromises()
  return wrapper
}
describe('TCP trap configuration', () => {
  it('saves parsed ports and CIDRs without overwriting a draft during polling', async () => {
    await page()
    wrapper.vm.edit()
    wrapper.vm.draft.enabled = true
    wrapper.vm.portText = '18870， 18871\n18872'
    wrapper.vm.allowText = '192.0.2.0/24\n 2001:db8::/64 \n'
    await wrapper.vm.load()
    expect(request.get).toHaveBeenCalledTimes(1)
    await wrapper.vm.save()
    expect(request.put).toHaveBeenCalledWith(
      '/api/protection/trap',
      expect.objectContaining({
        enabled: true,
        ports: [18870, 18871, 18872],
        allowIps: ['192.0.2.0/24', '2001:db8::/64']
      })
    )
    expect(wrapper.vm.editing).toBe(false)
  })
  it('rejects duplicate or malformed ports before calling the backend', async () => {
    await page()
    wrapper.vm.edit()
    wrapper.vm.draft.enabled = true
    for (const ports of ['18870,18870', '1.5', '65536', '80xyz', '']) {
      wrapper.vm.portText = ports
      await wrapper.vm.save()
    }
    expect(request.put).not.toHaveBeenCalled()
    expect(wrapper.vm.editing).toBe(true)
  })
  it('preserves a failed binding draft and shows the backend error', async () => {
    await page()
    wrapper.vm.edit()
    wrapper.vm.portText = '18871'
    request.put.mockRejectedValueOnce({ response: { data: { msg: '端口被占用' } } })
    await wrapper.vm.save()
    expect(wrapper.vm.portText).toBe('18871')
    expect(wrapper.vm.editing).toBe(true)
    expect(wrapper.vm.error).toBe('端口被占用')
  })
})
