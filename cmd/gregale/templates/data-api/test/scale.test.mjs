import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createHash } from 'node:crypto'
import { EventEmitter } from 'node:events'
import { createServer } from '../server.mjs'
import { runtimeConfig, limits, enginePool } from '../config.mjs'
import { createConnectionReporter } from '../connection-observations.mjs'
import { createServerKeyConcurrency } from '../user-concurrency.mjs'

// Engine pool size and server-key limits (ADR-20261021180534).

const baseEnv = { DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' }
const keyA = 'gdsk_' + 'A'.repeat(43), keyB = 'gdsk_' + 'B'.repeat(43)
const hash = key => createHash('sha256').update(key).digest('hex')
const keys = { DATA_API_SERVER_KEY_A_SHA256: hash(keyA), DATA_API_SERVER_KEY_B_SHA256: hash(keyB) }

test('DATA_API_POOL sizes the engine from 1 to 8 and coordinated refresh keeps one connection', () => {
  const config = env => runtimeConfig({ ...baseEnv, ...env })
  assert.deepEqual([config({}).pool, config({}).engine, config({}).postgrestEnv.PGRST_DB_POOL], [2, 2, '2'], 'the default stays two')
  assert.deepEqual([config({ DATA_API_POOL: '8' }).engine, config({ DATA_API_POOL: '8' }).postgrestEnv.PGRST_DB_POOL], [8, '8'])
  assert.equal(config({ DATA_API_POOL: '1' }).engine, 1)
  for (const bad of ['0', '9', '-1', '2.5', '02', '', 'many']) assert.throws(() => config({ DATA_API_POOL: bad }), /DATA_API_POOL/, bad)
  assert.throws(() => config({ DATA_API_POOL: '1', DATA_API_COORDINATED_REFRESH: '1' }), /at least 2/)
  assert.equal(config({ DATA_API_POOL: '4', DATA_API_COORDINATED_REFRESH: '1' }).engine, 3)
  assert.equal(limits.poolMax, 8)
  assert.equal(enginePool({ coordinatedRefresh: true }), 1, 'configs built without a pool keep the old default')
  assert.equal(config({ DATA_API_POOL: '4', DATA_API_USER_CONCURRENCY: '4' }).userConcurrency, 4, 'per-user limits follow the engine')
  assert.equal(config({ DATA_API_USER_CONCURRENCY: '3' }).userConcurrency, 2, 'a value above the engine acts as the engine')
  assert.throws(() => config({ DATA_API_USER_CONCURRENCY: '9' }), /USER_CONCURRENCY/)
})

test('server keys keep a slot for users by default; owners can set the limit and a per-key rate', () => {
  const config = env => runtimeConfig({ ...baseEnv, ...keys, ...env })
  assert.equal(config({}).serverKeyConcurrency, 1)
  assert.equal(config({ DATA_API_POOL: '6' }).serverKeyConcurrency, 5)
  assert.equal(config({ DATA_API_POOL: '6', DATA_API_SERVER_KEY_CONCURRENCY: '3' }).serverKeyConcurrency, 3)
  for (const env of [{ DATA_API_POOL: '1' }, { DATA_API_COORDINATED_REFRESH: '1' }, { DATA_API_POOL: '6', DATA_API_SERVER_KEY_CONCURRENCY: '6' }, { DATA_API_POOL: '6', DATA_API_SERVER_KEY_CONCURRENCY: '8' }]) {
    assert.equal(config(env).serverKeyConcurrency, null, `${JSON.stringify(env)}: a limit at or above the engine keeps nothing back, so only admission bounds server keys`)
  }
  for (const bad of ['0', '9', 'x']) assert.throws(() => config({ DATA_API_POOL: '6', DATA_API_SERVER_KEY_CONCURRENCY: bad }), /SERVER_KEY_CONCURRENCY/)
  assert.equal(config({}).serverKeyRateLimit, null)
  assert.deepEqual({ ...config({ DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND: '5' }).serverKeyRateLimit }, { perSecond: 5, burst: limits.rateLimitDefaultBurst })
  assert.throws(() => config({ DATA_API_SERVER_KEY_RATE_LIMIT_BURST: '0' }), /rate-limit/)
})

// fixture holds every upstream request until release() so tests can see how
// many run at once.
async function fixture(t, env = {}) {
  const config = runtimeConfig({ ...baseEnv, ...keys, ...env })
  config.fingerprint = 'a'.repeat(64)
  config.functions = []
  const held = []
  let running = 0, peak = 0
  const upstream = http.createServer((req, res) => {
    req.resume()
    running++
    peak = Math.max(peak, running)
    held.push(() => { running--; res.end('[]') })
  })
  const ready = http.createServer((_req, res) => res.end('{}'))
  await Promise.all([upstream, ready].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstream.address().port, ready.address().port, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const release of held.splice(0)) release()
    for (const server of [gateway, upstream, ready]) server.closeAllConnections()
    await Promise.all([gateway, upstream, ready].map(server => new Promise(resolve => server.close(resolve))))
  })
  const open = (headers = {}) => {
    const client = http.request({ host: '127.0.0.1', port: gateway.address().port, path: '/rest/v1/notes', headers })
    client.on('error', () => {})
    client.end()
    return client
  }
  const send = (headers = {}) => new Promise((resolve, reject) => {
    const client = http.request({ host: '127.0.0.1', port: gateway.address().port, path: '/rest/v1/notes', headers }, res => {
      const chunks = []
      res.on('data', chunk => chunks.push(chunk))
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString() }))
    })
    client.on('error', reject)
    client.end()
  })
  const until = async condition => { for (let i = 0; i < 200 && !condition(); i++) await new Promise(resolve => setTimeout(resolve, 5)) }
  return { open, send, held, until, get running() { return running }, get peak() { return peak } }
}

