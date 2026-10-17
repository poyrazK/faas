import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createServer } from '../server.mjs'
import { runtimeConfig } from '../config.mjs'
import { runtimeFailure } from '../logging.mjs'

const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/

test('request logs correlate success, auth and rejection without recording caller data', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:password-sentinel@localhost/database-sentinel', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_ALLOWED_ORIGINS: 'https://origin-sentinel.example' })
  config.functions = [{ schema: 'api', name: 'function_sentinel' }]
  const logs = []
  const upstream = http.createServer((req, res) => {
    res.setHeader('X-Request-Id', 'upstream-sentinel')
    res.writeHead(req.method === 'PATCH' ? 409 : 200)
    res.end('{"message":"response-body-sentinel"}')
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const server = createServer(config, async token => {
    if (token !== 'token-sentinel') throw new Error('verifier-secret-sentinel')
    return { sub: 'subject-sentinel', exp: 9999999999, role: 'role-sentinel' }
  }, upstream.address().port, undefined, record => logs.push(record))
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close(); upstream.closeAllConnections(); upstream.close() })
  const base = `http://127.0.0.1:${server.address().port}`
  const headers = { Authorization: 'Bearer token-sentinel', Origin: 'https://origin-sentinel.example', 'X-Request-Id': 'caller-sentinel', Cookie: 'cookie-sentinel', 'Content-Type': 'application/json' }
  const cases = [
    { path: '/rest/v1/table_sentinel?body=eq.query-sentinel', method: 'POST', body: '{"value":"request-body-sentinel"}', headers, status: 200, route: 'rest', outcome: 'completed', code: null },
    { path: '/rest/v1/rpc/function_sentinel', method: 'POST', body: '{}', headers, status: 200, route: 'rpc', outcome: 'completed', code: null },
    { path: '/rest/v1/table_sentinel', method: 'PATCH', body: '{}', headers, status: 409, route: 'rest', outcome: 'upstream_error', code: 'upstream_error' },
    { path: '/rest/v1/table_sentinel', method: 'GET', headers: {}, status: 401, route: 'rest', outcome: 'authentication_failed', code: 'token_required' },
    { path: '/rest/v1/table_sentinel', method: 'GET', headers: { Authorization: 'Bearer invalid-token-sentinel' }, status: 401, route: 'rest', outcome: 'authentication_failed', code: 'token_invalid' },
    { path: '/unknown-sentinel?secret=query-sentinel', method: 'GET', headers, status: 404, route: 'unknown', outcome: 'rejected', code: 'not_found' },
    { path: '/rest/v1/table_sentinel', method: 'OPTIONS', headers: { Origin: 'https://denied-origin-sentinel.example' }, status: 403, route: 'rest', outcome: 'rejected', code: 'origin_not_allowed' },
  ]
  const ids = new Set()
  for (const item of cases) {
    const response = await fetch(base + item.path, { method: item.method, body: item.body, headers: item.headers })
    assert.equal(response.status, item.status)
    const id = response.headers.get('x-request-id')
    assert.match(id, uuid)
    assert.ok(!ids.has(id)); ids.add(id)
    if (item.headers.Origin === headers.Origin) assert.ok(response.headers.get('access-control-expose-headers').includes('X-Request-Id'))
    const body = await response.json()
    if (item.code !== 'upstream_error' && item.status >= 400) assert.equal(body.request_id, id)
    await new Promise(setImmediate)
    assert.equal(logs.length, ids.size, 'finish and close must emit once')
    const record = logs.at(-1)
    assert.equal(record.request_id, id)
    assert.equal(record.status, item.status)
    assert.equal(record.method, item.method)
    assert.equal(record.route, item.route)
    assert.equal(record.outcome, item.outcome)
    assert.equal(record.code, item.code)
    assert.ok(Number.isFinite(record.duration_ms) && record.duration_ms >= 0)
    assert.ok(Number.isFinite(Date.parse(record.time)))
    const fields = ['code', 'duration_ms', 'event', 'level', 'method', 'outcome', 'request_id', 'route', 'status', 'time']
    if (item.headers.Authorization && item.route !== 'unknown') {
      fields.push('admission', 'timings')
      assert.equal(record.admission.outcome, 'admitted')
      assert.ok(record.admission.wait_ms >= 0)
    }
    assert.deepEqual(Object.keys(record).sort(), fields.sort())
  }
  assert.doesNotMatch(JSON.stringify(logs), /sentinel|postgres:|Bearer/)
})

