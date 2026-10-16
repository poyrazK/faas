import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { randomBytes } from 'node:crypto'
import * as sdk from '../../../sdk/data/dist/index.js'
import { LocalFixture, localSettings } from '../load/local.mjs'
import { options } from '../load/contracts.mjs'
import { verifyLoad } from '../load/runner.mjs'
import { HostedFixture, hostedScenarios } from './load.mjs'
import { hostedProfile } from './load-contracts.mjs'

const enabled = process.env.DATA_API_HOSTED_LOAD_SQL_TEST === '1'
test('hosted adapter exercises real SQL, JWT tenant RLS, receipts and runtime pressure with simulated platform controls', { skip: !enabled, timeout: 90000 }, async t => {
  const workload = options({ requests: 40, concurrency: 8, users: 4 })
  const local = new LocalFixture(localSettings(process.env), workload), signal = t.signal
  t.after(async () => assert.equal((await local.cleanup(AbortSignal.timeout(30000))).complete, true))
  await local.setup(randomBytes(12).toString('hex'), signal)
  await local.stop()
  await local.owner.query(await readFile(new URL('./load-fixture.sql', import.meta.url), 'utf8'))
  await local.owner.query(`GRANT SELECT,INSERT,UPDATE,DELETE ON api.load_notes TO "${local.role}";
    GRANT EXECUTE ON FUNCTION api.load_write(text,text),api.load_visible(),api.load_ping(),api.load_pause() TO "${local.role}"`)
  const deniedURL = new URL(local.login)
  const client = local.client(deniedURL.toString()); await client.connect()
  try {
    await client.query('BEGIN'); await client.query('SET LOCAL ROLE NONE')
    const acl = (await local.owner.query("SELECT proacl::text FROM pg_proc WHERE oid='api.load_write(text,text)'::regprocedure")).rows[0].proacl
    assert.doesNotMatch(acl, /[,{]=X\//)
  } finally { await client.query('ROLLBACK'); await client.end() }
  await local.start(signal)
  let wakes = 0, parked = false
  const app = { id: 'local-app', slug: 'local-api', url: 'https://fixture.apps.example' }
  const parent = {
    prefix: 'load-fixture', workspace: '/tmp', repoRoot: new URL('../../../', import.meta.url).pathname,
    config: { environment: 'staging', sourceCommit: 'a'.repeat(40), builder_node_ids: ['simulated-builder'] },
    report: { checks: ['staging_account'], resources: [{ kind: 'database', id: 'local-db', state: 'created' }, { kind: 'app', id: app.id, slug: app.slug, state: 'created' }],
      deployments: [{ app: app.slug, deployment_id: 'local-deployment', build_id: 'local-build', builder_node_id: 'simulated-builder', source_sha256: 'b'.repeat(64) }], syncs: [{ fingerprint: 'c'.repeat(64) }] },
    appURL: () => app.url,
    exec: async () => {}, // LocalFixture already uses the same 2/8 rate policy.
    refresh: async () => { await local.stop(); await local.start(signal); return { status: 'completed', ready: true, wake_id: `simulated-wake-${++wakes}` } },
    contract: async () => {},
    publicFetch: (input, init) => fetch(local.base + new URL(input).pathname + new URL(input).search, { ...init, redirect: 'error' }),
    request: async () => [{ id: 'simulated-instance', deployment_id: 'local-deployment', state: parked ? 'RUNNING' : 'PARKED' }],
    wait: async (read, ready) => { const result = await read(); assert.ok(ready(result)); parked = true; return result },
  }
  const jwt = (subject, _audience, _lifetime, tenant) => new local.jose.SignJWT({ sub: subject, tenant }).setProtectedHeader({ alg: 'ES256' })
    .setIssuer('https://issuer.invalid').setAudience('load-fixture').setExpirationTime('5m').sign(local.keys.privateKey)
  const adapter = new HostedFixture(parent, { app, url: app.url, jwt, sdk, sdkDirectory: join(parent.repoRoot, 'sdk/data/dist') })
  const report = await verifyLoad(adapter, workload, { signal, steps: hostedScenarios, profile: hostedProfile })
  assert.equal(report.result, 'passed', JSON.stringify({ failure: report.failure,
    scenarios: report.scenarios.map(({ name, result, failure, traffic }) => ({ name, result, failure, statuses: traffic?.statuses, codes: traffic?.codes })) }))
  assert.equal(report.scenarios.length, 7); assert.equal(report.cleanup.state, 'complete')
  const rows = (await local.owner.query('SELECT subject,tenant,key,invocations FROM api.load_notes')).rows
  assert.ok(rows.length > 0); assert.ok(rows.every(row => row.tenant === 'canary' && row.invocations === 1))
  assert.equal(rows.filter(row => row.key === 'lost-reply').length, 1)
  assert.equal(rows.filter(row => row.key.endsWith('-forbidden')).length, 0)
})
