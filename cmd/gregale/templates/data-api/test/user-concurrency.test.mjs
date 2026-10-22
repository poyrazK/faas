import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { generateKeyPair, SignJWT } from 'jose'
import { runtimeConfig, limits } from '../config.mjs'
import { createUserConcurrency } from '../user-concurrency.mjs'
import { createServer, tokenVerifier } from '../server.mjs'

const env = { DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }
async function until(check) {
  const end = Date.now() + 5000
  while (!check()) {
    if (Date.now() >= end) throw Error('User concurrency fixture did not settle')
    await new Promise(resolve => setTimeout(resolve, 10))
  }
}

test('user concurrency is opt-in, strictly bounded and respects the refresh reservation', () => {
  assert.equal(runtimeConfig(env).userConcurrency, null)
  assert.equal(runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '1' }).userConcurrency, 1)
  assert.equal(runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '2' }).userConcurrency, 2)
  assert.equal(runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '1', DATA_API_COORDINATED_REFRESH: '1' }).userConcurrency, 1)
  for (const value of ['', '0', '-1', '1.5', '01', ' 1', '1e0', '9', '999999999999999999999', true]) assert.throws(() => runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: value }))
  // ADR-20261021180534: a value above the engine pool acts as the pool.
  assert.equal(runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '3' }).userConcurrency, 2)
  assert.equal(runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '2', DATA_API_COORDINATED_REFRESH: '1' }).userConcurrency, 1)
  assert.throws(() => createUserConcurrency(3))
  const disabled = createUserConcurrency(null)
  for (let i = 0; i <= limits.userConcurrencyMaxSubjects; i++) assert.equal(disabled.take(String(i)).lease, null)
  assert.equal(disabled.tracked, 0)
})

test('both handler and downstream must settle; uncertain dispatches cool down without renewal or stale release', () => {
  let now = 0
  const limiter = createUserConcurrency(1, 2, () => now)
  const first = limiter.take('alice').lease
  first.downstreamDone()
  now = 100000
  assert.equal(limiter.take('alice').rejected.active, 1, 'client close cannot release pending handler work')
  first.settled()
  const uncertain = limiter.take('alice').lease
  uncertain.dispatched(); uncertain.settled()
  assert.equal(limiter.take('alice').rejected.active, 1, 'handler completion cannot release a live response')
  uncertain.downstreamDone()
  assert.deepEqual(limiter.take('alice').rejected, { reason: 'subject', max_concurrent: 1, active: 0, cooling_down: 1, retry_after_seconds: 15 })
  uncertain.downstreamDone(); uncertain.settled()
  now += limits.userConcurrencyCooldownMs - 1
  assert.equal(limiter.take('alice').rejected.retry_after_seconds, 1)
  now++
  const replacement = limiter.take('alice').lease
  assert.ok(replacement)
  uncertain.completed(); uncertain.settled()
  assert.equal(limiter.take('alice').rejected.active, 1, 'late completion cannot release a replacement lease')
  replacement.dispatched(); replacement.completed(); replacement.downstreamDone(); replacement.settled()
  assert.equal(limiter.tracked, 0)
})

test('completed engine work still occupies streaming requests and a late acknowledgement clears only its cooldown', () => {
  const limiter = createUserConcurrency(1)
  const stream = limiter.take('alice').lease
  stream.dispatched(); stream.completed(); stream.settled()
  assert.equal(limiter.take('alice').rejected.active, 1)
  stream.downstreamDone()
  const interrupted = limiter.take('alice').lease
  interrupted.dispatched(); interrupted.downstreamDone(); interrupted.settled()
  assert.equal(limiter.take('alice').rejected.cooling_down, 1)
  interrupted.completed()
  assert.ok(limiter.take('alice').lease)
})

