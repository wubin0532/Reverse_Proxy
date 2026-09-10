// 客户端格式校验：与后端保持一致（internal/forward/api.go、internal/webproxy/api.go），仅用于提前提示，后端仍是最终裁决

export function isValidPort(value) {
  if (!/^\d+$/.test(String(value))) return false
  const port = Number(value)
  return port >= 1 && port <= 65535
}

// 拆分 host:port；host 可省略（":8080"），IPv6 需用 [] 包裹
function splitHostPort(value) {
  const v = String(value || '').trim()
  if (!v) return null
  if (v.startsWith('[')) {
    const m = /^\[([0-9a-fA-F:.]+)\]:(\d+)$/.exec(v)
    return m ? { host: m[1], port: m[2] } : null
  }
  const idx = v.lastIndexOf(':')
  if (idx < 0) return null
  const host = v.slice(0, idx)
  if (host.includes(':')) return null
  return { host, port: v.slice(idx + 1) }
}

// 监听地址：[host:]port，也允许裸端口（后端会补成 ":port"）
export function isValidListen(value) {
  const v = String(value || '').trim()
  if (!v) return false
  if (!v.includes(':')) return isValidPort(v)
  const hp = splitHostPort(v)
  return !!hp && isValidPort(hp.port)
}

// 目标地址：host:port，host 必填（IP 或主机名），多个用英文逗号分隔
export function isValidHostPort(value) {
  const hp = splitHostPort(value)
  return !!hp && hp.host !== '' && isValidPort(hp.port)
}

export function isValidIPv4(value) {
  const parts = String(value).split('.')
  return parts.length === 4 && parts.every((p) => /^\d{1,3}$/.test(p) && Number(p) <= 255 && String(Number(p)) === p)
}

export function isValidIPv6(value) {
  const v = String(value)
  if (v.length > 45 || !/^[0-9a-fA-F:.]+$/.test(v)) return false
  const halves = v.split('::')
  if (halves.length > 2) return false
  const groups = [...halves[0].split(':'), ...(halves.length === 2 ? halves[1].split(':') : [])].filter(Boolean)
  let v4Weight = 0
  if (groups.length && groups[groups.length - 1].includes('.')) {
    if (!isValidIPv4(groups.pop())) return false
    v4Weight = 2
  }
  if (!groups.every((g) => /^[0-9a-fA-F]{1,4}$/.test(g))) return false
  const total = groups.length + v4Weight
  return halves.length === 2 ? total < 8 : total === 8
}

// IP 或 CIDR（IPv4/IPv6 均可）
export function isValidIPOrCIDR(value) {
  const v = String(value || '').trim()
  if (!v) return false
  const [ip, prefix] = v.split('/')
  const isV4 = isValidIPv4(ip)
  if (!isV4 && !isValidIPv6(ip)) return false
  if (prefix === undefined) return true
  if (v.split('/').length > 2 || !/^\d+$/.test(prefix)) return false
  return Number(prefix) <= (isV4 ? 32 : 128)
}

export function splitList(text) {
  return (text || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}

// 以下为 el-form rules 用的 validator 工厂，错误消息走 i18n

export function listenValidator(t) {
  return (_rule, value, done) => {
    if (isValidListen(value)) done()
    else done(new Error(t('validate.listenFormat')))
  }
}

export function targetsValidator(t) {
  return (_rule, value, done) => {
    const list = splitList(value)
    if (list.length && list.every(isValidHostPort)) done()
    else done(new Error(t('validate.targetFormat')))
  }
}

// 返回第一条无效项，无效时给出提示文案，全部有效返回 ''
export function ipListError(t, text) {
  const bad = splitList(text).find((item) => !isValidIPOrCIDR(item))
  return bad ? t('validate.ipOrCidr', { value: bad }) : ''
}

export function ipListValidator(t) {
  return (_rule, value, done) => {
    const msg = ipListError(t, value)
    if (msg) done(new Error(msg))
    else done()
  }
}
