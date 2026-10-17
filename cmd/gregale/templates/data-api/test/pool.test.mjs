import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createPoolObservation, parsePoolMetrics } from '../pool.mjs'
import { createHealth } from '../health.mjs'
import { limits } from '../config.mjs'

const metrics = (capacity = 2, available = 1, waiting = 0, timeouts = 0) => `# TYPE pgrst_db_pool_max gauge\npgrst_db_pool_max ${capacity}.0\npgrst_db_pool_available ${available}.0\npgrst_db_pool_waiting ${waiting}.0\npgrst_db_pool_timeouts_total ${timeouts}.0\nprivate_metric{secret="private-upstream-secret"} 1\n`

async function fixture(t, handler) {
  const server = http.createServer(handler)
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  return server
}

test('pool metrics expose only the pinned integer counters and preserve lazy pool semantics', () => {
  assert.deepEqual(parsePoolMetrics(metrics(2, 1, 3, 7), 2), { capacity: 2, available: 1, waiting: 3, timeouts_total: 7 })
  assert.deepEqual(parsePoolMetrics(metrics(1, 0), 1), { capacity: 1, available: 0, waiting: 0, timeouts_total: 0 })
  assert.equal(parsePoolMetrics(metrics(2), 1), null)
  assert.equal(parsePoolMetrics(metrics(2, 3), 2), null)
  for (const replacement of ['', '-1', '0.5', 'NaN', '+Inf', '1e309', '9007199254740992', '0x2', '2 12345', '{secret="private"} 1']) {
    const body = metrics().replace('pgrst_db_pool_available 1.0', `pgrst_db_pool_available ${replacement}`)
    assert.equal(parsePoolMetrics(body, 2), null, replacement)
  }
  assert.equal(parsePoolMetrics(metrics() + 'pgrst_db_pool_max 2.0\n', 2), null)
  assert.equal(parsePoolMetrics(metrics().replace('pgrst_db_pool_max 2.0', 'pgrst_db_pool_max{secret="private"} 2.0'), 2), null)
})

test('pool observations use loopback metrics only, share concurrent reads and never cache completed counts', async t => {
  let reads = 0, release, body = metrics()
  const server = await fixture(t, (req, res) => {
    reads++
    assert.equal(req.url, '/metrics')
    assert.equal(req.headers.authorization, undefined)
    assert.equal(req.headers.cookie, undefined)
    release = () => res.end(body)
  })
  const pool = createPoolObservation(server.address().port, 2)
  const pending = Array.from({ length: 32 }, () => pool())
  while (!release) await new Promise(setImmediate)
  release()
  const observations = await Promise.all(pending)
  assert.equal(reads, 1)
  assert.ok(observations.every(value => value === observations[0]))
  assert.deepEqual(Object.keys(observations[0]).sort(), ['available', 'capacity', 'checked_at', 'state', 'timeouts_total', 'waiting'])
  assert.doesNotMatch(JSON.stringify(observations), /private-/)
  body = metrics(2, 0, 2, 9); release = undefined
  const next = pool()
  while (!release) await new Promise(setImmediate)
  release()
  assert.equal((await next).waiting, 2)
  assert.equal(reads, 2)
})

test('unavailable, unsupported, malformed, oversized, redirected and truncated metrics remain unknown', async t => {
  for (const [kind, reason] of [['failure', 'unavailable'], ['unsupported', 'unsupported'], ['malformed', 'invalid_metrics'], ['oversized', 'too_large'], ['redirect', 'unavailable'], ['truncated', 'unavailable']]) {
    await t.test(kind, async t => {
      const server = await fixture(t, (_req, res) => {
        if (kind === 'failure') { res.writeHead(503); return res.end('private-error-body') }
        if (kind === 'unsupported') { res.writeHead(404); return res.end('private-error-body') }
        if (kind === 'redirect') { res.writeHead(302, { Location: 'http://private-upstream-secret.invalid/' }); return res.end() }
        if (kind === 'truncated') { res.writeHead(200, { 'Content-Length': 1000 }); res.write('partial'); return res.destroy() }
        res.end(kind === 'malformed' ? 'private-error-body' : 'x'.repeat(limits.healthBytes + 1))
      })
      const observed = await createPoolObservation(server.address().port, 2)()
      assert.deepEqual(Object.keys(observed).sort(), ['checked_at', 'reason', 'state'])
      assert.equal(observed.state, 'unknown')
      assert.equal(observed.reason, reason)
      assert.ok(Number.isFinite(Date.parse(observed.checked_at)))
      assert.doesNotMatch(JSON.stringify(observed), /private-/)
    })
  }
})

test('caller timeouts retain shared work until the response settles', { timeout: 7000 }, async t => {
  let release, reads = 0
  const server = await fixture(t, (_req, res) => { reads++; release = () => res.end(metrics()) })
  const pool = createPoolObservation(server.address().port, 2)
  const first = await pool()
  assert.equal(first.reason, 'timeout')
  assert.equal(reads, 1)
  const next = pool()
  release()
  assert.equal((await next).state, 'observed')
  assert.equal(reads, 1)
  assert.deepEqual(Object.keys(first).sort(), ['checked_at', 'reason', 'state'])
})

test('continuous metrics chunks cannot extend the hard cap or create repeated pending work', { timeout: 22000 }, async t => {
  let recovered = false, closed = false, reads = 0
  const server = await fixture(t, (_req, res) => {
    reads++
    if (recovered) return res.end(metrics())
    res.write('# pending\n')
    const timer = setInterval(() => res.write('# waiting\n'), 40)
    res.once('close', () => { closed = true; clearInterval(timer) })
  })
  const pool = createPoolObservation(server.address().port, 2)
  const started = performance.now()
  const observations = await Promise.all(Array.from({ length: 20 }, () => pool()))
  assert.ok(performance.now() - started >= limits.healthProbeMs - 20)
  assert.ok(performance.now() - started < limits.healthProbeMs + 1000)
  assert.ok(observations.every(value => value.reason === 'timeout' && value.state === 'unknown'))
  assert.equal((await pool()).reason, 'timeout')
  assert.equal(reads, 1)
  while (!closed) await new Promise(resolve => setTimeout(resolve, 20))
  assert.ok(performance.now() - started >= limits.queryMs - 20)
  assert.ok(performance.now() - started < limits.queryMs + 1000)
  recovered = true
  assert.equal((await pool()).state, 'observed')
  assert.equal(closed, true)
  assert.equal(reads, 2)
})

test('metrics failure does not change SQL readiness and coordinated refresh uses the engine capacity', async t => {
  let supported = false
  const server = await fixture(t, (req, res) => {
    if (req.url === '/metrics' && supported) return res.end(metrics(1))
    res.end(req.url === '/metrics' ? 'private-error-body' : '{}')
  })
  const health = createHealth({ coordinatedRefresh: true, refreshStatus: async () => ({ paused: false }) }, server.address().port, server.address().port)
  const admission = () => ({ active: 0, queued: 0, capacity: 1, queue_capacity: 8 })
  const failed = await health.snapshot(admission)
  assert.equal(failed.ready, true)
  assert.equal(failed.pool.state, 'unknown')
  supported = true
  const recovered = await health.snapshot(admission)
  assert.equal(recovered.ready, true)
  assert.equal(recovered.pool.capacity, 1)
  assert.equal(recovered.pool.state, 'observed')
})
