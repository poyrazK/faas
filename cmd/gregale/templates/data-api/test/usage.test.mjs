import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { EventEmitter } from 'node:events'
import { runtimeConfig, usageConfig, monthlyCaps, limits } from '../config.mjs'
import { createUsage, rowsFromContentRange, accessReport } from '../usage.mjs'
import { createServer } from '../server.mjs'

const identityEndpoint = 'http://127.0.0.1:2773/oidc/token'
const env = { DATA_API_USAGE_URL: 'https://api.gregale.example/', FAAS_WORKLOAD_IDENTITY_ENDPOINT: identityEndpoint }
const baseEnv = { DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }
const token = instance => `h.${Buffer.from(JSON.stringify({ instance_id: instance })).toString('base64url')}.s`

test('usage reporting needs an HTTPS platform URL and the loopback identity endpoint', () => {
  assert.equal(usageConfig({}), null)
  const config = usageConfig(env)
  assert.equal(config.endpoint, 'https://api.gregale.example/v1/runtime/data-api/usage')
  assert.equal(new URL(config.identity).searchParams.get('audience'), 'gregale:data-api:usage')
  assert.equal(config.caps, null)
  for (const bad of [{ ...env, DATA_API_USAGE_URL: 'http://api.example/' }, { ...env, DATA_API_USAGE_URL: 'https://u:p@api.example/' }, { ...env, DATA_API_USAGE_URL: 'https://api.example/v1' },
    { ...env, FAAS_WORKLOAD_IDENTITY_ENDPOINT: 'http://10.0.0.1/oidc/token' }, { DATA_API_USAGE_URL: env.DATA_API_USAGE_URL }]) {
    assert.throws(() => usageConfig(bad), /usage reporting/, JSON.stringify(bad))
  }
  assert.throws(() => usageConfig({ DATA_API_MONTHLY_CAPS: '{"version":1,"requests":5}' }), /requires DATA_API_USAGE_URL/)
  assert.equal(runtimeConfig({ ...baseEnv, ...env }).usage.endpoint, config.endpoint)
})

test('monthly caps are versioned, positive and bounded', () => {
  assert.deepEqual(monthlyCaps('{"version":1,"requests":1000}'), { requests: 1000, anonymous_requests: null })
  assert.deepEqual(monthlyCaps('{"version":1,"anonymous_requests":50}'), { requests: null, anonymous_requests: 50 })
  for (const bad of ['{}', '{"version":1}', '{"version":2,"requests":1}', '{"version":1,"requests":0}', '{"version":1,"requests":1.5}', `{"version":1,"requests":${limits.usageMonthlyCapMax + 1}}`, '{"version":1,"rows":5}', '[]', 'x']) {
    assert.throws(() => monthlyCaps(bad), /DATA_API_MONTHLY_CAPS/, bad)
  }
})

test('rows come from the engine Content-Range', () => {
  assert.equal(rowsFromContentRange('0-24/*'), 25)
  assert.equal(rowsFromContentRange('10-10/100'), 1)
  for (const value of [undefined, '*/0', '*/*', 'items 0-1/2', '5-1/*']) assert.equal(rowsFromContentRange(value), 0, String(value))
})

function harness({ caps = null, start = Date.UTC(2026, 9, 19, 10, 30), answer = { requests: 0, anonymous_requests: 0 }, access = null } = {}) {
  const clock = { wall: start, mono: 1000 }
  const calls = []
  let instance = 'instance-a', status = 200
  const fetchImpl = async (url, init = {}) => {
    if (url.startsWith(identityEndpoint)) return new Response(JSON.stringify({ token_type: 'Bearer', access_token: token(instance) }))
    calls.push(JSON.parse(init.body))
    if (status !== 200) return new Response('{}', { status })
    return new Response(JSON.stringify({ month_to_date: { month: new Date(clock.wall).toISOString().slice(0, 7), ...answer } }))
  }
  const usage = createUsage({ ...usageConfig(env), caps }, { now: () => clock.wall, monotonic: () => clock.mono, fetchImpl, nonce: 'n1', access })
  const request = (fields, rows = 0, bytes = 0) => {
    const res = Object.assign(new EventEmitter(), { headersSent: true, statusCode: fields.status ?? 200, getHeader: () => (rows ? `0-${rows - 1}/*` : undefined) })
    const entry = usage.start(res)
    Object.assign(entry, { access: fields.access ?? null, capped: fields.capped ?? false })
    entry.bytes += bytes
    res.emit('close')
  }
  return { usage, clock, calls, request, setInstance: v => { instance = v }, setStatus: v => { status = v }, answer }
}

