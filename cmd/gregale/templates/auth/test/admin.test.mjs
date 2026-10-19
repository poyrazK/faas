import test from 'node:test'
import assert from 'node:assert/strict'
import { createPublicKey, generateKeyPairSync, verify } from 'node:crypto'
import { once } from 'node:events'
import { adminKeyHashes, createAdminHandler, deleteOwnAccount, importRecord, newAdminKey, serviceToken, signingKey } from '../admin.mjs'
import { createServer, goTrueAdminPath } from '../server.mjs'

const user = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'
const org = '6f1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'
const bcrypt = '$2a$10$' + 'N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy'
const dashboard = 'https://api.gregale.dev'

test('admin keys are random, hashed and parsed from the environment', () => {
  const { key, hash } = newAdminKey()
  assert.match(key, /^gaak_[A-Za-z0-9_-]{43}$/)
  assert.match(hash, /^[0-9a-f]{64}$/)
  assert.equal(adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash, AUTH_ADMIN_KEY_SHORT: hash, AUTH_ADMIN_KEY_0A1B2C3E: 'nothex', OTHER: hash }).length, 1)
})

test('service tokens are ES256 service_role tokens under the signing key', () => {
  const { privateKey } = generateKeyPairSync('ec', { namedCurve: 'P-256' })
  const jwk = privateKey.export({ format: 'jwk' })
  const keys = JSON.stringify([{ ...jwk, alg: 'ES256', kid: 'verifyonly01', key_ops: ['verify'] }, { ...jwk, alg: 'ES256', kid: 'signer000001', key_ops: ['sign', 'verify'] }])
  const token = serviceToken(signingKey(keys), 1700000000000)
  const [header, payload, signature] = token.split('.')
  assert.deepEqual(JSON.parse(Buffer.from(header, 'base64url')), { alg: 'ES256', typ: 'JWT', kid: 'signer000001' })
  assert.deepEqual(JSON.parse(Buffer.from(payload, 'base64url')), { role: 'service_role', iat: 1700000000, exp: 1700000060 })
  assert.equal(verify('sha256', Buffer.from(`${header}.${payload}`), { key: createPublicKey(privateKey), dsaEncoding: 'ieee-p1363' }, Buffer.from(signature, 'base64url')), true)
})

test('validates import records and refuses plain-text passwords', () => {
  assert.deepEqual(importRecord({ email: 'a@example.test', password_hash: bcrypt, email_confirmed: true, id: user, user_metadata: { name: 'A' } }),
    { email: 'a@example.test', email_confirm: true, id: user, password_hash: bcrypt, user_metadata: { name: 'A' } })
  assert.deepEqual(importRecord({ email: 'b@example.test' }), { email: 'b@example.test', email_confirm: false })
  assert.doesNotThrow(() => importRecord({ email: 'c@example.test', password_hash: '$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2g' }))
  assert.doesNotThrow(() => importRecord({ email: 'd@example.test', password_hash: '$fbscrypt$v=1,n=14,r=8,p=1,ss=Bw==,sk=a2V5$c2FsdA==$aGFzaA==' }))
  for (const [record, code] of [
    [{ email: 'a@example.test', password: 'hunter2' }, 'unknown_field'],
    [{ email: 'not-an-email' }, 'invalid_email'],
    [{ email: 'a@example.test', password_hash: 'md5:abc' }, 'unsupported_password_hash'],
    [{ email: 'a@example.test', id: 'x' }, 'invalid_id'],
    [{ email: 'a@example.test', email_confirmed: 'yes' }, 'invalid_email_confirmed'],
    [{ email: 'a@example.test', app_metadata: [] }, 'invalid_app_metadata'],
    [[], 'invalid_record'],
  ]) {
    assert.throws(() => importRecord(record), { code }, JSON.stringify(record))
  }
})

test('blocks GoTrue admin endpoints on the public path', () => {
  for (const path of ['/admin/users', '/admin', '/Admin/users', '//admin/users', '/%61dmin/users', '/invite', '/invite/', '/%zz']) assert.equal(goTrueAdminPath(path), true, path)
  for (const path of ['/user', '/token?grant_type=password', '/administrator', '/settings', '/invitee']) assert.equal(goTrueAdminPath(path), false, path)
})

