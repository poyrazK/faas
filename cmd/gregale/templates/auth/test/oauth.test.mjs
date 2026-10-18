import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createHmac, generateKeyPairSync } from 'node:crypto'
import { consentPath, oauthAudience, oauthEnv, runtimeConfig } from '../config.mjs'
import { createHookServer, hookEnv, newHookSecret } from '../email.mjs'
import { accessTokenClaims, bearerClaims, clientInput, clientTokenAllowed, consentLocation, isClientToken, oauthRoute, wellKnownPath } from '../oauth.mjs'
import { createServer } from '../server.mjs'
import { route } from '../authz.mjs'

const site = 'https://shop.example.com'
const external = 'https://shop-auth.gregale.app'
const session = '5a1b2c3d-0000-4000-8000-000000000001'
const client = '6a1b2c3d-0000-4000-8000-000000000002'
const user = '7a1b2c3d-0000-4000-8000-000000000003'
const token = claims => ['e30', Buffer.from(JSON.stringify(claims)).toString('base64url'), 'c2ln'].join('.')

function env(overrides = {}) {
  const { privateKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' })
  return {
    DATABASE_URL: 'postgresql://gregale_auth_x:secret@db.example.com:5432/app?sslmode=require',
    AUTH_EXTERNAL_URL: external, AUTH_SITE_URL: site, AUTH_JWT_SECRET: 'x'.repeat(48),
    AUTH_JWT_KEY_0A1B2C3D: JSON.stringify({ ...privateKey.export({ format: 'jwk' }), alg: 'ES256', kid: 'gregale-0a1b2c3d' }),
    AUTH_JWT_SIGNER_0A1B2C3D: '1700000000',
    ...overrides,
  }
}

test('the OAuth server and open registration are separate settings', () => {
  assert.deepEqual(oauthEnv({}), { GOTRUE_OAUTH_SERVER_ENABLED: 'false' })
  assert.deepEqual(oauthEnv({ AUTH_OAUTH_REGISTRATION: '1' }), { GOTRUE_OAUTH_SERVER_ENABLED: 'false' }, 'registration alone never fails startup')
  const on = runtimeConfig(env({ AUTH_OAUTH_SERVER: '1', AUTH_OAUTH_REGISTRATION: '1' }))
  assert.equal(on.oauth, true)
  assert.equal(on.gotrueEnv.GOTRUE_OAUTH_SERVER_ENABLED, 'true')
  assert.equal(on.gotrueEnv.GOTRUE_OAUTH_SERVER_ALLOW_DYNAMIC_REGISTRATION, 'true')
  assert.equal(on.gotrueEnv.GOTRUE_OAUTH_SERVER_AUTHORIZATION_PATH, consentPath)
  assert.ok(on.gotrueEnv.GOTRUE_URI_ALLOW_LIST.split(',').includes(external), 'the consent page origin passes GoTrue\'s origin check')
  assert.deepEqual(on.settings.oauth, { enabled: true, dynamic_registration: true, issuer: `${external}/auth/v1`, metadata_url: `${external}/.well-known/oauth-authorization-server/auth/v1`, default_audience: oauthAudience })
  const off = runtimeConfig(env())
  assert.equal(off.oauth, false)
  assert.ok(!off.gotrueEnv.GOTRUE_URI_ALLOW_LIST.split(',').includes(external))
  assert.throws(() => runtimeConfig(env({ AUTH_OAUTH_SERVER: 'yes' })), /AUTH_OAUTH_SERVER/)
  assert.equal(hookEnv('s').GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_ENABLED, undefined, 'the token hook runs only with the OAuth server')
  assert.equal(hookEnv('s', { accessToken: true }).GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_URI, 'http://127.0.0.1:9998/custom-access-token')
})

test('consent, discovery and client tokens on the front', () => {
  assert.equal(consentLocation(`${site}${consentPath}?authorization_id=abc_1`, site + '/', external), `${external}/ui/consent?authorization_id=abc_1`)
  assert.equal(consentLocation(`${site}/callback?code=1`, site, external), `${site}/callback?code=1`)
  assert.equal(consentLocation(`https://evil.example${consentPath}?authorization_id=x`, site, external), `https://evil.example${consentPath}?authorization_id=x`)
  assert.equal(wellKnownPath('/.well-known/oauth-authorization-server/auth/v1'), '/.well-known/oauth-authorization-server')
  assert.equal(wellKnownPath('/.well-known/openid-configuration/auth/v1'), '/.well-known/openid-configuration')
  assert.equal(wellKnownPath('/.well-known/oauth-authorization-server'), null)
  assert.equal(isClientToken(bearerClaims(`Bearer ${token({ sub: user, client_id: client })}`)), true)
  assert.equal(isClientToken(bearerClaims(`Bearer ${token({ sub: user })}`)), false)
  assert.equal(bearerClaims('Bearer gaak_x'), null)
  assert.equal(clientTokenAllowed('GET', '/auth/v1/oauth/userinfo'), true)
  assert.equal(clientTokenAllowed('POST', '/auth/v1/logout?scope=local'), true)
  assert.equal(clientTokenAllowed('POST', '/authz/v1/authorize'), true)
  for (const [method, url] of [['POST', '/auth/v1/logout'], ['PUT', '/auth/v1/user'], ['GET', '/auth/v1/user'], ['GET', '/auth/v1/oauth/%75serinfo'], ['GET', '/authz/v1/orgs'], ['POST', '/authz/v1/orgs']]) {
    assert.equal(clientTokenAllowed(method, url), false, `${method} ${url}`)
  }
})

// fakePool answers the hook's three statements.
function fakePool({ remembered = [], pending = [] } = {}) {
  const calls = []
  return {
    calls,
    async query(sql, args) {
      calls.push(sql.trim().split(/\s+/).slice(0, 4).join(' '))
      if (sql.includes('FROM gregale_auth.oauth_session_resources')) return { rows: remembered }
      if (sql.includes('FROM auth.oauth_authorizations')) return { rows: pending }
      if (sql.startsWith('INSERT')) { remembered.push({ resource: args[2] }); return { rows: [] } }
      throw new Error('unexpected SQL')
    },
  }
}

test('a client token takes the audience of its resource, and keeps it on refresh', async () => {
  const base = { sub: user, session_id: session, aud: 'authenticated', role: 'authenticated' }
  const first = fakePool()
  assert.deepEqual(await accessTokenClaims(first, { claims: base }), { claims: base }, 'the app\'s own tokens pass unchanged')
  assert.deepEqual(await accessTokenClaims(first, { claims: { ...base, aud: ['authenticated'] } }), { claims: base }, 'as GoTrue would write them')
  assert.equal(first.calls.length, 0)
  const pool = fakePool({ pending: [{ resource: 'https://mcp.shop.example.com/mcp' }] })
  const exchange = await accessTokenClaims(pool, { claims: { ...base, client_id: client, scope: 'openid' } })
  assert.equal(exchange.claims.aud, 'https://mcp.shop.example.com/mcp')
  assert.equal(exchange.claims.scope, 'openid')
  const refresh = await accessTokenClaims(fakePool({ remembered: [{ resource: 'https://mcp.shop.example.com/mcp' }] }), { claims: { ...base, client_id: client } })
  assert.equal(refresh.claims.aud, 'https://mcp.shop.example.com/mcp')
  assert.equal((await accessTokenClaims(fakePool({ pending: [{ resource: null }] }), { claims: { ...base, client_id: client } })).claims.aud, oauthAudience)
  assert.equal((await accessTokenClaims(fakePool(), { claims: { ...base, client_id: client } })).claims.aud, oauthAudience)
  await assert.rejects(accessTokenClaims(fakePool({ pending: [{ resource: 'https://a.example' }, { resource: 'https://b.example' }] }), { claims: { ...base, client_id: client } }), { status: 409, code: 'ambiguous_resource' })
  await assert.rejects(accessTokenClaims(fakePool(), { claims: { ...base, client_id: client, session_id: 'x' } }), { status: 400 })
  await assert.rejects(accessTokenClaims(fakePool(), {}), { status: 400 })
})

test('the loopback hook answers GoTrue\'s signed token hook', async t => {
  const secret = newHookSecret()
  const sign = (id, timestamp, body) => `v1,${createHmac('sha256', Buffer.from(secret.replace(/^v1,whsec_/, ''), 'base64')).update(`${id}.${timestamp}.${body}`).digest('base64')}`
  const call = async (server, payload) => {
    server.listen(0, '127.0.0.1')
    await once(server, 'listening')
    t.after(() => server.close())
    const body = JSON.stringify(payload)
    const timestamp = String(Math.floor(Date.now() / 1000))
    return fetch(`http://127.0.0.1:${server.address().port}/custom-access-token`, { method: 'POST', body, headers: { 'webhook-id': 'msg_1', 'webhook-timestamp': timestamp, 'webhook-signature': sign('msg_1', timestamp, body) } })
  }
  const on = await call(createHookServer({ secret, mailer: null, api: '', app: 'x', accessToken: async payload => ({ claims: { ...payload.claims, aud: 'r' } }) }), { claims: { sub: 'u' } })
  assert.deepEqual(await on.json(), { claims: { sub: 'u', aud: 'r' } })
  const failing = await call(createHookServer({ secret, mailer: null, api: '', app: 'x', accessToken: async () => { throw Object.assign(new Error('x'), { status: 409, code: 'ambiguous_resource' }) } }), { claims: {} })
  assert.deepEqual(await failing.json(), { error: { http_code: 409, message: 'ambiguous_resource' } })
  const off = await call(createHookServer({ secret, mailer: null, api: '', app: 'x' }), { claims: {} })
  assert.equal(off.status, 404)
})

async function front(t, oauth, upstreamHandler) {
  const seen = []
  const upstream = http.createServer((req, res) => { seen.push(`${req.method} ${req.url}`); upstreamHandler(req, res) })
  upstream.listen(0, '127.0.0.1')
  await once(upstream, 'listening')
  const server = createServer({ upstreamPort: upstream.address().port, oauth, authz: (req, res) => { res.writeHead(200); res.end('authz') } })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => { server.close(); upstream.close() })
  return { base: `http://127.0.0.1:${server.address().port}`, seen }
}

