import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createHash } from 'node:crypto'
import { createServer } from '../server.mjs'
import { runtimeConfig, publicReadConfig, publicRpcConfig, serverKeyConfig, serverKeyPermissions, publicReadMaxAge, limits } from '../config.mjs'
import { selectEmbeddings, clientKey, matchServerKey, serverKeyAllows, serverSubject, unsafePublicRelations, unsafePublicFunctions, publicReadTarget, publicRpcTarget, publicReadCache } from '../access.mjs'

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
  assert.deepEqual(target('/rest/v1/products?select=*,categories(name)'), { schema: 'api', relation: 'products', embedded: ['categories'] })
  assert.deepEqual(target('/rest/v1/products', 'HEAD'), { schema: 'api', relation: 'products', embedded: [] })
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
  config.functions = [{ schema: 'api', name: 'save_note' }, { schema: 'api', name: 'ship_order' }, { schema: 'api', name: 'search_products' }]
  const state = { calls: [], logs: [] }
  const upstream = http.createServer((req, res) => {
    req.resume()
    state.calls.push({ path: req.url, method: req.method, claims: claimsOf(req.headers.authorization) })
    if (req.url.includes('fail=1')) { res.writeHead(400, { 'Content-Type': 'application/json' }); res.end('{"code":"PGRST100"}'); return }
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
  // Parallel batches within the admission queue outpace refill even on a loaded host.
  let limited
  for (let batch = 0; batch < 20 && !limited; batch++) {
    const responses = await Promise.all(Array.from({ length: 8 }, () => send('/rest/v1/products', { headers: { 'x-faas-client-ip': '192.0.2.9' } })))
    limited = responses.find(response => response.status === 429)
  }
  assert.ok(limited, 'anonymous clients must be limited without an owner policy')
  const body = JSON.parse(limited.body)
  assert.equal(body.per_second, limits.rateLimitDefaultPerSecond)
  assert.equal(body.burst, limits.rateLimitDefaultBurst)
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

test('public read freshness is bounded and requires public reads', () => {
  assert.equal(publicReadMaxAge(undefined, false), null)
  assert.equal(publicReadMaxAge('30', true), 30)
  for (const value of ['0', '-1', '1.5', '30s', String(limits.publicReadMaxAgeSeconds + 1), '']) assert.throws(() => publicReadMaxAge(value, true), /MAX_AGE/, value)
  assert.throws(() => publicReadMaxAge('30', false), /requires DATA_API_PUBLIC_READ or DATA_API_PUBLIC_RPC/)
  assert.equal(runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_RPC: 'api.search_products', DATA_API_PUBLIC_READ_MAX_AGE: '60' }).publicReadMaxAge, 60, 'public RPCs alone may be cached')
  assert.throws(() => runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_READ_MAX_AGE: '30' }), /requires DATA_API_PUBLIC_READ/)
  assert.equal(runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_READ: 'api.products', DATA_API_PUBLIC_READ_MAX_AGE: '60' }).publicReadMaxAge, 60)
})