test('admission follows the configured pool', async t => {
  const f = await fixture(t, { DATA_API_POOL: '4' })
  const pending = [1, 2, 3, 4, 5].map(i => f.send({ authorization: `Bearer user-${i}` }))
  await f.until(() => f.running === 4)
  await new Promise(resolve => setTimeout(resolve, 30))
  assert.equal(f.running, 4, 'four engine connections run four requests; the fifth waits in the queue')
  while (f.held.length) f.held.shift()()
  await f.until(() => f.held.length === 1)
  f.held.shift()()
  assert.deepEqual((await Promise.all(pending)).map(r => r.status), [200, 200, 200, 200, 200])
  assert.equal(f.peak, 4)
})

test('a busy backend waits for its slot and cannot take the one users keep', async t => {
  const f = await fixture(t)
  const first = f.send({ authorization: `Bearer ${keyA}` })
  await f.until(() => f.running === 1)
  const waiting = [keyA, keyB, keyA].map(key => f.send({ authorization: `Bearer ${key}` }))
  await new Promise(resolve => setTimeout(resolve, 30))
  assert.equal(f.running, 1, 'server-key requests past the limit wait, whichever key slot sends them')
  const user = f.send({ authorization: 'Bearer alice' })
  await f.until(() => f.running === 2)
  assert.equal(f.running, 2, 'a signed-in user still gets the engine')
  while (f.held.length) f.held.shift()()
  assert.deepEqual([(await first).status, (await user).status], [200, 200])
  for (let i = 0; i < waiting.length; i++) {
    await f.until(() => f.held.length === 1)
    await new Promise(resolve => setTimeout(resolve, 10))
    assert.equal(f.running, 1, 'waiting server-key requests run one at a time')
    f.held.shift()()
  }
  assert.deepEqual((await Promise.all(waiting)).map(r => r.status), [200, 200, 200])
})

test('server-key requests past the queue or the wait are refused with 429', async t => {
  const f = await fixture(t)
  const first = f.send({ authorization: `Bearer ${keyA}` })
  await f.until(() => f.running === 1)
  const waiting = Array.from({ length: limits.queuedRequests }, () => f.send({ authorization: `Bearer ${keyB}` }))
  await new Promise(resolve => setTimeout(resolve, 30))
  const refused = await f.send({ authorization: `Bearer ${keyA}` })
  const problem = JSON.parse(refused.body)
  assert.deepEqual([refused.status, problem.code, problem.reason, problem.max_concurrent, problem.active, problem.cooling_down, refused.headers['retry-after']],
    [429, 'data_api_concurrency_limited', 'server_key', 1, 1, 0, '1'], 'a full queue refuses at once')
  for (const timedOut of await Promise.all(waiting)) {
    assert.deepEqual([timedOut.status, JSON.parse(timedOut.body).reason], [429, 'server_key'], 'waiters are refused after admission\'s wait')
  }
  f.held.shift()()
  assert.equal((await first).status, 200)
})

