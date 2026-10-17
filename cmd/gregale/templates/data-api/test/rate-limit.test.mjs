import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { generateKeyPair, SignJWT } from 'jose'
import { runtimeConfig, limits } from '../config.mjs'
import { createRateLimiter } from '../rate-limit.mjs'
import { createServer, tokenVerifier } from '../server.mjs'

const env = { DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }

test('rate-limit settings are opt-in, strict, bounded and immutable', () => {
  assert.equal(runtimeConfig(env).rateLimit, null)
  assert.deepEqual(runtimeConfig({ ...env, DATA_API_RATE_LIMIT_BURST: '3' }).rateLimit, { perSecond: limits.rateLimitDefaultPerSecond, burst: 3 })
  const policy = runtimeConfig({ ...env, DATA_API_RATE_LIMIT_PER_SECOND: '2' }).rateLimit
  assert.deepEqual(policy, { perSecond: 2, burst: limits.rateLimitDefaultBurst })
  assert.ok(Object.isFrozen(policy))
  assert.deepEqual(runtimeConfig({ ...env, DATA_API_RATE_LIMIT_PER_SECOND: '1000', DATA_API_RATE_LIMIT_BURST: '1000' }).rateLimit, { perSecond: 1000, burst: 1000 })
  for (const setting of ['DATA_API_RATE_LIMIT_BURST', 'DATA_API_RATE_LIMIT_PER_SECOND']) {
    for (const value of ['', '0', '-1', '1.5', ' 1', '01', '1e2', '1001', '999999999999999999999', true]) assert.throws(() => runtimeConfig({ ...env, [setting]: value }), /rate-limit policy/)
  }
  assert.throws(() => createRateLimiter({ perSecond: 1, burst: -1 }), /rate-limit policy/)
  const disabled = createRateLimiter(null)
  for (let i = 0; i < limits.rateLimitMaxSubjects + 1; i++) assert.equal(disabled.take(String(i)), null)
  assert.equal(disabled.tracked, 0)
})

test('token buckets refill monotonically, cap idle credit and keep users independent', () => {
  let now = 0
  const policy = { perSecond: 2, burst: 2 }, limiter = createRateLimiter(policy, () => now)
  policy.burst = 999
  assert.equal(limiter.take('alice'), null); assert.equal(limiter.take('alice'), null)
  assert.equal(limiter.take('alice').reason, 'subject')
  assert.equal(limiter.take('bob'), null)
  now = 499
  assert.equal(limiter.take('alice').retry_after_seconds, 1)
  now = 500
  assert.equal(limiter.take('alice'), null)
  assert.equal(limiter.take('alice').reason, 'subject')
  now = 100000
  assert.equal(limiter.take('alice'), null); assert.equal(limiter.take('alice'), null)
  assert.equal(limiter.take('alice').reason, 'subject')
})

test('runtime buckets and exact Unicode or prototype-like subjects remain independent', () => {
  const first = createRateLimiter({ perSecond: 1, burst: 1 }, () => 0), second = createRateLimiter({ perSecond: 1, burst: 1 }, () => 0)
  for (const subject of ['__proto__', 'constructor', '🙂', '\ud800', '\ud801']) {
    assert.equal(first.take(subject), null)
    assert.equal(first.take(subject).reason, 'subject')
    assert.equal(second.take(subject), null)
  }
})

test('tracking pressure cannot evict depleted users or grow memory; fully refilled entries expire', () => {
  let now = 0
  const limiter = createRateLimiter({ perSecond: 1, burst: 2 }, () => now)
  for (let i = 0; i < limits.rateLimitMaxSubjects; i++) {
    assert.equal(limiter.take(`user-${i}`), null)
    assert.equal(limiter.take(`user-${i}`), null)
  }
  for (let i = 0; i < 100; i++) assert.equal(limiter.take(`overflow-${i}`).reason, 'tracking_capacity')
  assert.equal(limiter.tracked, limits.rateLimitMaxSubjects)
  assert.equal(limiter.take('user-0').reason, 'subject')
  now = 1000
  assert.equal(limiter.take('new-user').reason, 'tracking_capacity')
  assert.equal(limiter.take('user-0'), null)
  now = 2000
  assert.equal(limiter.take('new-user'), null)
  assert.equal(limiter.tracked, 2, 'only the recently consumed bucket and new subject remain')
  assert.equal(limiter.take('user-0'), null)
  assert.equal(limiter.take('user-0').reason, 'subject', 'expiry cannot restore more than fully refilled credit')
})