test('only the default schema and full JSON representation can become shared entries', () => {
  const config = runtimeConfig({ ...baseEnv, DATA_API_SCHEMAS: 'api,catalog', DATA_API_PUBLIC_READ: 'api.products,api.categories,catalog.items', DATA_API_PUBLIC_READ_MAX_AGE: '30' })
  const cache = (headers = {}, target = { schema: 'api', relation: 'products', embedded: ['categories'] }) => publicReadCache({ headers }, config, target)
  assert.deepEqual(cache(), { cacheControl: 'public, max-age=30', tags: 'data-api, data-api:api.products, data-api:api.categories' })
  assert.deepEqual(cache({ accept: 'application/json, text/plain, */*', 'accept-profile': 'api' }).cacheControl, 'public, max-age=30')
  for (const headers of [{ range: '0-9' }, { 'range-unit': 'items' }, { prefer: 'count=exact' }, { cookie: 'a=b' }, { 'accept-profile': 'catalog' },
    { accept: 'text/csv' }, { accept: 'application/vnd.pgrst.object+json' }, { accept: 'application/json, text/csv' }]) {
    assert.equal(cache(headers), null, JSON.stringify(headers))
  }
  assert.equal(cache({}, { schema: 'catalog', relation: 'items', embedded: [] }), null, 'non-default schemas share paths with the default schema')
  assert.equal(publicReadCache({ headers: {} }, runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_READ: 'api.products' }), { schema: 'api', relation: 'products', embedded: [] }), null)
  const many = Array.from({ length: limits.cacheTagsMax }, (_, i) => `r${i}`)
  assert.equal(cache({}, { schema: 'api', relation: 'products', embedded: many }), null, 'untaggable responses cannot be purged precisely')
  const long = 's'.repeat(63), relation = 'x'.repeat(63)
  const longConfig = runtimeConfig({ ...baseEnv, DATA_API_SCHEMAS: long, DATA_API_PUBLIC_READ: `${long}.${relation}`, DATA_API_PUBLIC_READ_MAX_AGE: '30' })
  assert.equal(publicReadCache({ headers: {} }, longConfig, { schema: long, relation, embedded: [] }), null, 'tags over 128 bytes cannot be stored')
})

test('canonical anonymous reads are edge-cacheable; everything else stays no-store', async t => {
  const { state, send } = await fixture(t, { DATA_API_PUBLIC_READ_MAX_AGE: '30' })
  const cached = await send('/rest/v1/products?select=id,categories(name)&limit=10', { headers: { Origin: 'https://elsewhere.example', 'x-faas-client-ip': '203.0.113.1' } })
  assert.equal(cached.status, 200)
  assert.equal(cached.headers['cache-control'], 'public, max-age=30')
  assert.equal(cached.headers['cache-tag'], 'data-api, data-api:api.products, data-api:api.categories')
  assert.equal(cached.headers['access-control-allow-origin'], '*')
  assert.equal(cached.headers.vary, undefined, 'shared entries cannot vary on Origin')
  for (const [path, headers, why] of [
    ['/rest/v1/products', { Range: '0-9' }, 'range'],
    ['/rest/v1/products', { Prefer: 'count=exact' }, 'prefer'],
    ['/rest/v1/products', { Accept: 'text/csv' }, 'csv'],
    ['/rest/v1/products?fail=1', {}, 'engine error'],
  ]) {
    const response = await send(path, { headers: { ...headers, 'x-faas-client-ip': '203.0.113.2' } })
    assert.equal(response.headers['cache-control'], 'no-store', why)
    assert.equal(response.headers['cache-tag'], undefined, why)
  }
  const denied = await send('/rest/v1/notes')
  assert.equal(denied.status, 401)
  assert.equal(denied.headers['cache-control'], 'no-store')
  const user = await send('/rest/v1/products', { headers: { Authorization: 'Bearer header.payload.signature', Origin: 'https://app.example' } })
  assert.equal(user.headers['cache-control'], 'no-store')
  assert.equal(user.headers['access-control-allow-origin'], 'https://app.example')
  assert.equal(user.headers.vary, 'Origin')
  const preflight = await send('/rest/v1/products', { method: 'OPTIONS', headers: { Origin: 'https://app.example', 'Access-Control-Request-Method': 'GET' } })
  assert.equal(preflight.status, 204)
  assert.equal(preflight.headers['access-control-allow-origin'], 'https://app.example', 'preflights keep the owner origin allowlist')
  assert.equal(state.calls.length, 6)
})

test('without a freshness setting public reads keep per-origin CORS and no-store', async t => {
  const { send } = await fixture(t)
  const response = await send('/rest/v1/products', { headers: { Origin: 'https://app.example' } })
  assert.equal(response.status, 200)
  assert.equal(response.headers['cache-control'], 'no-store')
  assert.equal(response.headers['access-control-allow-origin'], 'https://app.example')
  assert.equal(response.headers.vary, 'Origin')
})

