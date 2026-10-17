import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile, mkdtemp, rm, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { setImmediate as turn } from 'node:timers/promises'
import * as sdk from '../../../sdk/data/dist/index.js'
import { options, LoadFailure, writeEvidence } from '../load/contracts.mjs'
import { verifyLoad } from '../load/runner.mjs'
import { Canary, validateConfig } from './canary.mjs'
import { hostedBounds, hostedProfile, hostedMetadata } from './load-contracts.mjs'
import { HostedFixture, hostedScenarios, qualifyHostedLoad } from './load.mjs'

const root = new URL('../../../', import.meta.url).pathname
const sdkDirectory = join(root, 'sdk/data/dist')
const fingerprint = 'b'.repeat(64)
const workload = { requests: 16, users: 2, concurrency: 4 }
async function fixture(t, failure = '') {
  const directory = await mkdtemp(join(tmpdir(), 'hosted-load-contract-')); t.after(() => rm(directory, { recursive: true, force: true }))
  const file = join(directory, 'evidence.json'), rows = new Map(), calls = [], rate = new Map()
  const app = { id: 'owned-id', slug: 'owned-api', url: 'https://owned.apps.example' }
  let wakes = 0, pausing = 0, rejectedWrite = false, interrupted, parent
  const entered = new Promise(resolve => { interrupted = resolve })
  const response = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })
  const visible = (subject, tenant) => [...rows.values()].filter(row => row.subject === subject && row.tenant === tenant)
  parent = {
    prefix: 'canary-run', workspace: directory, repoRoot: root,
    config: { environment: 'staging', api: 'https://management.example', token: 'private-management-token', sourceCommit: 'a'.repeat(40), builder_node_ids: ['native-builder'], load: workload },
    report: { checks: ['staging_account'], resources: [{ kind: 'database', id: 'db-id', state: 'created' }, { kind: 'app', id: app.id, slug: app.slug, state: 'created' }],
      deployments: [{ app: app.slug, deployment_id: 'deployment', build_id: 'build', builder_node_id: 'native-builder', source_sha256: 'a'.repeat(64) }], syncs: [{ fingerprint }] },
    appURL: () => app.url, loadModule: async () => sdk,
    save: async () => writeEvidence(file, parent.report),
    check: async (name, action) => { parent.report.current_check = name; await action(); parent.report.checks.push(name) },
    exec: async (args, _cwd, stdin, _token, signal) => {
      signal.throwIfAborted(); calls.push(args)
      assert.deepEqual(args, ['secrets', 'set', '--app', app.slug, '--from-stdin'])
      assert.equal(stdin, 'DATA_API_RATE_LIMIT_PER_SECOND=2\nDATA_API_RATE_LIMIT_BURST=8\n')
    },
    refresh: async (_app, signal) => {
      signal.throwIfAborted(); wakes++
      return { wake_id: failure === 'reused-wake' ? 'same-wake' : `wake-${wakes}`, status: failure === 'queued-restart' ? 'queued' : 'completed', ready: failure !== 'not-ready' }
    },
    contract: async (_url, _jwt, hash, signal) => { signal.throwIfAborted(); assert.equal(hash, fingerprint) },
    request: async (_path, { signal }) => {
      signal.throwIfAborted()
      return [{ id: 'instance-id', deployment_id: 'deployment', state: parent.parkObserved ? 'RUNNING' : 'PARKED' }]
    },
    wait: async (read, ready, _timeout, signal) => {
      signal.throwIfAborted(); const value = await read()
      if (failure === 'no-park' || !ready(value)) throw new LoadFailure('engine_restart_failed')
      parent.parkObserved = true; return value
    },
    publicFetch: async (input, init = {}) => {
      init.signal?.throwIfAborted()
      if (input.endsWith('/healthz')) return response({ ready: failure !== 'bad-health' })
      const url = new URL(input), auth = new Headers(init.headers).get('authorization')
      assert.doesNotMatch(auth, /private-management/)
      const [, subject, tenant] = auth.split('|')
      if (failure === 'interrupted') {
        interrupted()
        await new Promise(resolve => init.signal.addEventListener('abort', resolve, { once: true })); init.signal.throwIfAborted()
      }
      if (failure === 'raw-error') throw new Error('postgres://private-password@provider private-row-value')
      if (failure === 'large-body') return new Response('x'.repeat(1048577))
      const body = init.body ? JSON.parse(init.body) : null
      const op = url.pathname.split('/').at(-1)
      if (op === 'load_pause') {
        pausing++; await turn()
        if (pausing > 2) { pausing--; return response({ code: 'data_api_overloaded' }, 503) }
        await turn(); pausing--; return response({ subject, tenant })
      }
      if (op === 'load_ping') {
        const count = (rate.get(subject) ?? 0) + 1; rate.set(subject, count)
        if (count > 8) return response({ code: failure === 'wrong-rate-code' ? 'private-error' : 'data_api_rate_limited' }, 429)
        return response({ subject, tenant })
      }
      if (init.method === 'POST' && op !== 'load_visible') {
        const key = body.note_key ?? body.key
        if (body.subject && body.subject !== subject) return response({ code: '42501' }, 403)
        const row = rows.get(key) ?? { key, subject, tenant, body: body.payload ?? body.body, invocations: 0 }
        row.invocations++; rows.set(key, row)
        if (failure === 'rejected-commit' && op === 'load_notes' && !key.includes('seed') && !rejectedWrite) { rejectedWrite = true; return response({ code: 'data_api_overloaded' }, 503) }
        return response([row], op === 'load_notes' ? 201 : 200)
      }
      let found = visible(subject, tenant)
      if (url.searchParams.has('key')) found = found.filter(row => 'eq.' + row.key === url.searchParams.get('key'))
      if (failure === 'rls-leak' && found.length) found = found.map(row => ({ ...row, subject: 'private-other-subject' }))
      if (failure === 'duplicate' && subject.endsWith('-lost-reply')) found = found.map(row => ({ ...row, invocations: 2 }))
      return response(found)
    },
  }
  const jwt = (subject, _audience, _lifetime, tenant = 'canary') => `app|${subject}|${tenant}`
  const target = new HostedFixture(parent, { app, url: app.url, jwt, sdk, sdkDirectory })
  // Qualification contracts use real SDK HTTP serialization. Time-dependent
  // token refill and parking are simulated here; no staging claims are made.
  const steps = hostedScenarios.map(([name, action]) => [name, ctx => action({ ...ctx, wait: async () => { rate.clear() } })])
  return { parent, target, app, jwt, file, calls, entered, steps, run: extra => verifyLoad(target, workload, {
    profile: hostedProfile, steps, save: async report => { parent.report.load = report; await parent.save() }, ...extra,
  }) }
}

