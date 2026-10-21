import test from 'node:test'
import assert from 'node:assert/strict'
import { generateKeyPairSync, sign } from 'node:crypto'
import { AuthError, bearerToken, createClientCredentials, createVerifier, resource } from '../dist/server.js'

const origin = 'https://shop-auth.gregale.app'
const issuer = `${origin}/auth/v1`
const user = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'
const t0 = 1_800_000_000_000

const b64 = value => Buffer.from(value).toString('base64url')

function newKey(kid) {
  const { privateKey, publicKey } = generateKeyPairSync('ec', { namedCurve: 'prime256v1' })
  return { kid, privateKey, jwk: { ...publicKey.export({ format: 'jwk' }), alg: 'ES256', kid, key_ops: ['verify'] } }
}

function signToken(key, claims, header = { alg: 'ES256', typ: 'JWT', kid: key.kid }) {
  const unsigned = `${b64(JSON.stringify(header))}.${b64(JSON.stringify(claims))}`
  return `${unsigned}.${sign('sha256', Buffer.from(unsigned), { key: key.privateKey, dsaEncoding: 'ieee-p1363' }).toString('base64url')}`
}

const claimsAt = (ms, extra = {}) => ({ iss: issuer, aud: 'authenticated', sub: user, session_id: 's1', role: 'authenticated', email: 'ada@example.com', aal: 'aal1', iat: ms / 1000, exp: ms / 1000 + 600, app_metadata: { provider: 'email' }, ...extra })

// authApp serves the JWKS and /authz/v1/authorize like the Auth app.
function authApp(...keys) {
  const app = { keys: keys.map(k => k.jwk), jwksHits: 0, jwksStatus: 200, authorize: [200, { allowed: true }], requests: [] }
  app.fetch = async (url, init = {}) => {
    const u = new URL(url)
    app.requests.push({ url: u, init })
    if (u.pathname === '/auth/v1/.well-known/jwks.json') {
      app.jwksHits++
      return app.jwksStatus === 200 ? Response.json({ keys: app.keys }) : new Response('down', { status: app.jwksStatus })
    }
    if (u.pathname === '/authz/v1/authorize') return new Response(JSON.stringify(app.authorize[1]), { status: app.authorize[0] })
    return new Response('', { status: 404 })
  }
  return app
}

function verifier(app) {
  const clock = { now: t0 }
  return { clock, auth: createVerifier({ url: origin, fetch: app.fetch, now: () => clock.now }) }
}

test('verifies an Auth app token', async () => {
  const key = newKey('gregale-0a1b2c3d')
  const { auth } = verifier(authApp(key))
  const claims = await auth.verify(signToken(key, claimsAt(t0)))
  assert.equal(claims.sub, user)
  assert.equal(claims.sessionId, 's1')
  assert.equal(claims.email, 'ada@example.com')
  assert.equal(claims.aal, 'aal1')
  assert.equal(claims.expiresAt, t0 / 1000 + 600)
  assert.deepEqual(claims.appMetadata, { provider: 'email' })
  assert.equal(claims.isAnonymous, false)
  assert.equal(auth.issuer, issuer)
})

test('reads the active organization from organization claims', async () => {
  const key = newKey('gregale-0a1b2c3d')
  const { auth } = verifier(authApp(key))
  const claims = await auth.verify(signToken(key, claimsAt(t0, { org_id: 'o1', org_slug: 'acme', org_role: 'admin', org_permissions: ['org:read', 'members:manage'] })))
  assert.deepEqual(claims.organization, { id: 'o1', slug: 'acme', role: 'admin', permissions: ['org:read', 'members:manage'] })
  assert.equal((await auth.verify(signToken(key, claimsAt(t0)))).organization, undefined)
  assert.equal((await auth.verify(signToken(key, claimsAt(t0, { act: { sub: 'admin:0A1B2C3D' } })))).actor, 'admin:0A1B2C3D')
  assert.equal((await auth.verify(signToken(key, claimsAt(t0)))).actor, undefined)
})

