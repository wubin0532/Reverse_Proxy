import { brotliCompressSync, constants, gzipSync } from 'node:zlib'
import { readFileSync, readdirSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'

// Compress once on the build host, rather than spending router CPU per request.
export function precompressedAssets() {
  let outDir
  return {
    name: 'precompressed-assets',
    apply: 'build',
    configResolved(config) {
      outDir = resolve(config.root, config.build.outDir)
    },
    closeBundle() {
      function visit(dir) {
        for (const entry of readdirSync(dir, { withFileTypes: true })) {
          const path = join(dir, entry.name)
          if (entry.isDirectory()) visit(path)
          else if (/\.(?:html|css|js|svg|json)$/.test(entry.name)) {
            const content = readFileSync(path)
            for (const [extension, compressed] of [
              ['gz', gzipSync(content, { level: 9 })],
              ['br', brotliCompressSync(content, { params: { [constants.BROTLI_PARAM_QUALITY]: 9 } })]
            ]) {
              if (compressed.length < content.length) writeFileSync(`${path}.${extension}`, compressed)
            }
          }
        }
      }
      visit(outDir)
    }
  }
}
