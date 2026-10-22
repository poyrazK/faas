import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'

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
  const deadline = performance.now() + 10000
  while (!await check()) {
    if (performance.now() >= deadline) throw Error('Admission acceptance condition did not become true')
    await new Promise(resolve => setTimeout(resolve, 10))
  }
}

test('real SQL saturation never executes expired or cancelled queued writes and recovers with RLS', { skip: !enabled, timeout: 45000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `admission_${suffix}`, role = `admission_login_${suffix}`
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
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=15000`)
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
  const snapshot = await inspect(config.connection, config.schemas)
  config.snapshot = snapshot; config.fingerprint = fingerprint(snapshot); config.functions = snapshot.functions
  const upstreamPort = await port(), readyPort = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
  await until(async () => {
    try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false }
  })
  const records = []
  gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstreamPort, readyPort, record => records.push(record))
  let arrivals = 0
  gateway.on('request', () => arrivals++)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  const write = (id, signal) => fetch(base + '/rest/v1/notes', { method: 'POST', signal, headers: { Authorization: 'Bearer alice', 'Content-Type': 'application/json' }, body: JSON.stringify({ id, subject: 'alice' }) })

  // The migration owner holds a real SQL lock, exhausting both engine lanes.
  await owner.query('BEGIN; LOCK TABLE api.notes IN ACCESS EXCLUSIVE MODE')
  const active = [write('active-1'), write('active-2')]
  await until(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count) === limits.pool)
  const expired = await write('must-not-expire-into-a-write')
  assert.equal(expired.status, 503)
  assert.equal(expired.headers.get('retry-after'), '1')
  assert.equal((await expired.json()).code, 'data_api_overloaded')
  const controller = new AbortController(), before = arrivals
  const cancelled = write('must-not-cancel-into-a-write', controller.signal)
  const cancellation = assert.rejects(cancelled, error => error.name === 'AbortError')
  await until(() => arrivals > before)
  controller.abort()
  await cancellation
  await until(() => records.some(record => record.admission?.outcome === 'cancelled'))
  assert.equal((await owner.query('SELECT count(*) FROM api.notes')).rows[0].count, '0')
  // The health route bypasses admission, but /ready still tests the exhausted
  // SQL pool and must preserve its truthful unready response.
  const health = await fetch(base + '/healthz')
  assert.equal(health.status, 503)
  const observation = await health.json()
  assert.equal(observation.checks.engine, 'reachable')
  assert.equal(observation.checks.sql, 'unknown')
  assert.equal(observation.checks.database, 'unknown')
  assert.equal(observation.admission.active, limits.pool)
  assert.equal(observation.pool.state, 'unknown', 'the pinned metrics handler also checks the saturated SQL pool')
  assert.equal(observation.pool.reason, 'timeout')
  assert.equal(observation.pool.capacity, undefined)
  assert.equal((await fetch(base + '/livez')).status, 200)
  await owner.query('COMMIT')
  for (const response of await Promise.all(active)) { assert.equal(response.status, 201); await response.text() }
  const recovered = await write('recovered')
  assert.equal(recovered.status, 201); await recovered.text()
  assert.deepEqual((await owner.query('SELECT id FROM api.notes ORDER BY id')).rows.map(row => row.id), ['active-1', 'active-2', 'recovered'])
  const bob = await fetch(base + '/rest/v1/notes', { headers: { Authorization: 'Bearer bob' } })
  assert.equal(bob.status, 200); assert.deepEqual(await bob.json(), [])
  assert.equal(records.filter(record => record.admission?.outcome === 'queue_timeout').length, 1)
  assert.equal((await fetch(base + '/healthz')).status, 200)

  // Disconnect only this fixture's database, leaving the shared server intact.
  await admin.query(`ALTER DATABASE "${database}" ALLOW_CONNECTIONS false`)
  await admin.query('SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND usename=$2', [database, role])
  await until(async () => (await fetch(base + '/healthz')).status === 503)
  const disconnected = await (await fetch(base + '/healthz')).json()
  assert.equal(disconnected.checks.engine, 'reachable')
  assert.equal(disconnected.checks.database, 'unknown')
  assert.ok(['observed', 'unknown'].includes(disconnected.pool.state), 'metrics SQL checks can outlive the caller during a database outage')
  assert.equal((await fetch(base + '/livez')).status, 200)
  await admin.query(`ALTER DATABASE "${database}" ALLOW_CONNECTIONS true`)
  await until(async () => (await fetch(base + '/healthz')).status === 200)
  const recoveredRead = await fetch(base + '/rest/v1/notes', { headers: { Authorization: 'Bearer alice' } })
  assert.equal(recoveredRead.status, 200)
  assert.equal((await recoveredRead.json()).length, 3)
  child.kill(); await once(child, 'exit')
  const exited = await fetch(base + '/healthz')
  assert.equal(exited.status, 503)
  assert.equal((await exited.json()).checks.engine, 'unreachable')
  assert.equal((await fetch(base + '/livez')).status, 200, 'embedded gateway remains live; production supervises engine exit by closing it')
})
