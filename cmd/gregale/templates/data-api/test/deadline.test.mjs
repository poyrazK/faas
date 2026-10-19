import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { EventEmitter } from 'node:events'
import { SignJWT } from 'jose'
import { createDeadline } from '../deadline.mjs'
import { createServer } from '../server.mjs'
import { runtimeConfig, limits } from '../config.mjs'

const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done }); return { promise, resolve } }
async function until(check) {
  for (let i = 0; i < 2000; i++) {
    if (check()) return
    await new Promise(setImmediate)
  }
  throw Error('Deadline fixture did not reach its expected phase')
}

async function fixture(t, { verify, coordinated = false } = {}) {
  let now = 0
  t.mock.method(performance, 'now', () => now)
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', ...(coordinated ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) })
  config.fingerprint = 'a'.repeat(64)
  const state = { calls: [], logs: [], verified: 0, auto: false, closed: 0, readyCalls: 0, holdReady: false }
  const upstream = http.createServer((req, res) => {
    req.resume()
    state.calls.push({ req, res })
    res.once('close', () => state.closed++)
    if (state.auto) res.end('[]')
  })
  const ready = http.createServer((_req, res) => { state.readyCalls++; if (!state.holdReady) res.end('{}') })
  await Promise.all([upstream, ready].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const payload = { sub: 'private-subject', exp: 9999999999 }
  const gateway = createServer(config, async () => { state.verified++; return verify ? verify() : payload }, upstream.address().port, ready.address().port, record => state.logs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [gateway, upstream, ready]) server.closeAllConnections()
    await Promise.all([gateway, upstream, ready].map(server => new Promise(resolve => server.close(resolve))))
  })
  function start(path = '/rest/v1/notes', method = 'GET', end = true) {
    const observed = { status: null, bytes: 0 }
    const req = http.request({ hostname: '127.0.0.1', port: gateway.address().port, path, method,
      headers: { Authorization: 'Bearer private-token' } })
    const result = new Promise(resolve => {
      req.on('error', () => resolve({ ...observed, complete: false }))
      req.on('response', res => {
        observed.status = res.statusCode
        const chunks = []
        res.on('data', chunk => { observed.bytes += chunk.length; chunks.push(chunk) })
        res.on('error', () => {})
        res.on('close', () => resolve({ ...observed, complete: res.complete, body: Buffer.concat(chunks).toString() }))
      })
    })
    if (end) req.end()
    return { req, result, observed }
  }
  return { config, state, start, payload, advance(ms, tick = true) { now += ms; if (tick) t.mock.timers.tick(ms) } }
}

test('a timer cannot expire early and completed requests clear their deadline', t => {
  let now = 0, expired = 0
  t.mock.method(performance, 'now', () => now)
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const req = new EventEmitter(), res = new EventEmitter()
  const deadline = createDeadline(req, res, () => { expired++ })
  now = limits.queryMs - 1
  t.mock.timers.tick(limits.queryMs)
  assert.equal(expired, 0)
  now++
  t.mock.timers.tick(1)
  assert.equal(expired, 1)
  assert.equal(deadline.signal.aborted, true)
  assert.equal(deadline.check(), false)
  const completed = createDeadline(req, res, () => { expired++ })
  res.emit('finish')
  now += limits.queryMs; t.mock.timers.tick(limits.queryMs)
  assert.equal(expired, 1)
  assert.equal(completed.check(), false)
  assert.equal(req.listenerCount('aborted'), 0)
  assert.equal(res.listenerCount('close'), 0)
})

