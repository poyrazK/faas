import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, mkdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { createHash, randomUUID } from 'node:crypto'
import { Canary, CanaryFailure, validateConfig, command, apiRequest } from './canary.mjs'
import { readinessPath } from '../../../cmd/gregale/templates/data-api/config.mjs'

const raw = { environment: 'staging', api: 'https://staging.example', account_id: 'canary-account', region: 'eu', builder_node_ids: ['native-builder'], app_host_suffix: '.apps.example' }
const candidateVersion = '0123456789abcdef'
const staleVersion = 'fedcba9876543210'
const cacheJSONRequestID = '11111111-1111-4111-8111-111111111111'
const cacheCSVRequestID = '22222222-2222-4222-8222-222222222222'
const env = { FAAS_TOKEN: 'private-test-token', FAAS_DATA_API_SOURCE_COMMIT: 'a'.repeat(40), FAAS_DATA_API_EXPECTED_RUNTIME_VERSION: candidateVersion }

test('preflight requires explicit staging identity, target and native builder evidence inputs', () => {
  assert.equal(validateConfig(raw, env).api, raw.api)
  assert.equal(validateConfig(raw, env).mode, 'full')
  assert.equal(validateConfig(raw, env, { mode: 'smoke' }).mode, 'smoke')
  assert.throws(() => validateConfig(raw, env, { mode: 'short' }), /invalid_canary_mode/)
  for (const change of [
    { environment: 'production' }, { account_id: '' }, { builder_node_ids: [] },
    { api: 'http://staging.example' }, { api: 'https://user:password@staging.example' },
    { api: 'https://staging.example/path' }, { app_host_suffix: '' }, { region: '../prod' },
  ]) assert.throws(() => validateConfig({ ...raw, ...change }, env))
  assert.throws(() => validateConfig(raw, { ...env, FAAS_API: 'https://other.example' }), /staging_api_mismatch/)
  assert.throws(() => validateConfig({ ...raw, connect: 'true' }, env), /invalid_connect_option/)
  assert.throws(() => validateConfig({ ...raw, write_inspection: 'true' }, env), /invalid_write_inspection_option/)
  assert.throws(() => validateConfig(raw, { ...env, FAAS_TOKEN: '' }), /staging_token_required/)
  assert.throws(() => validateConfig(raw, { ...env, FAAS_DATA_API_SOURCE_COMMIT: '' }), /source_commit_required/)
  assert.equal(validateConfig(raw, env).expectedRuntimeVersion, candidateVersion)
  for (const version of [undefined, '', candidateVersion.toUpperCase(), candidateVersion + '\n']) {
    assert.throws(() => validateConfig(raw, { ...env, FAAS_DATA_API_EXPECTED_RUNTIME_VERSION: version }), /expected_runtime_version_required/)
  }
  assert.equal(validateConfig(raw, { ...env, FAAS_DATA_API_EXPECTED_RUNTIME_VERSION: undefined }, { checkConfigOnly: true }).api, raw.api)
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
  assert.equal(await command(process.execPath, ['-e', 'console.error("expected private-test-token");process.stdout.write("safe");process.exit(1)'], { env: process.env, expectedExitCode: 1, expectedStderrIncludes: 'expected' }), 'safe')
  await assert.rejects(command(process.execPath, ['-e', 'console.error("private-test-token");process.exit(1)'], { env: process.env, expectedExitCode: 1, expectedStderrIncludes: 'different' }), /^Error: command_failed$/)
  await assert.rejects(command(process.execPath, ['-e', 'process.exit(0)'], { env: process.env, expectedExitCode: 1 }), /^Error: command_failed$/)
})

