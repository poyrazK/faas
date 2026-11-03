import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { runtimeConfig, changeEventsConfig, limits } from '../config.mjs'
import { createServer } from '../server.mjs'
import { createAdmission } from '../admission.mjs'
import { changeEvent, pack, settlement, unsafeChangeEvents, createChangeRelay } from '../changes.mjs'

// Change hooks relay (ADR-20261022121642).

const baseEnv = { DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }
const hooksEnv = { DATA_API_CHANGE_EVENTS_URL: 'https://api.gregale.test/', FAAS_WORKLOAD_IDENTITY_ENDPOINT: 'http://127.0.0.1:9/oidc/token' }
const claimsOf = authorization => JSON.parse(Buffer.from(authorization.split(' ')[1].split('.')[1], 'base64url').toString())
const row = (id, extra = {}) => ({ id, event_key: `00000000-0000-4000-8000-${String(id).padStart(12, '0')}`, relation_schema: 'api', relation_name: 'orders', op: 'insert',
  row_key: { id }, old_row: null, new_row: { id, status: 'new' }, changed: null, truncated: false, txid: 900 + id, captured_at: '2026-10-22T12:00:00.000001+00:00', attempts: 1, ...extra })

test('change events turn on with an HTTPS platform origin and the local workload identity', () => {
  assert.equal(changeEventsConfig(baseEnv, ['api']), null)
  const config = changeEventsConfig(hooksEnv, ['catalog', 'api'])
  assert.equal(config.schema, 'catalog', 'the first exposed schema holds the change event table')
  assert.equal(config.endpoint, 'https://api.gregale.test/v1/runtime/data-api/events')
  assert.equal(new URL(config.identity).searchParams.get('audience'), 'gregale:data-api:events')
  for (const bad of [{ DATA_API_CHANGE_EVENTS_URL: 'http://api.gregale.test/' }, { DATA_API_CHANGE_EVENTS_URL: 'https://api.gregale.test/v1' }, { DATA_API_CHANGE_EVENTS_URL: 'https://u:p@api.gregale.test/' },
    { FAAS_WORKLOAD_IDENTITY_ENDPOINT: 'http://identity.example/oidc/token' }, { FAAS_WORKLOAD_IDENTITY_ENDPOINT: undefined }]) {
    assert.throws(() => changeEventsConfig({ ...hooksEnv, ...bad }, ['api']), /change events configuration/, JSON.stringify(bad))
  }
  assert.equal(runtimeConfig({ ...baseEnv, ...hooksEnv }).changeEvents.schema, 'api')
})

test('a recorded change becomes a deterministic event', () => {
  const updated = changeEvent(row(3, { op: 'update', old_row: { id: 3, status: 'new' }, new_row: { id: 3, status: 'paid' }, changed: ['status'] }))
  assert.deepEqual(updated, { id: '00000000-0000-4000-8000-000000000003', type: 'api.orders.updated', time: '2026-10-22T12:00:00.000001+00:00',
    data: { schema: 'api', table: 'orders', op: 'update', key: { id: 3 }, new: { id: 3, status: 'paid' }, old: { id: 3, status: 'new' }, changed: ['status'], sequence: 3, txid: 903 } })
  assert.equal(changeEvent(row(4, { op: 'delete', old_row: { id: 4 }, new_row: null })).type, 'api.orders.deleted')
  assert.deepEqual(changeEvent(row(5, { new_row: null, truncated: true })).data.truncated, true, 'the database dropped an oversized row')
  const large = changeEvent(row(6, { row_key: { id: 'k'.repeat(limits.changeEventDataBytes) } }))
  assert.equal(large.data.truncated, true)
  assert.equal(large.data.new, null)
  assert.deepEqual(changeEvent(row(7)), changeEvent(row(7)), 'a republished change is identical')
})