test('two-slot policies combine active and cooling requests without double release', () => {
  const limiter = createUserConcurrency(2)
  const first = limiter.take('alice').lease, second = limiter.take('alice').lease
  first.dispatched(); first.settled(); first.downstreamDone()
  assert.deepEqual(limiter.take('alice').rejected, { reason: 'subject', max_concurrent: 2, active: 1, cooling_down: 1, retry_after_seconds: 1 })
  second.settled(); second.downstreamDone(); second.downstreamDone()
  const third = limiter.take('alice').lease
  assert.ok(third)
  assert.equal(limiter.take('alice').rejected.cooling_down, 1)
})

test('tracking saturation stays bounded, preserves held users and recovers at cooldown expiry', () => {
  let now = 0
  const limiter = createUserConcurrency(1, 2, () => now)
  for (let i = 0; i < limits.userConcurrencyMaxSubjects; i++) {
    const lease = limiter.take(`user-${i}`).lease
    lease.dispatched(); lease.settled(); lease.downstreamDone()
  }
  for (let i = 0; i < 100; i++) assert.equal(limiter.take(`extra-${i}`).rejected.reason, 'tracking_capacity')
  assert.equal(limiter.tracked, limits.userConcurrencyMaxSubjects)
  assert.equal(limiter.take('user-0').rejected.reason, 'subject')
  now = limits.userConcurrencyCooldownMs
  assert.ok(limiter.take('new-user').lease)
  assert.equal(limiter.tracked, 1)
  for (const subject of ['constructor', '__proto__', 'é', 'e\u0301']) {
    assert.ok(limiter.take(subject).lease)
    assert.equal(limiter.take(subject).rejected.reason, 'subject')
    assert.ok(createUserConcurrency(1).take(subject).lease, 'runtime budgets must be independent')
  }
})

test('verified users share limits across routes and JWT rotation; other users continue and cancellation is conservative', { timeout: 15000 }, async t => {
  let now = 0
  t.mock.method(performance, 'now', () => now)
  const config = runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '1', DATA_API_ALLOWED_ORIGINS: 'https://app.example' })
  config.fingerprint = 'a'.repeat(64); config.functions = [{ schema: 'api', name: 'save' }]
  const { publicKey, privateKey } = await generateKeyPair('ES256')
  const token = (sub, jti) => new SignJWT({ sub, gregale_concurrency_limit: 99 }).setProtectedHeader({ alg: 'ES256' }).setIssuer(env.DATA_API_ISSUER).setAudience(env.DATA_API_AUDIENCE).setExpirationTime('1h').setJti(jti).sign(privateKey)
  const alice = await token('private-alice', 'old'), rotated = await token('private-alice', 'new'), bob = await token('private-bob', 'bob')
  const held = [], records = []
  let calls = 0, probes = 0
  const engine = http.createServer((req, res) => {
    calls++; req.resume()
    if (req.url === '/hold') { held.push(res); return }
    if (req.url === '/stream') { res.writeHead(200); res.write('['); return }
    res.end('[]')
  })
  const admin = http.createServer((_req, res) => { probes++; res.end() })
  await Promise.all([engine, admin].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const gateway = createServer(config, tokenVerifier(config.auth, publicKey), engine.address().port, admin.address().port, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => { for (const server of [gateway, engine, admin]) server.closeAllConnections(); await Promise.all([gateway, engine, admin].map(server => new Promise(resolve => server.close(resolve)))) })
  const base = `http://127.0.0.1:${gateway.address().port}`
  const request = (jwt, path = '/rest/v1/notes', method = 'GET', signal) => fetch(base + path, { method, signal, headers: { Authorization: `Bearer ${jwt}`, Origin: 'https://app.example', 'X-Subject': 'forged-subject' } })
  assert.equal((await request('invalid')).status, 401)
  const first = request(alice, '/rest/v1/hold')
  await until(() => held.length === 1)
  for (const [path, method] of [['/rest/v1/notes', 'POST'], ['/rest/v1/rpc/save', 'POST'], ['/openapi.json', 'GET'], ['/__gregale/schema', 'GET'], ['/rest/v1/notes', 'HEAD']]) {
    const response = await request(rotated, path, method)
    assert.equal(response.status, 429); assert.equal(response.headers.get('retry-after'), '1')
    assert.match(response.headers.get('access-control-expose-headers'), /Retry-After/)
    if (method === 'HEAD') assert.equal(await response.text(), '')
    else { const body = await response.json(); assert.equal(body.code, 'data_api_concurrency_limited'); assert.equal(body.request_id, response.headers.get('x-request-id')); assert.equal(body.active, 1) }
  }
  assert.equal(calls, 1); assert.equal(probes, 0, 'rejections must precede refresh and readiness SQL too')
  const other = await request(bob); assert.equal(other.status, 200); await other.text()
  assert.equal((await request(alice, '/livez')).status, 200)
  assert.equal((await request(alice, '/rest/v1/notes', 'OPTIONS')).status, 204)
  held[0].end('[]'); await (await first).text()
  const recovered = await request(rotated); assert.equal(recovered.status, 200); await recovered.text()

  const abort = new AbortController()
  const pending = request(alice, '/rest/v1/hold', 'GET', abort.signal)
  const rejected = assert.rejects(pending, error => error.name === 'AbortError')
  await until(() => held.length === 2)
  abort.abort(); await rejected
  await until(() => records.some(record => record.outcome === 'aborted'))
  const cooling = await request(rotated), body = await cooling.json()
  assert.equal(cooling.status, 429); assert.equal(body.cooling_down, 1); assert.equal(cooling.headers.get('retry-after'), '15')
  now += limits.userConcurrencyCooldownMs
  const next = await request(rotated); assert.equal(next.status, 200); await next.text()

  const streamAbort = new AbortController()
  const stream = await request(alice, '/rest/v1/stream', 'GET', streamAbort.signal)
  const reader = stream.body.getReader(); await reader.read(); streamAbort.abort()
  await assert.rejects(reader.read(), error => error.name === 'AbortError')
  await until(() => records.filter(record => record.outcome === 'aborted').length === 2)
  const afterStream = await request(rotated); assert.equal(afterStream.status, 200); await afterStream.text()
  assert.equal(records.filter(record => record.concurrency_limit).length, 6)
  for (const record of records.filter(record => record.concurrency_limit)) {
    assert.equal(record.outcome, 'rejected'); assert.equal(record.timings.upstream, undefined)
    assert.equal(record.database_timings, undefined)
  }
  assert.doesNotMatch(JSON.stringify(records), /private-|forged-|postgres:/)
})

