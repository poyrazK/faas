import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, cp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { command } from './staging/canary.mjs'

test('starter SDK installation updates a replaced tarball and lock integrity', { timeout: 30000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'data-api-sdk-install-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  await mkdir(join(root, 'client'))
  await mkdir(join(root, 'tools'))
  await mkdir(join(root, 'sdk'))
  // A package with no registry dependencies keeps this installation regression
  // entirely local while exercising npm's real file-package cache and lockfile.
  await writeFile(join(root, 'client/package.json'), JSON.stringify({ name: 'fixture-client', private: true, dependencies: { '@gregale/data': 'file:vendor/gregale-data.tgz' } }))
  await writeFile(join(root, 'sdk/package.json'), JSON.stringify({ name: '@gregale/data', version: '0.1.0', private: true, files: ['marker'] }))
  const installer = fileURLToPath(new URL('../../cmd/gregale/templates/data-api-starter/tools/install-sdk.mjs', import.meta.url))
  await cp(installer, join(root, 'tools/install-sdk.mjs'))
  const env = { ...process.env, npm_config_offline: 'true' }
  let previousIntegrity
  for (const marker of ['first-build', 'replacement-with-same-version']) {
    await writeFile(join(root, 'sdk/marker'), marker)
    const receipt = JSON.parse(await command('npm', ['pack', '--json', '--pack-destination', root], { cwd: join(root, 'sdk'), env }))
    await command(process.execPath, [join(root, 'tools/install-sdk.mjs'), join(root, receipt[0].filename)], { cwd: root, env })
    assert.equal(await readFile(join(root, 'client/node_modules/@gregale/data/marker'), 'utf8'), marker)
    const lock = JSON.parse(await readFile(join(root, 'client/package-lock.json'), 'utf8'))
    const integrity = lock.packages['node_modules/@gregale/data'].integrity
    assert.equal(integrity, receipt[0].integrity)
    assert.notEqual(integrity, previousIntegrity)
    previousIntegrity = integrity
  }
})
