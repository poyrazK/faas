import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { runtimeConfig } from '../config.mjs'
import { createServer } from '../server.mjs'

test('an owner-selected read-only API rejects writes even when the JWT allows writes', async t => {
  const env = { DATABASE_URL: 'postgres://restricted:private@localhost/db', DATA_API_SCHEMAS: 'public', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'mirror', DATA_API_READ_ONLY: 'true', DATA_API_SQL_DEADLINE: '1', DATA_API_USER_CONCURRENCY: '1' }
  assert.throws(() => runtimeConfig({ ...env, DATA_API_READ_ONLY: 'yes' }))
  const config = runtimeConfig(env)
  assert.equal(config.sqlDeadline, true)
  assert.equal(config.postgrestEnv.PGRST_DB_PRE_REQUEST, 'gregale_data_api.check_deadline_request')
  let requests = 0
  const upstream = http.createServer((req, res) => { requests++; res.setHeader('Content-Type', 'application/json'); res.end('[]') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999, gregale_read_only: false }), upstream.address().port)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const url = `http://127.0.0.1:${gateway.address().port}/rest/v1/orders`
  const headers = { Authorization: 'Bearer valid', 'Content-Type': 'application/json' }
  for (const method of ['POST', 'PATCH', 'DELETE']) {
    const response = await fetch(url, { method, headers, body: method === 'DELETE' ? undefined : '{}' })
    assert.equal(response.status, 403)
    assert.equal((await response.json()).code, 'read_only_identity')
  }
  assert.equal(requests, 0)
  assert.equal((await fetch(url, { headers })).status, 200)
  assert.equal(requests, 1)
})
