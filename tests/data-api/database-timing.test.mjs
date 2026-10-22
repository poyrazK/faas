import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn, execFile } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { promisify } from 'node:util'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function probePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port
  await new Promise(resolve => server.close(resolve))
  return value
}
// A probe port is closed before it is used, so the OS can hand the same port
// to the next probe; PostgREST then refuses an admin port equal to its server
// port. Never return a port twice in one test file.
const issuedPorts = new Set()
async function port() {
  for (;;) {
    const value = await probePort()
    if (!issuedPorts.has(value)) {
      issuedPorts.add(value)
      return value
    }
  }
}
async function until(check) {
  const end = performance.now() + 10000
  while (!await check()) {
    if (performance.now() >= end) throw Error('Database timing fixture did not reach its expected state')
    await new Promise(resolve => setTimeout(resolve, 20))
  }
}

test('pinned engine transaction timings include pool contention and handle SQL errors, cancellation and restart', { skip: !enabled, timeout: 45000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `db_timing_${suffix}`, role = `db_timing_login_${suffix}`
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
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=2000`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.notes(subject text NOT NULL);
    INSERT INTO api.notes VALUES('private-subject');
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE TABLE api.blocked(id integer);
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT ON api.notes,api.blocked TO "${role}";
    CREATE FUNCTION api.timing_probe(delay_ms integer) RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS
      'BEGIN PERFORM pg_sleep(delay_ms / 1000.0); RETURN 1; END';
    REVOKE EXECUTE ON FUNCTION api.timing_probe(integer) FROM PUBLIC;
    GRANT EXECUTE ON FUNCTION api.timing_probe(integer) TO "${role}";
    COMMENT ON FUNCTION api.timing_probe(integer) IS '@gregale:rpc'`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  config.snapshot = await inspect(config.connection, config.schemas)
  config.functions = config.snapshot.functions; config.fingerprint = fingerprint(config.snapshot)
  const upstreamPort = await port(), readyPort = await port()
  async function startEngine() {
    child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
    await until(async () => {
      try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false }
    })
  }
  await startEngine()
  const records = []
  gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  const headers = { Authorization: 'Bearer private-subject', 'Content-Type': 'application/json' }
  async function logged(response) {
    const id = response.headers.get('x-request-id')
    await until(() => records.some(record => record.request_id === id))
    const timing = records.find(record => record.request_id === id).database_timings
    assert.equal(timing.source, 'postgrest_server_timing')
    assert.equal(timing.sql_execution, 'unknown'); assert.equal(timing.pool_wait, 'unknown')
    return timing
  }
  const slow = await fetch(base + '/rest/v1/rpc/timing_probe', { method: 'POST', headers, body: '{"delay_ms":250}' })
  assert.equal(slow.status, 200); assert.equal(await slow.json(), 1)
  const slowTiming = await logged(slow)
  assert.equal(slowTiming.state, 'observed')
  assert.ok(slowTiming.phases.transaction.duration_ms >= 200, 'engine header durations must be milliseconds')
  assert.equal(slowTiming.phases.transaction.complete, true)
  assert.equal(slow.headers.get('server-timing'), null, 'raw engine telemetry stays private')

  // Occupy both engine connections outside gateway admission. The gateway read
  // uses another, unlocked table, so its combined timer must include pool wait.
  await owner.query('BEGIN; LOCK TABLE api.blocked IN ACCESS EXCLUSIVE MODE')
  const token = await new SignJWT({ role, sub: 'private-subject' }).setProtectedHeader({ alg: 'HS256' }).setExpirationTime('1h').sign(new TextEncoder().encode(config.secret.toString('base64url')))
  const blockers = Array.from({ length: limits.pool }, () => fetch(`http://127.0.0.1:${upstreamPort}/blocked`, { headers: { Authorization: `Bearer ${token}` } }))
  await until(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count) === limits.pool)
  const contended = fetch(base + '/rest/v1/notes', { headers })
  await new Promise(resolve => setTimeout(resolve, 300))
  assert.equal(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename=$1', [role])).rows[0].count), limits.pool, 'telemetry must not add tenant SQL connections')
  await owner.query('COMMIT')
  for (const response of await Promise.all(blockers)) { assert.equal(response.status, 200); await response.text() }
  const pooled = await contended
  assert.equal(pooled.status, 200); assert.deepEqual(await pooled.json(), [{ subject: 'private-subject' }])
  const pooledTiming = await logged(pooled)
  assert.ok(pooledTiming.phases.transaction.duration_ms >= 200, 'transaction timer must include pool acquisition')

  const sqlError = await fetch(base + '/rest/v1/rpc/timing_probe', { method: 'POST', headers, body: '{"delay_ms":60000}' })
  assert.equal(sqlError.status, 500); await sqlError.text()
  const errorTiming = await logged(sqlError)
  assert.equal(errorTiming.state, 'unknown'); assert.equal(errorTiming.reason, 'missing_header')
  assert.equal(errorTiming.phases, undefined, 'SQL errors must not reuse a successful sample')

  const abort = new AbortController()
  const cancelled = fetch(base + '/rest/v1/rpc/timing_probe', { method: 'POST', headers, body: '{"delay_ms":60000}', signal: abort.signal })
  const rejected = assert.rejects(cancelled, error => error.name === 'AbortError')
  await until(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND state='active'", [role])).rows[0].count) > 0)
  abort.abort(); await rejected
  await until(() => records.some(record => record.outcome === 'aborted'))
  assert.equal(records.find(record => record.outcome === 'aborted').database_timings.reason, 'no_response')
  await until(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND state='active'", [role])).rows[0].count) === 0)

  child.kill(); await once(child, 'exit')
  const unavailable = await fetch(base + '/rest/v1/notes', { headers }); await unavailable.text()
  assert.equal(unavailable.status, 503)
  assert.equal((await logged(unavailable)).reason, 'no_response')
  await startEngine()
  const recovered = await fetch(base + '/rest/v1/notes', { headers: { Authorization: 'Bearer another-subject' } })
  assert.equal(recovered.status, 200); assert.deepEqual(await recovered.json(), [])
  assert.equal((await logged(recovered)).state, 'observed')
  assert.doesNotMatch(JSON.stringify(records), /test-only|private-subject|another-subject|postgres:|Bearer/)

  if (process.env.DATA_API_DEV_CLI) {
    const directory = await mkdtemp(join(tmpdir(), 'gregale-db-timing-logs-'))
    t.after(() => rm(directory, { recursive: true, force: true }))
    const file = join(directory, 'logs.ndjson')
    await writeFile(file, records.map(record => JSON.stringify(record)).join('\n'))
    const { stdout } = await promisify(execFile)(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file], { timeout: 10000, env: { ...process.env, FAAS_TOKEN: '' } })
    const report = JSON.parse(stdout)
    assert.equal(report.partial, false)
    assert.equal(report.data_requests.database_timings.observed_requests, 3)
    assert.equal(report.data_requests.database_timings.unknown_requests, 3)
    assert.equal(report.data_requests.database_timings.without_timings, 0)
    assert.equal(report.data_requests.database_timings.by_phase.transaction.completed, 3)
    assert.equal(report.data_requests.database_timings.pool_wait, 'unknown')
  }
})