test('hosted limits mirror canonical bounds and preserve strict workload validation', async () => {
  const source = await readFile(new URL('../../../pkg/api/limits.go', import.meta.url), 'utf8')
  for (const [key, suffix] of Object.entries({ runMs: 'RunTimeoutMS', scenarioMs: 'ScenarioTimeoutMS', parkMs: 'ParkTimeoutMS', restartMs: 'RestartTimeoutMS', pauseMs: 'PauseMS', responseBytes: 'ResponseMaxBytes' })) {
    assert.match(source, new RegExp(`DataAPIHostedLoad${suffix}\\s*=\\s*${hostedBounds[key]}\\b`))
  }
  assert.match(await readFile(new URL('./load-fixture.sql', import.meta.url), 'utf8'), new RegExp(`pg_sleep\\(${hostedBounds.pauseMs / 1000}\\)`))
  assert.throws(() => options({ requests: 1001 }), /invalid_options/)
  const raw = { environment: 'staging', api: 'https://management.example', account_id: 'id', region: 'eu', builder_node_ids: ['native'], app_host_suffix: '.apps.example', load: { concurrency: 33 } }
  assert.throws(() => validateConfig(raw, { FAAS_TOKEN: 'private', FAAS_DATA_API_SOURCE_COMMIT: 'a'.repeat(40) }), /invalid_load_options/)
})
test('seven hosted scenarios use the installed SDK, preserve receipts and retain explicit private evidence', async t => {
  const f = await fixture(t), report = await f.run()
  assert.equal(report.result, 'passed', report.failure?.code); assert.equal(report.cleanup.state, 'complete')
  assert.equal(report.scope, 'hosted-data-api-load'); assert.equal(report.qualification, 'isolated_staging_only')
  assert.equal(report.scenarios.length, 7); assert.equal(report.cleanup_scope, 'client_requests_only')
  assert.equal(report.resource_cleanup_owner, 'parent_staging_canary')
  assert.equal(report.coverage.unsupported.sql_connection_peaks, 'serving_role_sql_observer_not_available')
  assert.equal(report.sources.engine_sha256, undefined)
  assert.equal(f.parent.report.load_lifecycle.length, 2)
  assert.ok(report.scenarios.find(s => s.name === 'admission_pressure').traffic.statuses[503] > 0)
  assert.ok(report.scenarios.find(s => s.name === 'rate_pressure').traffic.statuses[429] > 0)
  assert.equal(report.scenarios.find(s => s.name === 'lost_reply').observations.reply_reconciled_without_replay, true)
  assert.equal((await stat(f.file)).mode & 0o777, 0o600)
  assert.doesNotMatch(await readFile(f.file, 'utf8'), /private-|fixture-value|app\||postgres:|https:\/\/owned/)
  assert.equal(f.target.pending.size, 0)
})
for (const [failure, code] of [
  ['rls-leak', 'rls_violation'], ['duplicate', 'receipt_not_reconciled'], ['rejected-commit', 'receipt_not_reconciled'],
  ['queued-restart', 'engine_restart_failed'], ['not-ready', 'engine_restart_failed'], ['reused-wake', 'engine_restart_failed'],
  ['no-park', 'engine_restart_failed'], ['bad-health', 'engine_restart_failed'], ['wrong-rate-code', 'unexpected_http_result'],
  ['raw-error', 'unexpected_http_result'], ['large-body', 'unexpected_http_result'],
]) test(`hosted ${failure} fails without publishing payloads or leaking active requests`, async t => {
  const f = await fixture(t, failure), report = await f.run()
  assert.equal(report.result, 'failed'); assert.equal(report.failure.code, code)
  assert.equal(report.cleanup.state, 'complete'); assert.equal(f.target.pending.size, 0)
  assert.doesNotMatch(await readFile(f.file, 'utf8'), /private-|fixture-value|app\||postgres:/)
})
test('unacknowledged resources and unqualified build identities fail before hosted mutation', async t => {
  for (const change of [f => { f.parent.report.resources[1].id = 'other-id' }, f => { f.parent.report.deployments[0].builder_node_id = 'unqualified' }, f => { f.parent.report.deployments[0].source_sha256 = 'private-source' }]) {
    const f = await fixture(t); change(f)
    const report = await f.run(); assert.equal(report.result, 'failed'); assert.equal(f.calls.length, 0)
  }
})
test('hosted interruption aborts SDK transport, saves partial results and drains requests', { timeout: 10000 }, async t => {
  const f = await fixture(t, 'interrupted'), controller = new AbortController()
  const pending = f.run({ signal: controller.signal })
  await f.entered; controller.abort()
  const report = await pending
  assert.equal(report.result, 'interrupted'); assert.equal(report.failure.code, 'interrupted')
  assert.equal(report.cleanup.state, 'complete'); assert.equal(f.target.pending.size, 0)
})
test('failed hosted checks propagate through the canary hook', async t => {
  const f = await fixture(t)
  f.parent.loadModule = async () => sdk
  await assert.rejects(qualifyHostedLoad(f.parent, { app: f.app, url: f.app.url, jwt: f.jwt, clientDir: '/missing-client' }))
  assert.equal(f.parent.report.load.result, 'failed')
})
test('metadata drops extras and rejects malformed identities', () => {
  assert.throws(() => hostedMetadata({ source_commit: 'private-token' }), /source_identity_invalid/)
})
test('public transport honors caller cancellation and refuses redirects', async t => {
  const original = globalThis.fetch; t.after(() => { globalThis.fetch = original })
  const controller = new AbortController(); controller.abort()
  globalThis.fetch = async (_input, init) => { assert.equal(init.redirect, 'error'); assert.equal(init.signal.aborted, true); throw new TypeError('aborted') }
  await assert.rejects(Canary.prototype.publicFetch.call({}, 'https://owned.apps.example', { signal: controller.signal }))
})
