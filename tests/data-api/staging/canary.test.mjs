import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { Canary, validateConfig, command, apiRequest } from './canary.mjs'

const raw = { environment: 'staging', api: 'https://staging.example', account_id: 'canary-account', region: 'eu', builder_node_ids: ['native-builder'], app_host_suffix: '.apps.example' }
const env = { FAAS_TOKEN: 'private-test-token', FAAS_DATA_API_SOURCE_COMMIT: 'a'.repeat(40) }

test('preflight requires explicit staging identity, target and native builder evidence inputs', () => {
  assert.equal(validateConfig(raw, env).api, raw.api)
  for (const change of [
    { environment: 'production' }, { account_id: '' }, { builder_node_ids: [] },
    { api: 'http://staging.example' }, { api: 'https://user:password@staging.example' },
    { api: 'https://staging.example/path' }, { app_host_suffix: '' }, { region: '../prod' },
  ]) assert.throws(() => validateConfig({ ...raw, ...change }, env))
  assert.throws(() => validateConfig(raw, { ...env, FAAS_API: 'https://other.example' }), /staging_api_mismatch/)
  assert.throws(() => validateConfig(raw, { ...env, FAAS_TOKEN: '' }), /staging_token_required/)
  assert.throws(() => validateConfig(raw, { ...env, FAAS_DATA_API_SOURCE_COMMIT: '' }), /source_commit_required/)
})

test('management bearer stays at the configured origin and redirects are refused', async () => {
  const config = validateConfig(raw, env)
  let calls = 0
  const fetchImpl = async (url, options) => {
    calls++
    assert.equal(url, 'https://staging.example/v1/apps')
    assert.equal(options.headers.Authorization, 'Bearer private-test-token')
    assert.equal(options.redirect, 'error')
    return new Response('{"id":"owned"}', { status: 200 })
  }
  assert.deepEqual(await apiRequest(config, '/v1/apps', { fetchImpl }), { id: 'owned' })
  await assert.rejects(apiRequest(config, 'https://other.example', { fetchImpl }), /invalid_api_path/)
  assert.equal(calls, 1)
  await assert.rejects(apiRequest(config, '/v1/apps', { fetchImpl: async () => new Response('secret-in-error-body', { status: 403 }) }), /^Error: management_http_403$/)
})

test('child failures and output overflow never expose captured credentials', async () => {
  await assert.rejects(command(process.execPath, ['-e', 'console.error("private-test-token");process.exit(7)'], { env: process.env }), /^Error: command_failed$/)
  await assert.rejects(command(process.execPath, ['-e', 'process.stdout.write("x".repeat(2e6))'], { env: process.env }), /command_output_limit/)
  await assert.rejects(command(process.execPath, ['-e', 'setInterval(()=>{},1000)'], { env: process.env, timeout: 20 }), /command_timeout/)
})

async function fixture(t, options = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'data-api-canary-contract-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  return new Canary(validateConfig(raw, env), { evidence: join(directory, 'evidence.json'), ...options })
}

test('cleanup touches acknowledged resources only and reports unknown creation outcomes', async t => {
  const calls = []
  const runner = await fixture(t, { request: async (path, options) => { calls.push([path, options?.method]); return { id: 'owned-id' } } })
  runner.report.resources = [{ kind: 'app', slug: 'unknown', state: 'create_pending' }, { kind: 'app', slug: 'owned', id: 'owned-id' }]
  runner.report.result = 'passed'
  await runner.cleanup()
  assert.deepEqual(calls, [['/v1/apps/owned', undefined], ['/v1/apps/owned', 'DELETE']])
  assert.equal(runner.report.result, 'failed')
  assert.equal(runner.report.cleanup.pending[0].reason, 'create_outcome_unknown')
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /private-test-token/)
})

test('cleanup rejects a changed app identity and retains failed resources for reconciliation', async t => {
  const calls = []
  const runner = await fixture(t, { request: async (path, options) => { calls.push(options?.method); return { id: 'different-id' } } })
  runner.report.resources = [{ kind: 'app', slug: 'owned', id: 'owned-id' }]
  await runner.cleanup()
  assert.deepEqual(calls, [undefined])
  assert.equal(runner.report.cleanup.result, 'failed')
  assert.equal(runner.report.cleanup.pending[0].id, 'owned-id')
})

test('a live receipt without matching native builder provenance cannot pass', async t => {
  const runner = await fixture(t, { request: async () => ({ build_id: 'build', source_sha256: 'a'.repeat(64), builder_node_id: 'other-node' }) })
  await assert.rejects(runner.deployed({ id: 'deploy', status: 'live', build_id: 'build' }, { slug: 'owned' }), /native_build_provenance_missing/)
  assert.deepEqual(runner.report.deployments, [])
})

