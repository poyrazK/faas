import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createServer } from '../server.mjs'
import { runtimeConfig, limits } from '../config.mjs'

test('real elapsed time closes a continuously streaming response and later requests recover', { timeout: 25000 }, async t => {
  let recovered = false, chunks = 0, closed = false
  const upstream = http.createServer((_req, res) => {
    if (recovered) { res.end('[]'); return }
    res.writeHead(200); res.write('[')
    const timer = setInterval(() => { chunks++; res.write(' ') }, 50)
    res.once('close', () => { closed = true; clearInterval(timer) })
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:test-only@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const records = []
  const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), upstream.address().port, undefined, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const url = `http://127.0.0.1:${gateway.address().port}/rest/v1/notes`
  const started = performance.now()
  const response = await fetch(url, { headers: { Authorization: 'Bearer fixture' } })
  assert.equal(response.status, 200)
  await assert.rejects(response.text())
  const elapsed = performance.now() - started
  assert.ok(elapsed >= limits.queryMs - 100 && elapsed < limits.queryMs + 4000)
  await new Promise(resolve => setImmediate(resolve))
  assert.ok(chunks > 100, 'the engine kept sending data throughout the deadline')
  assert.equal(records[0].code, 'request_deadline_exceeded')
  assert.equal(records[0].deadline.phase, 'streaming')
  assert.equal(records[0].outcome, 'aborted')
  recovered = true
  const next = await fetch(url, { headers: { Authorization: 'Bearer fixture' } })
  assert.equal(next.status, 200)
  assert.equal(await next.text(), '[]')
  assert.equal(closed, true)
})
