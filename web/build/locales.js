import path from 'node:path'
import { generateJavaScript } from '@intlify/bundle-utils'

// This project has four explicit locale files; no filesystem glob is needed.
// Keep Intlify's compiler, with the same JIT AST output and CSP-safe runtime.
export function localeResources(root) {
  const files = new Set(
    ['zh-CN.js', 'en-US.js', 'tunnel.zh-CN.js', 'tunnel.en-US.js'].map((name) =>
      path.resolve(root, 'src/locales', name)
    )
  )
  let production = false
  return {
    name: 'andey-locale-resources',
    enforce: 'pre',
    configResolved(config) {
      production = config.isProduction
    },
    transform(source, id) {
      if (!files.has(id)) return
      const result = generateJavaScript(source, {
        filename: id,
        env: production ? 'production' : 'development',
        jit: true,
        sourceMap: false,
        strictMessage: true,
        onError(message) {
          throw new Error(`${id}: ${message}`)
        }
      })
      return { code: result.code, map: { mappings: '' } }
    }
  }
}