test('server key permissions are versioned, bounded and only ever narrow access', () => {
  const parse = value => serverKeyPermissions(JSON.stringify(value), 'P')
  assert.equal(serverKeyPermissions(undefined, 'P'), null)
  assert.deepEqual(parse({ version: 1 }), { access: 'write', relations: null, rpcs: null, subjects: true })
  assert.deepEqual(parse({ version: 1, access: 'read', relations: ['api.orders'], rpcs: ['api.ship_order'], subjects: false }),
    { access: 'read', relations: [{ schema: 'api', name: 'orders' }], rpcs: [{ schema: 'api', name: 'ship_order' }], subjects: false })
  for (const value of [{}, { version: 2 }, { version: 1, extra: true }, { version: 1, access: 'admin' }, { version: 1, subjects: 'no' }, { version: 1, relations: [] },
    { version: 1, relations: ['orders'] }, { version: 1, relations: ['api.orders', 'api.orders'] }, { version: 1, rpcs: ['api.Ship'] },
    { version: 1, relations: Array.from({ length: limits.serverKeyPermissionsEntries + 1 }, (_, i) => `api.t${i}`) }, []]) {
    assert.throws(() => parse(value), /Invalid P/, JSON.stringify(value))
  }
  assert.throws(() => serverKeyPermissions('{', 'P'), /Invalid P/)
  assert.throws(() => serverKeyPermissions('x'.repeat(limits.serverKeyPermissionsBytes + 1), 'P'), /Invalid P/)
  const slots = serverKeyConfig({ DATA_API_SERVER_KEY_A_SHA256: hash(keyA), DATA_API_SERVER_KEY_A_PERMISSIONS: '{"version":1,"access":"read"}', DATA_API_SERVER_KEY_B_PERMISSIONS: 'not even json' })
  assert.equal(slots.length, 1, 'permissions without a key are ignored, never a startup failure')
  assert.equal(slots[0].permissions.access, 'read')
})

test('permission checks cover methods, relations, embeddings, RPCs, subjects and the catalog', () => {
  const config = runtimeConfig({ ...baseEnv, DATA_API_SCHEMAS: 'api,ops' })
  const read = serverKeyPermissions('{"version":1,"access":"read","relations":["api.orders","api.customers"],"subjects":false}', 'P')
  const write = serverKeyPermissions('{"version":1,"rpcs":["api.ship_order"]}', 'P')
  const allows = (permissions, url, method = 'GET', headers = {}, rpc = null) => serverKeyAllows({ url, method, headers }, permissions, config, rpc)
  assert.equal(allows(null, '/rest/v1/anything', 'DELETE'), true)
  assert.equal(allows(read, '/rest/v1/orders?select=id,customers(name)'), true)
  assert.equal(allows(read, '/rest/v1/orders', 'HEAD'), true)
  assert.equal(allows(read, '/__gregale/schema'), true)
  for (const [url, method, headers, rpc, why] of [
    ['/rest/v1/notes', 'GET', {}, null, 'unlisted relation'],
    ['/rest/v1/orders?select=*,notes(*)', 'GET', {}, null, 'unlisted embedding'],
    ['/rest/v1/orders?select=id&select=notes(*)', 'GET', {}, null, 'repeated select'],
    ['/rest/v1/orders?select="x"(y)', 'GET', {}, null, 'unparseable select'],
    ['/rest/v1/orders', 'GET', { 'accept-profile': 'ops' }, null, 'other schema'],
    ['/rest/v1/orders', 'PATCH', {}, null, 'read-only write'],
    ['/rest/v1/rpc/ship_order', 'POST', {}, { schema: 'api', name: 'ship_order' }, 'read-only RPC without allowlist'],
    ['/rest/v1/orders', 'GET', { 'x-gregale-subject': 'u1' }, null, 'subjects disabled'],
    ['/openapi.json', 'GET', {}, null, 'catalog with allowlists'],
  ]) assert.equal(allows(read, url, method, headers, rpc), false, why)
  assert.equal(allows(write, '/rest/v1/orders', 'POST'), true, 'write keys without relation lists reach any relation')
  assert.equal(allows(write, '/rest/v1/rpc/ship_order', 'POST', {}, { schema: 'api', name: 'ship_order' }), true)
  assert.equal(allows(write, '/rest/v1/rpc/save_note', 'POST', {}, { schema: 'api', name: 'save_note' }), false)
  assert.equal(allows(write, '/rest/v1/orders', 'GET', { 'x-gregale-subject': 'u1' }), true)
  assert.equal(allows(write, '/openapi.json'), false)
  assert.equal(allows(serverKeyPermissions('{"version":1,"access":"read"}', 'P'), '/openapi.json'), true)
})

