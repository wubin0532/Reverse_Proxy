import { isValidIPv6, isValidPort } from './validate'

// Existing API/configuration keeps host:port; only the editing fields are split.
export function parseSiteListen(value) {
  const text = String(value || '').trim()
  const match = text.match(/^\[([^\]]+)\]:(\d+)$/) || text.match(/^([^:]*):(\d+)$/)
  if (match) return { listenHost: match[1], listenPort: Number(match[2]) }
  if (/^\d+$/.test(text)) return { listenHost: '', listenPort: Number(text) }
  // Preserve a malformed legacy value so the user can correct it explicitly.
  return { listenHost: text, listenPort: undefined }
}
export function isValidListenHost(value) {
  const host = String(value || '').trim()
  if (host.includes(':')) return isValidIPv6(host)
  return !/[\s\[\]\/\\?#@]/.test(host)
}
export function composeSiteListen(host, port) {
  host = String(host || '').trim()
  if (!isValidListenHost(host) || !isValidPort(port)) throw new Error('Invalid listen address or port')
  return `${host.includes(':') ? `[${host}]` : host}:${Number(port)}`
}