test('expired verification returns 504, retains its slot until settlement and never dispatches old writes', async t => {
  const verification = deferred()
  let blocked = true
  const f = await fixture(t, { verify: () => blocked ? verification.promise : f.payload })
  const writes = [f.start('/rest/v1/notes', 'POST'), f.start('/rest/v1/notes', 'POST')]
  await until(() => f.state.verified === limits.pool)
  f.advance(limits.queryMs)
  for (const write of writes) {
    const response = await write.result
    assert.equal(response.status, 504)
    assert.equal(JSON.parse(response.body).code, 'request_deadline_exceeded')
  }
  const health = JSON.parse((await f.start('/healthz').result).body)
  assert.equal(health.admission.active, limits.pool)
  assert.equal(f.state.calls.length, 0)
  assert.deepEqual(f.state.logs.filter(r => r.deadline).map(r => r.deadline), Array(2).fill({ phase: 'verification', elapsed_ms: limits.queryMs, dispatched: false }))
  const timedOut = structuredClone(f.state.logs.filter(r => r.deadline))
  for (const record of timedOut) assert.deepEqual(record.timings, { verification: { duration_ms: limits.queryMs, complete: false } })
  blocked = false; f.state.auto = true
  verification.resolve(f.payload)
  assert.equal((await f.start().result).status, 200)
  assert.equal(f.state.calls.length, 1)
  assert.equal(f.state.calls[0].req.method, 'GET')
  assert.deepEqual(f.state.logs.filter(r => r.deadline), timedOut)
})

test('a late verifier is checked before dispatch even before the deadline timer runs', async t => {
  const verification = deferred()
  const f = await fixture(t, { verify: () => verification.promise })
  const request = f.start('/rest/v1/notes', 'POST')
  await until(() => f.state.verified === 1)
  f.advance(limits.queryMs, false)
  verification.resolve(f.payload)
  const response = await request.result
  assert.equal(response.status, 504)
  assert.equal(f.state.calls.length, 0)
  assert.equal(f.state.logs[0].deadline.phase, 'verification')
})

test('refresh and signing waits expire before engine dispatch', async t => {
  for (const phase of ['refresh', 'signing']) {
    await t.test(phase, async t => {
      const pending = deferred()
      const f = await fixture(t, { coordinated: phase === 'refresh' })
      let entered = false
      if (phase === 'refresh') f.config.refreshStatus = () => { entered = true; return pending.promise }
      else t.mock.method(SignJWT.prototype, 'sign', () => { entered = true; return pending.promise })
      const request = f.start('/rest/v1/notes', 'POST')
      await until(() => entered)
      f.advance(limits.queryMs)
      assert.equal((await request.result).status, 504)
      assert.equal(f.state.logs[0].deadline.phase, phase)
      assert.equal(f.state.logs[0].deadline.dispatched, false)
      assert.deepEqual(f.state.logs[0].timings[phase], { duration_ms: limits.queryMs, complete: false })
      assert.equal(f.state.logs[0].timings.verification.complete, true)
      pending.resolve(phase === 'refresh' ? { paused: false, fingerprint: f.config.fingerprint } : 'unused')
      await new Promise(setImmediate)
      assert.equal(f.state.calls.length, 0)
    })
  }
})

test('an upstream wait gets one 504 with a request ID, aborts HTTP work and recovers capacity', async t => {
  const f = await fixture(t)
  const request = f.start('/rest/v1/notes', 'POST')
  await until(() => f.state.calls.length === 1)
  f.advance(limits.queryMs)
  const response = await request.result
  assert.equal(response.status, 504)
  const body = JSON.parse(response.body)
  assert.equal(body.code, 'request_deadline_exceeded')
  assert.equal(body.request_id, f.state.logs[0].request_id)
  assert.deepEqual(f.state.logs[0].deadline, { phase: 'upstream', elapsed_ms: limits.queryMs, dispatched: true })
  assert.deepEqual(f.state.logs[0].timings.upstream, { duration_ms: limits.queryMs, complete: false })
  assert.equal(f.state.logs[0].timings.signing.complete, true)
  assert.equal(f.state.logs[0].timings.streaming, undefined)
  await until(() => f.state.closed === 1)
  f.state.auto = true
  assert.equal((await f.start().result).status, 200)
  assert.doesNotMatch(JSON.stringify(f.state.logs) + response.body, /private-|postgres:/)
})