test('restart admission does not count as schema refresh completion', async t => {
  const runner = await fixture(t)
  for (const receipt of [
    { wake_id: 'accepted-wake', status: 'queued', ready: true },
    { wake_id: 'accepted-wake', status: 'completed', ready: false },
  ]) {
    runner.exec = async args => {
      assert.deepEqual(args, ['data-api', 'refresh', 'owned', '--wait', '--timeout', '5m'])
      return receipt
    }
    await assert.rejects(runner.refresh({ slug: 'owned' }), /schema_refresh_not_ready/)
  }
  runner.exec = async () => ({ wake_id: 'completed-wake', status: 'completed', ready: true })
  await runner.refresh({ slug: 'owned' })
})

test('migration binding readiness is awaited before a release can consume it', async t => {
  const calls = []
  let reads = 0
  const runner = await fixture(t, { runCommand: async (_binary, argv) => {
    const action = argv.slice(1).join(' '); calls.push(action)
    if (argv[2] === 'attach') return JSON.stringify({ id: 'binding-id', app_id: 'app-id', access: 'migration', state: 'provisioning' })
    return JSON.stringify({ state: ++reads === 1 ? 'provisioning' : 'ready' })
  } })
  await runner.attachMigration('db-id', { id: 'app-id', slug: 'owned' })
  assert.deepEqual(calls, ['postgres attach db-id owned --scope default --access migration --env MIGRATION_DATABASE_URL', 'postgres bindings get binding-id', 'postgres bindings get binding-id'])
})

test('binding retirement completes before app and database deletion', async t => {
  const calls = []
  const runner = await fixture(t, {
    request: async (path, options) => { calls.push(options?.method === 'DELETE' ? 'app-delete' : 'app-read'); return { id: 'app-id' } },
    runCommand: async (_binary, argv) => {
      const action = argv.slice(1).join(' '); calls.push(action)
      if (action === 'postgres bindings list db-id') return JSON.stringify({ items: [{ id: 'binding-id', app_id: 'app-id', state: 'ready' }] })
      return JSON.stringify({ state: 'deleted' })
    },
  })
  runner.report.resources = [{ kind: 'database', id: 'db-id' }, { kind: 'app', id: 'app-id', slug: 'owned' }]
  await runner.cleanup()
  assert.deepEqual(calls, ['postgres bindings list db-id', 'postgres bindings delete binding-id', 'postgres bindings get binding-id', 'app-read', 'app-delete', 'postgres delete db-id', 'postgres get db-id'])
  assert.equal(runner.report.cleanup.result, 'passed')
})

test('deployment evidence binds the source hash to the allowlisted builder', async t => {
  const runner = await fixture(t, { request: async () => ({ build_id: 'build', source_sha256: 'a'.repeat(64), builder_node_id: 'native-builder' }) })
  const receipt = { id: 'deploy', status: 'live', build_id: 'build', source_sha256: 'a'.repeat(64) }
  await runner.deployed(receipt, { slug: 'owned' })
  assert.equal(runner.report.deployments.length, 1)
  await assert.rejects(runner.deployed({ ...receipt, source_sha256: 'b'.repeat(64) }, { slug: 'owned' }), /native_build_provenance_missing/)
})

