import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
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
  const seen = []
  const runner = await fixture(t, { request: async path => { seen.push(path); return { status: 'pending' } } })
  runner.exec = async () => ({ wake_id: 'accepted-wake' })
  runner.wait = async (read, ready) => {
    assert.equal(ready(await read()), false)
    throw new Error('operation_timeout')
  }
  await assert.rejects(runner.refresh({ slug: 'owned' }), /operation_timeout/)
  assert.deepEqual(seen, ['/v1/apps/owned/runtime-config-restarts/accepted-wake'])
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