function fakeAdmin(handlers) {
  const calls = []
  const admin = async (method, path, body) => {
    calls.push({ method, path, body })
    for (const [pattern, handler] of handlers) if (pattern.test(`${method} ${path}`)) return handler(body)
    return { status: 404, headers: {}, data: { error_code: 'user_not_found' } }
  }
  return { admin, calls }
}

function fakePool(answer) {
  const log = []
  return { log, query: async (sql, args) => { log.push({ sql, args }); return answer(sql, args) } }
}

async function serve(t, { admin, pool, keys }) {
  const server = createServer({ upstreamPort: 1, admin: createAdminHandler({ pool, admin, keys, origins: [dashboard] }) })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  const base = `http://127.0.0.1:${server.address().port}`
  return async (method, path, { key, body, headers = {} } = {}) => {
    const response = await fetch(base + path, { method, headers: { ...(key ? { Authorization: `Bearer ${key}` } : {}), ...(body ? { 'Content-Type': 'application/json' } : {}), ...headers }, body: body ? JSON.stringify(body) : undefined })
    const text = await response.text()
    return { status: response.status, headers: response.headers, body: text ? JSON.parse(text) : null }
  }
}

test('authenticates owner keys and answers the dashboard origin only', async t => {
  const { key, hash } = newAdminKey()
  const { admin } = fakeAdmin([[/^GET \/admin\/users\?/, () => ({ status: 200, headers: { 'x-total-count': '1' }, data: { users: [] } })]])
  const call = await serve(t, { admin, pool: fakePool(() => ({ rows: [] })), keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  assert.equal((await call('GET', '/admin/v1/users')).status, 401)
  assert.equal((await call('GET', '/admin/v1/users', { key: newAdminKey().key })).status, 401)
  const ok = await call('GET', '/admin/v1/users', { key, headers: { Origin: dashboard } })
  assert.equal(ok.status, 200)
  assert.equal(ok.headers.get('access-control-allow-origin'), dashboard)
  const elsewhere = await call('GET', '/admin/v1/users', { key, headers: { Origin: 'https://evil.example' } })
  assert.equal(elsewhere.headers.get('access-control-allow-origin'), null)
  const preflight = await call('OPTIONS', '/admin/v1/users', { headers: { Origin: dashboard, 'Access-Control-Request-Method': 'GET' } })
  assert.equal(preflight.status, 204)
  assert.equal((await call('OPTIONS', '/admin/v1/users', { headers: { Origin: 'https://evil.example' } })).status, 403)
  const disabled = await serve(t, { admin, pool: fakePool(() => ({ rows: [] })), keys: [] })
  assert.equal((await disabled('GET', '/admin/v1/users', { key })).status, 404)
})

test('lists users through GoTrue and returns only the user projection', async t => {
  const { key, hash } = newAdminKey()
  const { admin, calls } = fakeAdmin([[/^GET \/admin\/users\?/, () => ({ status: 200, headers: { 'x-total-count': '120' }, data: { users: [{ id: user, email: 'a@example.test', created_at: 'c', app_metadata: { provider: 'email', providers: ['email', 7], plan: 'pro' }, user_metadata: { secret: 'x' }, encrypted_password: 'nope' }] } })]])
  const call = await serve(t, { admin, pool: fakePool(() => ({ rows: [] })), keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  const listed = await call('GET', '/admin/v1/users?page=2&per_page=50&search=ali%25', { key })
  assert.equal(listed.status, 200)
  assert.equal(calls[0].path, '/admin/users?page=2&per_page=50&filter=ali%25')
  assert.deepEqual(listed.body, { users: [{ id: user, email: 'a@example.test', phone: null, created_at: 'c', last_sign_in_at: null, email_confirmed_at: null, banned_until: null, providers: ['email'], anonymous: false, claims: { plan: 'pro' } }], total: 120, page: 2, per_page: 50 })
  for (const bad of ['per_page=500', 'page=0', 'search=%00']) assert.equal((await call('GET', `/admin/v1/users?${bad}`, { key })).status, 400, bad)
})

test('custom claims merge into app_metadata, never GoTrue\'s own keys, within the size limit', async t => {
  const { key, hash } = newAdminKey()
  let metadata = { provider: 'email', providers: ['email'], plan: 'free', trial: true }
  const { admin, calls } = fakeAdmin([
    [/^GET \/admin\/users\//, () => ({ status: 200, headers: {}, data: { id: user, app_metadata: metadata } })],
    [/^PUT \/admin\/users\//, body => {
      metadata = { ...metadata, ...body.app_metadata }
      for (const [name, value] of Object.entries(metadata)) if (value === null) delete metadata[name]
      return { status: 200, headers: {}, data: { id: user, app_metadata: metadata } }
    }],
  ])
  const call = await serve(t, { admin, pool: fakePool(() => ({ rows: [] })), keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  const changed = await call('PATCH', `/admin/v1/users/${user}/claims`, { key, body: { set: { plan: 'pro', seats: 5, flags: { beta: true } }, unset: ['trial'] } })
  assert.deepEqual(changed.body, { claims: { plan: 'pro', seats: 5, flags: { beta: true } } })
  assert.deepEqual(calls.at(-1).body, { app_metadata: { plan: 'pro', seats: 5, flags: { beta: true }, trial: null } }, 'a merge patch; null removes')
  assert.deepEqual(metadata.providers, ['email'], "GoTrue's keys stay")
  for (const [body, code] of [
    [{ set: { providers: ['x'] } }, 'invalid_claim_name'], [{ unset: ['provider'] }, 'invalid_claim_name'], [{ set: { '1st': 1 } }, 'invalid_claim_name'],
    [{ set: { plan: null } }, 'invalid_claims'], [{ set: { plan: 'a' }, unset: ['plan'] }, 'invalid_claims'], [{}, 'invalid_claims'], [{ set: [] }, 'invalid_claims'], [{ add: {} }, 'invalid_claims'],
    [{ set: { blob: 'x'.repeat(1000) } }, 'claims_too_large'],
  ]) assert.deepEqual((await call('PATCH', `/admin/v1/users/${user}/claims`, { key, body })).body, { status: 400, code }, JSON.stringify(body).slice(0, 60))
  assert.equal(calls.filter(c => c.method === 'PUT').length, 1, 'refused changes send nothing')
})

test('signs out, bans and deletes users with organization safety', async t => {
  const { key, hash } = newAdminKey()
  let lastOwner = []
  const pool = fakePool(sql => {
    if (sql.includes("m.role = 'owner'")) return { rows: lastOwner }
    if (sql.startsWith('SELECT m.org_id')) return { rows: [{ org_id: org }] }
    if (sql.startsWith('DELETE FROM auth.sessions')) return { rows: [], rowCount: 2 }
    if (sql.startsWith('DELETE FROM gregale_auth.organizations')) return { rows: [], rowCount: 1 }
    if (sql.includes('count(*)::int AS sessions')) return { rows: [{ sessions: 3 }] }
    return { rows: [{ id: org, slug: 'acme', name: 'Acme', role: 'owner' }] }
  })
  const { admin, calls } = fakeAdmin([
    [new RegExp(`^GET /admin/users/${user}$`), () => ({ status: 200, headers: {}, data: { id: user, email: 'a@example.test' } })],
    [/^PUT \/admin\/users\//, body => ({ status: 200, headers: {}, data: { id: user, banned_until: body.ban_duration === 'none' ? null : 'later' } })],
    [/^DELETE \/admin\/users\//, () => ({ status: 200, headers: {}, data: {} })],
  ])
  const call = await serve(t, { admin, pool, keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  const detail = await call('GET', `/admin/v1/users/${user}`, { key })
  assert.equal(detail.body.active_sessions, 3)
  assert.deepEqual(detail.body.organizations, [{ id: org, slug: 'acme', name: 'Acme', role: 'owner' }])
  assert.deepEqual((await call('POST', `/admin/v1/users/${user}/sign-out`, { key, body: {} })).body, { sessions_ended: 2 })
  const banned = await call('POST', `/admin/v1/users/${user}/ban`, { key, body: { duration: '24h' } })
  assert.equal(banned.body.user.banned_until, 'later')
  assert.deepEqual(calls.at(-1).body, { ban_duration: '24h' })
  assert.equal(pool.log.filter(entry => entry.sql.startsWith('DELETE FROM auth.sessions')).length, 2)
  await call('POST', `/admin/v1/users/${user}/ban`, { key, body: { duration: 'none' } })
  assert.equal(pool.log.filter(entry => entry.sql.startsWith('DELETE FROM auth.sessions')).length, 2)
  assert.equal((await call('POST', `/admin/v1/users/${user}/ban`, { key, body: { duration: '1d' } })).body.code, 'invalid_duration')
  lastOwner = [{ slug: 'acme' }]
  assert.deepEqual((await call('DELETE', `/admin/v1/users/${user}`, { key })).body, { status: 409, code: 'last_owner', organizations: ['acme'] })
  assert.equal(calls.filter(c => c.method === 'DELETE').length, 0)
  lastOwner = []
  assert.deepEqual((await call('DELETE', `/admin/v1/users/${user}`, { key })).body, { deleted: true, organizations_deleted: 1 })
  assert.equal((await call('GET', '/admin/v1/users/not-a-uuid', { key })).status, 400)
  assert.equal((await call('GET', `/admin/v1/users/${org}`, { key })).body.code, 'not_found')
})

test('imports in bounded batches and reports skipped rows by index', async t => {
  const { key, hash } = newAdminKey()
  const { admin, calls } = fakeAdmin([[/^POST \/admin\/users$/, body => body.email.startsWith('dup') ? { status: 422, headers: {}, data: { error_code: 'email_exists', msg: 'A user with this email address has already been registered' } } : { status: 200, headers: {}, data: { id: user } }]])
  const call = await serve(t, { admin, pool: fakePool(() => ({ rows: [] })), keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  const result = await call('POST', '/admin/v1/users/import', { key, body: { users: [
    { email: 'a@example.test', password_hash: bcrypt, email_confirmed: true },
    { email: 'dup@example.test' },
    { email: 'c@example.test', password: 'plain' },
    { email: 'd@example.test' },
    { email: 'A@Example.test' },
  ] } })
  assert.deepEqual(result.body, { created: 2, skipped: [{ index: 1, code: 'email_exists' }, { index: 2, code: 'unknown_field' }, { index: 4, code: 'duplicate_in_batch' }] })
  assert.equal(calls.length, 3)
  assert.equal(JSON.stringify(result.body).includes('@'), false)
  assert.equal((await call('POST', '/admin/v1/users/import', { key, body: { users: Array.from({ length: 101 }, (_, i) => ({ email: `u${i}@example.test` })) } })).body.code, 'invalid_batch')
})

test('lists organizations and shows members and pending invitations', async t => {
  const { key, hash } = newAdminKey()
  const pool = fakePool(sql => {
    if (sql.includes('count(*) OVER ()')) return { rows: [{ id: org, slug: 'acme', name: 'Acme', created_at: 'c', members: 2, total: 7 }] }
    if (sql.startsWith('SELECT id, slug, name, created_at FROM gregale_auth.organizations')) return { rows: [{ id: org, slug: 'acme', name: 'Acme', created_at: 'c' }] }
    if (sql.includes('FROM gregale_auth.memberships m JOIN auth.users')) return { rows: [{ user_id: user, email: 'a@example.test', role: 'owner', created_at: 'c' }] }
    return { rows: [{ id: user, email: 'b@example.test', role: 'member', expires_at: 'e', created_at: 'c' }] }
  })
  const { admin } = fakeAdmin([])
  const call = await serve(t, { admin, pool, keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  const listed = await call('GET', '/admin/v1/orgs?search=a_c', { key })
  assert.deepEqual(listed.body, { organizations: [{ id: org, slug: 'acme', name: 'Acme', created_at: 'c', members: 2 }], total: 7, page: 1, per_page: 50 })
  assert.deepEqual(pool.log[0].args, ['a_c', '%a\\_c%', 50, 0])
  const detail = await call('GET', `/admin/v1/orgs/${org}`, { key })
  assert.equal(detail.body.members[0].role, 'owner')
  assert.equal(detail.body.invitations[0].email, 'b@example.test')
  assert.equal((await call('GET', '/admin/v1/orgs/nope', { key })).status, 400)
})

test('lists sign-in activity newest first, for everyone or one user', async t => {
  const { key, hash } = newAdminKey()
  const entry = i => ({ id: `e${i}`, created_at: `t${i}`, action: 'login', actor: 'user', user_id: user, email: 'a@example.test' })
  const pool = fakePool(() => ({ rows: [entry(1), entry(2), entry(3)] }))
  const call = await serve(t, { admin: fakeAdmin([]).admin, pool, keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  const listed = await call('GET', `/admin/v1/activity?user=${user}&per_page=2&page=3&search=log%25in`, { key })
  assert.equal(listed.status, 200)
  // One extra row tells the page there is more, without counting the log.
  assert.deepEqual(listed.body, { entries: [entry(1), entry(2)], more: true, page: 3, per_page: 2 })
  const { sql, args } = pool.log.at(-1)
  assert.match(sql, /FROM auth\.audit_log_entries/)
  assert.match(sql, /ORDER BY created_at DESC/)
  assert.deepEqual(args, [user, 'log%in', '%log\\%in%', 3, 4, '00000000-0000-0000-0000-000000000000'])
  // An administrator's action counts against the affected user.
  assert.match(sql, /payload->'traits'->>'user_id' = \$1/)
  assert.deepEqual((await call('GET', '/admin/v1/activity', { key })).body.more, false)
  assert.equal(pool.log.at(-1).args[0], '')
  assert.equal((await call('GET', '/admin/v1/activity?user=not-a-uuid', { key })).status, 400)
  assert.equal((await call('GET', '/admin/v1/activity')).status, 401)
})

test("deletes a user's own account only after a recent sign-in with any second step", async () => {
  const session = '5c1b9d2e-0b5f-4d8f-8a3a-7a512f0e6f70'
  let check = [{ allowed: true }]
  const pool = fakePool(sql => {
    if (sql.includes('make_interval')) return { rows: check }
    if (sql.includes("m.role = 'owner'")) return { rows: [] }
    if (sql.startsWith('SELECT m.org_id')) return { rows: [] }
    throw new Error(`unexpected ${sql}`)
  })
  const { admin, calls } = fakeAdmin([[new RegExp(`^DELETE /admin/users/${user}$`), () => ({ status: 200, headers: {}, data: {} })]])
  const claims = { sub: user, session_id: session }
  assert.deepEqual(await deleteOwnAccount({ pool, admin }, claims), [200, { deleted: true, organizations_deleted: 0 }])
  assert.deepEqual(pool.log[0].args, [user, session, 600], 'the session of the token, within AuthReauthenticationSeconds')
  assert.match(pool.log[0].sql, /is_anonymous OR/)
  assert.match(pool.log[0].sql, /aal = 'aal2' OR NOT EXISTS/)
  check = [{ allowed: false }]
  await assert.rejects(deleteOwnAccount({ pool, admin }, claims), { status: 403, code: 'reauthentication_required' })
  check = []
  await assert.rejects(deleteOwnAccount({ pool, admin }, claims), { status: 401, code: 'not_authenticated' })
  assert.equal(calls.length, 1, 'refusals never reach GoTrue')
})

test('serves email templates behind the admin key', async t => {
  const { key, hash } = newAdminKey()
  const call = await serve(t, { admin: fakeAdmin([]).admin, pool: fakePool(() => ({ rows: [] })), keys: adminKeyHashes({ AUTH_ADMIN_KEY_0A1B2C3D: hash }) })
  assert.equal((await call('GET', '/admin/v1/emails')).status, 401)
  const listed = await call('GET', '/admin/v1/emails', { key })
  assert.equal(listed.status, 200)
  assert.equal(listed.body.emails.length, 8)
  assert.equal((await call('PATCH', '/admin/v1/emails/signup', { key, body: { body: '{{nope}}' } })).body.code, 'unknown_placeholder')
})
