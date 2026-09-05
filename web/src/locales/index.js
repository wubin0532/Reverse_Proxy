import { createI18n } from 'vue-i18n'
import zhCN from './zh-CN'
import enUS from './en-US'

export const LANG_STORAGE_KEY = 'ap-lang'

function detectLocale() {
  const saved = localStorage.getItem(LANG_STORAGE_KEY)
  if (saved === 'zh-CN' || saved === 'en-US') return saved
  return (navigator.language || '').toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
}

const i18n = createI18n({
  legacy: false,
  locale: detectLocale(),
  fallbackLocale: 'zh-CN',
  messages: { 'zh-CN': zhCN, 'en-US': enUS }
})

function applyDocument() {
  document.documentElement.lang = i18n.global.locale.value
  document.title = i18n.global.t('app.title')
}

applyDocument()

export function setLocale(lang) {
  i18n.global.locale.value = lang
  localStorage.setItem(LANG_STORAGE_KEY, lang)
  applyDocument()
}

export default i18n
