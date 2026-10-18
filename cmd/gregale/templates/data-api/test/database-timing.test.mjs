import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { parseDatabaseTimings } from '../database-timing.mjs'
import { runtimeConfig, limits } from '../config.mjs'
import { createServer } from '../server.mjs'

const header = 'jwt;dur=0.0, parse;dur=0.1, plan;dur=0.2, transaction;dur=201.3, response;dur=0.4'
const config = () => runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
async function until(check) {
  const end = performance.now() + 3000
  while (!check()) {
    if (performance.now() > end) throw Error('timing log did not arrive')
    await new Promise(resolve => setTimeout(resolve, 10))
  }
}

test('pinned engine milliseconds preserve zero and distinguish transaction time from SQL and pool wait', () => {
  assert.equal(config().postgrestEnv.PGRST_SERVER_TIMING_ENABLED, 'true')
  const result = parseDatabaseTimings(header)
  assert.equal(result.version, 1)
  assert.equal(result.source, 'postgrest_server_timing')
  assert.equal(result.state, 'observed')
  assert.deepEqual(result.phases.transaction, { duration_ms: 201.3, complete: true })
  assert.deepEqual(result.phases.jwt, { duration_ms: 0, complete: true })
  assert.equal(result.sql_execution, 'unknown')
  assert.equal(result.pool_wait, 'unknown')
  assert.equal(parseDatabaseTimings(undefined).reason, 'missing_header')
  for (const invalid of [null, [], '', header + ', transaction;dur=999.0', header.replace('0.0', '-1.0'),
    header.replace('0.0', 'NaN'), header.replace('0.0', '1e3'), header.replace('0.0', '1'),
    header.replace('0.0', '99999999999999999999.0'), header.replace('0.0', '0.0;desc="private-sentinel"'),
    header.split(',').slice(1).join(','), header + ', private-sentinel;dur=1.0', 'x'.repeat(limits.healthBytes + 1)]) {
    const unknown = parseDatabaseTimings(invalid)
    assert.equal(unknown.state, 'unknown')
    assert.equal(unknown.reason, 'invalid_header')
    assert.equal(unknown.phases, undefined)
    assert.doesNotMatch(JSON.stringify(unknown), /private-sentinel|NaN|999999/)
  }
})

test('engine measurements correlate with requests, reject spoofing and survive downstream cancellation', async t => {
  let pendingStarted
  const pending = new Promise(resolve => { pendingStarted = resolve })
  const engine = http.createServer((req, res) => {
    assert.equal(req.headers['server-timing'], undefined)
    if (req.url === '/pending') { pendingStarted(); return }
    if (req.url !== '/missing') res.setHeader('Server-Timing', req.url === '/invalid' ? header + ',private-sentinel;dur=1.0' : header)
    if (req.url === '/stream') { res.writeHead(200); res.write('['); return }
    res.end('[]')
  })
  await new Promise(resolve => engine.listen(0, '127.0.0.1', resolve))
  const logs = []
  const gateway = createServer(config(), async () => ({ sub: 'private-subject', exp: 9999999999 }), engine.address().port, undefined, record => logs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); engine.closeAllConnections(); engine.close() })
  const base = `http://127.0.0.1:${gateway.address().port}/rest/v1`
  const headers = { Authorization: 'Bearer private-token', 'Server-Timing': 'transaction;dur=999.0;desc="private-sentinel"' }
  for (const [path, reason] of [['/observed', undefined], ['/invalid', 'invalid_header'], ['/missing', 'missing_header']]) {
    const response = await fetch(base + path, { headers })
    assert.equal(response.headers.get('server-timing'), null)
    await response.text()
    await until(() => logs.some(log => log.request_id === response.headers.get('x-request-id')))
    const record = logs.at(-1)
    assert.equal(record.database_timings.reason, reason)
    assert.equal(record.database_timings.state, reason ? 'unknown' : 'observed')
  }
  const abort = new AbortController()
  const blocked = fetch(base + '/pending', { headers, signal: abort.signal })
  const rejected = assert.rejects(blocked, error => error.name === 'AbortError')
  await pending
  abort.abort(); await rejected
  await until(() => logs.length === 4)
  assert.equal(logs.at(-1).database_timings.reason, 'no_response')
  const streamAbort = new AbortController()
  const stream = await fetch(base + '/stream', { headers, signal: streamAbort.signal })
  const reader = stream.body.getReader(); await reader.read()
  streamAbort.abort()
  await assert.rejects(reader.read(), error => error.name === 'AbortError')
  await until(() => logs.length === 5)
  assert.equal(logs.at(-1).outcome, 'aborted')
  assert.equal(logs.at(-1).timings.streaming.complete, false)
  assert.equal(logs.at(-1).database_timings.phases.transaction.complete, true)
  const recovered = await fetch(base + '/observed', { headers }); await recovered.text()
  await until(() => logs.length === 6)
  assert.equal(logs.at(-1).database_timings.state, 'observed')
  assert.doesNotMatch(JSON.stringify(logs), /private-|Bearer|postgres:/)
})