test('restricted server keys are refused before admission and logged as restricted', async t => {
  const { state, send } = await fixture(t, { DATA_API_SERVER_KEY_A_PERMISSIONS: '{"version":1,"access":"read","relations":["api.orders"],"subjects":false}' })
  const a = { Authorization: `Bearer ${keyA}` }, b = { Authorization: `Bearer ${keyB}` }
  assert.equal((await send('/rest/v1/orders?select=id', { headers: a })).status, 200)
  for (const [path, method, headers] of [
    ['/rest/v1/notes', 'GET', a],
    ['/rest/v1/orders?select=*,notes(body)', 'GET', a],
    ['/rest/v1/orders', 'POST', a],
    ['/rest/v1/rpc/ship_order', 'POST', a],
    ['/rest/v1/orders', 'GET', { ...a, 'X-Gregale-Subject': 'user-1' }],
    ['/openapi.json', 'GET', a],
  ]) {
    const response = await send(path, { method, headers })
    assert.equal(response.status, 403, `${method} ${path}`)
    assert.equal(JSON.parse(response.body).code, 'server_key_permission_denied')
  }
  assert.equal(state.calls.length, 1, 'denied requests never reach the engine')
  assert.equal((await send('/rest/v1/notes', { method: 'POST', headers: b })).status, 200, 'the unrestricted slot keeps full access')
  const restricted = state.logs.filter(entry => entry.access?.server_key_restricted === true)
  assert.equal(restricted.length, 7)
  assert.ok(restricted.every(entry => entry.access.server_key_slot === 'a'))
  assert.equal(state.logs.find(entry => entry.access?.server_key_slot === 'b').access.server_key_restricted, undefined)
  assert.doesNotMatch(JSON.stringify(state.logs), /gdsk_|user-1/)
})

test('public RPC functions are schema-qualified, exposed, unique and bounded', () => {
  assert.equal(publicRpcConfig(undefined, ['api']), null)
  assert.deepEqual(publicRpcConfig('api.search_products,catalog.featured', ['api', 'catalog']), [{ schema: 'api', name: 'search_products' }, { schema: 'catalog', name: 'featured' }])
  const many = Array.from({ length: limits.publicRpcMaxFunctions + 1 }, (_, i) => `api.f${i}`).join(',')
  for (const value of ['', 'search_products', 'api.Search', 'api.a.b', 'private.search', 'api.search,api.search', 'api.1search', many]) {
    assert.throws(() => publicRpcConfig(value, ['api']), /DATA_API_PUBLIC_RPC/, value)
  }
  assert.deepEqual(runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_RPC: 'api.search_products' }).publicRpc, [{ schema: 'api', name: 'search_products' }])
  assert.equal(runtimeConfig(baseEnv).publicRpc, null)
})

