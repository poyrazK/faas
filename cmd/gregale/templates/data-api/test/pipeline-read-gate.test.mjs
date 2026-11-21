import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { decodeJwt } from 'jose'
import { runtimeConfig } from '../config.mjs'
import { createServer } from '../server.mjs'

test('pipeline coordination claims come from the runtime and preserve SQL deadline composition', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://reader:private@localhost/db', DATA_API_SCHEMAS: 'public', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'mirror', DATA_API_READ_ONLY: 'true', DATA_API_SQL_DEADLINE: '1' })
  config.pipelineReadGate = true
  config.pipelinePreviousHook = 'gregale_data_api.check_deadline_request'
  const claims = []
  const engine = http.createServer((req, res) => {
    claims.push(decodeJwt(req.headers.authorization.slice('Bearer '.length)))
    res.writeHead(503, { 'Content-Type': 'application/json', 'Retry-After': '15', 'Cache-Control': 'no-store' })
    res.end('{"code":"mirror_unavailable"}')
  })
  await new Promise(resolve => engine.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999, gregale_pipeline_request: { version: 999, previous_hook: 'attacker' } }), engine.address().port)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); engine.closeAllConnections(); engine.close() })
  const response = await fetch(`http://127.0.0.1:${gateway.address().port}/rest/v1/orders`, { headers: { Authorization: 'Bearer valid' } })
  assert.equal(response.status, 503)
  assert.equal((await response.json()).code, 'mirror_unavailable')
  assert.equal(response.headers.get('retry-after'), '15')
  assert.equal(response.headers.get('cache-control'), 'no-store')
  assert.deepEqual(claims[0].gregale_pipeline_request, { version: 1, previous_hook: 'gregale_data_api.check_deadline_request' })
  assert.equal(claims[0].gregale_sql_deadline.version, 1)
})
