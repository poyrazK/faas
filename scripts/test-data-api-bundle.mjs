#!/usr/bin/env node
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { validateBundle, readJSON, verifyFile, hostArchive, run } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

async function main() {
  if (process.argv.length !== 3) throw new Error('Usage: node scripts/test-data-api-bundle.mjs BUNDLE_DIR')
  const bundleDir = resolve(process.argv[2])
  const bundle = validateBundle(await readJSON(join(bundleDir, 'data-api-bundle.json')))
  for (const entry of [...bundle.cli.archives, bundle.sdk]) await verifyFile(join(bundleDir, entry.file), entry)
  const work = await mkdtemp(join(tmpdir(), 'gregale-bundle-install-'))
  try {
    const bin = join(work, 'bin')
    await mkdir(bin)
    const archive = hostArchive(bundle)
    assert.equal(run('tar', ['-tzf', join(bundleDir, archive.file)]).trim(), 'gregale')
    run('tar', ['-xzf', join(bundleDir, archive.file), '-C', bin, '--no-same-owner'])
    const app = join(work, 'notes')
    run(join(bin, 'gregale'), ['init', '--template', 'data-api-starter', '--path', app], { cwd: work, stdio: 'inherit' })
    // Use the shipped CLI's embedded tools and an empty npm cache; neither the
    // SDK source tree nor its developer node_modules participates in installation.
    const env = { ...process.env, npm_config_cache: join(work, 'pin-cache'), npm_config_offline: 'false' }
    run(process.execPath, ['tools/artifacts.mjs', 'pin', join(bundleDir, 'data-api-bundle.json')], { cwd: app, env, stdio: 'inherit' })
    assert.deepEqual(JSON.parse(await readFile(join(app, 'data-api-artifacts.json'), 'utf8')).bundle, bundle)
    const version = JSON.parse(run(join(app, '.gregale-tools/gregale'), ['version', '--json']))
    assert.equal(version.version, bundle.cli.version)
    assert.equal(version.git_sha, bundle.source.commit)
    const pkg = JSON.parse(await readFile(join(app, 'client/node_modules/@gregale/data/package.json'), 'utf8'))
    assert.equal(pkg.version, bundle.sdk.version)
    await rm(join(app, 'client/node_modules'), { recursive: true })
    await rm(join(app, 'client/vendor'), { recursive: true })
    await rm(join(app, '.gregale-tools'), { recursive: true })
    // Simulate a new CI checkout with a second empty cache and only committed pins.
    env.npm_config_cache = join(work, 'ci-cache')
    run(process.execPath, ['tools/artifacts.mjs', 'restore', '--from', bundleDir, '--cli'], { cwd: app, env, stdio: 'inherit' })
    run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: app, env, stdio: 'inherit' })
    run('npm', ['test'], { cwd: app, env, stdio: 'inherit' })
    run('npm', ['ci', '--ignore-scripts', '--no-audit', '--no-fund'], { cwd: join(app, 'client'), env, stdio: 'inherit' })
    run('npm', ['run', 'typecheck'], { cwd: join(app, 'client'), env, stdio: 'inherit' })
    run('npm', ['test'], { cwd: join(app, 'client'), env, stdio: 'inherit' })
    console.log('Packaged CLI/SDK fresh starter and clean CI installation passed.')
  } finally { await rm(work, { recursive: true, force: true }) }
}

try { await main() } catch (error) {
  console.error(`Data API bundle installation: ${error.message}`)
  process.exitCode = 1
}
