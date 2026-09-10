// 逗号分隔文本转数组：去空白、去空项；空输入返回 []
export function splitList(text) {
  return (text || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
}
