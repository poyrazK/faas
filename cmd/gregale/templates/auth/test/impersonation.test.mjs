import test from 'node:test'
import assert from 'node:assert/strict'
import { once } from 'node:events'
import { actClaims, impersonated, impersonationBlocked, impersonationsRoute, startImpersonation } from '../impersonation.mjs'
import { accessTokenClaims } from '../oauth.mjs'
import { createServer } from '../server.mjs'

const user = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'
const session = '9e1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'
const jwt = claims => `eyJhbGciOiJFUzI1NiJ9.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.c2ln`

test('an impersonated token cannot change the account, only sign itself out', () => {
  assert.equal(impersonated({ act: { sub: 'admin:0A1B2C3D' } }), true)
  assert.equal(impersonated({ act: { sub: 'someone' } }), false)
  assert.equal(impersonated({}), false)
  for (const [method, url] of [['PUT', '/auth/v1/user'], ['POST', '/auth/v1/factors'], ['DELETE', '/auth/v1/factors/f1'], ['POST', '/auth/v1/passkeys/registration/options'],
    ['GET', '/auth/v1/user/identities/authorize?provider=github'], ['DELETE', '/auth/v1/user/identities/i1'], ['GET', '/auth/v1/reauthenticate'],
    ['POST', '/auth/v1/logout'], ['POST', '/auth/v1/logout?scope=global'], ['POST', '/auth/v1/logout?scope=others'], ['POST', '/auth/v1/oauth/authorizations/a1/consent'],
    ['DELETE', '/authz/v1/account'], ['DELETE', '/authz/v1/sessions/s1'],
    // GoTrue's router serves these as the paths above.
    ['PUT', '/auth/v1/user/'], ['PUT', '/auth/v1/user//?x=1'], ['POST', '/auth/v1/factors/'], ['POST', '/auth/v1/logout/?scope=local']]) {
    assert.equal(impersonationBlocked(method, url), true, `${method} ${url}`)
  }
  for (const [method, url] of [['GET', '/auth/v1/user'], ['POST', '/auth/v1/logout?scope=local'], ['POST', '/auth/v1/token?grant_type=refresh_token'],
    ['GET', '/authz/v1/orgs'], ['POST', '/authz/v1/authorize'], ['GET', '/authz/v1/sessions']]) {
    assert.equal(impersonationBlocked(method, url), false, `${method} ${url}`)
  }
})

test('the front refuses account changes from an impersonated token before GoTrue sees them', async t => {
  const server = createServer({ upstreamPort: 1 })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  const call = (method, path, token, scheme = 'Bearer') => fetch(`http://127.0.0.1:${server.address().port}${path}`, { method, headers: { Authorization: `${scheme} ${token}` } })
  const marked = jwt({ sub: user, session_id: session, act: { sub: 'admin:0A1B2C3D' } })
  // GoTrue reads the scheme in any letter case and serves /user/ as /user.
  for (const [path, scheme] of [['/auth/v1/user', 'Bearer'], ['/auth/v1/user', 'bearer'], ['/auth/v1/user', 'BEARER'], ['/auth/v1/user/', 'Bearer']]) {
    const response = await call('PUT', path, marked, scheme)
    assert.equal(response.status, 403, `${scheme} ${path}`)
    assert.equal((await response.json()).code, 'impersonation_restricted')
  }
  // A bearer token GoTrue would try but the front cannot read never reaches it.
  const unreadable = await call('PUT', '/auth/v1/user', 'a.!!!.c')
  assert.deepEqual([unreadable.status, (await unreadable.json()).code], [401, 'invalid_token'])
  assert.equal((await call('PUT', '/auth/v1/user', jwt({ sub: user, session_id: session }))).status, 502, 'an ordinary token goes on to GoTrue')
})

test('the token hook marks an impersonated session with act', async () => {
  const pool = { query: async (sql, args) => ({ rows: args[0] === session ? [{ actor: 'admin:0A1B2C3D' }] : [] }) }
  assert.deepEqual(await actClaims(pool, session), { act: { sub: 'admin:0A1B2C3D' } })
  assert.deepEqual(await actClaims(pool, 'other'), {})
  const base = { sub: user, session_id: session, aud: 'authenticated', role: 'authenticated' }
  assert.deepEqual((await accessTokenClaims(pool, { claims: base }, { impersonation: true })).claims, { ...base, act: { sub: 'admin:0A1B2C3D' } })
  assert.deepEqual((await accessTokenClaims(pool, { claims: base })).claims, base, 'off: never')
})