test('the front confines client tokens and steers consent to the hosted page', async t => {
  let open = true
  const oauth = {
    location: location => consentLocation(location, site, external),
    registrationOpen: async () => open,
    resource: async id => id === 'auth_1' ? 'https://mcp.shop.example.com/mcp' : null,
  }
  const { base, seen } = await front(t, oauth, (req, res) => {
    if (req.url.startsWith('/oauth/authorize')) { res.writeHead(302, { Location: `${site}${consentPath}?authorization_id=auth_1` }); return res.end() }
    if (req.url === '/oauth/authorizations/auth_1') { res.writeHead(200, { 'Content-Type': 'application/json' }); return res.end(JSON.stringify({ authorization_id: 'auth_1', scope: 'openid' })) }
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end('{}')
  })
  const thirdParty = { Authorization: `Bearer ${token({ sub: user, client_id: client })}` }
  for (const [method, path] of [['PUT', '/auth/v1/user'], ['GET', '/auth/v1/factors'], ['POST', '/auth/v1/logout'], ['GET', '/authz/v1/orgs']]) {
    const response = await fetch(base + path, { method, headers: thirdParty })
    assert.equal(response.status, 403, `${method} ${path}`)
    assert.equal((await response.json()).code, 'oauth_client_token')
  }
  assert.equal((await fetch(`${base}/auth/v1/oauth/userinfo`, { headers: thirdParty })).status, 200)
  assert.equal((await fetch(`${base}/auth/v1/logout?scope=local`, { method: 'POST', headers: thirdParty })).status, 200)
  assert.equal((await fetch(`${base}/authz/v1/orgs`, { headers: { Authorization: `Bearer ${token({ sub: user })}` } })).status, 200, 'the app\'s own tokens are unaffected')
  const authorize = await fetch(`${base}/auth/v1/oauth/authorize?client_id=${client}`, { redirect: 'manual' })
  assert.equal(authorize.headers.get('location'), `${external}/ui/consent?authorization_id=auth_1`)
  assert.deepEqual(await (await fetch(`${base}/auth/v1/oauth/authorizations/auth_1`, { headers: { Authorization: `Bearer ${token({ sub: user })}` } })).json(),
    { authorization_id: 'auth_1', scope: 'openid', resource: 'https://mcp.shop.example.com/mcp' })
  assert.equal((await fetch(`${base}/.well-known/oauth-authorization-server/auth/v1`)).status, 200)
  assert.ok(seen.includes('GET /.well-known/oauth-authorization-server'))
  assert.equal((await fetch(`${base}/auth/v1/oauth/clients/register`, { method: 'POST', body: '{}' })).status, 200)
  open = false
  const full = await fetch(`${base}/auth/v1/oauth/clients/register`, { method: 'POST', body: '{}' })
  assert.equal(full.status, 403)
  assert.equal((await full.json()).code, 'oauth_client_limit')
  assert.equal(seen.filter(line => line === 'POST /oauth/clients/register').length, 1)
})

