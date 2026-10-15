/**
 * 产物同步脚本：把 frontend/dist 同步到后端静态目录。
 *
 *   node scripts/sync-dist.mjs                  # 默认 -> ../www/admin
 *   node scripts/sync-dist.mjs --target python  # -> ../boot-analysis/backend/frontend
 *   node scripts/sync-dist.mjs --target all     # 两处都同步
 *
 * 说明：后端按契约红线只提供静态挂点，前端不做任何后端代码改动。
 * 采用 hash 路由 + 相对 base，产物可直接落任意静态目录。
 */
import { existsSync, rmSync, cpSync, mkdirSync, readdirSync, statSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const dist = join(root, 'dist')

const TARGETS = {
  go: resolve(root, '..', 'adminui'),
  python: resolve(root, '..', 'boot-analysis', 'backend', 'frontend'),
}

const arg = process.argv.indexOf('--target')
const which = arg > -1 ? process.argv[arg + 1] : 'go'
const list = which === 'all' ? ['go', 'python'] : [which]

function sizeOf(p) {
  return readdirSync(p, { withFileTypes: true }).reduce((n, e) => {
    const full = join(p, e.name)
    return n + (e.isDirectory() ? sizeOf(full) : statSync(full).size)
  }, 0)
}

if (!existsSync(dist)) {
  console.error('[sync] dist 不存在，请先执行 npm run build')
  process.exit(1)
}

for (const key of list) {
  const dest = TARGETS[key]
  if (!dest) {
    console.error(`[sync] 未知目标: ${key}`)
    process.exit(1)
  }
  mkdirSync(dest, { recursive: true })
  // 清空旧产物（保留目录本身，后端可能已挂其它资源）
  for (const name of readdirSync(dest)) {
    rmSync(join(dest, name), { recursive: true, force: true })
  }
  cpSync(dist, dest, { recursive: true })
  const bytes = sizeOf(dest)
  console.log(`[sync] ${key.padEnd(6)} -> ${dest}  (${(bytes / 1024).toFixed(1)} KB)`)
}
console.log('[sync] 完成')