test('publications stay within the event and byte bounds', () => {
  const small = Array.from({ length: limits.changeEventsBatch + 5 }, (_, i) => changeEvent(row(i + 1)))
  assert.equal(pack(small).length, limits.changeEventsBatch)
  const big = Array.from({ length: 30 }, (_, i) => changeEvent(row(i + 1, { new_row: { body: 'x'.repeat(60000) } })))
  const taken = pack(big)
  assert.ok(taken.length > 1 && taken.length < 30)
  assert.ok(Buffer.byteLength(JSON.stringify({ version: 1, events: taken })) <= limits.changeEventsBatchBytes)
  assert.deepEqual(taken, big.slice(0, taken.length), 'events keep their order')
})

test('settlement stops at the first event that may succeed later', () => {
  const rows = [row(1), row(2), row(3), row(4)]
  assert.deepEqual(settlement(rows, [{ index: 0, status: 'accepted' }, { index: 1, status: 'duplicate' }, { index: 2, status: 'rejected', retryable: false, problem: { code: 'event_schema_invalid' } }, { index: 3, status: 'rejected', retryable: false, problem: { code: 'Not A Code' } }]),
    { published: [1, 2], failed: [{ id: 3, code: 'event_schema_invalid' }, { id: 4, code: 'rejected' }], retry: false })
  assert.deepEqual(settlement(rows, [{ index: 0, status: 'accepted' }, { index: 1, status: 'unknown', retryable: true }, { index: 2, status: 'accepted' }]), { published: [1], failed: [], retry: true })
  assert.deepEqual(settlement(rows, [{ index: 0, status: 'rejected', retryable: true }]), { published: [], failed: [], retry: true })
  assert.deepEqual(settlement(rows, [{ index: 1, status: 'accepted' }]), { published: [], failed: [], retry: true }, 'results must line up with events')
  assert.deepEqual(settlement(rows, null), { published: [], failed: [], retry: true })
})

test('the catalog check names what a migration still has to add', () => {
  assert.match(unsafeChangeEvents(undefined), /missing/)
  assert.match(unsafeChangeEvents({ kind: 'v', rls: true, settles: true, functions: 2 }), /not a table/)
  assert.match(unsafeChangeEvents({ kind: 'r', rls: false, settles: true, functions: 2 }), /row-level security/)
  assert.match(unsafeChangeEvents({ kind: 'r', rls: true, settles: false, functions: 2 }), /cannot settle/)
  assert.match(unsafeChangeEvents({ kind: 'r', rls: true, settles: true, functions: 1 }), /gregale_change_claim/)
  assert.equal(unsafeChangeEvents({ kind: 'r', rls: true, settles: true, functions: 2 }), null)
})

test('background work borrows only a free admission slot', () => {
  const admitted = createAdmission(2, () => {})
  const a = admitted.tryAcquire(), b = admitted.tryAcquire()
  assert.ok(a && b)
  assert.equal(admitted.tryAcquire(), null)
  assert.equal(admitted.snapshot().active, 2)
  a(); a()
  assert.equal(admitted.snapshot().active, 1, 'release is idempotent')
  b()
  assert.equal(admitted.snapshot().active, 0)
})

// harness runs a relay against a fake PostgREST and platform. step() fires
// the pending timer and resolves with the timer the cycle schedules next.
async function harness(t, { claims = [], publish = () => ({ status: 200, body: { results: [] } }), acquire = true, env = {} } = {}) {
  const rpc = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => {
      const call = { path: req.url, headers: req.headers, claims: claimsOf(req.headers.authorization), body: JSON.parse(body) }
      rpc.push(call)
      if (req.url === '/rpc/gregale_change_claim') return res.end(JSON.stringify(claims.shift() ?? []))
      res.statusCode = 204
      res.end()
    })
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  t.after(() => { upstream.closeAllConnections(); upstream.close() })
  const config = runtimeConfig({ ...baseEnv, ...hooksEnv, ...env })
  config.fingerprint = 'f'.repeat(64)
  const fetches = []
  const fetchImpl = async (url, options = {}) => {
    fetches.push({ url: String(url), options })
    if (String(url).startsWith('http://127.0.0.1:9/oidc/token')) return new Response(JSON.stringify({ token_type: 'Bearer', access_token: 'workload.token.sig' }))
    const answer = publish(JSON.parse(options.body))
    if (answer instanceof Error) throw answer
    return new Response(JSON.stringify(answer.body ?? {}), { status: answer.status })
  }
  let wall = Date.now(), mono = 1000, waiter = null
  const timers = []
  const relay = createChangeRelay(config, { upstreamPort: upstream.address().port, tryAcquire: () => acquire ? () => {} : null, fetchImpl, now: () => wall, monotonic: () => mono,
    setTimer: (work, delay) => { const timer = { work, delay }; timers.push(timer); waiter?.(timer); return timer }, clearTimer: () => {} })
  t.after(() => relay.stop())
  const step = () => {
    const next = new Promise(resolve => { waiter = resolve })
    timers.at(-1).work()
    return next
  }
  return { relay, rpc, fetches, timers, step, jump: ms => { wall += ms } }
}