test('only listed functions called by GET or HEAD can be anonymous, with publicly readable embeddings', () => {
  const config = runtimeConfig({ ...baseEnv, DATA_API_SCHEMAS: 'api,catalog', DATA_API_PUBLIC_READ: 'api.products,api.categories', DATA_API_PUBLIC_RPC: 'api.search_products,catalog.featured' })
  const target = (url, method = 'GET', rpc = { schema: 'api', name: 'search_products' }) => publicRpcTarget({ url, method, headers: {} }, config, rpc)
  assert.deepEqual(target('/rest/v1/rpc/search_products?q=lamp'), { schema: 'api', rpc: 'search_products', embedded: [] })
  assert.deepEqual(target('/rest/v1/rpc/search_products?select=id,categories(name)', 'HEAD'), { schema: 'api', rpc: 'search_products', embedded: ['categories'] })
  assert.deepEqual(target('/rest/v1/rpc/featured', 'GET', { schema: 'catalog', name: 'featured' }), { schema: 'catalog', rpc: 'featured', embedded: [] })
  assert.equal(target('/rest/v1/rpc/search_products', 'POST').code, 'public_rpc_requires_get')
  assert.equal(target('/rest/v1/rpc/save_note', 'GET', { schema: 'api', name: 'save_note' }).code, 'token_required')
  assert.equal(target('/rest/v1/rpc/search_products', 'GET', { schema: 'catalog', name: 'search_products' }).code, 'token_required', 'listing is per schema')
  assert.equal(target('/rest/v1/rpc/search_products?select=*,notes(body)').code, 'public_read_not_allowed')
  assert.equal(target('/rest/v1/rpc/search_products?select=id&select=notes(*)').code, 'public_read_not_allowed')
  assert.equal(target('/rest/v1/rpc/featured?select=*,products(id)', 'GET', { schema: 'catalog', name: 'featured' }).code, 'public_read_not_allowed', 'embeddings resolve in the function schema')
  const rpcOnly = runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_RPC: 'api.search_products' })
  assert.equal(publicRpcTarget({ url: '/rest/v1/rpc/search_products?select=*,products(id)', method: 'GET', headers: {} }, rpcOnly, { schema: 'api', name: 'search_products' }).code, 'public_read_not_allowed')
  assert.equal(publicReadTarget({ url: '/rest/v1/products', method: 'GET', headers: {} }, rpcOnly).code, 'token_required', 'public RPCs do not open relations')
})

test('public RPCs must be approved STABLE or IMMUTABLE security-invoker functions', () => {
  const entries = ['search', 'touch', 'unapproved', 'missing', 'owner', 'constant'].map(name => ({ schema: 'api', name }))
  const rows = [
    { schema: 'api', name: 'search', volatility: 's', definer: false },
    { schema: 'api', name: 'touch', volatility: 'v', definer: false },
    { schema: 'api', name: 'unapproved', volatility: 's', definer: false },
    { schema: 'api', name: 'missing', volatility: null, definer: null },
    { schema: 'api', name: 'owner', volatility: 's', definer: true },
    { schema: 'api', name: 'constant', volatility: 'i', definer: false },
  ]
  const approved = ['search', 'touch', 'missing', 'owner', 'constant'].map(name => ({ schema: 'api', name }))
  assert.deepEqual(unsafePublicFunctions(entries, rows, approved), ['api.touch', 'api.unapproved', 'api.missing', 'api.owner'])
  assert.deepEqual(unsafePublicFunctions([entries[0]], [...rows, rows[0]], approved), ['api.search'], 'overloads are refused')
})

test('public RPC responses are tagged by function and embedded relation', () => {
  const config = runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_READ: 'api.categories', DATA_API_PUBLIC_RPC: 'api.search_products', DATA_API_PUBLIC_READ_MAX_AGE: '30' })
  assert.deepEqual(publicReadCache({ headers: {} }, config, { schema: 'api', rpc: 'search_products', embedded: ['categories'] }),
    { cacheControl: 'public, max-age=30', tags: 'data-api, data-api-rpc:api.search_products, data-api:api.categories' })
  assert.equal(publicReadCache({ headers: { prefer: 'count=exact' } }, config, { schema: 'api', rpc: 'search_products', embedded: [] }), null)
})