test('logging exceptions do not change a gateway response', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const server = createServer(config, async () => { throw Error() }, undefined, undefined, () => { throw new Error('logger failed') })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  const response = await fetch(`http://127.0.0.1:${server.address().port}/rest/v1/notes`)
  assert.equal(response.status, 401)
  assert.equal((await response.json()).request_id, response.headers.get('x-request-id'))
})

test('default writer emits JSON lines and redacts runtime failure inputs', async t => {
  const lines = []
  t.mock.method(process.stderr, 'write', line => { lines.push(line); return true })
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const server = createServer(config, async () => { throw Error() })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  const response = await fetch(`http://127.0.0.1:${server.address().port}/rest/v1/notes`)
  await response.text()
  await new Promise(setImmediate)
  runtimeFailure('data_api_postgrest_start_failed')
  runtimeFailure('postgres://secret-sentinel\nforged-event')
  assert.equal(lines.length, 3)
  for (const line of lines) { assert.ok(line.endsWith('\n')); assert.equal(line.split('\n').length, 2); assert.doesNotMatch(line, /secret-sentinel|postgres:|forged-event/) }
  assert.equal(JSON.parse(lines[0]).request_id, response.headers.get('x-request-id'))
  assert.equal(JSON.parse(lines[1]).event, 'data_api_postgrest_start_failed')
  assert.equal(JSON.parse(lines[2]).event, 'data_api_runtime_failed')
})

test('opt-in query shapes use catalog metadata and redact values, claims, bodies and unknown fields', async t => {
 const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_QUERY_SHAPES: '1' })
 config.fingerprint = 'a'.repeat(64)
 config.snapshot = { tables: [{ schema: 'api', name: 'notes', columns: [{name:'id'},{name:'body'}] }] }
 config.functions = [{ schema: 'api', name: 'count_notes', args: [] }]
 const logs = []
 const upstream = http.createServer((req,res) => { res.writeHead(req.method==='PATCH'?409:200);res.end('[]') })
 await new Promise(resolve=>upstream.listen(0,'127.0.0.1',resolve))
 const server = createServer(config, async()=>({sub:'private-subject',exp:9999999999}),upstream.address().port,undefined,record=>logs.push(record))
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
 t.after(()=>{server.closeAllConnections();server.close();upstream.closeAllConnections();upstream.close()})
 const base=`http://127.0.0.1:${server.address().port}`
 for (const path of ['/rest/v1/notes?body=eq.private-a&select=id,body&order=id.desc&limit=20','/rest/v1/notes?body=eq.private-b&select=body,id&order=id.desc&limit=200','/rest/v1/notes?private-key=eq.private-value&or=(body.eq.private-data)&select=private-field','/rest/v1/private-table','/rest/v1/rpc/count_notes']) {
  const response=await fetch(base+path,{method:path.includes('/rpc/')?'POST':'GET',headers:{Authorization:'Bearer private-token'}});await response.text();await new Promise(setImmediate)
 }
 assert.equal(logs[0].operation,'api.notes');assert.equal(logs[0].query_shape_id,logs[1].query_shape_id)
 assert.equal(logs[0].query_shape.order[0].nulls_first,true)
 assert.equal(logs[2].query_shape.complex,true);assert.equal(logs[3].operation,undefined)
 assert.equal(logs[4].operation_kind,'rpc')
 assert.doesNotMatch(JSON.stringify(logs),/private-|Bearer/)
 const response=await fetch(base+'/rest/v1/notes');await response.text();await new Promise(setImmediate)
 assert.equal(logs.at(-1).operation,undefined)
 assert.throws(()=>runtimeConfig({ DATABASE_URL:'postgres://restricted:p@localhost/db',DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'notes',DATA_API_QUERY_SHAPES:'anything' }))
})
