import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createHash } from 'node:crypto'
import { createServer } from '../server.mjs'
import { runtimeConfig, publicReadConfig, serverKeyConfig, limits } from '../config.mjs'
import { selectEmbeddings, clientKey, matchServerKey, serverSubject, unsafePublicRelations, publicReadTarget } from '../access.mjs'

const keyA = 'gdsk_' + 'A'.repeat(43)
const keyB = 'gdsk_' + 'b'.repeat(42) + '-'
const hash = value => createHash('sha256').update(value).digest('hex')
const baseEnv = { DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_ALLOWED_ORIGINS: 'https://app.example' }

test('public read relations are schema-qualified, exposed, unique and bounded', () => {
  assert.deepEqual(publicReadConfig('api.products,api.Categories', ['api']), [{ schema: 'api', relation: 'products' }, { schema: 'api', relation: 'Categories' }])
  assert.equal(publicReadConfig(undefined, ['api']), null)
  for (const value of ['', 'products', 'private.products', 'api.products,api.products', 'api.a.b', 'api.bad-name', 'API.products',
    Array.from({ length: limits.publicReadMaxRelations + 1 }, (_, i) => `api.t${i}`).join(',')]) {
    assert.throws(() => publicReadConfig(value, ['api']), /DATA_API_PUBLIC_READ/, value)
  }
})

test('server keys are stored as two distinct SHA-256 slots and matched by exact shape', () => {
  const slots = serverKeyConfig({ DATA_API_SERVER_KEY_A_SHA256: hash(keyA), DATA_API_SERVER_KEY_B_SHA256: hash(keyB) })
  assert.equal(matchServerKey(slots, keyA), 'a')
  assert.equal(matchServerKey(slots, keyB), 'b')
  assert.equal(matchServerKey(slots, 'gdsk_' + 'C'.repeat(43)), null)
  assert.equal(matchServerKey(slots, keyA + 'x'), null)
  assert.equal(matchServerKey(null, keyA), null)
  assert.equal(serverKeyConfig({}), null)
  assert.throws(() => serverKeyConfig({ DATA_API_SERVER_KEY_A_SHA256: 'ABC' }), /DATA_API_SERVER_KEY_A_SHA256/)
  assert.throws(() => serverKeyConfig({ DATA_API_SERVER_KEY_A_SHA256: hash(keyA), DATA_API_SERVER_KEY_B_SHA256: hash(keyA) }), /differ/)
  assert.deepEqual(serverSubject(undefined), { subject: null })
  assert.deepEqual(serverSubject('user-1'), { subject: 'user-1' })
  for (const value of ['', 'a\nb', 'x'.repeat(limits.serverKeySubjectMaxBytes + 1)]) assert.equal(serverSubject(value).invalid, true)
})

test('select embedding detection lists relations and rejects shapes it cannot classify', () => {
  assert.deepEqual(selectEmbeddings('*'), [])
  assert.deepEqual(selectEmbeddings('id,name,price::text'), [])
  assert.deepEqual(selectEmbeddings('id,categories(name)'), ['categories'])
  assert.deepEqual(selectEmbeddings('c:categories!products_category_fkey(name),...brands!inner(name)'), ['categories', 'brands'])
  assert.deepEqual(selectEmbeddings('total:amount.sum(),count(),avg:price.avg()::int'), [])
  assert.deepEqual(selectEmbeddings('orders(amount.sum(),items(sku))'), ['orders', 'items'])
  // PostgREST reads count(id) as an embedded relation named count.
  assert.deepEqual(selectEmbeddings('count(id)'), ['count'])
  for (const value of ['"quoted"(x)', 'a(b', 'a)b(', 'amount.median()', 'bad-name(x)', 'a!bad-hint(x)', 'x'.repeat(4097)]) {
    assert.equal(selectEmbeddings(value), null, value)
  }
})