test('refuses tokens it should not accept', async () => {
  const key = newKey('gregale-0a1b2c3d'), other = newKey('gregale-0a1b2c3d')
  const { auth } = verifier(authApp(key))
  const valid = signToken(key, claimsAt(t0))
  const [head, body, sig] = valid.split('.')
  const cases = {
    'other issuer': [signToken(key, claimsAt(t0, { iss: 'https://evil.example/auth/v1' })), 'invalid_token'],
    'other audience': [signToken(key, claimsAt(t0, { aud: 'service' })), 'invalid_token'],
    'no subject': [signToken(key, { ...claimsAt(t0), sub: undefined }), 'invalid_token'],
    'no expiry': [signToken(key, { ...claimsAt(t0), exp: undefined }), 'invalid_token'],
    expired: [signToken(key, claimsAt(t0, { exp: t0 / 1000 - 61 })), 'token_expired'],
    'not valid yet': [signToken(key, claimsAt(t0, { nbf: t0 / 1000 + 61 })), 'invalid_token'],
    'service token': [signToken(key, { role: 'service_role', iat: t0 / 1000, exp: t0 / 1000 + 60 }), 'invalid_token'],
    'other key': [signToken(other, claimsAt(t0)), 'invalid_token'],
    tampered: [`${head}.${b64('{"sub":"x"}')}.${sig}`, 'invalid_token'],
    HS256: [`${b64('{"alg":"HS256","kid":"gregale-0a1b2c3d"}')}.${body}.${sig}`, 'invalid_token'],
    none: [`${b64('{"alg":"none"}')}.${body}.`, 'invalid_token'],
    crit: [signToken(key, claimsAt(t0), { alg: 'ES256', kid: key.kid, crit: ['b64'] }), 'invalid_token'],
    'two segments': [`${head}.${body}`, 'invalid_token'],
    'short signature': [`${head}.${body}.${b64('short')}`, 'invalid_token'],
    'bad base64url': [`${head}.${body}.abc+def`, 'invalid_token'],
    oversized: ['a'.repeat(16385), 'invalid_token'],
    'unknown key': [signToken(newKey('gregale-ffffffff'), claimsAt(t0)), 'invalid_token'],
  }
  for (const [name, [token, code]] of Object.entries(cases)) {
    await assert.rejects(auth.verify(token), err => err instanceof AuthError && err.status === 401 && err.code === code, name)
  }
  await auth.verify(signToken(key, claimsAt(t0, { exp: t0 / 1000 - 59, aud: ['authenticated', 'other'] })))
})

test('learns rotated keys at once and forgets revoked keys within five minutes', async () => {
  const old = newKey('gregale-0a1b2c3d'), next = newKey('gregale-1b2c3d4e')
  const app = authApp(old)
  const { auth, clock } = verifier(app)
  await auth.verify(signToken(old, claimsAt(clock.now)))
  app.keys = [old.jwk, next.jwk]
  clock.now += 31_000
  await auth.verify(signToken(next, claimsAt(clock.now)))
  assert.equal(app.jwksHits, 2)
  // Unknown key IDs refetch at most once every 30 seconds, and concurrent
  // verifications share one fetch.
  clock.now += 31_000
  const unknown = signToken(newKey('gregale-ffffffff'), claimsAt(clock.now))
  await Promise.allSettled(Array.from({ length: 5 }, () => auth.verify(unknown)))
  assert.equal(app.jwksHits, 3)
  app.keys = [next.jwk]
  await auth.verify(signToken(old, claimsAt(clock.now)))
  clock.now += 300_000
  await assert.rejects(auth.verify(signToken(old, claimsAt(clock.now))), { code: 'invalid_token' })
})

test('keeps cached keys while the Auth app is down', async () => {
  const key = newKey('gregale-0a1b2c3d')
  const app = authApp(key)
  const { auth, clock } = verifier(app)
  app.jwksStatus = 502
  await assert.rejects(auth.verify(signToken(key, claimsAt(clock.now))), { status: 503, code: 'auth_unavailable' })
  app.jwksStatus = 200
  clock.now += 30_000
  await auth.verify(signToken(key, claimsAt(clock.now)))
  app.jwksStatus = 502
  clock.now += 300_000
  await auth.verify(signToken(key, claimsAt(clock.now)))
})

test('skips keys it cannot use', async () => {
  const good = newKey('gregale-0a1b2c3d')
  const app = authApp()
  app.keys = [{ ...good.jwk, kid: 'off-curve', y: b64(Buffer.alloc(32)) }, { ...good.jwk, kid: 'sign-only', key_ops: ['sign'] }, { kty: 'RSA', kid: 'rsa', n: 'AQAB', e: 'AQAB' }]
  const { auth, clock } = verifier(app)
  await assert.rejects(auth.verify(signToken(good, claimsAt(clock.now))), { code: 'auth_unavailable' })
  app.keys.push(good.jwk)
  clock.now += 30_000
  await auth.verify(signToken(good, claimsAt(clock.now)))
  await assert.rejects(auth.verify(signToken(good, claimsAt(clock.now), { alg: 'ES256', kid: 'sign-only' })), { code: 'invalid_token' })
})