async function fixture(t, { mode, ...options } = {}) {
  const directory = await mkdtemp(join(tmpdir(), 'data-api-canary-contract-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  return new Canary(validateConfig(raw, env, { mode }), { evidence: join(directory, 'evidence.json'), ...options })
}

test('optional write inspection pins the deployed binding and retains only redacted evidence', async t => {
  const runner = await fixture(t)
  runner.workspace = dirname(runner.evidence)
  runner.cli = 'gregale'
  const app = { id: 'app-id', slug: 'fixture-app' }, db = { id: 'database-id' }
  const binding = { id: 'binding-id', database_id: db.id, app_id: app.id, access: 'data_api', scope: 'default', environment_key: 'DATABASE_URL', state: 'ready', rotation_pending: false, credential_generation: 4 }
  runner.report.deployments.push({ app: app.slug, deployment_id: 'deployment-id' })
  runner.exec = async args => { assert.deepEqual(args, ['postgres', 'bindings', 'list', db.id]); return { items: [binding] } }
  const report = { version: 1, status: 'review_required', authorization: 'not_tested', binding_delivery: 'not_independently_verified', app_id: app.id, database_id: db.id, binding_id: binding.id, deployment_id: 'deployment-id', credential_generation: 4, task_id: '11111111-1111-4111-8111-111111111111', scope: { schema: 'api', table: 'notes', operations: ['insert'] }, binding_identity: { status: 'verified', reason: 'provider_target_and_trust_matched' }, observation: { private: 'private-fixture-row' } }
  runner.runCommand = async (binary, args, options) => {
    assert.equal(binary, 'gregale')
    assert.deepEqual(args, ['--json', 'data-api', 'write-readiness', '--database', db.id, '--app', app.slug, '--binding', binding.id, '--schema', 'api', '--table', 'notes', '--operations', 'insert'])
    assert.equal(options.expectedExitCode, 1)
    assert.equal(options.env.FAAS_TOKEN, env.FAAS_TOKEN)
    return JSON.stringify(report)
  }
  await runner.qualifyWriteInspection({ db, app })
  assert.deepEqual(runner.report.write_inspection, { task_id: report.task_id, deployment_id: 'deployment-id', binding_id: binding.id, credential_generation: 4, binding_identity: 'verified', authorization: 'not_tested' })
  assert.ok(!(await readFile(runner.evidence, 'utf8')).includes('private-fixture-row'))
  for (const change of [
    { binding_identity: { status: 'unknown', reason: 'inspection_trust_mismatch' } },
    { deployment_id: 'changed' }, { credential_generation: 5 }, { authorization: 'passed' }, { task_id: 'private-fixture-row' },
    { scope: { schema: 'api', table: 'notes', operations: ['delete'] } },
  ]) {
    runner.runCommand = async () => JSON.stringify({ ...report, ...change })
    await assert.rejects(runner.qualifyWriteInspection({ db, app }), /inspection_report_invalid/)
  }
  runner.exec = async () => ({ items: [binding, binding] })
  await assert.rejects(runner.qualifyWriteInspection({ db, app }), /inspection_binding_missing/)
})

test('hosted SDK bundle must match the source and the exact SDK bytes', async t => {
  const runner = await fixture(t)
  const root = dirname(runner.evidence)
  const archive = join(root, 'gregale-data-0.1.0.tgz')
  const manifest = join(root, 'data-api-bundle.json')
  const sdk = Buffer.from('simulated-sdk')
  await writeFile(archive, sdk)
  const bundle = {format:1,contract:1,source:{commit:env.FAAS_DATA_API_SOURCE_COMMIT,epoch:1},tools:{go:'go1.26.11',node:'22.0.0',npm:'10.0.0'},cli:{version:'v0.1.0',action_sha:'b'.repeat(40),archives:[{file:'gregale_0.1.0_linux_amd64.tar.gz',bytes:1,sha256:'c'.repeat(64),os:'linux',arch:'amd64'}]},sdk:{file:'gregale-data-0.1.0.tgz',bytes:sdk.length,sha256:createHash('sha256').update(sdk).digest('hex'),name:'@gregale/data',version:'0.1.0',integrity:'sha512-'+createHash('sha512').update(sdk).digest('base64')}}
  await writeFile(manifest, JSON.stringify(bundle))
  runner.sdkBundle = manifest; runner.sdkTarball = archive
  assert.equal(await runner.verifiedHostedBundle(), manifest)
  runner.workspace = root
  await runner.freezeHostedBundle()
  const frozenManifest = runner.sdkBundle
  await writeFile(archive, 'source-was-replaced')
  assert.equal(await runner.verifiedHostedBundle(), frozenManifest)
  await writeFile(archive, sdk)
  runner.sdkBundle = manifest; runner.sdkTarball = archive
  bundle.source.commit = 'f'.repeat(40); await writeFile(manifest, JSON.stringify(bundle))
  await assert.rejects(runner.verifiedHostedBundle(), /hosted_bundle_source_mismatch/)
  bundle.source.commit = env.FAAS_DATA_API_SOURCE_COMMIT; await writeFile(manifest, JSON.stringify(bundle)); await writeFile(archive, 'altered-sdk')
  await assert.rejects(runner.verifiedHostedBundle(), /hosted_bundle_invalid/)
})

test('missing hosted bundle fails before any provider action', async t => {
  let requests = 0
  const runner = await fixture(t, { request: async () => { requests++; throw new Error('unexpected provider call') } })
  runner.config.connect = true
  await assert.rejects(runner.run(), /staging_canary_failed/)
  assert.equal(requests, 0)
  assert.equal(runner.report.failure.code, 'hosted_bundle_required')
  assert.deepEqual(runner.report.resources, [])
})

async function connectionScenario(t, failure = '') {
  const calls = []
  const runner = await fixture(t)
  runner.workspace = dirname(runner.evidence)
  runner.cli = 'gregale'
  runner.repoRoot = join(runner.workspace, 'simulated-repo')
  const compilerDir = join(runner.repoRoot, 'sdk/data/node_modules/typescript')
  await mkdir(compilerDir, { recursive: true })
  await writeFile(join(compilerDir, 'package.json'), '{"name":"typescript","version":"5.9.3"}')
  runner.verifiedHostedBundle = async () => '/approved/data-api-bundle.json'
  const fingerprint = 'c'.repeat(64)
  runner.runCommand = async (binary, args, options) => {
    if (binary === 'npm') { assert.equal(options.env.FAAS_TOKEN, undefined); assert.equal(options.env.GREGALE_CONNECT_ALICE, undefined); return '' }
    calls.push(args)
    assert.equal(binary, 'gregale')
    assert.ok(options.env.GREGALE_CONNECT_ALICE)
    if (args.includes('integrate')) assert.equal(options.env.GREGALE_CONNECT_BOB, undefined)
    else assert.ok(options.env.GREGALE_CONNECT_BOB)
    assert.ok(!JSON.stringify(args).includes('test-token'))
    if (args.includes('--prepare-checks')) {
      const matrix = JSON.parse(await readFile(args[args.indexOf('--prepare-checks') + 1], 'utf8'))
      assert.equal(matrix.status, 'reviewed')
      assert.deepEqual(matrix.fixtures.map(f => f.visible_to), [['alice'], ['bob']])
      await writeFile(args[args.indexOf('--collection') + 1], JSON.stringify({version:1,requests:[{expect:{json:[{id:1}]}},{expect:{json:[]}}]}))
    }
    if (args.includes('--status') && options.expectedExitCode === 1) return JSON.stringify({stage:'verified',status:'blocked',resource_status:'changed_or_unavailable',reason:failure === 'unrelated-status' ? 'database unavailable' : 'app configuration or deployment changed'})
    if (args.includes('--requalify-plan')) await writeFile(args[args.indexOf('--requalify-plan') + 1], JSON.stringify({previous_deployment_id:'owned-deployment',candidate:{deployment_id:failure === 'wrong-plan' ? 'other' : 'new-deployment',stage:'deployed',fingerprint}}))
    if (args.includes('--requalify-apply') && options.expectedExitCode === 1) {
      assert.equal(options.expectedStderrIncludes, 'authorization checks failed')
      if (failure === 'bad-check-created-receipt') await writeFile(join(runner.workspace, 'requalify-wrong.receipt.json'), '{}')
      if (failure === 'bad-check-passed') throw new CanaryFailure('command_failed')
      return ''
    }
    if (args.includes('--requalify-apply')) await writeFile(join(runner.workspace, 'requalify-receipt.json'), JSON.stringify({ version: 1, app: `${runner.prefix}-connect`, app_id: 'owned-app', database_id: 'owned-db', binding_id:'owned-binding', deployment_id: 'new-deployment', fingerprint, output:join(runner.workspace,'requalify.types.ts'), stage: 'verified' }))
    if (args.includes('--apply')) await writeFile(join(runner.workspace, 'connect-receipt.json'), JSON.stringify({ version: 1, app: `${runner.prefix}-connect`, app_id: 'owned-app', database_id: 'owned-db', binding_id:'owned-binding', deployment_id: 'owned-deployment', stage: 'verified' }))
    if (args.includes('integrate')) return JSON.stringify({event:'hosted_integration_result',status:'passed',app:`${runner.prefix}-connect`,fingerprint:failure === 'wrong-hosted-fingerprint' ? 'd'.repeat(64) : fingerprint}) + '\n'
    return ''
  }
  runner.request = async path => path.startsWith('/v1/apps/') ? { id: 'owned-app', slug: `${runner.prefix}-connect`, url: 'https://canary.apps.example' } : { id: 'owned-deployment' }
  runner.deployed = async (deployment, app) => { assert.ok(['owned-deployment','new-deployment'].includes(deployment.id)); assert.equal(app.id, 'owned-app') }
  runner.checkControlPlaneRuntime = async () => {}
  runner.checkServingRuntime = async () => {}
  runner.exec = async args => args.includes('deploy') ? {id:failure === 'same-deployment' ? 'owned-deployment' : 'new-deployment',app_id:'owned-app'} : { stage: 'verified', resource_status: 'matches_receipt', types_status: 'matches_receipt' }
  return {runner,calls,fingerprint}
}

test('connection canary verifies redeployment, rejection and hosted integration', async t => {
  const {runner,calls,fingerprint} = await connectionScenario(t)
  await runner.qualifyConnect({ db: { id: 'owned-db' }, app: { slug: 'inspector' }, issuerURL: 'https://issuer.example', jwt: subject => `test-token-${subject}` })
  assert.equal(calls.length, 11)
  assert.ok(calls[0].includes('--prepare-checks'))
  assert.ok(!calls[1].includes('--inspect-app'))
  assert.ok(!calls[2].includes('--inspect-app'))
  assert.ok(calls[3].includes('--verify'))
  assert.ok(calls[4].includes('--resume'))
  assert.ok(calls[5].includes('--status'))
  assert.ok(calls[6].includes('--requalify-plan'))
  assert.ok(calls[7].includes('--requalify-apply'))
  assert.ok(calls[8].includes('--requalify-plan'))
  assert.ok(calls[9].includes('--requalify-apply'))
  assert.ok(calls[10].includes('integrate'))
  assert.deepEqual(runner.report.connected_journey, {original_deployment_id:'owned-deployment',candidate_deployment_id:'new-deployment',schema_fingerprint:fingerprint,stale_receipt_rejected:true,failed_assertion_rejected:true,hosted_client_verified:true})
  assert.equal(runner.report.resources[0].id, 'owned-app')
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /test-token/)
  assert.doesNotMatch(await readFile(join(runner.workspace, 'connect.identities.json'), 'utf8'), /test-token/)
})

for (const [failure, reason, stage] of [
  ['same-deployment','requalification_new_deployment_missing','connected_runtime_redeployment'],
  ['unrelated-status','old_receipt_not_stale','connected_stale_receipt'],
  ['wrong-plan','requalification_plan_mismatch','connected_requalification'],
  ['bad-check-passed','command_failed','connected_failed_authorization'],
  ['bad-check-created-receipt','failed_requalification_created_receipt','connected_failed_authorization'],
  ['wrong-hosted-fingerprint','hosted_integration_result_invalid','connected_hosted_client'],
]) test(`connected journey rejects ${failure}`, async t => {
  const {runner} = await connectionScenario(t, failure)
  await assert.rejects(runner.qualifyConnect({ db: { id:'owned-db' }, app: { slug:'inspector' }, issuerURL:'https://issuer.example', jwt:subject=>`test-token-${subject}` }), new RegExp(reason))
  assert.equal(runner.report.connected_journey, undefined)
  assert.equal(runner.report.resources[0].id, 'owned-app')
  assert.equal(runner.report.current_check, stage)
})

test('failed requalification leaves cleanup scoped to the acknowledged app', async t => {
  const {runner} = await connectionScenario(t, 'bad-check-created-receipt')
  await assert.rejects(runner.qualifyConnect({ db:{id:'owned-db'}, app:{slug:'inspector'}, issuerURL:'https://issuer.example', jwt:subject=>`test-token-${subject}` }), /failed_requalification_created_receipt/)
  const actions = []
  runner.request = async (path, options) => { actions.push([path, options?.method || 'GET']); return {id:'owned-app'} }
  await runner.cleanup()
  assert.deepEqual(actions, [[`/v1/apps/${runner.prefix}-connect`,'GET'], [`/v1/apps/${runner.prefix}-connect`,'DELETE']])
  assert.equal(runner.report.cleanup.result, 'passed')
})

test('connection canary retains only an acknowledged app identity after failure', async t => {
  const runner = await fixture(t)
  runner.workspace = dirname(runner.evidence)
  runner.runCommand = async (_binary, args) => {
    if (!args.includes('--apply')) return ''
    await writeFile(join(runner.workspace, 'connect-receipt.json'), JSON.stringify({ version: 1, app: `${runner.prefix}-connect`, app_id: 'owned-app', database_id: 'owned-db', stage: 'creating' }))
    throw new CanaryFailure('command_failed')
  }
  await assert.rejects(runner.qualifyConnect({ db: { id: 'owned-db' }, app: { slug: 'inspector' }, issuerURL: 'https://issuer.example', jwt: () => 'test-token' }), /command_failed/)
  assert.equal(runner.report.resources[0].id, 'owned-app')
  assert.equal(runner.report.resources[0].state, 'created')
})

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
        return { id: 'id-' + options.body.slug, slug: options.body.slug, url: 'https://issuer.apps.example' }
      }
      if (path.endsWith('/data-api/status')) return { runtime_version: candidateVersion }
      if (options?.method === 'PATCH') assert.deepEqual(options.body, { require_authn: false, public_auth: { mode: 'open' } })
      return { id: 'id-' + path.split('/').at(-1) }
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
  const admission = { outcome: 'admitted', wait_ms: 10, active: 2, queued: 8 }
  assert.equal(matchingRuntimeLog(envelope({ ...runtimeRecord(), admission }), info, 'deployment', 'POST', 'rpc').request_id, requestID)
  const timings = { verification: { duration_ms: 0, complete: true }, upstream: { duration_ms: 10, complete: true } }
  assert.equal(matchingRuntimeLog(envelope({ ...runtimeRecord(), admission, timings }), info, 'deployment', 'POST', 'rpc').request_id, requestID)
  const database_timings = { version: 1, source: 'postgrest_server_timing', state: 'observed', sql_execution: 'unknown', pool_wait: 'unknown', phases: Object.fromEntries(['jwt', 'parse', 'plan', 'transaction', 'response'].map(phase => [phase, { duration_ms: 0, complete: true }])) }
  assert.equal(matchingRuntimeLog(envelope({ ...runtimeRecord(), database_timings }), info, 'deployment', 'POST', 'rpc').request_id, requestID)
  for (const change of [{ source: 'private-test-token' }, { sql_execution: 'observed' }, { pool_wait: 'observed' }, { phases: {} }, { phases: { ...database_timings.phases, transaction: { duration_ms: 0, complete: false } } }, { secret: 'private-test-token' }]) {
    assert.throws(() => matchingRuntimeLog(envelope({ ...runtimeRecord(), database_timings: { ...database_timings, ...change } }), info, 'deployment', 'POST', 'rpc'), /runtime_log_database_timings_invalid/)
  }
  const unknownTiming = { version: 1, source: 'postgrest_server_timing', state: 'unknown', sql_execution: 'unknown', pool_wait: 'unknown', reason: 'missing_header' }
  assert.equal(matchingRuntimeLog(envelope({ ...runtimeRecord(), database_timings: unknownTiming }), info, 'deployment', 'POST', 'rpc').request_id, requestID)
  for (const invalid of [{}, [], { secret: timings.upstream }, { upstream: { complete: true } }, { upstream: { duration_ms: -1, complete: true } }, { upstream: { duration_ms: 10, complete: false } }, { upstream: { ...timings.upstream, secret: 'private-test-token' } }]) {
    assert.throws(() => matchingRuntimeLog(envelope({ ...runtimeRecord(), timings: invalid }), info, 'deployment', 'POST', 'rpc'), /runtime_log_timings_invalid/)
  }
  for (const change of [{ outcome: 'queue_full' }, { active: 3 }, { queued: 9 }, { wait_ms: -1 }, { secret: 'private-test-token' }]) {
    assert.throws(() => matchingRuntimeLog(envelope({ ...runtimeRecord(), admission: { ...admission, ...change } }), info, 'deployment', 'POST', 'rpc'), /runtime_log_admission_invalid/)
  }
  assert.equal(matchingRuntimeLog('', info, 'deployment', 'POST', 'rpc'), null)
  assert.equal(matchingRuntimeLog('not JSON\n'+envelope({ ...runtimeRecord(), request_id: 'other' }), info, 'deployment', 'POST', 'rpc'), null)
  for (const change of [{ status: 409 }, { method: 'GET' }, { route: 'rest' }, { code: 'upstream_error' }, { outcome: 'aborted' }, { secret: 'private-test-token' }]) {
    assert.throws(() => matchingRuntimeLog(envelope({ ...runtimeRecord(), ...change }), info, 'deployment', 'POST', 'rpc'), /runtime_log_contract_invalid/)
  }
  assert.throws(() => matchingRuntimeLog(envelope(runtimeRecord()), info, 'other-deployment', 'POST', 'rpc'), /runtime_log_contract_invalid/)
  for (const change of [{ requestId: null }, { requestId: 'secret' }, { status: 0 }, { durationMs: -1 }, { token: 'private-test-token' }]) assert.throws(() => verifiedDiagnostic({ ...info, ...change }), /sdk_diagnostics_invalid/)
})