test('a cancelled shared control read keeps the user slot until the handler actually settles', { timeout: 10000 }, async t => {
  const config = runtimeConfig({ ...env, DATA_API_USER_CONCURRENCY: '1' })
  config.fingerprint = 'a'.repeat(64)
  let resolveGate, calls = 0, reading = false
  config.refreshStatus = () => { reading = true; return new Promise(resolve => { resolveGate = resolve }) }
  const engine = http.createServer((_req, res) => { calls++; res.end('[]') })
  await new Promise(resolve => engine.listen(0, '127.0.0.1', resolve))
  const records = [], gateway = createServer(config, async () => ({ sub: 'private-user', exp: 9999999999 }), engine.address().port, undefined, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); engine.closeAllConnections(); engine.close() })
  const base = `http://127.0.0.1:${gateway.address().port}`, headers = { Authorization: 'Bearer private-token' }
  const abort = new AbortController(), pending = fetch(base + '/__gregale/schema', { headers, signal: abort.signal })
  const rejected = assert.rejects(pending, error => error.name === 'AbortError')
  await until(() => reading); abort.abort(); await rejected
  await until(() => records.some(record => record.outcome === 'aborted'))
  const blocked = await fetch(base + '/rest/v1/notes', { headers })
  assert.equal(blocked.status, 429); assert.equal((await blocked.json()).active, 1)
  resolveGate({ paused: false, fingerprint: config.fingerprint })
  await new Promise(setImmediate)
  const recovered = await fetch(base + '/rest/v1/notes', { headers })
  assert.equal(recovered.status, 200); await recovered.text(); assert.equal(calls, 1)
})