test('requests are counted per hour and reported as deltas with stable ids', async () => {
  const h = harness()
  h.request({ access: 'user' }, 3, 120)
  h.request({ access: 'anonymous' }, 1, 40)
  h.request({ access: 'server', status: 503 })
  h.request({ access: null, status: 401 })
  h.setStatus(503)
  await h.usage.report()
  assert.equal(h.calls.length, 1)
  const first = h.calls[0]
  assert.equal(first.report_id, 'instance-a.n1:1')
  assert.deepEqual(first.hours, [{ hour: '2026-10-19T10:00:00.000Z', user_requests: 1, anonymous_requests: 1, server_requests: 1, rejected_requests: 1, error_requests: 1, capped_requests: 0, rows_returned: 4, response_bytes: 160 }])
  h.request({ access: 'user' })
  h.setStatus(200)
  await h.usage.report()
  assert.deepEqual(h.calls[1], first, 'a failed report is retried unchanged, so the platform applies it once')
  await h.usage.report()
  assert.equal(h.calls[2].report_id, 'instance-a.n1:2')
  assert.equal(h.calls[2].hours[0].user_requests, 1, 'counts after the first report go in the next one')
  h.usage.stop()
})

test('a snapshot restore drops pending counts but keeps the in-flight report id', async () => {
  const h = harness()
  h.request({ access: 'user' })
  h.setStatus(503)
  await h.usage.report()
  h.request({ access: 'anonymous' })
  // Restored later as another instance: the wall clock jumped, the monotonic clock did not.
  h.clock.wall += 10 * 60 * 1000
  h.setInstance('instance-b')
  h.setStatus(200)
  await h.usage.report()
  assert.equal(h.calls[1].report_id, 'instance-a.n1:1', 'the in-flight report keeps its id')
  assert.equal(h.calls[1].hours[0].anonymous_requests, 0, 'counts pending at the snapshot are not reported')
  h.request({ access: 'server' })
  await h.usage.report()
  assert.equal(h.calls[2].report_id, 'instance-b.n1:2', 'new reports name the restored instance')
  assert.equal(h.calls[2].hours[0].hour, '2026-10-19T10:00:00.000Z')
  assert.equal(h.calls[2].hours[0].server_requests, 1)
  h.usage.stop()
})

test('caps compare month-to-date totals plus unreported counts', async () => {
  const h = harness({ caps: { requests: 10, anonymous_requests: 4 }, answer: { requests: 8, anonymous_requests: 3 } })
  await h.usage.report()
  assert.equal(h.calls[0].hours.length, 0, 'an empty report learns the totals')
  assert.equal(h.usage.capReached('user'), null)
  assert.equal(h.usage.capReached('anonymous'), null)
  h.request({ access: 'anonymous' })
  const anonymous = h.usage.capReached('anonymous')
  assert.deepEqual({ ...anonymous, retryAfter: undefined }, { name: 'anonymous_requests', limit: 4, observed: 4, retryAfter: undefined })
  assert.equal(anonymous.retryAfter, Math.ceil((Date.UTC(2026, 10, 1) - h.clock.wall) / 1000), 'retry when the next UTC month starts')
  assert.equal(h.usage.capReached('user'), null, 'signed-in users keep their access')
  h.request({ access: 'user' })
  assert.equal(h.usage.capReached('server').name, 'requests')
  h.usage.stop()
})

test('a new month starts the caps afresh', async () => {
  const h = harness({ caps: { requests: 5, anonymous_requests: null }, start: Date.UTC(2026, 9, 31, 23, 59), answer: { requests: 9, anonymous_requests: 0 } })
  await h.usage.report()
  assert.equal(h.usage.capReached('user').name, 'requests')
  h.clock.wall = Date.UTC(2026, 10, 1, 0, 1)
  h.clock.mono += 2 * 60 * 1000
  assert.equal(h.usage.capReached('user'), null, 'last month\'s totals do not apply')
  h.usage.stop()
})

