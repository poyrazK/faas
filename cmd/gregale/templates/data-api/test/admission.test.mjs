import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { EventEmitter } from 'node:events'
import { createServer } from '../server.mjs'
import { createAdmission } from '../admission.mjs'
import { runtimeConfig, limits } from '../config.mjs'

const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done }); return { promise, resolve } }
async function until(check) {
  const deadline = performance.now() + 3000
  while (!check()) {
    if (performance.now() > deadline) throw Error('Condition did not become true')
    await new Promise(resolve => setTimeout(resolve, 2))
  }
}

async function fixture(t, { coordinated = false, verify } = {}) {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_ALLOWED_ORIGINS: 'https://app.example', ...(coordinated ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) })
  config.fingerprint = 'a'.repeat(64)
  config.functions = [{ schema: 'api', name: 'save_note' }]
  const state = { calls: [], logs: [], verified: 0, arrivals: 0, paused: false, auto: false }
  config.refreshStatus = async () => ({ paused: state.paused, fingerprint: config.fingerprint })
  const upstream = http.createServer((req, res) => {
    req.resume()
    state.calls.push({ path: req.url, method: req.method, res })
    if (state.auto) res.end('[]')
  })
  const ready = http.createServer((_req, res) => res.end('{}'))
  await Promise.all([upstream, ready].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const gateway = createServer(config, async token => {
    state.verified++
    if (verify) return verify(token)
    if (token === 'invalid') throw Error('private-verifier-error')
    return { sub: 'private-subject', exp: 9999999999 }
  }, upstream.address().port, ready.address().port, record => state.logs.push(record))
  gateway.on('request', () => state.arrivals++)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [gateway, upstream, ready]) server.closeAllConnections()
    await Promise.all([gateway, upstream, ready].map(server => new Promise(resolve => server.close(resolve))))
  })
  function start(path = '/rest/v1/notes', method = 'GET', token = 'private-token') {
    const client = http.request({ host: '127.0.0.1', port: gateway.address().port, path, method,
      headers: { Authorization: `Bearer ${token}`, Origin: 'https://app.example', ...(method === 'POST' ? { 'Content-Type': 'application/json' } : {}) } })
    const result = new Promise(resolve => {
      client.on('error', error => resolve({ error }))
      client.on('response', res => {
        const chunks = []
        res.on('data', chunk => chunks.push(chunk))
        res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString() }))
      })
    })
    client.end(method === 'POST' ? '{"value":"private-body"}' : undefined)
    return { client, result }
  }
  async function arrive(...args) {
    const count = state.arrivals
    const request = start(...args)
    await until(() => state.arrivals > count)
    return request
  }
  return { state, start, arrive }
}

test('bursts use bounded FIFO admission, reject excess before verification and keep health responsive', { timeout: 12000 }, async t => {
  const { state, start, arrive } = await fixture(t)
  const requests = []
  for (let i = 0; i < limits.pool; i++) {
    requests.push(start(`/rest/v1/notes?id=eq.${i}`))
    await until(() => state.calls.length === i + 1)
  }
  for (let i = 0; i < limits.queuedRequests; i++) requests.push(await arrive(`/rest/v1/notes?id=eq.${i + 2}`))
  for (let i = 0; i < 3; i++) {
    const rejected = await start('/rest/v1/rpc/save_note', 'POST').result
    assert.equal(rejected.status, 503)
    assert.equal(rejected.headers['retry-after'], String(limits.overloadRetrySeconds))
    assert.match(rejected.headers['access-control-expose-headers'], /Retry-After/)
    const body = JSON.parse(rejected.body)
    assert.equal(body.code, 'data_api_overloaded')
    assert.equal(body.request_id, rejected.headers['x-request-id'])
    assert.equal(rejected.headers['cache-control'], 'no-store')
  }
  assert.equal(state.verified, limits.pool)
  assert.equal(state.calls.length, limits.pool)
  const healthResponse = await start('/healthz').result
  assert.equal(healthResponse.status, 200)
  const liveness = await start('/livez').result
  assert.equal(liveness.status, 200)
  assert.equal(JSON.parse(liveness.body).live, true)
  const health = JSON.parse(healthResponse.body)
  assert.equal(health.admission.active, limits.pool)
  assert.equal(health.admission.queued, limits.queuedRequests)
  assert.equal((await start('/rest/v1/notes', 'OPTIONS').result).status, 204)
  const overloads = state.logs.filter(record => record.code === 'data_api_overloaded')
  assert.equal(overloads.length, 3)
  for (const record of overloads) {
    assert.equal(record.outcome, 'overloaded')
    assert.equal(record.admission.outcome, 'queue_full')
    assert.equal(record.admission.active, limits.pool)
    assert.equal(record.admission.queued, limits.queuedRequests)
  }
  // Completing one response admits exactly the oldest queued request.
  for (let i = 0; i < requests.length; i++) {
    await until(() => state.calls.length > i)
    assert.equal(state.calls[i].path, `/notes?id=eq.${i}`)
    state.calls[i].res.end('[]')
    assert.equal((await requests[i].result).status, 200)
    // Observe this promotion before releasing another slot: separate upstream
    // connections need not arrive in order when two promotions overlap.
    const dispatched = Math.min(requests.length, limits.pool + i + 1)
    await until(() => state.calls.length === dispatched)
  }
  state.auto = true
  assert.equal((await start().result).status, 200, 'capacity recovers after the burst')
  assert.doesNotMatch(JSON.stringify(state.logs), /private-|Bearer|postgres:/)
})