test('the relay claims, publishes in order and settles', async t => {
  const { rpc, fetches, timers, step } = await harness(t, {
    claims: [[row(2), row(1)]],
    publish: body => ({ status: 200, body: { source: 'app.x', results: body.events.map((_, index) => ({ index, status: index ? 'duplicate' : 'accepted' })) } }),
  })
  assert.equal(timers[0].delay, 0, 'the relay starts at once')
  const next = await step()
  assert.equal(next.delay, 0, 'a published batch may have more behind it')
  const [claim, settle] = rpc
  assert.equal(claim.path, '/rpc/gregale_change_claim')
  assert.equal(claim.headers['content-profile'], 'api')
  assert.deepEqual({ access: claim.claims.gregale_access, role: claim.claims.role }, { access: 'hooks', role: 'restricted' })
  assert.match(claim.body.relay, /^relay\.[0-9a-f-]{36}$/)
  assert.deepEqual([claim.body.max_events, claim.body.lease_ms], [limits.changeEventsBatch, limits.changeEventsLeaseMs])
  assert.equal(new URL(fetches[0].url).searchParams.get('audience'), 'gregale:data-api:events')
  assert.equal(fetches[1].url, 'https://api.gregale.test/v1/runtime/data-api/events')
  assert.equal(fetches[1].options.headers.Authorization, 'Bearer workload.token.sig')
  const sent = JSON.parse(fetches[1].options.body)
  assert.equal(sent.version, 1)
  assert.deepEqual(sent.events.map(e => [e.id, e.type, e.data.sequence]), [['00000000-0000-4000-8000-000000000001', 'api.orders.inserted', 1], ['00000000-0000-4000-8000-000000000002', 'api.orders.inserted', 2]])
  assert.deepEqual(settle.body, { relay: claim.body.relay, published: [1, 2], failed: [], retry_after_ms: 0 })
  assert.equal(settle.claims.gregale_access, 'hooks')
})

test('the relay carries the SQL deadline and refresh claims the engine expects', async t => {
  const { rpc, step } = await harness(t, { env: { DATA_API_SQL_DEADLINE: '1', DATA_API_COORDINATED_REFRESH: '1' } })
  await step()
  assert.equal(rpc[0].claims.gregale_sql_deadline.version, 1)
  assert.equal(rpc[0].claims.gregale_sql_deadline.coordinated_refresh, true)
  assert.equal(rpc[0].claims.gregale_serving_fingerprint, 'f'.repeat(64))
})

test('a retryable failure holds the rest back with growing backoff', async t => {
  const { rpc, step } = await harness(t, {
    claims: [[row(1), row(2), row(3)], [row(2), row(3)]],
    publish: body => ({ status: 200, body: { results: body.events.map((_, index) => index === 0 && body.events[0].data.sequence === 1 ? { index, status: 'accepted' } : { index, status: 'not_attempted', retryable: true }) } }),
  })
  assert.equal((await step()).delay, 1000)
  assert.deepEqual(rpc[1].body, { relay: rpc[0].body.relay, published: [1], failed: [], retry_after_ms: 1000 })
  assert.equal((await step()).delay, 2000)
  assert.deepEqual(rpc[3].body.retry_after_ms, 2000)
})