test('client keys come from the stamped edge address and group IPv6 by /64', () => {
  const key = value => clientKey({ headers: value === undefined ? {} : { 'x-faas-client-ip': value } })
  assert.equal(key('203.0.113.7'), '203.0.113.7')
  assert.equal(key('::ffff:203.0.113.7'), '203.0.113.7')
  assert.equal(key('2001:db8:1:2:aaaa::1'), '2001:db8:1:2::/64')
  assert.equal(key('2001:db8:1:2:ffff:ffff:ffff:ffff'), '2001:db8:1:2::/64')
  assert.equal(key('2001:db8::1'), '2001:db8:0:0::/64')
  assert.equal(key(undefined), 'unknown')
  assert.equal(key('not-an-address'), 'unknown')
})

test('public reads require RLS tables or security-invoker views', () => {
  assert.deepEqual(unsafePublicRelations([
    { schema: 'api', relation: 'products', kind: 'r', rls: true, invoker: '' },
    { schema: 'api', relation: 'partitioned', kind: 'p', rls: true, invoker: '' },
    { schema: 'api', relation: 'catalog', kind: 'v', rls: false, invoker: 'on' },
    { schema: 'api', relation: 'open_table', kind: 'r', rls: false, invoker: '' },
    { schema: 'api', relation: 'owner_view', kind: 'v', rls: false, invoker: '' },
    { schema: 'api', relation: 'totals', kind: 'm', rls: false, invoker: '' },
    { schema: 'api', relation: 'missing', kind: null, rls: null, invoker: '' },
  ]), ['api.open_table', 'api.owner_view', 'api.totals', 'api.missing'])
})

test('aggregates are opt-in and require the SQL deadline', () => {
  assert.equal(runtimeConfig(baseEnv).postgrestEnv.PGRST_DB_AGGREGATES_ENABLED, undefined)
  assert.throws(() => runtimeConfig({ ...baseEnv, DATA_API_AGGREGATES: '1' }), /DATA_API_SQL_DEADLINE/)
  assert.throws(() => runtimeConfig({ ...baseEnv, DATA_API_AGGREGATES: 'true', DATA_API_SQL_DEADLINE: '1' }), /aggregates/)
  const config = runtimeConfig({ ...baseEnv, DATA_API_AGGREGATES: '1', DATA_API_SQL_DEADLINE: '1' })
  assert.equal(config.aggregates, true)
  assert.equal(config.postgrestEnv.PGRST_DB_AGGREGATES_ENABLED, 'true')
})

test('only listed GET/HEAD relation reads can be anonymous', () => {
  const config = runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_READ: 'api.products,api.categories' })
  const target = (url, method = 'GET', headers = {}) => publicReadTarget({ url, method, headers }, config)
  assert.deepEqual(target('/rest/v1/products?select=*,categories(name)'), { schema: 'api', relation: 'products' })
  assert.deepEqual(target('/rest/v1/products', 'HEAD'), { schema: 'api', relation: 'products' })
  assert.equal(target('/rest/v1/notes').code, 'token_required')
  assert.equal(target('/rest/v1/products', 'POST').code, 'token_required')
  assert.equal(target('/rest/v1/rpc/save_note', 'POST').code, 'token_required')
  assert.equal(target('/openapi.json').code, 'token_required')
  assert.equal(target('/rest/v1/products', 'GET', { 'accept-profile': 'private' }).code, 'token_required')
  assert.equal(target('/rest/v1/products?select=*,notes(*)').code, 'public_read_not_allowed')
  assert.equal(target('/rest/v1/products?select=id&select=notes(*)').code, 'public_read_not_allowed')
  assert.equal(publicReadTarget({ url: '/rest/v1/products', method: 'GET', headers: {} }, runtimeConfig(baseEnv)).code, 'token_required')
})

const claimsOf = authorization => JSON.parse(Buffer.from(authorization.split(' ')[1].split('.')[1], 'base64url').toString())

