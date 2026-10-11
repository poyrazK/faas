import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, cp, writeFile, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { command } from './staging/canary.mjs'
import { createHash } from 'node:crypto'
import { main as artifacts } from '../../cmd/gregale/templates/data-api-starter/tools/artifacts.mjs'

test('generic SDK installation pins and restores verified artifacts without a CLI archive', { timeout: 30000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'data-api-generic-install-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const client = join(root, 'web'), sdk = join(root, 'sdk'), source = join(root, 'approved')
  for (const directory of [client, sdk, source]) await mkdir(directory)
  await writeFile(join(client, 'package.json'), JSON.stringify({ name: 'generic-client', private: true }))
  await writeFile(join(sdk, 'package.json'), JSON.stringify({ name: '@gregale/data', version: '0.1.0', files: ['marker'] }))
  await writeFile(join(sdk, 'marker'), 'approved-build')
  const packed = JSON.parse(await command('npm', ['pack', '--json', '--pack-destination', source], { cwd: sdk }))[0]
  const bytes = await readFile(join(source, packed.filename))
  const bundle = { format: 1, contract: 1, source: { commit: 'a'.repeat(40), epoch: 1757000000 },
    tools: { go: 'go1.25.13', node: '22.19.0', npm: '10.9.3' },
    cli: { version: 'v0.1.0-test.1', action_sha: 'b'.repeat(40), archives: [{ file: 'gregale_0.1.0-test.1_linux_amd64.tar.gz', bytes: 20, sha256: 'c'.repeat(64), os: 'linux', arch: 'amd64' }] },
    sdk: { file: packed.filename, bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex'), name: '@gregale/data', version: packed.version, integrity: packed.integrity } }
  const manifest = join(source, 'data-api-bundle.json')
  await writeFile(manifest, JSON.stringify(bundle))
  await artifacts(['pin', manifest, '--sdk-only'], root, 'web')
  assert.equal(await readFile(join(client, 'node_modules/@gregale/data/marker'), 'utf8'), 'approved-build')
  const lock = await readFile(join(client, 'package-lock.json'), 'utf8')
  await rm(join(client, 'node_modules'), { recursive: true })
  await rm(join(client, 'vendor'), { recursive: true })
  await artifacts(['restore', '--from', source, '--sdk-only'], root, 'web')
  assert.equal(await readFile(join(client, 'node_modules/@gregale/data/marker'), 'utf8'), 'approved-build')
  assert.equal(await readFile(join(client, 'package-lock.json'), 'utf8'), lock)
  await writeFile(join(client, 'vendor/gregale-data.tgz'), Buffer.alloc(bytes.length))
  await assert.rejects(artifacts(['restore', '--from', source, '--sdk-only'], root, 'web'), /checksum mismatch/)
  assert.equal(await readFile(join(client, 'node_modules/@gregale/data/marker'), 'utf8'), 'approved-build')
})

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