async function journey(t, failure = '', mode = 'full') {
  const apps = new Map(), deployments = [], logLines = new Map()
  const edgeHealthPaths = new Map()
  let runner, deletedDB = false, rotated = false, syncCount = 0, instanceReads = 0, cacheConfigured = false, cacheWarm = false
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
      delete: () => { deleted = true; return builder }, insert: () => { builder.then = resolve => resolve({ error: { code: '42501' }, data: null }); return builder }, retry: () => builder,
      then: resolve => { emit(options.onResponse, 'GET', 'rest'); resolve({ error: null, data: deleted ? [{ id: 7 }] : single ? { id: 7, body: 'updated', priority: 0 } : [] }) },
    }
    return builder
  } }) }) }
  runner = await fixture(t, {
    mode,
    repoRoot: new URL('../../../', import.meta.url).pathname, cli: 'gregale', sdkTarball: '/unused/sdk.tgz',
    loadModule: async path => {
      if (path.endsWith('dist/smoke-client.js')) {
        assert.equal(mode, 'smoke')
        return {
          exercise: async () => 7,
          exerciseExact: async (_url, _token, id) => {
            assert.equal(id, 7)
            if (failure === 'exact-round-trip') throw new Error('private-test-token')
          },
        }
      }
      assert.equal(mode, 'full')
      if (path.endsWith('workflow-client.js')) return { exerciseRPC: async (_url, _alice, _bob, _fetch, callback) => {
        if (rotated && failure === 'rotated-rpc') throw new Error('postgres://private-test-token@provider')
        emit(callback, 'POST', 'rpc')
      } }
      return path.endsWith('dist/client.js') ? { exercise: async () => 7 } : fakeSDK
    },
    request: async (path, options) => {
      if (path === '/v1/account') return { id: raw.account_id }
      if (path === '/v1/apps' && options?.method === 'POST') {
        const slug = options.body.slug
        const app = { id: 'id-'+slug, slug, url: `https://${slug}.apps.example` }; apps.set(slug, app); return app
      }
      if (path.endsWith('/data-api/status')) return { runtime_version: failure === 'control-plane-runtime-stale' ? staleVersion : candidateVersion }
      if (options?.method === 'PATCH' && options.body.health_path) edgeHealthPaths.set(path.split('/').at(-1), options.body.health_path)
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
      if (binary === 'npm') {
        assert.equal(options.env.FAAS_TOKEN, undefined)
        assert.equal(options.env.STAGING_CONFIG_JSON, undefined)
        return '{}'
      }
      if (binary.endsWith('/tsc')) {
        assert.equal(mode, 'smoke')
        assert.ok(argv.includes('database.large.types.ts'))
        if (failure === 'large-contract-compile') throw new Error('private-test-token')
        return ''
      }
      const args = argv.slice(1)
      if (args[0] === 'deploy') return JSON.stringify(makeDeployment(args[args.indexOf('--name')+1]))
      if (args[0] === 'data-api' && args[1] === 'create') return JSON.stringify(makeDeployment(args[2]))
      if (args[0] === 'data-api' && args[1] === 'configure') {
        assert.equal(mode, 'smoke')
        assert.deepEqual(args.slice(3), ['--public-read', 'api.smoke_public_catalog', '--public-read-cache', '60s', '--no-restart'])
        cacheConfigured = true
        return '{}'
      }
      if (args[0] === 'data-api' && args[1] === 'refresh') {
        assert.ok(cacheConfigured)
        return JSON.stringify({ wake_id: 'cache-wake', status: 'completed', ready: true })
      }
      if (args[0] === 'data-api' && args[1] === 'types') {
        if (mode === 'smoke') {
          const file = args[args.indexOf('--output') + 1]
          const hash = failure === 'large-contract-stale' ? 'c'.repeat(64) : fingerprint
          const size = failure === 'large-contract-small' ? 100 : 1024 * 1024
          const marker = failure === 'large-contract-missing' ? '' : '"smoke_large_079" "smoke_field_069' + '_'.repeat(45) + '"'
          await writeFile(file, `// Schema fingerprint: ${hash}\n` + ' '.repeat(size) + marker)
        }
        return '{}'
      }
      if (args[0] === 'data-api' && args[1] === 'sync') {
        assert.equal(options.env.FAAS_TOKEN, env.FAAS_TOKEN)
        assert.notEqual(options.env.GREGALE_DATA_API_ACCESS_TOKEN, env.FAAS_TOKEN)
        const claims = JSON.parse(Buffer.from(options.env.GREGALE_DATA_API_ACCESS_TOKEN.split('.')[1], 'base64url'))
        assert.equal(claims.sub, 'alice'); assert.ok(claims.exp - Math.floor(Date.now()/1000) > 1200)
        const config = JSON.parse(await readFile(args[args.indexOf('--config')+1], 'utf8'))
        assert.ok(config.migrate.command.includes('--wait'))
        assert.equal(config.numeric_mode, mode === 'smoke' ? 'exact' : undefined)
        assert.ok(config.check.command.includes(mode === 'smoke' ? 'smoke-client.ts' : 'workflow-client.ts'))
        assert.doesNotMatch(JSON.stringify(config), /private-test-token|eyJ/)
        assert.match(await readFile(join(config.migrate.directory, 'gregale.yaml'), 'utf8'), /node release.mjs/)
        assert.equal(await readFile(join(config.migrate.directory, 'rpc-runtime/types.mjs'), 'utf8'), await readFile(join(runner.repoRoot, 'cmd/gregale/templates/data-api/types.mjs'), 'utf8'))
        assert.equal(await readFile(join(config.migrate.directory, 'rpc-runtime/type-render.mjs'), 'utf8'), await readFile(join(runner.repoRoot, 'cmd/gregale/templates/data-api/type-render.mjs'), 'utf8'))
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
        if (args[1] === 'get') return JSON.stringify({ id: 'db-id', state: deletedDB ? 'deleted' : 'ready' })
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
    const requestURL = new URL(url)
    const slug = requestURL.hostname.split('.')[0]
    if ((edgeHealthPaths.get(slug) ?? '/healthz') === requestURL.pathname || (requestURL.pathname === readinessPath && failure === 'edge-health')) {
      return Response.json({ status: 'ok', source: 'edge' }, { headers: { 'X-Faas-Health-Source': 'edge' } })
    }
    if (requestURL.pathname === '/healthz') {
      if (slug.endsWith('-migrate')) return Response.json({ migration_credential_present: false })
    }
    if (requestURL.pathname === readinessPath && slug.endsWith('-api')) {
      assert.equal(edgeHealthPaths.has(slug), false, 'Data API must exercise the default edge health policy')
      const runtime_version = failure === 'serving-runtime-stale' ? staleVersion
        : failure === 'serving-runtime-invalid' ? 'private-test-token' : candidateVersion
      return Response.json({ version: 1, live: true, ready: true, runtime_version })
    }
    if (requestURL.pathname === '/rest/v1/smoke_public_catalog') {
      assert.ok(cacheConfigured)
      assert.equal(requestURL.searchParams.get('select'), 'id,name')
      assert.equal(options.headers['Accept-Profile'], 'api')
      assert.equal(options.headers.Authorization, undefined)
      if (options.headers.Accept === 'application/json') {
        const hit = cacheWarm && failure !== 'cache-no-hit'
        cacheWarm = true
        const headers = hit ? { 'X-Faas-Cache': 'hit', 'X-Faas-Request-Id': cacheJSONRequestID } : {}
        if (failure === 'cache-missing-id') delete headers['X-Faas-Request-Id']
        return Response.json([{ id: 1, name: 'canary' }], { headers })
      }
      assert.equal(options.headers.Accept, 'text/csv')
      assert.ok(cacheWarm)
      if (failure === 'cache-csv-json') return Response.json([{ id: 1, name: 'canary' }])
      if (failure === 'cache-csv-hit') return new Response('id,name\n1,canary\n', { headers: { 'Content-Type': 'text/csv', 'X-Faas-Cache': 'hit' } })
      if (failure === 'cache-csv-stale') return new Response('id,name\n1,canary\n', { headers: { 'Content-Type': 'text/csv', 'X-Faas-Cache': 'stale-while-revalidate' } })
      if (failure === 'cache-csv-body') return new Response('id,name\n1,wrong\n', { headers: { 'Content-Type': 'text/csv' } })
      return new Response('id,name\n1,canary\n', { headers: { 'Content-Type': 'text/csv', 'X-Faas-Request-Id': failure === 'cache-reused-id' ? cacheJSONRequestID : cacheCSVRequestID } })
    }
    if (url.includes('/rest/v1/rpc/')) return new Response('{}', { status: 404 })
    if (url.endsWith('/__gregale/schema') && !options?.headers?.Authorization) return new Response('{}', { status: 401 })
    if (url.endsWith('/__gregale/schema')) return new Response(JSON.stringify({ version: 1, ready: true, fingerprint: failure === 'serving-stale' ? 'c'.repeat(64) : runner.report.syncs.at(-1).fingerprint }))
    if (url.endsWith('/openapi.json')) return new Response('{"paths":{"/notes":{},"/rpc/create_note":{"post":{}}}}')
    return new Response('{}', { status: 401 })
  }
  runner.captureContract = async () => '/simulated/contract.json'
  runner.qualifyRollout = async () => assert.equal(mode, 'full')
  runner.qualifyLoad = async () => runner.check('hosted_load_and_recovery', async () => {
    assert.equal(mode, 'full')
    if (failure === 'hosted-load') throw new CanaryFailure('hosted_load_failed')
  })
  // The simulated API is immediately ready; no sleeping or provider access.
  runner.wait = async (read, ready) => {
    const value = await read()
    if (failure === 'cache-no-hit' && value === null) throw new CanaryFailure('operation_timeout')
    assert.ok(ready(value), 'simulated operation must be complete')
    return value
  }
  return runner
}

test('complete canary journey verifies three syncs, RPC grants, SDK logs, rotation and cleanup', async t => {
  const runner = await journey(t)
  const report = await runner.run()
  assert.equal(report.result, 'passed')
  assert.equal(report.mode, 'full')
  assert.equal(report.cleanup.result, 'passed')
  assert.equal(report.syncs.length, 3)
  assert.equal(report.requests.length, 3)
  assert.deepEqual(report.requests.map(x => [x.method,x.route]), [['POST','rpc'],['GET','rest'],['POST','rpc']])
  assert.ok(report.checks.includes('rpc_denied_before_permission_setup'))
  assert.ok(report.checks.includes('schema_migration_sync_native_build'))
  assert.ok(report.checks.includes('credential_rotation_preserves_data'))
  assert.ok(report.checks.includes('hosted_load_and_recovery'))
  assert.deepEqual(report.runtime, { candidate_version: candidateVersion, control_plane_version: candidateVersion, serving_version: candidateVersion })
  assert.ok(report.checks.indexOf('candidate_control_plane_runtime') < report.checks.indexOf('database_ready'))
  assert.ok(report.checks.indexOf('candidate_serving_runtime') < report.checks.indexOf('typed_crud_and_subject_isolation'))
  assert.equal(report.deployments.length, 6)
  assert.deepEqual(report.rotation, { binding_id: 'api-binding', previous_generation: 1, current_generation: 2, completed: true })
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /private-test-token|postgres:\/\/|eyJ|atomic canary/)
})

test('smoke verifies the candidate and exact types with one sync before cleaning up', async t => {
  const runner = await journey(t, '', 'smoke')
  const report = await runner.run()
  assert.equal(report.result, 'passed')
  assert.equal(report.mode, 'smoke')
  assert.equal(report.cleanup.result, 'passed')
  assert.ok(report.resources.every(resource => resource.state === 'deleted'))
  assert.equal(report.deployments.length, 4)
  assert.equal(report.syncs.length, 1)
  assert.ok(report.large_contract.bytes > 1024 * 1024)
  assert.ok(report.checks.includes('large_contract_download'))
  assert.equal(report.runtime.serving_version, candidateVersion)
  assert.ok(report.checks.includes('serving_fingerprint_agreement'))
  assert.ok(report.checks.includes('typed_crud_and_subject_isolation'))
  assert.ok(report.checks.includes('exact_bigint_round_trip'))
  assert.equal(report.checks.at(-1), 'public_cache_json_then_csv')
  assert.deepEqual(report.public_cache, { json_hit: true, csv_verified: true, json_request_id: cacheJSONRequestID, csv_request_id: cacheCSVRequestID, gateway_attribution: 'unavailable' })
  assert.deepEqual(report.instances, [])
  assert.equal(report.rotation, undefined)
})

for (const mode of ['smoke', 'full']) {
  test(`${mode} runs the optional inspection against its deployed fixture`, async t => {
    const runner = await journey(t, '', mode)
    runner.config.write_inspection = true
    let called = 0
    runner.qualifyWriteInspection = async ({ db, app }) => {
      called++
      assert.ok(db.id && app.id)
      assert.ok(runner.report.deployments.some(x => x.app === app.slug))
    }
    const report = await runner.run()
    assert.equal(called, 1)
    assert.ok(report.checks.includes('write_inspection_binding_identity'))
    assert.equal(report.cleanup.result, 'passed')
  })
}

test('failed optional inspection fails the canary and cleans up', async t => {
  const runner = await journey(t, '', 'smoke')
  runner.config.write_inspection = true
  runner.qualifyWriteInspection = async () => { throw new CanaryFailure('inspection_report_invalid') }
  await assert.rejects(runner.run(), /staging_canary_failed/)
  assert.equal(runner.report.failure.check, 'write_inspection_binding_identity')
  assert.equal(runner.report.cleanup.result, 'passed')
})

for (const failure of ['cache-no-hit', 'cache-csv-json', 'cache-csv-hit', 'cache-csv-stale', 'cache-csv-body', 'cache-missing-id', 'cache-reused-id']) {
  test(`smoke rejects ${failure} and cleans up`, async t => {
    const runner = await journey(t, failure, 'smoke')
    await assert.rejects(runner.run(), /staging_canary_failed/)
    assert.equal(runner.report.failure.check, 'public_cache_json_then_csv')
    assert.equal(runner.report.cleanup.result, 'passed')
    assert.ok(runner.report.resources.every(resource => resource.state === 'deleted'))
    assert.equal(runner.report.public_cache, undefined)
  })
}

for (const failure of ['large-contract-small', 'large-contract-stale', 'large-contract-missing', 'large-contract-compile']) {
  test(`smoke rejects ${failure} and cleans up`, async t => {
    const runner = await journey(t, failure, 'smoke')
    await assert.rejects(runner.run(), /staging_canary_failed/)
    assert.equal(runner.report.failure.check, 'large_contract_download')
    assert.equal(runner.report.cleanup.result, 'passed')
    assert.ok(runner.report.resources.every(resource => resource.state === 'deleted'))
  })
}

test('smoke fails and cleans up when the exact numeric round trip fails', async t => {
  const runner = await journey(t, 'exact-round-trip', 'smoke')
  await assert.rejects(runner.run(), /staging_canary_failed/)
  assert.equal(runner.report.result, 'failed')
  assert.equal(runner.report.failure.check, 'exact_bigint_round_trip')
  assert.equal(runner.report.cleanup.result, 'passed')
  assert.ok(runner.report.resources.every(resource => resource.state === 'deleted'))
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /private-test-token|postgres:\/\/|eyJ/)
})