async function fixture(t, env = {}, verify = async () => ({ sub: 'private-subject', exp: 9999999999, gregale_access: 'server', gregale_server_key: 'a' })) {
  const config = runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_READ: 'api.products,api.categories', DATA_API_SERVER_KEY_A_SHA256: hash(keyA), DATA_API_SERVER_KEY_B_SHA256: hash(keyB), ...env })
  config.fingerprint = 'a'.repeat(64)
  config.functions = [{ schema: 'api', name: 'save_note' }]
  const state = { calls: [], logs: [] }
  const upstream = http.createServer((req, res) => {
    req.resume()
    state.calls.push({ path: req.url, method: req.method, claims: claimsOf(req.headers.authorization) })
    res.end('[]')
  })
  const ready = http.createServer((_req, res) => res.end('{}'))
  await Promise.all([upstream, ready].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const gateway = createServer(config, verify, upstream.address().port, ready.address().port, record => state.logs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [gateway, upstream, ready]) server.closeAllConnections()
    await Promise.all([gateway, upstream, ready].map(server => new Promise(resolve => server.close(resolve))))
  })
  const send = (path, { method = 'GET', headers = {} } = {}) => new Promise((resolve, reject) => {
    const client = http.request({ host: '127.0.0.1', port: gateway.address().port, path, method, headers }, res => {
      const chunks = []
      res.on('data', chunk => chunks.push(chunk))
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString() }))
    })
    client.on('error', reject)
    client.end()
  })
  return { state, send }
}

test('anonymous reads sign subject-free claims and never reach unlisted relations', async t => {
  const { state, send } = await fixture(t)
  const allowed = await send('/rest/v1/products?select=id,categories(name)', { headers: { 'x-faas-client-ip': '203.0.113.7' } })
  assert.equal(allowed.status, 200)
  assert.equal(state.calls.length, 1)
  const claims = state.calls[0].claims
  assert.equal(claims.role, 'restricted')
  assert.equal(claims.gregale_access, 'anonymous')
  assert.equal(claims.sub, undefined)
  assert.ok(claims.exp > Date.now() / 1000)
  for (const [path, method, status, code] of [
    ['/rest/v1/notes', 'GET', 401, 'token_required'],
    ['/rest/v1/products', 'DELETE', 401, 'token_required'],
    ['/rest/v1/products?select=*,notes(body)', 'GET', 403, 'public_read_not_allowed'],
    ['/openapi.json', 'GET', 401, 'token_required'],
    ['/__gregale/schema', 'GET', 401, 'token_required'],
  ]) {
    const response = await send(path, { method })
    assert.equal(response.status, status, path)
    assert.equal(JSON.parse(response.body).code, code, path)
  }
  assert.equal(state.calls.length, 1, 'rejected anonymous requests never dispatch')
  const invalid = await send('/rest/v1/products', { headers: { Authorization: 'Bearer invalid' } })
  assert.equal(invalid.status, 200, 'the stub verifier accepts any JWT; the request stays a user request')
  assert.equal(state.calls[1].claims.gregale_access, 'user')
  const record = state.logs.find(entry => entry.access?.kind === 'anonymous')
  assert.deepEqual(record.access, { kind: 'anonymous' })
  assert.doesNotMatch(JSON.stringify(state.logs), /203\.0\.113\.7|private-/)
})

test('a present but invalid credential is never downgraded to anonymous', async t => {
  const { state, send } = await fixture(t, {}, async () => { throw new Error('bad token') })
  for (const authorization of ['Bearer not-a-jwt', 'Basic abc', 'Bearer ']) {
    const response = await send('/rest/v1/products', { headers: { Authorization: authorization } })
    assert.equal(response.status, 401, authorization)
  }
  assert.equal(state.calls.length, 0)
})

