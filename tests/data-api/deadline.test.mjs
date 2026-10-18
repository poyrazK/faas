import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function port() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const number = server.address().port
  await new Promise(resolve => server.close(resolve))
  return number
}
async function until(check) {
  const end = performance.now() + 10000
  while (!await check()) {
    if (performance.now() >= end) throw Error('SQL deadline fixture did not recover')
    await new Promise(resolve => setTimeout(resolve, 20))
  }
}

test('a real SQL lock hits the total HTTP deadline and recovers with restricted-role RLS', { skip: !enabled, timeout: 45000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `deadline_${suffix}`, role = `deadline_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null && child.signalCode === null) { child.kill(); await once(child, 'exit') }
    if (owner) { await owner.query('ROLLBACK'); await owner.end() }
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  // The fixture's SQL timeout is deliberately longer than the HTTP deadline.
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=30000`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.notes(id text PRIMARY KEY, subject text NOT NULL);
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT ON api.notes TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  config.snapshot = await inspect(config.connection, config.schemas)
  config.fingerprint = fingerprint(config.snapshot)
  const upstreamPort = await port(), readyPort = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
  await until(async () => {
    try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false }
  })
  const records = []
  gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  await owner.query('BEGIN; LOCK TABLE api.notes IN ACCESS EXCLUSIVE MODE')
  const started = performance.now()
  const blocked = fetch(base + '/rest/v1/notes', { headers: { Authorization: 'Bearer alice' } })
  await until(async () => (await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count === '1')
  const response = await blocked
  const body = await response.json()
  assert.equal(response.status, 504)
  assert.equal(body.code, 'request_deadline_exceeded')
  assert.ok(performance.now() - started >= limits.queryMs - 100)
  assert.ok(performance.now() - started < limits.queryMs + 4000)
  await until(() => records.some(record => record.deadline))
  assert.equal(records[0].request_id, body.request_id)
  assert.equal(records[0].deadline.phase, 'upstream')
  assert.equal(records[0].deadline.dispatched, true)
  assert.equal(records[0].timings.verification.complete, true)
  assert.equal(records[0].timings.signing.complete, true)
  assert.equal(records[0].timings.upstream.complete, false)
  assert.ok(records[0].timings.upstream.duration_ms >= limits.queryMs - 1000)
  assert.equal(records[0].timings.streaming, undefined)
  assert.equal(records[0].database_timings.state, 'unknown')
  assert.equal(records[0].database_timings.reason, 'no_response')
  assert.equal(records[0].database_timings.phases, undefined)
  assert.equal((await fetch(base + '/livez')).status, 200)
  await owner.query('COMMIT')
  const recovered = await fetch(base + '/rest/v1/notes', { method: 'POST', headers: { Authorization: 'Bearer alice', 'Content-Type': 'application/json' }, body: JSON.stringify({ id: 'recovered', subject: 'alice' }) })
  assert.equal(recovered.status, 201); await recovered.text()
  const isolated = await fetch(base + '/rest/v1/notes', { headers: { Authorization: 'Bearer bob' } })
  assert.equal(isolated.status, 200)
  assert.deepEqual(await isolated.json(), [])
  assert.equal((await owner.query('SELECT count(*) FROM api.notes')).rows[0].count, '1')
  const recoveredRecord = records.find(record => record.status === 201)
  assert.equal(recoveredRecord.timings.upstream.complete, true)
  assert.equal(recoveredRecord.timings.streaming.complete, true)
  assert.equal(recoveredRecord.database_timings.state, 'observed')
  assert.equal(recoveredRecord.database_timings.phases.transaction.complete, true)
})