test('anonymous public RPC calls run by GET as anonymous; other methods and functions need a token', async t => {
  const { state, send } = await fixture(t, { DATA_API_PUBLIC_RPC: 'api.search_products', DATA_API_PUBLIC_READ_MAX_AGE: '30' })
  const called = await send('/rest/v1/rpc/search_products?q=lamp&select=id,categories(name)', { headers: { 'x-faas-client-ip': '203.0.113.9' } })
  assert.equal(called.status, 200)
  assert.equal(called.headers['cache-control'], 'public, max-age=30')
  assert.equal(called.headers['cache-tag'], 'data-api, data-api-rpc:api.search_products, data-api:api.categories')
  assert.equal(state.calls[0].method, 'GET')
  assert.equal(state.calls[0].path, '/rpc/search_products?q=lamp&select=id,categories(name)')
  assert.equal(state.calls[0].claims.gregale_access, 'anonymous')
  assert.equal(state.calls[0].claims.sub, undefined)
  assert.equal((await send('/rest/v1/rpc/search_products', { method: 'HEAD' })).status, 200)
  const posted = await send('/rest/v1/rpc/search_products', { method: 'POST' })
  assert.equal(posted.status, 405)
  assert.equal(posted.headers.allow, 'GET, HEAD')
  assert.equal(JSON.parse(posted.body).code, 'public_rpc_requires_get')
  for (const [path, method, status, code] of [
    ['/rest/v1/rpc/save_note', 'POST', 401, 'token_required'],
    ['/rest/v1/rpc/save_note', 'GET', 405, 'method_not_allowed'],
    ['/rest/v1/rpc/search_products', 'DELETE', 405, 'method_not_allowed'],
    ['/rest/v1/rpc/search_products?select=*,notes(body)', 'GET', 403, 'public_read_not_allowed'],
    ['/rest/v1/rpc/unlisted', 'GET', 404, 'not_found'],
  ]) {
    const response = await send(path, { method })
    assert.equal(response.status, status, `${method} ${path}`)
    assert.equal(JSON.parse(response.body).code, code, `${method} ${path}`)
  }
  assert.equal(state.calls.length, 2, 'refused calls never dispatch')
  const user = await send('/rest/v1/rpc/search_products?q=lamp', { headers: { Authorization: 'Bearer header.payload.signature' } })
  assert.equal(user.status, 200)
  assert.equal(user.headers['cache-control'], 'no-store', 'signed-in calls are never shared')
  assert.equal(state.calls[2].claims.gregale_access, 'user')
  assert.equal((await send('/rest/v1/rpc/search_products', { method: 'POST', headers: { Authorization: 'Bearer header.payload.signature' } })).status, 200, 'signed-in callers may still POST')
  assert.ok(state.logs.some(entry => entry.code === 'public_rpc_requires_get' && entry.route === 'rpc'))
})

test('public RPCs alone enable the anonymous budget and keep relations closed', async t => {
  const { send } = await fixture(t, { DATA_API_PUBLIC_READ: undefined, DATA_API_PUBLIC_RPC: 'api.search_products', DATA_API_RATE_LIMIT_PER_SECOND: '1', DATA_API_RATE_LIMIT_BURST: '1' })
  const from = () => send('/rest/v1/rpc/search_products', { headers: { 'x-faas-client-ip': '198.51.100.40' } })
  assert.equal((await from()).status, 200)
  assert.equal((await from()).status, 429)
  assert.equal((await send('/rest/v1/products')).status, 401, 'relations stay closed without DATA_API_PUBLIC_READ')
})

test('the OpenAPI catalog keeps GET only for public functions', async t => {
  const config = runtimeConfig({ ...baseEnv, DATA_API_PUBLIC_RPC: 'api.search_products' })
  config.functions = [{ schema: 'api', name: 'search_products' }, { schema: 'api', name: 'save_note' }]
  const operation = () => ({ responses: { 200: { description: 'OK' } } })
  const upstream = http.createServer((req, res) => {
    req.resume()
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify({ swagger: '2.0', paths: { '/rpc/search_products': { get: operation(), post: operation() }, '/rpc/save_note': { get: operation(), post: operation() }, '/rpc/hidden': { post: operation() } } }))
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => ({ sub: 'alice', exp: 9999999999 }), upstream.address().port)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const response = await fetch(`http://127.0.0.1:${gateway.address().port}/openapi.json`, { headers: { Authorization: 'Bearer valid' } })
  const document = await response.json()
  assert.deepEqual(Object.keys(document.paths['/rpc/search_products']).sort(), ['get', 'post'])
  assert.deepEqual(Object.keys(document.paths['/rpc/save_note']), ['post'])
  assert.equal(document.paths['/rpc/hidden'], undefined)
})