test('owners manage clients through GoTrue\'s admin API', async () => {
  const calls = []
  const stored = { client_id: client, client_name: 'Claude', client_type: 'public', registration_type: 'manual', redirect_uris: ['http://localhost:3000/cb'], created_at: 'now' }
  let disabled = false
  const admin = async (method, path, body) => {
    calls.push([method, path, body])
    if (disabled) return { status: 404, data: { error_code: 'feature_disabled' } }
    if (method === 'POST' && path === '/admin/oauth/clients') return { status: 201, data: { ...stored, ...body, client_secret: null } }
    if (path.endsWith('/regenerate_secret')) return { status: 200, data: { ...stored, client_type: 'confidential', client_secret: 'new-secret' } }
    if (method === 'DELETE') return { status: 204, data: null }
    if (path === '/admin/oauth/clients') return { status: 200, data: { clients: [stored] } }
    return { status: 200, data: { ...stored, ...body } }
  }
  const goTrueError = () => Object.assign(new Error('gotrue'), { status: 400 })
  const route = (method, path, body) => oauthRoute({ admin, goTrueError }, method, path.split('/').filter(Boolean), body)
  const [created, view] = await route('POST', '/oauth/clients', { client_name: 'Claude', redirect_uris: ['http://localhost:3000/cb'], client_type: 'public' })
  assert.equal(created, 201)
  assert.equal(view.client.id, client)
  assert.deepEqual(calls[0][2], { registration_type: 'manual', client_name: 'Claude', redirect_uris: ['http://localhost:3000/cb'], client_type: 'public' })
  assert.equal((await route('GET', '/oauth/clients'))[1].clients[0].name, 'Claude')
  assert.equal((await route('PATCH', `/oauth/clients/${client}`, { client_name: 'Claude Desktop' }))[1].client.name, 'Claude Desktop')
  assert.equal(calls.at(-1)[0], 'PUT')
  assert.equal((await route('POST', `/oauth/clients/${client}/regenerate-secret`))[1].client_secret, 'new-secret')
  assert.deepEqual(await route('DELETE', `/oauth/clients/${client}`), [200, { id: client, deleted: true }])
  await assert.rejects(route('GET', '/oauth/clients/not-a-uuid'), { status: 400, code: 'invalid_id' })
  await assert.rejects(route('PATCH', `/oauth/clients/${client}`, {}), { code: 'invalid_client' })
  disabled = true
  await assert.rejects(route('GET', '/oauth/clients'), { status: 409, code: 'oauth_server_disabled' })
  assert.throws(() => clientInput({ redirect_uris: [] }, true), { code: 'invalid_redirect_uris' })
  assert.throws(() => clientInput({ client_name: 'x' }, true), { code: 'invalid_redirect_uris' })
  assert.throws(() => clientInput({ client_type: 'public' }, false), { code: 'invalid_client' }, 'a client keeps its type')
  assert.throws(() => clientInput({ grant_types: ['implicit'], redirect_uris: ['https://a.example/cb'] }, true), { code: 'invalid_client' })
})

