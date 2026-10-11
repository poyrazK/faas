import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createServer } from '../server.mjs'
import { runtimeConfig } from '../config.mjs'

test('disconnect while authenticating never dispatches a mutation upstream', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  let release, entered, calls = 0
  const authenticating = new Promise(resolve => { entered = resolve })
  const verified = new Promise(resolve => { release = resolve })
  const upstream = http.createServer((_req, res) => { calls++; res.end('[]') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => { entered(); await verified; return { sub: 'alice', exp: 9999999999 } }, upstream.address().port, undefined, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  let disconnected
  const closed = new Promise(resolve => { disconnected = resolve })
  gateway.on('request', (_req, res) => res.on('close', disconnected))
  const request = http.request({ host: '127.0.0.1', port: gateway.address().port, path: '/rest/v1/notes', method: 'POST', headers: { Authorization: 'Bearer valid' } })
  request.on('error', () => {})
  request.end('{}')
  await authenticating
  request.destroy()
  await closed
  release()
  await new Promise(resolve => setTimeout(resolve, 30))
  assert.equal(calls, 0)
})
