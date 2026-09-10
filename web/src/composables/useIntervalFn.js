// 轮询定时器：start/stop 幂等，组件卸载时自动清理
import { onUnmounted, ref } from 'vue'

export function useIntervalFn(fn, interval) {
  const active = ref(false)
  let timer = null

  function start() {
    if (timer) return
    timer = setInterval(fn, interval)
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

  onUnmounted(stop)
  return { active, start, stop, restart }
}
