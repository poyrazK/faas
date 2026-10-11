import { copyFile, mkdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawnSync } from 'node:child_process'

if (process.argv.length !== 3) {
  console.error('Usage: node tools/install-sdk.mjs /path/to/gregale-data.tgz')
  process.exitCode = 1
} else {
  const client = fileURLToPath(new URL('../client/', import.meta.url))
  await mkdir(new URL('../client/vendor/', import.meta.url), { recursive: true })
  await copyFile(resolve(process.argv[2]), new URL('../client/vendor/gregale-data.tgz', import.meta.url))
  const result = spawnSync('npm', ['install', '--save-exact', '--ignore-scripts', '--no-audit', '--no-fund', './vendor/gregale-data.tgz'], { cwd: client, stdio: 'inherit' })
  process.exitCode = result.status ?? 1
}