test('rejections become failed events; unreachable platforms keep events waiting', async t => {
  const rejected = await harness(t, { claims: [[row(1), row(2, { event_key: 'not-a-uuid' })]],
    publish: () => ({ status: 200, body: { results: [{ index: 0, status: 'rejected', retryable: false, problem: { code: 'event_schema_invalid' } }] } }) })
  await rejected.step()
  assert.deepEqual(rejected.rpc[1].body.failed, [{ id: 1, code: 'event_schema_invalid' }, { id: 2, code: 'invalid_change_event' }])
  const refused = await harness(t, { claims: [[row(1)]], publish: () => ({ status: 413 }) })
  await refused.step()
  assert.deepEqual(refused.rpc[1].body.failed, [{ id: 1, code: 'publication_rejected_413' }])
  const down = await harness(t, { claims: [[row(1)]], publish: () => new Error('connection refused') })
  assert.equal((await down.step()).delay, 1000)
  assert.deepEqual(down.rpc[1].body, { relay: down.rpc[0].body.relay, published: [], failed: [], retry_after_ms: 1000 })
  const unavailable = await harness(t, { claims: [[row(1)]], publish: () => ({ status: 503 }) })
  await unavailable.step()
  assert.deepEqual(unavailable.rpc[1].body.published, [])
})

test('an idle relay backs off, never queues for the engine and changes identity after a restore', async t => {
  const idle = await harness(t)
  const delays = []
  for (let i = 0; i < 7; i++) delays.push((await idle.step()).delay)
  assert.deepEqual(delays, [1000, 2000, 4000, 8000, 16000, 30000, 30000])
  idle.relay.kick()
  assert.equal(idle.timers.at(-1).delay, 0, 'a write wakes the relay at once')
  const relayBefore = idle.rpc.at(-1).body.relay
  idle.jump(60000)
  await idle.step()
  assert.notEqual(idle.rpc.at(-1).body.relay, relayBefore)
  const busy = await harness(t, { acquire: false })
  assert.equal((await busy.step()).delay, limits.changeEventsIdleMinMs)
  assert.equal(busy.rpc.length, 0)
})

test('the gateway hides the change event table and wakes the relay after writes', async t => {
  const claims = []
  const upstream = http.createServer((req, res) => {
    if (req.url.startsWith('/rpc/gregale_change_claim')) claims.push(Date.now())
    res.statusCode = req.method === 'POST' && req.url.startsWith('/notes') ? 201 : 200
    res.end(req.url.startsWith('/rpc/') ? '[]' : '')
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const config = runtimeConfig({ ...baseEnv, ...hooksEnv, DATA_API_ALLOWED_ORIGINS: 'https://app.example' })
  const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), upstream.address().port, undefined, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const request = (path, method = 'GET') => new Promise((resolve, reject) => {
    const req = http.request({ host: '127.0.0.1', port: gateway.address().port, path, method, headers: { Authorization: 'Bearer valid', 'Content-Type': 'application/json' } }, res => {
      let body = ''
      res.on('data', chunk => { body += chunk })
      res.on('end', () => resolve({ status: res.statusCode, body }))
    })
    req.on('error', reject)
    req.end(method === 'POST' ? '{}' : undefined)
  })
  for (const path of ['/rest/v1/gregale_change_events', '/rest/v1/gregale_change_events?select=*', '/rest/v1/rpc/gregale_change_claim']) {
    const answer = await request(path)
    assert.equal(answer.status, 404, path)
  }
  // The relay starts at once, then idles for a second; a write wakes it.
  for (let i = 0; i < 50 && !claims.length; i++) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(claims.length, 1)
  const write = await request('/rest/v1/notes', 'POST')
  assert.equal(write.status, 201)
  for (let i = 0; i < 50 && claims.length < 2; i++) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(claims.length, 2, 'the write woke the relay before its idle poll')
  assert.ok(claims[1] - claims[0] < limits.changeEventsIdleMinMs)
})
