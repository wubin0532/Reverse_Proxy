import { ref } from 'vue'

const key = 'andey-proxy.restore-warnings'
function readWarnings() {
  try {
    const value = JSON.parse(sessionStorage.getItem(key) || '[]')
    return Array.isArray(value) ? value.filter((item) => typeof item === 'string') : []
  } catch {
    return []
  }
}

// Contains runtime warnings only, never the imported configuration or passwords.
export const restoreWarnings = ref(readWarnings())
export function setRestoreWarnings(warnings) {
  restoreWarnings.value = Array.isArray(warnings) ? warnings.filter((item) => typeof item === 'string') : []
  try {
    if (restoreWarnings.value.length) sessionStorage.setItem(key, JSON.stringify(restoreWarnings.value))
    else sessionStorage.removeItem(key)
  } catch {
    // The reactive notice still works when browser storage is unavailable.
  }
}