test('anonymous clients have their own per-address budget', async t => {
  const { state, send } = await fixture(t, { DATA_API_RATE_LIMIT_PER_SECOND: '1', DATA_API_RATE_LIMIT_BURST: '2' })
  const from = ip => send('/rest/v1/products', { headers: { 'x-faas-client-ip': ip } })
  assert.equal((await from('198.51.100.1')).status, 200)
  assert.equal((await from('198.51.100.1')).status, 200)
  const limited = await from('198.51.100.1')
  assert.equal(limited.status, 429)
  assert.equal(JSON.parse(limited.body).code, 'data_api_rate_limited')
  assert.equal((await from('198.51.100.2')).status, 200, 'another client keeps its budget')
  assert.equal((await from('2001:db8:5:6::1')).status, 200)
  assert.equal((await from('2001:db8:5:6::2')).status, 200)
  assert.equal((await from('2001:db8:5:6::3')).status, 429, 'one IPv6 /64 shares a bucket')
  assert.equal(state.calls.length, 5)
})

test('anonymous budgets apply by default when the owner set no rate limit', async t => {
  const { send } = await fixture(t)
  const statuses = []
  for (let i = 0; i < limits.rateLimitDefaultBurst + 1; i++) statuses.push((await send('/rest/v1/products', { headers: { 'x-faas-client-ip': '192.0.2.9' } })).status)
  assert.equal(statuses.filter(status => status === 200).length, limits.rateLimitDefaultBurst)
  assert.equal(statuses.at(-1), 429)
})

test('server keys sign server claims, may act for a subject and stay out of browsers', async t => {
  const { state, send } = await fixture(t)
  assert.equal((await send('/rest/v1/notes', { method: 'POST', headers: { Authorization: `Bearer ${keyA}` } })).status, 200)
  assert.deepEqual(Object.keys(state.calls[0].claims).sort(), ['exp', 'gregale_access', 'gregale_server_key', 'role'])
  assert.equal(state.calls[0].claims.gregale_access, 'server')
  assert.equal(state.calls[0].claims.gregale_server_key, 'a')
  assert.equal((await send('/rest/v1/rpc/save_note', { method: 'POST', headers: { Authorization: `Bearer ${keyB}`, 'X-Gregale-Subject': 'user-42' } })).status, 200)
  assert.equal(state.calls[1].claims.sub, 'user-42')
  assert.equal(state.calls[1].claims.gregale_server_key, 'b')
  assert.equal((await send('/openapi.json', { headers: { Authorization: `Bearer ${keyA}` } })).status, 200)
  for (const [headers, status, code] of [
    [{ Authorization: `Bearer ${keyA}`, Origin: 'https://app.example' }, 403, 'server_key_browser_forbidden'],
    [{ Authorization: 'Bearer gdsk_' + 'Z'.repeat(43) }, 401, 'token_invalid'],
    [{ Authorization: 'Bearer gdsk_short' }, 401, 'token_invalid'],
    [{ Authorization: `Bearer ${keyA}`, 'X-Gregale-Subject': 'bad\tsubject' }, 401, 'token_invalid'],
  ]) {
    const response = await send('/rest/v1/notes', { headers })
    assert.equal(response.status, status)
    assert.equal(JSON.parse(response.body).code, code)
  }
  assert.equal(state.calls.length, 3)
  assert.deepEqual(state.logs.filter(entry => entry.access?.kind === 'server').map(entry => entry.access.server_key_slot).slice(0, 2), ['a', 'b'])
  assert.doesNotMatch(JSON.stringify(state.logs), /gdsk_|user-42/)
})

test('server keys are rejected when no slot is configured', async t => {
  const { state, send } = await fixture(t, { DATA_API_SERVER_KEY_A_SHA256: undefined, DATA_API_SERVER_KEY_B_SHA256: undefined })
  const response = await send('/rest/v1/notes', { headers: { Authorization: `Bearer ${keyA}` } })
  assert.equal(response.status, 401)
  assert.equal(state.calls.length, 0)
})

test('application JWTs cannot claim another access kind or server slot', async t => {
  const { state, send } = await fixture(t)
  assert.equal((await send('/rest/v1/notes', { headers: { Authorization: 'Bearer header.payload.signature' } })).status, 200)
  assert.equal(state.calls[0].claims.gregale_access, 'user')
  assert.equal(state.calls[0].claims.gregale_server_key, undefined)
  assert.equal(state.calls[0].claims.sub, 'private-subject')
})