for (const [failure, stage, code] of [
  ['control-plane-runtime-stale', 'candidate_control_plane_runtime', 'control_plane_runtime_version_mismatch'],
  ['serving-runtime-stale', 'candidate_serving_runtime', 'serving_runtime_version_mismatch'],
  ['serving-runtime-invalid', 'candidate_serving_runtime', 'serving_runtime_version_missing'],
  ['edge-health', 'candidate_serving_runtime', 'serving_runtime_health_unavailable'],
  ['sync-unverified','rpc_permissions_sync_native_build'], ['serving-stale','serving_fingerprint_agreement'],
  ['unsafe-log','typed_rpc_subject_isolation_and_logs'], ['rotated-rpc','credential_rotation_preserves_data'],
  ['rotation-unchanged','credential_rotation_preserves_data'],
  ['hosted-load','hosted_load_and_recovery'],
]) test(`failed ${failure} canary retains redacted evidence and cleans resources`, async t => {
  const runner = await journey(t, failure)
  await assert.rejects(runner.run(), /staging_canary_failed/)
  assert.equal(runner.report.result, 'failed')
  assert.equal(runner.report.failure.check, stage)
  if (code) assert.equal(runner.report.failure.code, code)
  if (failure === 'control-plane-runtime-stale') {
    assert.deepEqual(runner.report.runtime, { candidate_version: candidateVersion, control_plane_version: staleVersion, serving_version: null })
    assert.deepEqual(runner.report.resources.map(resource => resource.kind), ['app'])
  }
  if (stage === 'candidate_serving_runtime') {
    assert.equal(runner.report.runtime.serving_version, failure === 'serving-runtime-stale' ? staleVersion : null)
    assert.ok(!runner.report.checks.includes('typed_crud_and_subject_isolation'))
  }
  assert.equal(runner.report.cleanup.result, 'passed')
  assert.doesNotMatch(await readFile(runner.evidence, 'utf8'), /private-test-token|postgres:\/\/|eyJ/)
})