test('authorize asks the Auth app with the user token', async () => {
  const app = authApp()
  const { auth } = verifier(app)
  const target = resource('org-1', 'doc', 42)
  assert.equal(target, 'org/org-1/doc/42')
  assert.equal(await auth.authorize('tok', 'read', target), true)
  const sent = app.requests.at(-1)
  assert.equal(sent.url.href, `${origin}/authz/v1/authorize`)
  assert.equal(sent.init.headers.Authorization, 'Bearer tok')
  assert.deepEqual(JSON.parse(sent.init.body), { action: 'read', resource: target })
  app.authorize = [200, { allowed: false }]
  assert.equal(await auth.authorize('tok', 'write', target), false)
  app.authorize = [401, { status: 401, code: 'not_authenticated' }]
  await assert.rejects(auth.authorize('tok', 'read', target), { status: 401, code: 'not_authenticated' })
  const down = createVerifier({ url: origin, fetch: async () => { throw new TypeError('fetch failed') } })
  await assert.rejects(down.authorize('tok', 'read', target), { status: 503, code: 'auth_unavailable' })
})

test('bearer tokens and Auth app origins', () => {
  assert.equal(bearerToken('Bearer abc'), 'abc')
  assert.equal(bearerToken('bearer abc'), 'abc')
  for (const value of [undefined, null, '', 'Bearer', 'Basic abc', 'Bearer a b']) {
    assert.throws(() => bearerToken(value), { status: 401, code: 'no_token' }, String(value))
  }
  for (const url of ['http://shop-auth.gregale.app', 'https://u:p@shop-auth.gregale.app', 'https://shop-auth.gregale.app/?x=1']) {
    assert.throws(() => createVerifier({ url }), undefined, url)
  }
  assert.equal(createVerifier({ url: 'http://localhost:9999' }).issuer, 'http://localhost:9999/auth/v1')
})

test('tells a service account\'s token from a person\'s', async () => {
  const key = newKey('gregale-0a1b2c3d')
  const { auth } = verifier(authApp(key))
  const account = '7c9e6679-7425-40de-944b-e07fc1f90ae7'
  const claims = await auth.verify(signToken(key, claimsAt(t0, { sub: account, session_id: undefined, email: undefined, service_account: true, secret_id: 's-1',
    org_id: 'o1', org_slug: 'acme', org_role: 'deployer', org_permissions: ['docs:read'], scope: 'docs:read' })))
  assert.deepEqual(claims.serviceAccount, { id: account, secretId: 's-1', scope: ['docs:read'] })
  assert.deepEqual(claims.organization, { id: 'o1', slug: 'acme', role: 'deployer', permissions: ['docs:read'] })
  assert.equal(claims.sessionId, undefined)
  assert.equal((await auth.verify(signToken(key, claimsAt(t0)))).serviceAccount, undefined)
  // The marker alone, without the secret it came from, is not an account.
  assert.equal((await auth.verify(signToken(key, claimsAt(t0, { service_account: true })))).serviceAccount, undefined)
})

test('client credentials are exchanged once and cached until shortly before expiry', async () => {
  const clientId = '7c9e6679-7425-40de-944b-e07fc1f90ae7'
  const clientSecret = 'gasa_' + 'a'.repeat(43)
  const clock = { now: t0 }
  const requests = []
  let answer = [200, { access_token: 'token-1', token_type: 'Bearer', expires_in: 600 }]
  const fetch = async (url, init) => {
    requests.push({ url: String(url), init })
    await new Promise(resolve => setTimeout(resolve, 5))
    return new Response(JSON.stringify(answer[1]), { status: answer[0] })
  }
  const machine = createClientCredentials({ url: origin, clientId, clientSecret, scope: ['docs:read', 'docs:write'], fetch, now: () => clock.now })
  assert.deepEqual(await Promise.all([machine.token(), machine.token()]), ['token-1', 'token-1'])
  assert.equal(requests.length, 1)
  assert.equal(requests[0].url, `${origin}/auth/v1/oauth/token`)
  assert.equal(requests[0].init.headers.Authorization, `Basic ${Buffer.from(`${clientId}:${clientSecret}`).toString('base64')}`)
  assert.equal(requests[0].init.body, 'grant_type=client_credentials&scope=docs%3Aread+docs%3Awrite')
  clock.now += 539_000
  assert.equal(await machine.token(), 'token-1')
  answer = [200, { access_token: 'token-2', token_type: 'Bearer', expires_in: 600 }]
  clock.now += 1_000
  assert.equal(await machine.token(), 'token-2')
  machine.clear()
  answer = [401, { error: 'invalid_client' }]
  await assert.rejects(machine.token(), err => err instanceof AuthError && err.status === 401 && err.code === 'invalid_client')
  answer = [429, { error: 'slow_down' }]
  await assert.rejects(machine.token(), err => err.status === 429 && err.code === 'slow_down')
  assert.throws(() => createClientCredentials({ url: origin, clientId: 'nope', clientSecret }), /clientId/)
  assert.throws(() => createClientCredentials({ url: origin, clientId, clientSecret: 'secret' }), /clientSecret/)
})