test('a disconnected server-key request keeps its slot while its SQL may still run', async t => {
  const f = await fixture(t)
  const client = f.open({ authorization: `Bearer ${keyA}` })
  await f.until(() => f.running === 1)
  client.destroy()
  await new Promise(resolve => setTimeout(resolve, 30))
  const user = f.send({ authorization: 'Bearer alice' })
  await f.until(() => f.running === 2)
  assert.equal(f.running, 2, 'users keep their slot')
  const refused = await f.send({ authorization: `Bearer ${keyB}` })
  const problem = JSON.parse(refused.body)
  assert.deepEqual([refused.status, problem.reason, problem.active, problem.cooling_down], [429, 'server_key', 0, 1])
  assert.ok(Number(refused.headers['retry-after']) > limits.overloadRetrySeconds, 'retry once the cooldown ends')
  while (f.held.length) f.held.shift()()
  assert.equal((await user).status, 200)
})

test('server-key leases settle like user leases and waiters are served in order', async () => {
  let now = 0
  const limiter = createServerKeyConcurrency(1, () => now)
  const client = () => ({ req: new EventEmitter(), res: new EventEmitter() })
  const [a, b, c, d] = [client(), client(), client(), client()]
  const first = (await limiter.take(a.req, a.res)).lease
  const second = limiter.take(b.req, b.res), third = limiter.take(c.req, c.res)
  first.downstreamDone()
  first.dispatched(); first.settled()
  now = limits.userConcurrencyCooldownMs - 1
  const fourth = limiter.take(d.req, d.res)
  d.res.emit('close')
  assert.deepEqual(await fourth, {}, 'a client that leaves stops waiting')
  now++
  const nudge = client()
  const probe = limiter.take(nudge.req, nudge.res)
  nudge.res.emit('close')
  assert.deepEqual(await probe, {})
  const granted = (await second).lease
  assert.ok(granted, 'the cooldown ends and the oldest waiter gets the slot')
  granted.dispatched(); granted.completed(); granted.settled(); granted.downstreamDone()
  const last = (await third).lease
  assert.ok(last, 'a settled lease frees its slot for the next waiter')
  granted.settled(); granted.downstreamDone()
  const e = client()
  const after = limiter.take(e.req, e.res)
  e.res.emit('close')
  assert.deepEqual(await after, {}, 'a released lease cannot free its slot twice')
  last.settled(); last.downstreamDone()
  assert.ok((await limiter.take(client().req, client().res)).lease)
})

test('the per-key rate limit refuses one slot without touching the other', async t => {
  const f = await fixture(t, { DATA_API_POOL: '4', DATA_API_SERVER_KEY_RATE_LIMIT_PER_SECOND: '1', DATA_API_SERVER_KEY_RATE_LIMIT_BURST: '1' })
  const respond = async headers => {
    const response = f.send(headers)
    await f.until(() => f.held.length > 0)
    while (f.held.length) f.held.shift()()
    return response
  }
  assert.equal((await respond({ authorization: `Bearer ${keyA}` })).status, 200)
  const limited = await f.send({ authorization: `Bearer ${keyA}` })
  assert.equal(limited.status, 429)
  assert.deepEqual([JSON.parse(limited.body).code, JSON.parse(limited.body).reason], ['data_api_rate_limited', 'server_key'])
  assert.equal((await respond({ authorization: `Bearer ${keyB}` })).status, 200)
  assert.equal((await respond({ authorization: 'Bearer alice' })).status, 200, 'users are not server keys')
})

test('connection reports carry the configured engine pool', async () => {
  const policy = { api_url: 'https://control.example', binding_id: '11111111-1111-4111-8111-111111111111', database_identity: 'a'.repeat(64), database_generation: 3, credential_generation: 2 }
  const config = runtimeConfig({ ...baseEnv, DATA_API_POOL: '6', DATA_API_CONNECTION_OBSERVATION: JSON.stringify(policy), FAAS_WORKLOAD_IDENTITY_ENDPOINT: 'http://127.0.0.1:2773/oidc/token' })
  let sent
  await createConnectionReporter(config, async () => ({ state: 'observed', capacity: 6 }), async (url, options) => {
    if (url === config.connectionObservation.identity) return Response.json({ token_type: 'Bearer', access_token: 'token' })
    sent = JSON.parse(options.body)
    return new Response(null, { status: 204 })
  })()
  assert.deepEqual([sent.state, sent.engine_connections, sent.refresh_connections], ['observed', 6, 0])
})