test('an early socket timeout waits for the total deadline before responding or aborting', async t => {
  const originalRequest = http.request
  let upstream
  t.mock.method(http, 'request', function (...args) {
    const request = Reflect.apply(originalRequest, this, args)
    if (args[0]?.signal) upstream = request
    return request
  })
  const f = await fixture(t)
  const request = f.start('/rest/v1/notes', 'POST')
  await until(() => f.state.calls.length === 1)
  assert.ok(upstream)
  f.advance(limits.queryMs - 1)
  upstream.emit('timeout')
  assert.equal(upstream.destroyed, false)
  assert.equal(request.observed.status, null)
  f.advance(1)
  const response = await request.result
  assert.equal(response.status, 504)
  assert.equal(JSON.parse(response.body).code, 'request_deadline_exceeded')
  assert.deepEqual(f.state.logs[0].deadline, { phase: 'upstream', elapsed_ms: limits.queryMs, dispatched: true })
  await until(() => f.state.closed === 1)
})

test('steady response chunks cannot extend the total deadline or retain an active slot', async t => {
  const f = await fixture(t)
  const request = f.start()
  await until(() => f.state.calls.length === 1)
  const response = f.state.calls[0].res
  response.writeHead(200); response.write('[')
  await until(() => request.observed.bytes === 1)
  for (let i = 1; i < limits.queryMs / 1000; i++) {
    f.advance(1000)
    response.write(' ')
    await until(() => request.observed.bytes === i + 1)
  }
  f.advance(1000)
  const result = await request.result
  assert.equal(result.status, 200)
  assert.equal(result.complete, false)
  await until(() => f.state.logs.length === 1)
  assert.equal(f.state.logs[0].code, 'request_deadline_exceeded')
  assert.equal(f.state.logs[0].outcome, 'aborted')
  assert.deepEqual(f.state.logs[0].deadline, { phase: 'streaming', elapsed_ms: limits.queryMs, dispatched: true })
  assert.deepEqual(f.state.logs[0].timings.streaming, { duration_ms: limits.queryMs, complete: false })
  assert.deepEqual(f.state.logs[0].timings.upstream, { duration_ms: 0, complete: true })
  f.state.auto = true
  assert.equal((await f.start().result).status, 200)
})

test('slow request bodies and metadata probes share the same admitted deadline', async t => {
  for (const phase of ['upstream', 'readiness']) {
    await t.test(phase, async t => {
      const f = await fixture(t)
      f.state.holdReady = true
      const request = phase === 'upstream' ? f.start('/rest/v1/notes', 'POST', false) : f.start('/__gregale/schema')
      if (phase === 'upstream') {
        request.req.setHeader('Transfer-Encoding', 'chunked')
        request.req.write('{')
        await until(() => f.state.calls.length === 1)
      } else await until(() => f.state.readyCalls === 1)
      f.advance(limits.queryMs)
      const response = await request.result
      assert.equal(response.status, 504)
      assert.equal(f.state.logs[0].deadline.phase, phase)
      assert.equal(f.state.logs[0].deadline.dispatched, phase === 'upstream')
      assert.deepEqual(f.state.logs[0].timings[phase], { duration_ms: limits.queryMs, complete: false })
    })
  }
})

test('client cancellation and successful responses cancel the timer without false timeout logs', async t => {
  const f = await fixture(t)
  const request = f.start()
  await until(() => f.state.calls.length === 1)
  f.advance(25)
  request.req.destroy()
  await request.result
  await until(() => f.state.logs.length === 1)
  f.advance(limits.queryMs)
  assert.equal(f.state.logs[0].code, null)
  assert.equal(f.state.logs[0].outcome, 'aborted')
  assert.deepEqual(f.state.logs[0].timings.upstream, { duration_ms: 25, complete: false })
  f.state.auto = true
  assert.equal((await f.start().result).status, 200)
  f.advance(limits.queryMs)
  assert.equal(f.state.logs.filter(r => r.deadline).length, 0)
})