test('response headers and partial streaming do not release an active slot', { timeout: 7000 }, async t => {
  const { state, start, arrive } = await fixture(t)
  const active = [start(), start()]
  await until(() => state.calls.length === limits.pool)
  const headers = active.map(request => new Promise(resolve => request.client.once('response', resolve)))
  for (const call of state.calls) { call.res.writeHead(200); call.res.write('[') }
  await Promise.all(headers)
  const queued = await arrive('/rest/v1/rpc/save_note', 'POST')
  assert.equal(state.verified, limits.pool)
  assert.equal(state.calls.length, limits.pool)
  state.auto = true
  for (const call of state.calls) call.res.end(']')
  const results = await Promise.all([...active, queued].map(request => request.result))
  assert.ok(results.every(result => result.status === 200))
  assert.equal(state.calls.length, limits.pool + 1)
})

test('queued mutations expire without dispatch; a later request succeeds after recovery', { timeout: 7000 }, async t => {
  const { state, start } = await fixture(t)
  const active = [start(), start()]
  await until(() => state.calls.length === limits.pool)
  const queued = start('/rest/v1/rpc/save_note', 'POST')
  const expired = await queued.result
  assert.equal(expired.status, 503)
  assert.equal(JSON.parse(expired.body).code, 'data_api_overloaded')
  assert.equal(expired.headers['retry-after'], '1')
  assert.equal(state.verified, limits.pool)
  assert.equal(state.calls.length, limits.pool)
  const record = state.logs.find(record => record.admission?.outcome === 'queue_timeout')
  assert.ok(record.admission.wait_ms >= limits.admissionWaitMs)
  for (const call of state.calls) call.res.end('[]')
  await Promise.all(active.map(request => request.result))
  state.auto = true
  assert.equal((await start().result).status, 200)
  assert.ok(state.calls.every(call => call.method === 'GET'))
})

test('disconnect removes a queued write and reclaims its queue position', { timeout: 7000 }, async t => {
  const { state, start, arrive } = await fixture(t)
  const active = [start(), start()]
  await until(() => state.calls.length === limits.pool)
  const cancelled = await arrive('/rest/v1/rpc/save_note', 'POST')
  cancelled.client.destroy()
  await until(() => state.logs.some(record => record.admission?.outcome === 'cancelled'))
  const queued = []
  for (let i = 0; i < limits.queuedRequests; i++) queued.push(await arrive())
  assert.equal((await start().result).status, 503)
  state.auto = true
  for (const call of state.calls) call.res.end('[]')
  const results = await Promise.all([...active, ...queued].map(request => request.result))
  assert.ok(results.every(result => result.status === 200))
  assert.ok(state.calls.every(call => call.method === 'GET'))
})

test('disconnected verification retains its slot until verification settles', { timeout: 7000 }, async t => {
  const verification = deferred()
  const { state, start, arrive } = await fixture(t, { verify: async () => {
    await verification.promise
    return { sub: 'private-subject', exp: 9999999999 }
  } })
  const first = [start('/rest/v1/rpc/save_note', 'POST'), start('/rest/v1/rpc/save_note', 'POST')]
  await until(() => state.verified === limits.pool)
  first.forEach(request => request.client.destroy())
  await until(() => state.logs.length === limits.pool)
  const queued = []
  for (let i = 0; i < limits.queuedRequests; i++) queued.push(await arrive())
  assert.equal((await start().result).status, 503)
  assert.equal(state.verified, limits.pool)
  state.auto = true
  verification.resolve()
  assert.ok((await Promise.all(queued.map(request => request.result))).every(result => result.status === 200))
  assert.equal(state.calls.length, limits.queuedRequests)
  assert.ok(state.calls.every(call => call.method === 'GET'))
})

test('queued work rechecks authentication and refresh admission with the reserved control connection', { timeout: 7000 }, async t => {
  const { state, start, arrive } = await fixture(t, { coordinated: true })
  const active = start()
  await until(() => state.calls.length === 1)
  const invalid = await arrive('/rest/v1/notes', 'GET', 'invalid')
  const paused = await arrive('/rest/v1/rpc/save_note', 'POST')
  assert.equal(state.verified, 1)
  state.paused = true
  state.calls[0].res.end('[]')
  assert.equal((await active.result).status, 200)
  assert.equal((await invalid.result).status, 401)
  assert.equal(JSON.parse((await paused.result).body).code, 'data_api_refresh_paused')
  assert.equal(state.calls.length, 1)
  assert.ok(state.logs.filter(record => record.admission).every(record => record.admission.active <= 1))
})

test('promotion checks elapsed queue time even before the deadline timer runs', async t => {
  let now = 0
  t.mock.method(performance, 'now', () => now)
  const gate = createAdmission(1, res => { res.writableEnded = true; res.emit('finish') })
  const request = () => new EventEmitter()
  const response = () => Object.assign(new EventEmitter(), { destroyed: false, writableEnded: false })
  const activeRes = response(), queuedRes = response()
  let calls = 0
  const active = gate(request(), activeRes, async () => { calls++ })
  const queued = gate(request(), queuedRes, async () => { calls++ })
  await new Promise(setImmediate)
  now = limits.admissionWaitMs + 1
  activeRes.writableEnded = true
  activeRes.emit('finish')
  await Promise.all([active, queued])
  assert.equal(calls, 1)
  assert.equal(queuedRes.writableEnded, true)
})