test('a failed remote build still cleans acknowledged resources and redacts its error', async t => {
  const runner = await fixture(t, {
    repoRoot: new URL('../../../', import.meta.url).pathname,
    cli: 'gregale',
    request: async (path, options) => {
      if (path === '/v1/account') return { id: raw.account_id }
      if (path === '/v1/apps' && options.method === 'POST') {
        assert.equal(options.body.require_authn, false)
        return { id: 'app-id', slug: options.body.slug, url: 'https://issuer.apps.example' }
      }
      if (options?.method === 'PATCH') assert.deepEqual(options.body, { require_authn: false, public_auth: { mode: 'open' } })
      return { id: 'app-id' }
    },
    runCommand: async (_binary, argv) => {
      const args = argv.slice(1)
      if (args[0] === 'deploy') {
        assert.ok(args.includes('--no-require-authn'))
        throw new Error('postgres://private-test-token@provider/private')
      }
      if (args[0] === 'postgres' && args[1] === 'create') return JSON.stringify({ id: 'db-id', name: args[2] })
      if (args[1] === 'bindings') return JSON.stringify({ items: [] })
      return JSON.stringify({ state: args[1] === 'get' && runner.report.resources.some(x => x.state === 'deleted') ? 'deleted' : 'ready' })
    },
  })
  await assert.rejects(runner.run(), /staging_canary_failed/)
  const evidence = await readFile(runner.evidence, 'utf8')
  assert.doesNotMatch(evidence, /private-test-token|postgres:\/\//)
  assert.equal(runner.report.result, 'failed')
  assert.equal(runner.report.cleanup.result, 'passed')
  assert.equal(runner.report.failure.check, 'issuer_native_build')
})

const { verifiedSync, verifiedContract, verifiedDiagnostic, matchingRuntimeLog } = await import('./workflow.mjs')
const fingerprint = 'a'.repeat(64)
const generated = `// Schema fingerprint: ${fingerprint}\nexport type Database = {}`
const requestID = '11111111-1111-4111-8111-111111111111'
const info = { requestId: requestID, status: 200, durationMs: 1 }
const runtimeRecord = () => ({ time: new Date().toISOString(), level: 'info', event: 'data_api_request', request_id: requestID, method: 'POST', route: 'rpc', status: 200, duration_ms: 1, outcome: 'completed', code: null })
const envelope = record => JSON.stringify({ deployment_id: 'deployment', instance: '22222222-2222-4222-8222-222222222222', line: JSON.stringify(record) })

test('sync admission, mismatched fingerprints and incomplete receipts cannot qualify a workflow', () => {
  const receipt = { app: 'owned', status: 'completed', contract_verified: true, wake_id: 'wake', task_id: 'task', deployment_id: 'deployment', fingerprint }
  assert.equal(verifiedSync(receipt, { slug: 'owned' }, generated).contract_verified, true)
  for (const change of [{ status: 'queued' }, { contract_verified: false }, { app: 'other' }, { task_id: '' }, { fingerprint: 'b'.repeat(64) }]) {
    assert.throws(() => verifiedSync({ ...receipt, ...change }, { slug: 'owned' }, generated), /sync_contract_not_verified/)
  }
  assert.throws(() => verifiedSync(receipt, { slug: 'owned' }, 'invalid'), /generated_fingerprint_missing/)
  assert.throws(() => verifiedContract({ version: 1, ready: true, fingerprint: 'b'.repeat(64) }, fingerprint), /serving_fingerprint_mismatch/)
  assert.throws(() => verifiedContract({ version: 1, ready: false, fingerprint }, fingerprint), /serving_fingerprint_mismatch/)
})

test('SDK IDs must match safe runtime records from the serving deployment', () => {
  verifiedDiagnostic(info)
  assert.equal(matchingRuntimeLog(envelope(runtimeRecord()), info, 'deployment', 'POST', 'rpc').request_id, requestID)
  assert.equal(matchingRuntimeLog('', info, 'deployment', 'POST', 'rpc'), null)
  assert.equal(matchingRuntimeLog('not JSON\n'+envelope({ ...runtimeRecord(), request_id: 'other' }), info, 'deployment', 'POST', 'rpc'), null)
  for (const change of [{ status: 409 }, { method: 'GET' }, { route: 'rest' }, { code: 'upstream_error' }, { outcome: 'aborted' }, { secret: 'private-test-token' }]) {
    assert.throws(() => matchingRuntimeLog(envelope({ ...runtimeRecord(), ...change }), info, 'deployment', 'POST', 'rpc'), /runtime_log_contract_invalid/)
  }
  assert.throws(() => matchingRuntimeLog(envelope(runtimeRecord()), info, 'other-deployment', 'POST', 'rpc'), /runtime_log_contract_invalid/)
  for (const change of [{ requestId: null }, { requestId: 'secret' }, { status: 0 }, { durationMs: -1 }, { token: 'private-test-token' }]) assert.throws(() => verifiedDiagnostic({ ...info, ...change }), /sdk_diagnostics_invalid/)
})

async function journey(t, failure = '') {
  const apps = new Map(), deployments = [], logLines = new Map()
  let runner, deletedDB = false, rotated = false, syncCount = 0, instanceReads = 0
  const retired = new Set()
  const makeDeployment = slug => {
    const receipt = { id: `deployment-${deployments.length}`, status: 'live', build_id: `build-${deployments.length}`, source_sha256: 'a'.repeat(64), app: slug }
    deployments.push(receipt); return receipt
  }
  const emit = (callback, method, route) => {
    if (!callback) return
    const id = randomUUID()
    const apiDeployment = deployments.find(x => x.app.endsWith('-api'))
    const record = { ...runtimeRecord(), request_id: id, method, route }
    if (failure === 'unsafe-log') record.secret = 'private-test-token'
    logLines.set(id, JSON.stringify({ deployment_id: apiDeployment.id, instance: '22222222-2222-4222-8222-222222222222', line: JSON.stringify(record) }))
    callback({ requestId: id, status: 200, durationMs: 1 })
  }
  const fakeSDK = { createDataClient: options => ({ schema: () => ({ from: () => {
    let single = false, deleted = false
    const builder = {
      select: () => builder, eq: () => builder, single: () => { single = true; return builder },
      delete: () => { deleted = true; return builder }, retry: () => builder,
      then: resolve => { emit(options.onResponse, 'GET', 'rest'); resolve({ error: null, data: deleted ? [{ id: 7 }] : single ? { id: 7, body: 'updated', priority: 0 } : [] }) },
    }
    return builder
  } }) }) }
  runner = await fixture(t, {
    repoRoot: new URL('../../../', import.meta.url).pathname, cli: 'gregale', sdkTarball: '/unused/sdk.tgz',
    loadModule: async path => path.endsWith('workflow-client.js') ? { exerciseRPC: async (_url, _alice, _bob, _fetch, callback) => {
      if (rotated && failure === 'rotated-rpc') throw new Error('postgres://private-test-token@provider')
      emit(callback, 'POST', 'rpc')
    } } : path.endsWith('dist/client.js') ? { exercise: async () => 7 } : fakeSDK,
    request: async (path, options) => {
      if (path === '/v1/account') return { id: raw.account_id }
      if (path === '/v1/apps' && options?.method === 'POST') {
        const slug = options.body.slug
        const app = { id: 'id-'+slug, slug, url: `https://${slug}.apps.example` }; apps.set(slug, app); return app
      }
      if (path.endsWith('/provenance')) {
        const build = deployments.find(x => path.includes(x.build_id))
        return { build_id: build.build_id, source_sha256: build.source_sha256, builder_node_id: 'native-builder' }
      }
      if (path.endsWith('/deployments')) return { items: deployments.filter(x => path.includes(x.app)) }
      if (path.startsWith('/v1/deployments/')) return deployments.find(x => x.id === path.split('/').at(-1))
      if (path.endsWith('/instances')) return [{ id: '22222222-2222-4222-8222-222222222222', deployment_id: deployments.find(x => x.app.endsWith('-api')).id, state: instanceReads++ === 0 ? 'PARKED' : 'RUNNING' }]
      return apps.get(path.split('/')[3])
    },
    runCommand: async (binary, argv, options) => {
      if (binary === 'npm') return '{}'
      const args = argv.slice(1)
      if (args[0] === 'deploy') return JSON.stringify(makeDeployment(args[args.indexOf('--name')+1]))
      if (args[0] === 'data-api' && args[1] === 'create') return JSON.stringify(makeDeployment(args[2]))
      if (args[0] === 'data-api' && args[1] === 'types') return '{}'
      if (args[0] === 'data-api' && args[1] === 'sync') {
        assert.equal(options.env.FAAS_TOKEN, env.FAAS_TOKEN)
        assert.notEqual(options.env.GREGALE_DATA_API_ACCESS_TOKEN, env.FAAS_TOKEN)
        const claims = JSON.parse(Buffer.from(options.env.GREGALE_DATA_API_ACCESS_TOKEN.split('.')[1], 'base64url'))
        assert.equal(claims.sub, 'alice'); assert.ok(claims.exp - Math.floor(Date.now()/1000) > 1200)
        const config = JSON.parse(await readFile(args[args.indexOf('--config')+1], 'utf8'))
        assert.ok(config.migrate.command.includes('--wait'))
        assert.ok(config.check.command.includes('workflow-client.ts'))
        assert.doesNotMatch(JSON.stringify(config), /private-test-token|eyJ/)
        assert.match(await readFile(join(config.migrate.directory, 'gregale.yaml'), 'utf8'), /node release.mjs/)
        assert.equal(await readFile(join(config.migrate.directory, 'rpc-runtime/types.mjs'), 'utf8'), await readFile(join(runner.repoRoot, 'cmd/gregale/templates/data-api/types.mjs'), 'utf8'))
        syncCount++
        if (syncCount > 1) assert.ok(config.check.command.includes('priority.ts'))
        const hash = syncCount === 1 ? fingerprint : 'b'.repeat(64)
        await writeFile(join(options.cwd, 'database.types.ts'), `// Schema fingerprint: ${hash}\n"priority"`)
        makeDeployment(config.migrate.command[config.migrate.command.indexOf('--name')+1])
        return JSON.stringify({ app: args[2], status: 'completed', contract_verified: failure !== 'sync-unverified', wake_id: 'wake', task_id: 'task', deployment_id: deployments.find(x => x.app.endsWith('-api')).id, fingerprint: hash })
      }
      if (args[0] === 'logs') return logLines.get(args[args.indexOf('--grep')+1]) + '\n'
      if (args[0] === 'postgres') {
        if (args[1] === 'create') return JSON.stringify({ id: 'db-id', name: args[2] })
        if (args[1] === 'get') return JSON.stringify({ state: deletedDB ? 'deleted' : 'ready' })
        if (args[1] === 'delete') { deletedDB = true; return '{}' }
        if (args[1] === 'attach') return JSON.stringify({ id: 'migration-binding', app_id: apps.get(args[3]).id, access: 'migration' })
        if (args[2] === 'rotate') { rotated = true; return JSON.stringify({ id: 'api-binding', app_id: [...apps.values()].find(x => x.slug.endsWith('-api')).id, access: 'data_api', state: 'ready', rotation_pending: false, credential_generation: failure === 'rotation-unchanged' ? 1 : 2 }) }
        if (args[2] === 'list') return JSON.stringify({ items: [{ id: 'api-binding', app_id: [...apps.values()].find(x => x.slug.endsWith('-api')).id, access: 'data_api', state: 'ready', credential_generation: rotated ? 2 : 1 }] })
        if (args[2] === 'delete') { retired.add(args[3]); return '{}' }
        if (args[2] === 'get') return JSON.stringify({ state: retired.has(args[3]) ? 'deleted' : 'ready' })
      }
      return '{}'
    },
  })
  runner.publicFetch = async (url, options) => {
    assert.notEqual(options?.headers?.Authorization, 'Bearer '+env.FAAS_TOKEN)
    if (url.endsWith('/healthz')) return new Response('{"migration_credential_present":false}')
    if (url.includes('/rest/v1/rpc/')) return new Response('{}', { status: 404 })
    if (url.endsWith('/__gregale/schema') && !options?.headers?.Authorization) return new Response('{}', { status: 401 })
    if (url.endsWith('/__gregale/schema')) return new Response(JSON.stringify({ version: 1, ready: true, fingerprint: failure === 'serving-stale' ? 'c'.repeat(64) : runner.report.syncs.at(-1).fingerprint }))
    if (url.endsWith('/openapi.json')) return new Response('{"paths":{"/notes":{},"/rpc/create_note":{"post":{}}}}')
    return new Response('{}', { status: 401 })
  }
  // The simulated API is immediately ready; no sleeping or provider access.
  runner.wait = async (read, ready) => { const value = await read(); assert.ok(ready(value), 'simulated operation must be complete'); return value }
  return runner
}

test('complete canary journey verifies three syncs, RPC grants, SDK logs, rotation and cleanup', async t => {
  const runner = await journey(t)
  const report = await runner.run()
  assert.equal(report.result, 'passed')
  assert.equal(report.cleanup.result, 'passed')
  assert.equal(report.syncs.length, 3)
  assert.equal(report.requests.length, 3)
  assert.deepEqual(report.requests.map(x => [x.method,x.route]), [['POST','rpc'],['GET','rest'],['POST','rpc']])
  assert.ok(report.checks.includes('rpc_denied_before_permission_setup'))
  assert.ok(report.checks.includes('schema_migration_sync_native_build'))
  assert.ok(report.checks.includes('credential_rotation_preserves_data'))
  assert.equal(report.deployments.length, 6)
  assert.deepEqual(report.rotation, { binding_id: 'api-binding', previous_generation: 1, current_generation: 2, completed: true })
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /private-test-token|postgres:\/\/|eyJ|atomic canary/)
})

for (const [failure, stage] of [
  ['sync-unverified','rpc_permissions_sync_native_build'], ['serving-stale','serving_fingerprint_agreement'],
  ['unsafe-log','typed_rpc_subject_isolation_and_logs'], ['rotated-rpc','credential_rotation_preserves_data'],
  ['rotation-unchanged','credential_rotation_preserves_data'],
]) test(`failed ${failure} canary retains redacted evidence and cleans resources`, async t => {
  const runner = await journey(t, failure)
  await assert.rejects(runner.run(), /staging_canary_failed/)
  assert.equal(runner.report.result, 'failed')
  assert.equal(runner.report.failure.check, stage)
  assert.equal(runner.report.cleanup.result, 'passed')
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /private-test-token|postgres:\/\/|eyJ/)
})