test('verified subjects share budgets across JWT rotation and routes, while other users and probes recover', { timeout: 15000 }, async t => {
  let now = 0
  t.mock.method(performance, 'now', () => now)
  const config = runtimeConfig({ ...env, DATA_API_ALLOWED_ORIGINS: 'https://app.example', DATA_API_RATE_LIMIT_PER_SECOND: '1', DATA_API_RATE_LIMIT_BURST: '2' })
  config.functions = [{ schema: 'api', name: 'save' }]; config.fingerprint = 'a'.repeat(64)
  const { publicKey, privateKey } = await generateKeyPair('ES256')
  const token = async (sub, jti, extra = {}) => new SignJWT({ sub, ...extra }).setProtectedHeader({ alg: 'ES256' }).setIssuer(env.DATA_API_ISSUER).setAudience(env.DATA_API_AUDIENCE).setExpirationTime('1h').setJti(jti).sign(privateKey)
  const alice = await token('private-alice', 'old'), rotated = await token('private-alice', 'new'), bob = await token('private-bob', 'bob')
  let dispatched = 0, probes = 0
  const upstream = http.createServer((req, res) => { dispatched++; req.resume(); res.end('[]') })
  const admin = http.createServer((_req, res) => { probes++; res.end() })
  await Promise.all([upstream, admin].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const records = [], gateway = createServer(config, tokenVerifier(config.auth, publicKey), upstream.address().port, admin.address().port, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => { for (const server of [gateway, upstream, admin]) server.closeAllConnections(); await Promise.all([gateway, upstream, admin].map(server => new Promise(resolve => server.close(resolve)))) })
  const request = async (jwt, path = '/rest/v1/notes', method = 'GET') => {
    const response = await fetch(`http://127.0.0.1:${gateway.address().port}${path}`, { method, headers: { Authorization: `Bearer ${jwt}`, Origin: 'https://app.example', 'X-Subject': 'forged-private-subject' } })
    const body = await response.text()
    return { response, body }
  }
  for (const invalid of ['forged-token', await token('private-alice', 'bad', { gregale_read_only: 'invalid' })]) assert.equal((await request(invalid)).response.status, 401)
  assert.equal((await request(alice)).response.status, 200)
  assert.equal((await request(rotated, '/rest/v1/rpc/save', 'POST')).response.status, 200)
  for (const [path, method] of [['/rest/v1/notes', 'POST'], ['/rest/v1/rpc/save', 'POST'], ['/openapi.json', 'GET'], ['/__gregale/schema', 'GET'], ['/rest/v1/notes', 'HEAD']]) {
    const { response, body } = await request(rotated, path, method)
    assert.equal(response.status, 429); assert.equal(response.headers.get('retry-after'), '1')
    assert.equal(response.headers.get('cache-control'), 'no-store')
    assert.match(response.headers.get('access-control-expose-headers'), /Retry-After/)
    if (method === 'HEAD') assert.equal(body, '')
    else { assert.equal(JSON.parse(body).code, 'data_api_rate_limited'); assert.equal(JSON.parse(body).request_id, response.headers.get('x-request-id')); assert.equal(JSON.parse(body).reason, 'subject') }
  }
  assert.equal(dispatched, 2); assert.equal(probes, 0, 'rate rejection must precede admin SQL probes too')
  assert.equal((await request(bob)).response.status, 200)
  assert.equal((await request(alice, '/livez')).response.status, 200)
  assert.equal((await request(alice, '/rest/v1/notes', 'OPTIONS')).response.status, 204)
  now = 1000
  assert.equal((await request(rotated)).response.status, 200)
  assert.equal(dispatched, 4)
  const rejections = records.filter(record => record.rate_limit)
  assert.equal(rejections.length, 5)
  assert.ok(rejections.every(record => record.status === 429 && record.outcome === 'rejected' && record.timings.upstream === undefined && record.rate_limit.retry_after_seconds === 1))
  assert.doesNotMatch(JSON.stringify(records), /private-|forged-|postgres:/)
})
