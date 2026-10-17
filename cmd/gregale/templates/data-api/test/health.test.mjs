import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { EventEmitter } from 'node:events'
import { createServer } from '../server.mjs'
import { createHealth } from '../health.mjs'
import { runtimeConfig, limits } from '../config.mjs'

async function fixture(t, coordinated = false) {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', ...(coordinated ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) })
  const state = { sql: 200, live: 200, paused: false, failed: false, reads: 0, probes: [], verified: 0, logs: [] }
  config.refreshStatus = async () => {
    state.reads++
    if (state.failed) throw Error('private-database-host-and-password')
    return { paused: state.paused, fingerprint: 'a'.repeat(64) }
  }
  const admin = http.createServer((req, res) => {
    state.probes.push(req.url)
    if (req.url === '/metrics') return res.end(`pgrst_db_pool_max ${coordinated ? 1 : 2}.0\npgrst_db_pool_available 1.0\npgrst_db_pool_waiting 0.0\npgrst_db_pool_timeouts_total 3.0\n`)
    res.writeHead(req.url === '/ready' ? state.sql : state.live)
    res.end('private-upstream-body')
  })
  await new Promise(resolve => admin.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => { state.verified++; throw Error('must-not-verify') }, admin.address().port, admin.address().port, record => state.logs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [gateway, admin]) server.closeAllConnections()
    await Promise.all([gateway, admin].map(server => new Promise(resolve => server.close(resolve))))
  })
  return { config, state, admin, base: `http://127.0.0.1:${gateway.address().port}` }
}

test('liveness needs no engine, database, JWT or admission and readiness distinguishes observations', async t => {
  const { state, admin, base } = await fixture(t)
  let response = await fetch(base + '/livez')
  assert.equal(response.status, 200)
  assert.equal(response.headers.get('cache-control'), 'no-store')
  assert.deepEqual(await response.json(), { version: 1, live: true })
  assert.equal(state.probes.length, 0)
  response = await fetch(base + '/healthz')
  let report = await response.json()
  assert.equal(response.status, 200)
  assert.deepEqual(report.checks, { engine: 'reachable', sql: 'ready', database: 'reachable', refresh: 'disabled' })
  assert.deepEqual(report.admission, { active: 0, queued: 0, capacity: 2, queue_capacity: 8 })
  assert.equal(report.pool.state, 'observed')
  assert.equal(report.pool.capacity, 2)
  assert.equal(report.pool.available, 1)
  assert.equal(report.pool.timeouts_total, 3)
  assert.ok(Number.isFinite(Date.parse(report.pool.checked_at)))
  state.sql = 503
  response = await fetch(base + '/healthz'); report = await response.json()
  assert.equal(response.status, 503)
  assert.equal(report.live, true)
  assert.deepEqual(report.checks, { engine: 'reachable', sql: 'unready', database: 'unknown', refresh: 'disabled' })
  admin.closeAllConnections(); await new Promise(resolve => admin.close(resolve))
  response = await fetch(base + '/healthz'); report = await response.json()
  assert.equal(response.status, 503)
  assert.deepEqual(report.checks, { engine: 'unreachable', sql: 'unknown', database: 'unknown', refresh: 'disabled' })
  assert.equal(report.pool.state, 'unknown')
  assert.equal(report.pool.reason, 'unavailable')
  assert.equal(report.pool.capacity, undefined)
  assert.equal((await fetch(base + '/livez')).status, 200)
  assert.equal(state.verified, 0)
  assert.ok(state.logs.every(record => record.route === 'health' && !record.admission))
  assert.doesNotMatch(JSON.stringify(state.logs) + JSON.stringify(report), /private-/)
})

test('refresh pause and reader failure remain distinct from SQL readiness and recover', async t => {
  const { state, base } = await fixture(t, true)
  state.paused = true
  let response = await fetch(base + '/healthz'), report = await response.json()
  assert.equal(response.status, 200)
  assert.equal(report.refresh_paused, true)
  assert.equal(report.checks.refresh, 'paused')
  assert.equal(report.admission.capacity, 1)
  state.sql = 503
  response = await fetch(base + '/healthz'); report = await response.json()
  assert.equal(response.status, 503)
  assert.equal(report.checks.database, 'reachable', 'the separate control reader successfully reached SQL')
  state.failed = true
  response = await fetch(base + '/healthz'); report = await response.json()
  assert.equal(response.status, 503)
  assert.equal(report.checks.refresh, 'unavailable')
  assert.equal(report.checks.database, 'unknown')
  state.sql = 200
  response = await fetch(base + '/healthz'); report = await response.json()
  assert.equal(response.status, 503, 'a failed refresh reader fails readiness even with working SQL')
  assert.equal(report.checks.sql, 'ready')
  assert.equal(report.checks.database, 'reachable')
  assert.equal(report.checks.refresh, 'unavailable')
  state.failed = false; state.paused = false; state.sql = 200
  response = await fetch(base + '/healthz'); report = await response.json()
  assert.equal(response.status, 200)
  assert.equal(report.checks.refresh, 'active')
  assert.equal(report.refresh_paused, false)
})

test('concurrent hung probes have a total deadline and share bounded engine and refresh work', { timeout: 10000 }, async t => {
  let release, reads = 0, probes = 0
  const admin = http.createServer(req => { if (req.url === '/ready') probes++ })
  await new Promise(resolve => admin.listen(0, '127.0.0.1', resolve))
  t.after(() => { admin.closeAllConnections(); admin.close() })
  const config = { coordinatedRefresh: true, refreshStatus: () => { reads++; return new Promise(resolve => { release = resolve }) } }
  const health = createHealth(config, admin.address().port, admin.address().port)
  const admission = () => ({ active: 1, queued: 8, capacity: 1, queue_capacity: 8 })
  const started = performance.now()
  const reports = await Promise.all(Array.from({ length: 32 }, () => health.snapshot(admission)))
  assert.ok(performance.now() - started >= limits.healthProbeMs - 20)
  assert.ok(performance.now() - started < limits.healthProbeMs + 1500)
  assert.equal(reads, 1)
  assert.equal(probes, 1)
  assert.ok(reports.every(report => !report.ready && report.checks.refresh === 'unavailable'))
  const next = health.snapshot(admission)
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(reads, 1, 'a timed-out reader must stay shared until it settles')
  release({ paused: false })
  assert.equal((await next).checks.refresh, 'active')
  assert.equal(probes, 1, 'a caller deadline must not queue a second engine SQL check')
})

test('a retained readiness probe expires at its hard cap and a fresh probe recovers', async t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let probes = 0, destroyed = 0
  t.mock.method(http, 'get', (_options, respond) => {
    const request = new EventEmitter()
    request.destroy = () => { destroyed++ }
    if (++probes > 1) queueMicrotask(() => respond({ statusCode: 200, destroy() {} }))
    return request
  })
  const health = createHealth({ coordinatedRefresh: false }, 1, 2)
  const first = health.sql()
  t.mock.timers.tick(limits.healthProbeMs)
  assert.equal(await first, 'unknown')
  const shared = health.sql()
  t.mock.timers.tick(limits.healthProbeMs)
  assert.equal(await shared, 'unknown')
  assert.equal(probes, 1, 'caller timeouts must not add SQL waiters')
  t.mock.timers.tick(limits.queryMs - 2 * limits.healthProbeMs)
  // Let the shared probe's finally handler remove the expired request.
  await Promise.resolve()
  assert.equal(destroyed, 1)
  assert.equal(await health.sql(), 'ready')
  assert.equal(probes, 2, 'recovery uses a fresh probe after the hard cap')
})
