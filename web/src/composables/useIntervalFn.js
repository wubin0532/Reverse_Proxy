// 轮询定时器：start/stop 幂等，组件卸载时自动清理
import { onUnmounted, ref } from 'vue'

export function useIntervalFn(fn, interval) {
  const active = ref(false)
  let timer = null
  let running = false
  async function tick() {
    if (running || document.hidden) return
    running = true
    try {
      await fn()
    } catch {
      /* API errors are surfaced by the caller/interceptor. */
    } finally {
      running = false
    }
  }
  function resume() {
    if (active.value && !document.hidden) tick()
  }
  document.addEventListener('visibilitychange', resume)

  function start() {
    if (timer) return
    timer = setInterval(tick, interval)
    active.value = true
  }

  function stop() {
    clearInterval(timer)
    timer = null
    active.value = false
  }

  function restart() {
    stop()
    start()
  }

  onUnmounted(() => {
    stop()
    document.removeEventListener('visibilitychange', resume)
  })
  return { active, start, stop, restart }
}