test('a client token may check its user\'s permissions and nothing else in /authz', async () => {
  const claims = { sub: user, session_id: session, client_id: client }
  const statements = []
  const pool = { connect: async () => ({ query: async (sql, args) => { statements.push([sql, args]); return { rows: [{ allowed: true }] } }, release() {} }) }
  assert.deepEqual(await route(pool, claims, 'POST', '/authorize', { action: 'read', resource: 'org/o1/doc/d1' }), [200, { allowed: true }])
  assert.equal(JSON.parse(statements.find(([sql]) => sql.includes('set_config'))[1][0]).client_id, client, 'policies can see which client acts')
  for (const [method, path] of [['GET', '/orgs'], ['POST', '/orgs'], ['POST', '/invitations/accept'], ['POST', '/storage/sign']]) {
    await assert.rejects(route(pool, claims, method, path, {}), { status: 403, code: 'oauth_client_token' }, `${method} ${path}`)
  }
})

test('first-party tokens carry the session organization with AUTH_ORG_CLAIMS on', async () => {
  const base = { sub: user, session_id: session, aud: 'authenticated', role: 'authenticated' }
  const queries = []
  let row = { id: '6f1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11', slug: 'acme', role: 'admin', permissions: ['org:read', 'members:manage'] }
  const pool = { query: async (sql, args) => { queries.push({ sql, args }); return { rows: row ? [row] : [] } } }
  const { claims } = await accessTokenClaims(pool, { claims: base }, { orgClaims: true })
  assert.deepEqual(claims, { ...base, org_id: row.id, org_slug: 'acme', org_role: 'admin', org_permissions: ['org:read', 'members:manage'] })
  assert.deepEqual(queries[0].args, [session, user])
  assert.match(queries[0].sql, /JOIN gregale_auth\.memberships m ON m\.org_id = s\.org_id AND m\.user_id = \$2/, 'the current membership decides')
  row = null
  assert.deepEqual((await accessTokenClaims(pool, { claims: base }, { orgClaims: true })).claims, base, 'no active organization, or no longer a member')
  queries.length = 0
  assert.deepEqual((await accessTokenClaims(pool, { claims: base })).claims, base, 'off: no query')
  assert.equal(queries.length, 0)
  const forClient = await accessTokenClaims(fakePool(), { claims: { ...base, client_id: client } }, { orgClaims: true })
  assert.equal(forClient.claims.org_id, undefined, 'a client token never carries them')
})
