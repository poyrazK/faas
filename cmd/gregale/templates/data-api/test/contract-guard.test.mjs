import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createServer } from '../server.mjs'
import { runtimeConfig } from '../config.mjs'
import { generate, fingerprint } from '../types.mjs'

test('contract guards authenticate and reject mismatches before every upstream operation', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_ALLOWED_ORIGINS: 'https://app.example' })
  const expected = 'a'.repeat(64), different = 'b'.repeat(64)
  config.fingerprint = expected
  config.functions = [{ schema: 'api', name: 'approved' }]
  const calls = [], records = []
  const upstream = http.createServer((req, res) => { calls.push({ path: req.url, method: req.method }); req.resume(); res.end('[]') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const verify = async token => { if (token !== 'valid') throw Error('invalid'); return { sub: 'alice', exp: 9999999999 } }
  const gateway = createServer(config, verify, upstream.address().port, undefined, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const base = `http://127.0.0.1:${gateway.address().port}`
  const route = (hash, path = 'notes?select=id') => `${base}/__gregale/contract/${hash}/rest/v1/${path}`
  const headers = { Authorization: 'Bearer valid', 'Content-Type': 'application/json', Origin: 'https://app.example' }
  for (const authorization of [undefined, 'Bearer invalid']) {
    const response = await fetch(route(different), { headers: authorization ? { Authorization: authorization } : {} })
    assert.equal(response.status, 401)
    assert.doesNotMatch(await response.text(), /schema_contract_mismatch|[ab]{64}/)
  }
  for (const [method, path] of [['GET', 'notes'], ['HEAD', 'notes'], ['POST', 'notes'], ['PATCH', 'notes'], ['DELETE', 'notes'], ['POST', 'rpc/approved'], ['POST', 'rpc/removed']]) {
    const response = await fetch(route(different, path), { method, headers, ...(['POST', 'PATCH'].includes(method) ? { body: '{"value":"body-sentinel"}' } : {}) })
    assert.equal(response.status, 409)
    assert.equal(response.headers.get('cache-control'), 'no-store')
    assert.equal(response.headers.get('access-control-allow-origin'), 'https://app.example')
    if (method !== 'HEAD') {
      const body = await response.json()
      assert.equal(body.code, 'schema_contract_mismatch')
      assert.equal(body.request_id, response.headers.get('x-request-id'))
      assert.doesNotMatch(JSON.stringify(body), /body-sentinel|[ab]{64}/)
    }
  }
  assert.equal(calls.length, 0)
  const preflight = await fetch(route(different), { method: 'OPTIONS', headers: { Origin: 'https://app.example', 'Access-Control-Request-Method': 'POST', 'Access-Control-Request-Headers': 'authorization,content-type' } })
  assert.equal(preflight.status, 204); assert.equal(calls.length, 0)
  // The advertised serving contract is immutable for this gateway instance.
  config.fingerprint = different
  assert.equal((await fetch(route(expected), { headers })).status, 200)
  assert.equal((await fetch(route(expected, 'rpc/approved'), { method: 'POST', headers, body: '{}' })).status, 200)
  assert.equal((await fetch(`${base}/rest/v1/notes`, { headers })).status, 200)
  assert.deepEqual(calls, [{ path: '/notes?select=id', method: 'GET' }, { path: '/rpc/approved', method: 'POST' }, { path: '/notes', method: 'GET' }])
  await new Promise(setImmediate)
  const rejected = records.filter(record => record.code === 'schema_contract_mismatch')
  assert.equal(rejected.length, 7)
  assert.ok(rejected.every(record => record.outcome === 'rejected' && ['rest', 'rpc'].includes(record.route)))
  assert.doesNotMatch(JSON.stringify(records), /body-sentinel|[ab]{64}|Bearer/)
})

test('missing serving fingerprints fail closed and malformed guard routes never reach the engine', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  let calls = 0
  const upstream = http.createServer((req, res) => { calls++; res.end('[]') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), upstream.address().port, undefined, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const base = `http://127.0.0.1:${gateway.address().port}`
  const headers = { Authorization: 'Bearer valid' }
  const unavailable = await fetch(`${base}/__gregale/contract/${'a'.repeat(64)}/rest/v1/notes`, { headers })
  assert.equal(unavailable.status, 503); assert.equal((await unavailable.json()).code, 'serving_contract_unavailable')
  for (const path of [`/__gregale/contract/${'A'.repeat(64)}/rest/v1/notes`, `/__gregale/contract/${'a'.repeat(63)}/rest/v1/notes`, `/__gregale/contract/${'a'.repeat(64)}/__gregale/schema`]) {
    assert.equal((await fetch(base + path, { headers })).status, 404)
  }
  assert.equal(calls, 0)
})

test('generated fingerprint values match the schema snapshot fingerprint', () => {
  const snapshot = { version: 3, schemas: ['api'], tables: [], functions: [], enums: {}, composites: [] }
  const hash = fingerprint(snapshot)
  assert.ok(generate(snapshot).includes(`export const schemaFingerprint = "${hash}"`))
})

test('accepted older contracts are serving-bound, immutable, authenticated and revocable', async t => {
  const current = 'a'.repeat(64), older = 'b'.repeat(64), rejected = 'c'.repeat(64)
  const env = { DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }
  const policy = { version: 1, current_fingerprint: current, accepted_fingerprints: [older] }
  const config = runtimeConfig({ ...env, DATA_API_ACCEPTED_CONTRACTS: JSON.stringify(policy) })
  config.fingerprint = current
  config.functions = [{ schema: 'api', name: 'approved' }]
  const calls = []
  const upstream = http.createServer((req, res) => { calls.push(req.url); req.resume(); res.end('[]') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const verify = async token => { if (token !== 'valid') throw Error(); return { sub: 'alice', exp: 9999999999 } }
  const gateway = createServer(config, verify, upstream.address().port, undefined, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const route = (hash, path = 'notes') => `http://127.0.0.1:${gateway.address().port}/__gregale/contract/${hash}/rest/v1/${path}`
  const headers = { Authorization: 'Bearer valid' }
  assert.equal((await fetch(route(older))).status, 401)
  assert.equal((await fetch(route(rejected), { method: 'POST', headers, body: '{}' })).status, 409)
  assert.equal(calls.length, 0)
  // Neither config mutation nor client claims can expand the startup policy.
  config.acceptedContracts = { ...policy, accepted_fingerprints: [rejected] }
  for (const hash of [current, older]) {
    for (const method of ['GET', 'POST', 'PATCH', 'DELETE']) {
      assert.equal((await fetch(route(hash), { method, headers, ...(['POST', 'PATCH'].includes(method) ? { body: '{}' } : {}) })).status, 200)
    }
    assert.equal((await fetch(route(hash, 'rpc/approved'), { method: 'POST', headers, body: '{}' })).status, 200)
    assert.equal((await fetch(route(hash, 'rpc/removed'), { method: 'POST', headers, body: '{}' })).status, 404)
  }
  assert.equal(calls.length, 10)
  assert.equal((await fetch(route(rejected), { headers })).status, 409)
  assert.throws(() => createServer({ ...config, fingerprint: rejected }, verify), /do not match/)
  const revoked = createServer({ ...config, acceptedContracts: undefined }, verify, upstream.address().port, undefined, () => {})
  await new Promise(resolve => revoked.listen(0, '127.0.0.1', resolve))
  t.after(() => { revoked.closeAllConnections(); revoked.close() })
  assert.equal((await fetch(`http://127.0.0.1:${revoked.address().port}/__gregale/contract/${older}/rest/v1/notes`, { headers })).status, 409)
  assert.equal(calls.length, 10)
  for (const invalid of ['', 'null', '{}', '{', JSON.stringify({ ...policy, extra: true }), JSON.stringify({ ...policy, version: 2 }), JSON.stringify({ ...policy, accepted_fingerprints: [] }), JSON.stringify({ ...policy, accepted_fingerprints: [current] }), JSON.stringify({ ...policy, accepted_fingerprints: [older, older] }), JSON.stringify({ ...policy, accepted_fingerprints: ['B'.repeat(64)] }), JSON.stringify({ ...policy, current_fingerprint: null })]) {
    assert.throws(() => runtimeConfig({ ...env, DATA_API_ACCEPTED_CONTRACTS: invalid }), /Invalid DATA_API_ACCEPTED_CONTRACTS/)
  }
})
