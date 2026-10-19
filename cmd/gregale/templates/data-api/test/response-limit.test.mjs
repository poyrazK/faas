import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
import { runtimeConfig, limits } from '../config.mjs'
import { createServer } from '../server.mjs'
import { streamResponse } from '../response-limit.mjs'

const tick = () => new Promise(setImmediate)
async function until(check) {
  for (let i = 0; i < 5000; i++) { if (check()) return; await tick() }
  throw Error('Response fixture did not settle')
}
function sendBytes(res, size) {
  const chunk = Buffer.alloc(65536, 'x')
  let left = size
  const send = () => {
    while (left > 0 && !res.destroyed) {
      const part = chunk.subarray(0, Math.min(chunk.length, left)); left -= part.length
      if (!res.write(part)) { res.once('drain', send); return }
    }
    if (!res.destroyed) res.end()
  }
  send()
}
async function fixture(t, handler) {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  config.functions = [{ schema: 'api', name: 'large' }]
  const logs = [], calls = []
  const upstream = http.createServer((req, res) => { req.resume(); calls.push({ req, res }); handler(req, res) })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => ({ sub: 'private-subject', exp: 9999999999 }), upstream.address().port, undefined, record => logs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [gateway, upstream]) server.closeAllConnections()
    await Promise.all([gateway, upstream].map(server => new Promise(resolve => server.close(resolve))))
  })
  const request = (path = '/rest/v1/notes', method = 'GET') => {
    const req = http.request({ hostname: '127.0.0.1', port: gateway.address().port, path, method, headers: { Authorization: 'Bearer private-token' } })
    const result = new Promise(resolve => {
      req.on('error', () => resolve({ complete: false, bytes: 0 }))
      req.on('response', res => {
        let bytes = 0, body = ''
        res.on('data', chunk => { bytes += chunk.length; if (res.statusCode >= 400) body += chunk })
        res.on('error', () => {})
        res.on('close', () => resolve({ status: res.statusCode, headers: res.headers, complete: res.complete, bytes, body }))
      })
    })
    req.end()
    return { req, result }
  }
  return { logs, calls, request }
}

test('known oversized responses reject before forwarding headers and release admission', async t => {
  let large = true, closed = false
  const f = await fixture(t, (_req, res) => {
    if (!large) return res.end('[]')
    res.writeHead(200, { 'Content-Length': limits.responseBytes + 1, 'Content-Type': 'application/private-data' }); res.flushHeaders()
    res.on('close', () => { closed = true })
  })
  const result = await f.request().result
  assert.equal(result.status, 502)
  assert.equal(result.complete, true)
  const body = JSON.parse(result.body)
  assert.equal(body.code, 'response_too_large')
  assert.equal(body.limit, limits.responseBytes)
  assert.equal(body.observed, limits.responseBytes + 1)
  assert.equal(body.observed_is_lower_bound, true)
  assert.equal(body.request_id, result.headers['x-request-id'])
  await until(() => closed && f.logs.length === 1)
  assert.deepEqual(f.logs[0].response_limit, { source: 'content_length', limit_bytes: limits.responseBytes, observed_at_least_bytes: limits.responseBytes + 1 })
  assert.equal(f.logs[0].timings.streaming.complete, false)
  large = false
  assert.equal((await f.request().result).status, 200)
  assert.doesNotMatch(JSON.stringify(f.logs) + result.body, /private-|postgres:/)
})

test('byte boundaries apply to chunked REST, RPC and upstream error responses', async t => {
  for (const [size, path, method, status] of [
    [limits.responseBytes - 1, '/rest/v1/notes', 'GET', 200],
    [limits.responseBytes, '/rest/v1/notes', 'GET', 200],
    [limits.responseBytes + 1, '/rest/v1/notes', 'GET', 200],
    [limits.responseBytes + 1, '/rest/v1/rpc/large', 'POST', 200],
    [limits.responseBytes + 1, '/rest/v1/notes', 'GET', 500],
  ]) await t.test(`${method} ${path} ${status} ${size}`, async t => {
    const f = await fixture(t, (_req, res) => { res.writeHead(status, { 'Transfer-Encoding': 'chunked' }); sendBytes(res, size) })
    const result = await f.request(path, method).result
    await until(() => f.logs.length === 1)
    assert.equal(result.status, status)
    assert.ok(result.bytes <= limits.responseBytes)
    assert.equal(result.complete, size <= limits.responseBytes)
    if (size <= limits.responseBytes) {
      assert.equal(result.bytes, size)
      assert.equal(f.logs[0].response_limit, undefined)
    } else {
      assert.equal(f.logs[0].outcome, 'aborted')
      assert.equal(f.logs[0].code, 'response_too_large')
      assert.equal(f.logs[0].response_limit.source, 'stream')
      assert.equal(f.logs[0].timings.streaming.complete, false)
    }
  })
})

test('HEAD and bodyless responses ignore representation Content-Length, and exact declared bodies succeed', async t => {
  for (const [method, status] of [['HEAD', 200], ['GET', 204], ['GET', 304]]) {
    await t.test(`${method} ${status}`, async t => {
      const f = await fixture(t, (_req, res) => { res.writeHead(status, { 'Content-Length': limits.responseBytes + 1 }); res.end() })
      const result = await f.request('/rest/v1/notes', method).result
      assert.equal(result.status, status); assert.equal(result.bytes, 0); assert.equal(result.complete, true)
      assert.equal(f.logs[0].response_limit, undefined)
    })
  }
  const f = await fixture(t, (_req, res) => { res.setHeader('Content-Length', limits.responseBytes); sendBytes(res, limits.responseBytes) })
  assert.equal((await f.request().result).bytes, limits.responseBytes)
})

test('the stream guard preserves UTF-8 bytes and pauses the engine on backpressure', () => {
  const source = new PassThrough(), sink = new EventEmitter()
  let drained = false, body = ''
  Object.assign(sink, { headersSent: false, writeHead() { this.headersSent = true }, write(chunk) { body += chunk.toString(); return drained }, end() {}, destroyed: false })
  source.headers = {}; source.statusCode = 200
  streamResponse(source, sink, 'GET', { check: () => true }, () => assert.fail('unexpected rejection'))
  source.write(Buffer.from('🙂'))
  assert.equal(source.isPaused(), true)
  source.write(Buffer.from('x'))
  assert.equal(body, '🙂')
  drained = true; sink.emit('drain')
  return until(() => body === '🙂x').then(() => source.end())
})

test('a first oversized chunk can still return a problem without forwarding bytes', () => {
  const source = new PassThrough(), sink = new EventEmitter()
  Object.assign(sink, { headersSent: false, setHeader() {}, writeHead() { assert.fail('headers forwarded') }, write() { assert.fail('body forwarded') } })
  source.headers = {}; source.statusCode = 200
  let rejected
  streamResponse(source, sink, 'GET', { check: () => true }, (_res, status, code) => { rejected = { status, code } })
  source.write(Buffer.alloc(limits.responseBytes + 1))
  assert.deepEqual(rejected, { status: 502, code: 'response_too_large' })
  assert.equal(source.destroyed, true)
})