async function gateway(t, extraEnv) {
  const config = runtimeConfig({ ...baseEnv, ...env, DATA_API_PUBLIC_READ: 'api.products', ...extraEnv })
  config.functions = []
  const upstream = http.createServer((req, res) => { req.resume(); res.writeHead(200, { 'Content-Range': '0-1/*', 'Content-Type': 'application/json' }); res.end('[{},{}]') })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gw = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), upstream.address().port, undefined, () => {})
  await new Promise(resolve => gw.listen(0, '127.0.0.1', resolve))
  t.after(() => { gw.closeAllConnections(); gw.close(); upstream.closeAllConnections(); upstream.close() })
  return `http://127.0.0.1:${gw.address().port}`
}

test('a reached cap answers 429 before admission with the limit, observed value and next-month Retry-After', async t => {
  const base = await gateway(t, { DATA_API_MONTHLY_CAPS: '{"version":1,"anonymous_requests":2}' })
  const anonymous = () => fetch(`${base}/rest/v1/products`, { headers: { 'x-faas-client-ip': '203.0.113.5' } })
  assert.equal((await anonymous()).status, 200)
  assert.equal((await anonymous()).status, 200)
  const refused = await anonymous()
  assert.equal(refused.status, 429)
  const body = await refused.json()
  assert.equal(body.code, 'data_api_monthly_cap_reached')
  assert.equal(body.cap, 'anonymous_requests')
  assert.equal(body.limit, 2)
  assert.equal(body.observed, 2)
  assert.match(body.docs, /usage-and-monthly-caps/)
  assert.ok(Number(refused.headers.get('retry-after')) >= 1)
  assert.equal((await fetch(`${base}/rest/v1/products`, { headers: { Authorization: 'Bearer valid' } })).status, 200, 'signed-in users are not anonymous traffic')
  assert.equal((await fetch(`${base}/healthz`)).status !== 429, true, 'health checks are never capped')
})

test('access settings report schemas, the token issuer, the allowlist and key hashes, never keys', () => {
  const hash = 'ab'.repeat(32)
  const config = runtimeConfig({ ...baseEnv, ...env, DATA_API_SCHEMAS: 'api,catalog', DATA_API_PUBLIC_READ: 'api.products',
    DATA_API_SERVER_KEY_A_SHA256: hash, DATA_API_SERVER_KEY_B_SHA256: 'cd'.repeat(32),
    DATA_API_SERVER_KEY_B_PERMISSIONS: '{"version":1,"access":"read","relations":["api.orders"],"subjects":false}' })
  const auth = { issuer: 'https://issuer.example', jwks_url: 'https://issuer.example/jwks', audience: 'notes' }
  assert.deepEqual(accessReport(config), { version: 1, schemas: ['api', 'catalog'], auth, public_read: ['api.products'], server_keys: [
    { slot: 'a', sha256: hash },
    { slot: 'b', sha256: 'cd'.repeat(32), permissions: { access: 'read', relations: ['api.orders'], subjects: false } },
  ] })
  assert.deepEqual(accessReport(runtimeConfig({ ...baseEnv, ...env })), { version: 1, schemas: ['api'], auth })
})

test('access settings go out after start, after a restore and hourly', async () => {
  const access = { version: 1, schemas: ['api'], public_read: ['api.products'] }
  const h = harness({ access })
  await h.usage.report()
  assert.deepEqual(h.calls[0].access, access, 'the first report carries the settings')
  assert.deepEqual(h.calls[0].hours, [])
  h.request({ access: 'user' })
  await h.usage.report()
  assert.equal(h.calls[1].access, undefined, 'not again within the hour')
  await h.usage.report()
  assert.equal(h.calls.length, 2, 'nothing pending and nothing due sends nothing')
  h.clock.wall += 61 * 60 * 1000
  h.clock.mono += 61 * 60 * 1000
  await h.usage.report()
  assert.deepEqual(h.calls[2].access, access, 'hourly')
  h.clock.wall += 30 * 60 * 1000 // restored: the wall clock jumped, the monotonic clock did not
  await h.usage.report()
  assert.deepEqual(h.calls[3].access, access, 'after a restore')
  h.usage.stop()
})
