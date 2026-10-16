import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer, upstreamPath } from '../server.mjs'

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

async function harness(t, upstreamHandler, options = {}) {
  const seen = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => { seen.push({ method: req.method, url: req.url, headers: req.headers, body }); upstreamHandler(req, res) })
  })
  const upstreamPort = await listen(upstream)
  const front = createServer({ upstreamPort, ...options })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  return { base: `http://127.0.0.1:${port}`, seen }
}

test('maps the public prefix onto GoTrue without decoding', () => {
  assert.equal(upstreamPath('/auth/v1'), '/')
  assert.equal(upstreamPath('/auth/v1/token?grant_type=password'), '/token?grant_type=password')
  assert.equal(upstreamPath('/auth/v1?x=1'), '/?x=1')
  assert.equal(upstreamPath('/auth/v1/%2e%2e/admin'), '/%2e%2e/admin')
  assert.equal(upstreamPath('/auth/v10/token'), null)
  assert.equal(upstreamPath('/token'), null)
  assert.equal(upstreamPath('/'), null)
})

test('proxies auth requests and preserves the trusted client address', async t => {
  const { base, seen } = await harness(t, (req, res) => {
    res.writeHead(200, { 'Content-Type': 'application/json', 'X-Upstream': 'gotrue' })
    res.end('{"access_token":"t"}')
  })
  const response = await fetch(`${base}/auth/v1/token?grant_type=password`, { method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Real-IP': '203.0.113.7' }, body: '{"email":"a@example.com"}' })
  assert.equal(response.status, 200)
  assert.equal(response.headers.get('x-upstream'), 'gotrue')
  assert.deepEqual(await response.json(), { access_token: 't' })
  assert.equal(seen[0].url, '/token?grant_type=password')
  assert.equal(seen[0].headers['x-real-ip'], '203.0.113.7')
  assert.equal(seen[0].body, '{"email":"a@example.com"}')
})

test('serves nothing outside the auth prefix', async t => {
  const { base, seen } = await harness(t, (req, res) => res.end())
  for (const path of ['/', '/token', '/admin/users', '/auth/v10/x']) {
    const response = await fetch(base + path)
    assert.equal(response.status, 404, path)
  }
  assert.equal(seen.length, 0)
})

test('bounds request bodies, declared or chunked', async t => {
  const { base, seen } = await harness(t, (req, res) => res.end('ok'), { bodyBytes: 16 })
  const declared = await fetch(`${base}/auth/v1/signup`, { method: 'POST', body: 'x'.repeat(17) })
  assert.equal(declared.status, 413)
  const chunked = await new Promise(resolve => {
    const url = new URL(`${base}/auth/v1/signup`)
    const req = http.request({ host: url.hostname, port: url.port, path: url.pathname, method: 'POST' }, resolve)
    req.on('error', () => resolve({ statusCode: 'reset' }))
    req.write('x'.repeat(10))
    req.end('x'.repeat(10))
  })
  assert.ok(chunked.statusCode === 413 || chunked.statusCode === 'reset', String(chunked.statusCode))
  assert.ok(seen.every(r => r.body.length <= 16))
})

test('healthz reflects GoTrue health', async t => {
  let healthy = true
  const { base } = await harness(t, (req, res) => { res.writeHead(req.url === '/health' && healthy ? 200 : 503); res.end() })
  assert.equal((await fetch(`${base}/healthz`)).status, 200)
  healthy = false
  assert.equal((await fetch(`${base}/healthz`)).status, 503)
})

test('reports an unavailable upstream', async t => {
  const front = createServer({ upstreamPort: 1 })
  const port = await listen(front)
  t.after(() => front.close())
  assert.equal((await fetch(`http://127.0.0.1:${port}/auth/v1/settings`)).status, 502)
  assert.equal((await fetch(`http://127.0.0.1:${port}/healthz`)).status, 503)
})

test('serves public settings without credentials', async t => {
  const settings = { app: 'shop.example.com', keys: { signing: '0A1B2C3D', verifying: [] } }
  const { base, seen } = await harness(t, (req, res) => res.end(), { settings })
  const res = await fetch(`${base}/settings/v1`)
  assert.equal(res.status, 200)
  assert.equal(res.headers.get('access-control-allow-origin'), '*')
  assert.equal(res.headers.get('cache-control'), 'no-store')
  assert.deepEqual(await res.json(), settings)
  assert.equal((await fetch(`${base}/settings/v1`, { method: 'POST' })).status, 405)
  assert.equal(seen.length, 0)
})
