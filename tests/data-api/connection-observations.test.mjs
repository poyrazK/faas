import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { pathToFileURL } from 'node:url'

test('concurrent health and reporting share one pending engine metrics observation', { skip: !process.env.DATA_API_RUNTIME_DIR }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const { createPoolObservation } = await import(new URL('pool.mjs', runtime))
  const { createHealth } = await import(new URL('health.mjs', runtime))
  const { createConnectionReporter } = await import(new URL('connection-observations.mjs', runtime))
  let metrics = 0, reports = 0
  const admin = http.createServer((req, res) => {
    if (req.url !== '/metrics') return res.end('')
    metrics++
    setTimeout(() => res.end('pgrst_db_pool_max 2\npgrst_db_pool_available 1\npgrst_db_pool_waiting 0\npgrst_db_pool_timeouts_total 0\n'), 50)
  })
  await new Promise(resolve => admin.listen(0, '127.0.0.1', resolve))
  t.after(() => new Promise(resolve => { admin.closeAllConnections(); admin.close(resolve) }))
  const pool = createPoolObservation(admin.address().port, 2)
  const config = { role: 'restricted', poolObservation: pool, connectionObservation: {
    identity: 'http://127.0.0.1:2773/oidc/token?audience=gregale:data-api:connections',
    endpoint: 'https://control.example/v1/runtime/data-api/connection-observations',
    binding_id: '11111111-1111-4111-8111-111111111111', database_identity: 'a'.repeat(64), database_generation: 1, credential_generation: 1,
  } }
  const health = createHealth(config, admin.address().port, admin.address().port)
  const report = createConnectionReporter(config, pool, async (url, request) => {
    if (url === config.connectionObservation.identity) return Response.json({ token_type: 'Bearer', access_token: 'test-token' })
    assert.equal(JSON.parse(request.body).state, 'observed')
    reports++
    return new Response(null, { status: 204 })
  })
  const results = await Promise.all([report(), ...Array.from({ length: 16 }, () => health.snapshot(() => ({})))])
  assert.equal(metrics, 1, 'reporting must not add another pending SQL-backed admin metrics request')
  assert.equal(reports, 1)
  assert.ok(results.slice(1).every(result => result.pool.state === 'observed' && result.ready))
})
