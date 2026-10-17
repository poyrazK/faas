import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import http from 'node:http'
import { spawn } from 'node:child_process'
import { once, EventEmitter } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function port() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port
  await new Promise(resolve => server.close(resolve))
  return value
}
async function until(check, timeout = 10000) {
  const deadline = performance.now() + timeout
  while (!await check()) {
    if (performance.now() >= deadline) throw Error('Pool observation fixture did not reach its expected state')
    await new Promise(resolve => setTimeout(resolve, 20))
  }
}

for (const stallAdminProbe of [false, true]) {
test('real engine pool observations survive SQL saturation, counter resets and engine recovery' + (stallAdminProbe ? ' with a stalled admin probe' : ''), { skip: !enabled, timeout: 45000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const { createPoolObservation } = await import(new URL('pool.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `pool_${suffix}`, role = `pool_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    // Release blocked engine work before waiting for its process to exit.
    if (owner) await owner.query('ROLLBACK')
    if (child && child.exitCode === null && child.signalCode === null) { child.kill(); await once(child, 'exit') }
    if (owner) await owner.end()
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
  config.snapshot = await inspect(config.connection, config.schemas); config.fingerprint = fingerprint(config.snapshot)
  const upstreamPort = await port(), readyPort = await port(), engineAdmin = `http://127.0.0.1:${readyPort}`
  async function startEngine() {
    // Shorten this fixture's acquisition timeout so real counter increments can
    // be observed without changing the shipped pool/statement timeout policy.
    child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort), PGRST_DB_POOL_ACQUISITION_TIMEOUT: '1' }, stdio: 'ignore' })
    await until(async () => {
      try { return (await fetch(engineAdmin + '/ready')).status === 200 } catch { return false }
    })
  }
  await startEngine()
  t.diagnostic('pool fixture: engine ready')
  const records = []
  gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  const healthy = await (await fetch(base + '/healthz')).json()
  assert.equal(healthy.ready, true)
  assert.equal(healthy.pool.state, 'observed')
  assert.equal(healthy.pool.capacity, limits.pool)
  assert.equal((await fetch(base + '/metrics')).status, 404)

  await owner.query('BEGIN; LOCK TABLE api.notes IN ACCESS EXCLUSIVE MODE')
  const writes = ['first', 'second'].map(id => fetch(base + '/rest/v1/notes', { method: 'POST', headers: { Authorization: 'Bearer alice', 'Content-Type': 'application/json' }, body: JSON.stringify({ id, subject: 'alice' }) }))
  await until(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count) === limits.pool)
  // The pinned admin handler checks SQL twice before serving every endpoint.
  // Readiness fails under full saturation; metrics may finish or time out.
  // The admin handler itself needs a SQL slot. Never await it indefinitely
  // while this fixture owns the lock that prevents the slot from draining.
  if (stallAdminProbe) {
    // Keep the gateway's own readiness probe pending through its hard cap,
    // independently of the fixture's short-lived direct fetch below.
    const getNormally = http.get
    let retained = false
    t.mock.method(http, 'get', (options, respond) => {
      if (options.port === readyPort && options.path === '/ready' && !retained) {
        retained = true
        const request = new EventEmitter()
        request.destroy = () => {}
        return request
      }
      return getNormally(options, respond)
    })
    const fetchNormally = globalThis.fetch
    let stalled = false
    t.mock.method(globalThis, 'fetch', (input, options) => {
      if (input === engineAdmin + '/ready' && !stalled) {
        stalled = true
        return new Promise((resolve, reject) => {
          options.signal.addEventListener('abort', () => reject(options.signal.reason), { once: true })
        })
      }
      return fetchNormally(input, options)
    })
  }
  const waiter = fetch(engineAdmin + '/ready', { signal: AbortSignal.timeout(limits.healthProbeMs) })
    .then(async response => { await response.body?.cancel(); return response.status })
    .catch(error => { if (error.name === 'TimeoutError') return null; throw error })
  const response = await fetch(base + '/healthz'), saturated = await response.json()
  assert.equal(response.status, 503)
  assert.equal(saturated.checks.engine, 'reachable')
  assert.equal(saturated.checks.database, 'unknown')
  // Acquisition failures may let the metrics handler finish just before the
  // caller's deadline. Trust actual counters when present; never guess them
  // when that handler remains blocked.
  if (saturated.pool.state === 'observed') {
    assert.equal(saturated.pool.capacity, limits.pool)
    assert.equal(saturated.pool.available, 0)
    assert.ok(Number.isSafeInteger(saturated.pool.waiting) && saturated.pool.waiting >= 0)
  } else {
    assert.equal(saturated.pool.state, 'unknown')
    assert.equal(saturated.pool.reason, 'timeout')
    assert.equal(saturated.pool.available, undefined)
  }
  assert.ok(Number.isFinite(Date.parse(saturated.pool.checked_at)))
  assert.notEqual(await waiter, 200)
  assert.equal(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename=$1', [role])).rows[0].count), limits.pool, 'diagnostics did not open a SQL connection pool')
  t.diagnostic('pool fixture: saturated probes finished; releasing SQL lock')
  await owner.query('COMMIT')
  for (const result of await Promise.all(writes)) { assert.equal(result.status, 201); await result.text() }
  // Health probes run readiness and metrics concurrently and can themselves
  // briefly queue in the engine. Check quiescence with only the metrics probe
  // issuing new diagnostic SQL, allowing the retained probes their hard cap.
  const observePool = createPoolObservation(readyPort, limits.pool)
  let drained
  await until(async () => {
    drained = await observePool()
    return drained.state === 'observed' && drained.waiting === 0
  }, limits.queryMs + limits.healthProbeMs)
  assert.equal(drained.waiting, 0)
  assert.ok(drained.timeouts_total > 0)
  t.diagnostic('pool fixture: SQL queue drained')
  // The gateway owns separate shared /ready and /metrics probes. A drained
  // metrics observation does not imply its retained /ready request settled.
  // Allow that request's hard cap and one fresh caller probe to recover.
  let recoveredResponse, recovered
  await until(async () => {
    recoveredResponse = await fetch(base + '/healthz')
    recovered = await recoveredResponse.json()
    return recoveredResponse.status === 200 && recovered.pool.state === 'observed'
  }, limits.queryMs + limits.healthProbeMs)
  assert.equal(recoveredResponse.status, 200)
  const bob = await fetch(base + '/rest/v1/notes', { headers: { Authorization: 'Bearer bob' } })
  assert.deepEqual(await bob.json(), [])
  assert.equal((await owner.query('SELECT count(*) FROM api.notes')).rows[0].count, '2')

  t.diagnostic('pool fixture: restarting engine')
  child.kill(); await once(child, 'exit')
  const stopped = await (await fetch(base + '/healthz')).json()
  assert.equal(stopped.pool.state, 'unknown')
  assert.equal(stopped.pool.reason, 'unavailable')
  assert.equal(stopped.pool.capacity, undefined)
  assert.equal((await fetch(base + '/livez')).status, 200)
  await startEngine()
  const restartedResponse = await fetch(base + '/healthz'), restarted = await restartedResponse.json()
  assert.equal(restartedResponse.status, 200)
  assert.equal(restarted.pool.state, 'observed')
  assert.equal(restarted.pool.timeouts_total, 0)
  assert.doesNotMatch(JSON.stringify([healthy, saturated, drained, recovered, stopped, restarted, records]), /test-only|alice|bob|postgres:/)
})
}
