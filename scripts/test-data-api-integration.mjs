#!/usr/bin/env node
// Exercise the shipped CLI and SDK in an existing application, with real Docker services.
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, readFile, writeFile, rm } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { spawnSync } from 'node:child_process'
import { hostArchive, readJSON, validateBundle, verifyFile, run } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

function refused(cli, args, reason) {
  const result = spawnSync(cli, args, { encoding: 'utf8', timeout: 240000, maxBuffer: 4 << 20 })
  assert.ifError(result.error)
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, reason)
}

async function main() {
  if (process.argv.length !== 3) throw new Error('Usage: node scripts/test-data-api-integration.mjs BUNDLE_DIR')
  const bundleDir = resolve(process.argv[2])
  const bundle = validateBundle(await readJSON(join(bundleDir, 'data-api-bundle.json')))
  const archive = hostArchive(bundle)
  await verifyFile(join(bundleDir, archive.file), archive)
  await verifyFile(join(bundleDir, bundle.sdk.file), bundle.sdk)
  const resources = () => run('docker', ['container', 'ls', '--all', '--filter', 'label=gregale.data-api.dev=true', '--format', '{{.Names}}']).trim().split('\n').filter(Boolean).sort()
  const before = resources()
  const work = await mkdtemp(join(tmpdir(), 'gregale-existing-data-'))
  try {
    const bin = join(work, 'bin'), app = join(work, 'existing TypeScript app')
    await mkdir(bin)
    await mkdir(join(app, 'src'), { recursive: true })
    run('tar', ['-xzf', join(bundleDir, archive.file), '-C', bin, '--no-same-owner'])
    const cli = join(bin, 'gregale')
    const metadata = { name: 'existing-typescript-app', private: true, type: 'module', scripts: { dev: 'node existing-server.mjs', typecheck: 'tsc -p tsconfig.json' }, devDependencies: { typescript: '5.9.3' } }
    const config = JSON.stringify({ compilerOptions: { target: 'ES2022', module: 'ESNext', moduleResolution: 'Bundler', strict: true, noEmit: true, types: [] }, include: ['src/**/*.ts'] })
    await writeFile(join(app, 'package.json'), JSON.stringify(metadata))
    await writeFile(join(app, 'tsconfig.json'), config)
    await writeFile(join(app, 'src/existing.ts'), 'export const application = "unchanged"\n')
    const args = ['--json', 'data-api', 'integrate', '--directory', app, '--bundle', join(bundleDir, 'data-api-bundle.json')]
    const preview = JSON.parse(run(cli, [...args, '--dry-run']))
    assert.equal(preview.event, 'integration_plan')
    assert.equal(preview.dry_run, true)
    assert.equal(await readFile(join(app, 'package.json'), 'utf8'), JSON.stringify(metadata))
    await assert.rejects(readFile(join(app, 'gregale-data/data-api.dev.json')), { code: 'ENOENT' })
    const events = run(cli, args, { timeout: 240000, stdio: ['ignore', 'pipe', 'inherit'] }).trim().split('\n').map(JSON.parse)
    assert.ok(events.some(event => event.step === 'local_activation' && event.status === 'passed'))
    assert.deepEqual(JSON.parse(await readFile(join(app, 'package.json'), 'utf8')).scripts, metadata.scripts)
    assert.equal(await readFile(join(app, 'tsconfig.json'), 'utf8'), config)
    assert.equal(await readFile(join(app, 'src/existing.ts'), 'utf8'), 'export const application = "unchanged"\n')
    run('npm', ['run', 'typecheck'], { cwd: app, stdio: 'inherit' })
    const typesPath = join(app, 'src/gregale-data/database.types.ts')
    const types = await readFile(typesPath, 'utf8')
    assert.match(types, /notes/)
    const verify = ['--json', 'data-api', 'serve', '--directory', app, '--config', 'gregale-data/data-api.dev.json', '--port', '0', '--once', '--check', '--replay', 'gregale-data/requests.json']
    await writeFile(join(app, 'gregale-data/migrations/0002_label.sql'), 'ALTER TABLE api.notes ADD COLUMN label text;\n')
    refused(cli, [...verify, '--check-types'], /types are stale/i)
    assert.equal(await readFile(typesPath, 'utf8'), types, 'stale-types check rewrote declarations')
    run(cli, verify, { timeout: 240000, stdio: 'inherit' })
    assert.match(await readFile(typesPath, 'utf8'), /label/)
    // A permissive second policy must fail authorization, not report activation.
    await writeFile(join(app, 'gregale-data/migrations/0003_bad_policy.sql'), 'CREATE POLICY broken_isolation ON api.notes USING (true) WITH CHECK (true);\n')
    refused(cli, verify, /required project authorization verification/)
    assert.deepEqual(resources(), before, 'local services leaked after verification')
    console.log('Existing TypeScript app: integration, generated types, schema evolution, two-user isolation and negative-policy checks passed.')
  } finally {
    await rm(work, { recursive: true, force: true })
  }
}

try { await main() } catch (error) { console.error(error.message); process.exitCode = 1 }
