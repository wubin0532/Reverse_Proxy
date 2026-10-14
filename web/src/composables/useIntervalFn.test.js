import { describe, it, vi, expect, afterEach } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount, flushPromises } from '@vue/test-utils'
import { useIntervalFn } from './useIntervalFn'
afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
})
describe('visibility aware polling', () => {
  it('does not overlap pending requests or poll hidden documents and cleans up', async () => {
    vi.useFakeTimers()
    const hidden = vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    let complete
    const fn = vi.fn(
      () =>
        new Promise((resolve) => {
          complete = resolve
        })
    )
    let poll
    const wrapper = mount(
      defineComponent({
        setup() {
          poll = useIntervalFn(fn, 1000)
          poll.start()
          return () => h('div')
        }
      })
    )
    await vi.advanceTimersByTimeAsync(3000)
    expect(fn).toHaveBeenCalledTimes(1)
    complete()
    await flushPromises()
    hidden.mockReturnValue(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(fn).toHaveBeenCalledTimes(1)
    hidden.mockReturnValue(false)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(fn).toHaveBeenCalledTimes(2)
    complete()
    await flushPromises()
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(3000)
    expect(fn).toHaveBeenCalledTimes(2)
  })
})