test('queued requests receive a fresh admitted budget after the separate queue wait', async t => {
  const f = await fixture(t)
  const first = f.start(), second = f.start()
  await until(() => f.state.calls.length === limits.pool)
  const queued = f.start()
  await until(() => f.state.verified === limits.pool)
  // Reading health flushes the preceding request onto the server's queue.
  assert.equal(JSON.parse((await f.start('/healthz').result).body).admission.queued, 1)
  f.advance(limits.admissionWaitMs / 2)
  f.state.calls[0].res.end('[]')
  assert.equal((await first.result).status, 200)
  await until(() => f.state.calls.length === 3)
  f.advance(limits.queryMs - limits.admissionWaitMs / 2)
  assert.equal((await second.result).status, 504)
  assert.equal(queued.observed.status, null)
  f.advance(limits.admissionWaitMs / 2)
  assert.equal((await queued.result).status, 504)
  assert.equal(f.state.logs.filter(r => r.deadline).length, 2)
  assert.equal(f.state.logs.at(-1).deadline.elapsed_ms, limits.queryMs)
})

test('successful requests distinguish verification, refresh, signing, engine wait and streaming', async t => {
  const verification = deferred(), refresh = deferred(), signing = deferred()
  const f = await fixture(t, { verify: () => verification.promise, coordinated: true })
  let refreshing = false, signingStarted = false
  f.config.refreshStatus = () => { refreshing = true; return refresh.promise }
  t.mock.method(SignJWT.prototype, 'sign', () => { signingStarted = true; return signing.promise })
  const request = f.start()
  await until(() => f.state.verified === 1)
  f.advance(11)
  verification.resolve(f.payload)
  await until(() => refreshing)
  f.advance(22)
  refresh.resolve({ paused: false, fingerprint: f.config.fingerprint })
  await until(() => signingStarted)
  f.advance(33)
  signing.resolve('test-only-engine-token')
  await until(() => f.state.calls.length === 1)
  f.advance(44)
  f.state.calls[0].res.write('[')
  await until(() => request.observed.bytes === 1)
  f.advance(55)
  f.state.calls[0].res.end(']')
  assert.equal((await request.result).status, 200)
  assert.deepEqual(f.state.logs[0].timings, {
    verification: { duration_ms: 11, complete: true },
    refresh: { duration_ms: 22, complete: true },
    signing: { duration_ms: 33, complete: true },
    upstream: { duration_ms: 44, complete: true },
    streaming: { duration_ms: 55, complete: true },
  })
  assert.equal(f.state.logs[0].duration_ms, 165)
  await f.start('/livez').result
  assert.equal(f.state.logs.at(-1).timings, undefined)
  assert.doesNotMatch(JSON.stringify(f.state.logs), /private-|test-only-engine-token/)
})

test('failed verification is a completed phase observation', async t => {
  const verification = deferred()
  const f = await fixture(t, { verify: () => verification.promise })
  const request = f.start()
  await until(() => f.state.verified === 1)
  f.advance(21)
  verification.resolve({})
  assert.equal((await request.result).status, 401)
  assert.deepEqual(f.state.logs[0].timings, { verification: { duration_ms: 21, complete: true } })
  assert.equal(f.state.calls.length, 0)
})


test('an early socket timeout cannot preempt the total request deadline', async t => {
  const f = await fixture(t)
  const original = http.request
  let outgoing
  t.mock.method(http, 'request', function (options, ...args) {
    const request = original.call(this, options, ...args)
    if (options.signal) outgoing = request
    return request
  })
  const pending = f.start()
  await until(() => f.state.calls.length === 1)
  f.advance(limits.queryMs - 1, false)
  outgoing.emit('timeout')
  await new Promise(setImmediate)
  assert.equal(pending.observed.status, null)
  f.advance(1)
  // Flush the timer armed when the request began; elapsed time is authoritative.
  t.mock.timers.tick(limits.queryMs)
  const result = await pending.result
  assert.equal(result.status, 504)
  assert.equal(JSON.parse(result.body).code, 'request_deadline_exceeded')
  await until(() => f.state.logs.length === 1)
  assert.equal(f.state.logs[0].deadline.dispatched, true)
})
