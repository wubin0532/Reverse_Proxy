// 统一时间格式化：随界面语言使用 zh-CN/en-US locale，24 小时制；空值或非法时间显示为 '-'
import i18n from '../locales'

export function formatTime(ts) {
  if (!ts) return '-'
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return '-'
  return d.toLocaleString(i18n.global.locale.value, { hour12: false })
}

// 人性化字节格式：1024 进制 B/KB/MB/GB/TB，小于 100 保留 1 位小数；空值或 0 显示 '0 B'
export function formatBytes(n) {
  if (!n || n <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n, i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return `${i === 0 || v >= 100 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}
