import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createHash } from 'node:crypto'
import { createServer } from '../server.mjs'
import { runtimeConfig, limits, enginePool } from '../config.mjs'
import { createConnectionReporter } from '../connection-observations.mjs'

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
  assert.throws(() => config({ DATA_API_USER_CONCURRENCY: '3' }), /user concurrency/)
})

test('server keys keep a slot for users by default; owners can set the limit and a per-key rate', () => {
  const config = env => runtimeConfig({ ...baseEnv, ...keys, ...env })
  assert.equal(config({}).serverKeyConcurrency, 1)
  assert.equal(config({ DATA_API_POOL: '6' }).serverKeyConcurrency, 5)
  assert.equal(config({ DATA_API_POOL: '1' }).serverKeyConcurrency, 1, 'a one-connection engine has only that slot')
  assert.equal(config({ DATA_API_POOL: '6', DATA_API_SERVER_KEY_CONCURRENCY: '6' }).serverKeyConcurrency, 6)
  assert.equal(config({ DATA_API_POOL: '6', DATA_API_SERVER_KEY_CONCURRENCY: '8' }).serverKeyConcurrency, 6, 'a value above the pool acts as the pool')
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
  return { send, held, until, get running() { return running }, get peak() { return peak } }
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

test('a busy backend cannot take the slot users keep', async t => {
  const f = await fixture(t)
  const first = f.send({ authorization: `Bearer ${keyA}` })
  await f.until(() => f.running === 1)
  const second = await f.send({ authorization: `Bearer ${keyA}` })
  assert.equal(second.status, 429)
  const problem = JSON.parse(second.body)
  assert.deepEqual([problem.code, problem.reason, problem.max_concurrent, problem.active, second.headers['retry-after']], ['data_api_concurrency_limited', 'server_key', 1, 1, '1'])
  assert.equal((await f.send({ authorization: `Bearer ${keyB}` })).status, 429, 'the limit covers both key slots')
  const user = f.send({ authorization: 'Bearer alice' })
  await f.until(() => f.running === 2)
  assert.equal(f.running, 2, 'a signed-in user still gets the engine')
  while (f.held.length) f.held.shift()()
  assert.deepEqual([(await first).status, (await user).status], [200, 200])
  const after = f.send({ authorization: `Bearer ${keyA}` })
  await f.until(() => f.held.length === 1)
  f.held.shift()()
  assert.equal((await after).status, 200, 'the slot frees when the response ends')
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
