#!/usr/bin/env node
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, readFile, writeFile, rm, cp } from 'node:fs/promises'
import { join, resolve } from 'node:path'
import { tmpdir } from 'node:os'
import { prepareCustomProject, customProfile, verifyCustomContract } from './data-api-bundle-custom.mjs'
import { verifyPackagedFeatures, verifyPackagedExamples } from './data-api-bundle-examples.mjs'
import { verifyBundleRollout } from './data-api-bundle-rollout.mjs'
import { bundleEvidence, clearReleaseReport, writeReleaseReport, releaseChecks } from './data-api-release-report.mjs'
import { validateBundle, readJSON, verifyFile, hostArchive, run } from '../cmd/gregale/templates/data-api-starter/tools/artifact-lib.mjs'

async function main() {
  if (process.argv.length !== 3) throw new Error('Usage: node scripts/test-data-api-bundle.mjs BUNDLE_DIR')
  const bundleDir = resolve(process.argv[2])
  await clearReleaseReport(bundleDir)
  const bundle = validateBundle(await readJSON(join(bundleDir, 'data-api-bundle.json')))
  const evidence = await bundleEvidence(bundleDir,bundle)
  for (const entry of [...bundle.cli.archives, bundle.sdk]) await verifyFile(join(bundleDir, entry.file), entry)
  const resources = () => ({containers:run('docker',['container','ls','--all','--filter','label=gregale.data-api.dev=true','--format','{{.Names}}']).trim().split('\n').filter(Boolean).sort(),volumes:run('docker',['volume','ls','--format','{{.Name}}']).trim().split('\n').filter(Boolean).sort()})
  const before = resources()
  let contracts, customContracts, examples
  const work = await mkdtemp(join(tmpdir(), 'gregale-bundle-install-'))
  try {
    const bin = join(work, 'bin')
    await mkdir(bin)
    const archive = hostArchive(bundle)
    assert.equal(run('tar', ['-tzf', join(bundleDir, archive.file)]).trim(), 'gregale')
    run('tar', ['-xzf', join(bundleDir, archive.file), '-C', bin, '--no-same-owner'])
    const app = join(work, 'notes')
    const activation = run(join(bin, 'gregale'), ['--json', 'data-api', 'onboard', '--path', app, '--bundle', join(bundleDir, 'data-api-bundle.json')], { cwd: work, stdio: ['ignore', 'pipe', 'inherit'] }).trim().split('\n').map(line => JSON.parse(line))
    assert.deepEqual(activation.map(({ step, status }) => [step, status]), [
      ['scaffold', 'passed'], ['install', 'passed'], ['verify', 'passed'], ['local_activation', 'passed']
    ])
    assert.ok(activation.every(event => event.event === 'onboarding_step' && Number.isSafeInteger(event.duration_ms) && event.duration_ms >= 0))
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
    run('npm', ['run', 'build:browser'], { cwd: join(app, 'client'), env, stdio: 'inherit' })
    run('npm', ['run', 'typecheck'], { cwd: join(app, 'client'), env, stdio: 'inherit' })
    run('npm', ['test'], { cwd: join(app, 'client'), env, stdio: 'inherit' })
    // Exercise the same local replay command as the starter's client CI using
    // the restored binary, fresh dependencies, and its own embedded runtime.
    run(join(app, '.gregale-tools/gregale'), ['data-api', 'dev', '--port', '0', '--once', '--replay', 'data-api.requests.json'], { cwd: app, env, timeout: 420000, stdio: 'inherit' })
    const generic = join(work, 'generic-client')
    await mkdir(join(generic, 'web'), { recursive: true })
    await writeFile(join(generic, 'web/package.json'), JSON.stringify({ name: 'generic-client', private: true, type: 'module' }))
    await prepareCustomProject(generic)
    const cli = join(app, '.gregale-tools/gregale')
    run(cli, ['data-api', 'install', '--bundle', join(bundleDir, 'data-api-bundle.json'), '--directory', generic, '--client-directory', 'web'], { env, stdio: 'inherit' })
    assert.deepEqual(JSON.parse(await readFile(join(generic, 'data-api-artifacts.json'), 'utf8')).bundle, bundle)
    // Restore the custom project's CLI using tools embedded in this exact bundle.
    await cp(join(app,'tools'),join(generic,'tools'),{recursive:true})
    await writeFile(join(generic,'restore-cli.mjs'),"import { main } from './tools/artifacts.mjs'; await main(['restore','--from',process.argv[2],'--cli'],process.cwd(),'web')\n")
    run(process.execPath,['restore-cli.mjs',bundleDir],{cwd:generic,env,stdio:'inherit'})
    await rm(join(generic,'.gregale-tools'),{recursive:true})
    await rm(join(generic, 'web/node_modules'), { recursive: true })
    await rm(join(generic, 'web/vendor'), { recursive: true })
    run(cli, ['data-api', 'install', '--restore', '--from', bundleDir, '--directory', generic, '--client-directory', 'web'], { env, stdio: 'inherit' })
    assert.equal(JSON.parse(await readFile(join(generic, 'web/node_modules/@gregale/data/package.json'), 'utf8')).version, bundle.sdk.version)
    run(process.execPath,['restore-cli.mjs',bundleDir],{cwd:generic,env,stdio:'inherit'})
    const customCLI=join(generic,'.gregale-tools/gregale')
    const customVersion=JSON.parse(run(customCLI,['version','--json']))
    assert.equal(customVersion.version,bundle.cli.version)
    assert.equal(customVersion.git_sha,bundle.source.commit)
    contracts = await verifyBundleRollout(app,cli,env)
    customContracts = await verifyBundleRollout(generic,customCLI,env,customProfile)
    await verifyCustomContract(generic)
    await verifyPackagedFeatures(join(generic,'web'),env)
    examples=await verifyPackagedExamples(work,customCLI,bundleDir,env)
    console.log('Packaged CLI/SDK fresh starter, generic client install/restore, clean CI installation and scenario replay passed.')
  } finally { await rm(work, { recursive: true, force: true }) }
  assert.deepEqual(resources(),before,'Release checks must clean up their local services and volumes')
  assert.deepEqual(await bundleEvidence(bundleDir,bundle),evidence,'Verified artifact bytes must remain unchanged')
  await writeReleaseReport(bundleDir,bundle,releaseChecks,contracts,customContracts,examples)
  console.log('Release-candidate report: '+join(bundleDir,'data-api-verification.json'))
}

try { await main() } catch (error) {
  console.error(`Data API bundle installation: ${error.message}`)
  process.exitCode = 1
}
