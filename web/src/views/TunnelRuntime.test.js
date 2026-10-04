import { flushPromises, mount } from '@vue/test-utils'
import ElementPlus from 'element-plus'
import { afterEach, describe, expect, it, vi } from 'vitest'
import TunnelRuntime from '../components/TunnelRuntime.vue'
import i18n from '../locales'
const mock = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('../api', () => ({ default: mock }))
let wrapper
const release = {
  id: 'release',
  version: '2026.9.3',
  asset: 'cloudflared-linux-amd64',
  size: 40000000,
  checkedAt: '2026-10-03T12:00:00Z'
}
const runtime = {
  goos: 'linux',
  goarch: 'amd64',
  downloadSupported: true,
  compatible: true,
  source: 'system',
  version: '2026.9.0'
}
function button(label) {
  return [...document.querySelectorAll('button')].find((el) => el.textContent.trim() === i18n.global.t(label))
}
async function start(props = {}) {
  mock.get.mockResolvedValue({ data: release })
  wrapper = mount(TunnelRuntime, {
    attachTo: document.body,
    props: { runtime, instances: [{ id: 'one', name: 'Home', enabled: true }], ...props },
    global: { plugins: [ElementPlus, i18n] }
  })
  await flushPromises()
}
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.clearAllMocks()
  vi.useRealTimers()
  document.body.innerHTML = ''
})
describe('Official cloudflared installer', () => {
  it('requires a password and explicit restart confirmation and preserves a failed form', async () => {
    await start()
    button('tunnel.installRuntime').click()
    await flushPromises()
    expect(document.body.textContent).toContain('Home')
    let submit = button('tunnel.confirmDownload')
    expect(submit.disabled).toBe(true)
    const input = document.querySelector('input[type="password"]')
    input.value = 'fixture-password'
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
    expect(submit.disabled).toBe(true)
    const checkbox = document.querySelector('input[type="checkbox"]')
    checkbox.click()
    await flushPromises()
    expect(submit.disabled).toBe(false)
    mock.post.mockRejectedValue(new Error('not enough disk space'))
    submit.click()
    await flushPromises()
    expect(input.value).toBe('fixture-password')
    expect(document.body.textContent).toContain(i18n.global.t('tunnel.restartConfirm'))
    expect(mock.post).toHaveBeenCalledWith('/api/tunnels/runtime/install', {
      releaseId: 'release',
      password: 'fixture-password',
      restartRunning: true,
      affectedInstances: ['one']
    })
  })
  it('restores background progress and a failure after reopening the page', async () => {
    vi.useFakeTimers()
    const op = {
      id: 'op',
      status: 'running',
      stage: 'downloadRuntime',
      version: '2026.9.3',
      downloadedBytes: 100,
      totalBytes: 1000
    }
    await start({ runtime: { ...runtime, operation: op } })
    expect(document.body.textContent).toContain(i18n.global.t('tunnel.downloadTask'))
    expect(wrapper.emitted('busy').at(-1)).toEqual([true])
    mock.get.mockResolvedValue({ data: { ...op, status: 'failed', error: 'checksum failure' } })
    await vi.advanceTimersByTimeAsync(1000)
    await flushPromises()
    expect(document.body.textContent).toContain('checksum failure')
    expect(wrapper.emitted('busy').at(-1)).toEqual([false])
    expect(wrapper.emitted('installed')).toBeUndefined()
  })
  it('does not request or enable automatic installation on MIPS', async () => {
    await start({ runtime: { ...runtime, goarch: 'mipsle', downloadSupported: false } })
    expect(mock.get).not.toHaveBeenCalled()
    expect(button('tunnel.installRuntime').disabled).toBe(true)
    expect(document.body.textContent).toContain(i18n.global.t('tunnel.downloadUnsupported'))
  })
})