test('starting redeems a magic link, caps the session, records why, and hands over a marked session', async () => {
  const calls = [], queries = []
  const responses = {
    ['GET /admin/users/' + user]: { status: 200, data: { id: user, email: 'ada@example.com', email_confirmed_at: 'c' } },
    'POST /admin/generate_link': { status: 200, data: { hashed_token: 'hash1' } },
    'POST /verify': { status: 200, data: { access_token: jwt({ sub: user, session_id: session }), refresh_token: 'r1' } },
    'POST /token?grant_type=refresh_token': { status: 200, data: { access_token: jwt({ sub: user, session_id: session, act: { sub: 'admin:0A1B2C3D' } }), refresh_token: 'r2', expires_in: 600, expires_at: 1700000600 } },
  }
  const admin = async (method, path, body) => { calls.push([method, path, body]); return responses[`${method} ${path}`] ?? { status: 404, data: {} } }
  const pool = { query: async (sql, args) => { queries.push(args); return { rows: [{ id: 'i1', expires_at: 'soon' }] } } }
  const ctx = { pool, admin, goTrueError: r => Object.assign(new Error('gotrue'), { status: r.status }), siteURL: 'https://shop.example.com/app', keyId: '0A1B2C3D' }
  const [status, body] = await startImpersonation(ctx, user, { reason: ' Ticket 4821: billing page blank ', minutes: 15 })
  assert.equal(status, 201)
  assert.deepEqual(calls.map(([m, p]) => `${m} ${p}`), ['GET /admin/users/' + user, 'POST /admin/generate_link', 'POST /verify', 'POST /token?grant_type=refresh_token'])
  assert.deepEqual(calls[1][2], { type: 'magiclink', email: 'ada@example.com' })
  assert.deepEqual(calls[3][2], { refresh_token: 'r1' }, 'the token handed over is the refreshed, marked one')
  assert.deepEqual(queries[0], [user, session, 'admin:0A1B2C3D', 'Ticket 4821: billing page blank', 15])
  const handoff = new URL(body.url)
  assert.equal(handoff.origin + handoff.pathname, 'https://shop.example.com/app')
  const params = new URLSearchParams(handoff.hash.slice(1))
  assert.deepEqual([params.get('refresh_token'), params.get('type'), body.session_id], ['r2', 'impersonation', session])
  for (const [extra, code] of [[{ reason: '' }, 'invalid_reason'], [{ reason: 'x'.repeat(201) }, 'invalid_reason'], [{ reason: 'a\nb' }, 'invalid_reason'],
    [{ reason: 'ok', minutes: 61 }, 'invalid_minutes'], [{ reason: 'ok', minutes: 4 }, 'invalid_minutes'], [{ reason: 'ok', minutes: 1.5 }, 'invalid_minutes']]) {
    await assert.rejects(startImpersonation(ctx, user, extra), { status: 400, code }, JSON.stringify(extra).slice(0, 40))
  }
  responses['GET /admin/users/' + user].data.email_confirmed_at = null
  await assert.rejects(startImpersonation(ctx, user, { reason: 'ok' }), { status: 409, code: 'impersonation_needs_confirmed_email' })
  responses['GET /admin/users/' + user].data = { id: user, email: 'ada@example.com', email_confirmed_at: 'c', banned_until: '2999-01-01T00:00:00Z' }
  await assert.rejects(startImpersonation(ctx, user, { reason: 'ok' }), { status: 409, code: 'user_banned' })
})

test('operators list impersonations and end one early', async () => {
  const log = []
  const pool = { query: async (sql, args) => {
    log.push(sql.trim().split(/\s+/).slice(0, 2).join(' '))
    if (sql.includes('FROM gregale_auth.impersonations i JOIN auth.users')) return { rows: [{ id: 'i1', user_id: user, active: true }] }
    if (sql.startsWith('WITH ended')) return { rows: args[0] === '1b1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11' ? [{ id: session }] : [] }
    return { rowCount: 0, rows: [] }
  } }
  const route = (method, path) => impersonationsRoute({ pool }, method, path.split('?')[0].split('/').filter(Boolean), new URL('http://x' + path))
  assert.deepEqual((await route('GET', `/impersonations?user=${user}`))[1].impersonations[0].active, true)
  await assert.rejects(route('GET', '/impersonations?user=nope'), { status: 400 })
  assert.deepEqual(await route('DELETE', '/impersonations/1b1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'), [204, undefined])
  await assert.rejects(route('DELETE', '/impersonations/2b1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'), { status: 404 })
})
