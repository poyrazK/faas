import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createHash } from 'node:crypto'
import { createAuthzHandler, hashToken, HTTPError } from '../authz.mjs'
import { createServer } from '../server.mjs'

const org = '6f1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'
const member = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'
const token = 'eyJhbGciOiJFUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln'

function fakePool(respond) {
  const log = []
  return {
    log,
    async connect() {
      return {
        async query(sql, args) {
          log.push({ sql, args })
          if (/^(BEGIN|COMMIT|ROLLBACK)$/.test(sql) || sql.includes('set_config')) return { rows: [] }
          return respond(sql, args)
        },
        release() { log.push({ sql: 'release' }) },
      }
    },
  }
}

async function serve(t, pool, { authenticate = async () => ({ sub: member, session_id: 'session' }), origins = ['https://app.example.test'], invitations, storage, workflows, teamEnabled = false, orgClaims = false } = {}) {
  const server = createServer({ upstreamPort: 1, authz: createAuthzHandler({ pool, authenticate, origins, invitations, storage, workflows, teamEnabled, orgClaims }) })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  const base = `http://127.0.0.1:${server.address().port}/authz/v1`
  return async (method, path, { body, headers = {} } = {}) => {
    const response = await fetch(base + path, { method, headers: { Authorization: `Bearer ${token}`, ...(body ? { 'Content-Type': 'application/json' } : {}), ...headers }, body: body ? JSON.stringify(body) : undefined })
    const text = await response.text()
    return { status: response.status, headers: response.headers, body: text ? JSON.parse(text) : null }
  }
}

test('team endpoints are hidden before authentication and preflight unless explicitly enabled', async t => {
  const pool = fakePool(() => { throw new Error('must not query') })
  let authenticated = 0
  const call = await serve(t, pool, { authenticate: async () => { authenticated++; throw new Error('must not authenticate') } })
  for (const [method, path] of [['GET', `/orgs/${org}/team`], ['POST', `/orgs/${org}/team-actions`], ['GET', `/orgs/${org}/team-actions/${member}`], ['OPTIONS', `/orgs/${org}/team-actions`]]) {
    assert.equal((await call(method, path)).status, 404)
  }
  assert.equal(authenticated, 0); assert.equal(pool.log.length, 0)
})

test('team actions reject ambiguous fields, unsafe context, and malformed cursors before SQL', async t => {
  const pool = fakePool(() => { throw new Error('must not query') })
  const call = await serve(t, pool, { teamEnabled: true })
  const action = { id: member, action: 'invite', email: 'friend@example.test', role: 'member', expected_snapshot: 'a'.repeat(64) }
  for (const patch of [{ tenant_id: org }, { context: { token: 'secret' } }, { context: { product_id: 'wrong' } }, { expires_in: 1 }, { target_id: member }, { role: 'Owner' }, { email: 'a@b\nsecret' }, { email: `${'é'.repeat(200)}@example.test` }, { action: 'set_role', target_id: member }]) {
    const value = await call('POST', `/orgs/${org}/team-actions`, { body: { ...action, ...patch } })
    assert.equal(value.status, 400, JSON.stringify(patch)); assert.ok(!JSON.stringify(value.body).includes('secret'))
  }
  for (const query of ['cursor=wrong', 'cursor=a&cursor=b', 'tenant_id=foreign']) assert.equal((await call('GET', `/orgs/${org}/team-actions?${query}`)).status, 400)
  assert.equal(pool.log.filter(entry => entry.sql.includes('gregale_auth.')).length, 0)
})

test('team reads use verified claims and a scoped transaction and hide upstream failures', async t => {
  const value = { actor_id: member, actor_role: 'owner', members: [], invitations: [], roles: [] }
  const pool = fakePool((sql, args) => {
    assert.match(sql, /gregale_auth.team_state/); assert.equal(args[0], org)
    return { rows: [{ state: value }] }
  })
  const call = await serve(t, pool, { teamEnabled: true })
  const response = await call('GET', `/orgs/${org}/team`)
  assert.equal(response.status, 200); assert.equal(response.body.protocol, 1); assert.match(response.body.snapshot, /^[a-f0-9]{64}$/)
  assert.deepEqual(JSON.parse(pool.log[1].args[0]), { sub: member, session_id: 'session' })
  assert.equal(response.headers.get('cache-control'), 'no-store')
  const bad = await serve(t, fakePool(() => { throw new Error('database password and token') }), { teamEnabled: true })
  assert.deepEqual((await bad('GET', `/orgs/${org}/team`)).body, { status: 500, code: 'internal_error' })
})

test('authenticates before any database work', async t => {
  const pool = fakePool(() => ({ rows: [] }))
  const call = await serve(t, pool, { authenticate: async () => { throw new HTTPError(401, 'not_authenticated') } })
  assert.equal((await call('GET', '/orgs')).status, 401)
  assert.equal((await call('GET', '/orgs', { headers: { Authorization: 'Bearer not-a-jwt' } })).status, 401)
  assert.equal((await call('GET', '/orgs', { headers: { Authorization: '' } })).status, 401)
  assert.equal(pool.log.length, 0)
})

test('runs each call in one transaction scoped to the caller claims', async t => {
  const pool = fakePool(() => ({ rows: [{ id: org, slug: 'acme', name: 'Acme', role: 'owner' }] }))
  const call = await serve(t, pool)
  const response = await call('GET', '/orgs')
  assert.equal(response.status, 200)
  assert.equal(response.body.organizations[0].slug, 'acme')
  const statements = pool.log.map(entry => entry.sql)
  assert.deepEqual(statements.slice(0, 2), ['BEGIN', "SELECT set_config('request.jwt.claims', $1, true)"])
  assert.deepEqual(JSON.parse(pool.log[1].args[0]), { sub: member, session_id: 'session' })
  assert.match(statements[2], /gregale_auth\.list_organizations\(\)/)
  assert.deepEqual(statements.slice(3), ['COMMIT', 'release'])
})

test('maps stable SQL errors and hides everything else', async t => {
  let failure
  const pool = fakePool(() => { throw failure })
  const call = await serve(t, pool)
  for (const [error, status, code] of [
    [{ code: '23505', message: 'slug_taken' }, 409, 'slug_taken'],
    [{ code: '42501', message: 'forbidden' }, 403, 'forbidden'],
    [{ code: '23514', message: 'last_owner' }, 409, 'last_owner'],
    [{ code: '28000', message: 'not_authenticated' }, 401, 'not_authenticated'],
    [{ code: '42501', message: 'permission denied for table memberships' }, 403, 'invalid_request'],
    [{ code: 'XX000', message: 'internal detail with secrets' }, 500, 'internal_error'],
  ]) {
    failure = Object.assign(new Error(error.message), { code: error.code })
    const response = await call('POST', '/orgs', { body: { slug: 'acme', name: 'Acme' } })
    assert.equal(response.status, status, error.message)
    assert.deepEqual(response.body, { status, code })
  }
  assert.ok(pool.log.some(entry => entry.sql === 'ROLLBACK'))
})

test('validates input before calling SQL', async t => {
  const pool = fakePool(() => ({ rows: [{}] }))
  const call = await serve(t, pool)
  assert.equal((await call('POST', '/orgs', { body: { name: 'Acme' } })).body.code, 'invalid_slug')
  assert.equal((await call('GET', '/orgs/not-a-uuid/members')).body.code, 'invalid_id')
  assert.equal((await call('PATCH', `/orgs/${org}/members/${member}`, { body: { role: '' } })).body.code, 'invalid_role')
  assert.equal((await call('POST', `/orgs/${org}/invitations`, { body: { email: 'a@example.test', role: 'member', expires_in: 1.5 } })).body.code, 'invalid_expires_in')
  assert.equal((await call('POST', '/invitations/accept', { body: { token: 'short' } })).body.code, 'invalid_token')
  assert.equal((await call('GET', '/nowhere')).status, 404)
  assert.equal(pool.log.filter(entry => entry.sql.includes('gregale_auth.')).length, 0)
})

test('rejects non-JSON bodies', async t => {
  const call = await serve(t, fakePool(() => ({ rows: [{}] })))
  const response = await call('POST', '/orgs', { headers: { 'Content-Type': 'text/plain' } })
  assert.equal(response.status, 415)
})

test('returns an invitation token once and stores only its hash', async t => {
  const pool = fakePool((sql, args) => ({ rows: [{ id: member, email: args[1], role: args[2], expires_at: 'later' }] }))
  const call = await serve(t, pool)
  const response = await call('POST', `/orgs/${org}/invitations`, { body: { email: 'bob@example.test', role: 'member' } })
  assert.equal(response.status, 201)
  assert.match(response.body.token, /^[A-Za-z0-9_-]{43}$/)
  const invite = pool.log.find(entry => entry.sql.includes('gregale_auth.invite('))
  const expected = createHash('sha256').update(Buffer.from(response.body.token, 'base64url')).digest()
  assert.deepEqual(invite.args[3], expected)
  assert.equal(invite.args[4], 604800)
  assert.ok(!JSON.stringify(invite.args).includes(response.body.token))
  assert.deepEqual(hashToken(response.body.token), expected)
})

test('answers authorization checks', async t => {
  const pool = fakePool(() => ({ rows: [{ allowed: true }] }))
  const call = await serve(t, pool)
  const response = await call('POST', '/authorize', { body: { action: 'read', resource: `org/${org}/doc/1` } })
  assert.deepEqual(response.body, { allowed: true })
  assert.deepEqual(pool.log.find(entry => entry.sql.includes('authorize(')).args, ['read', `org/${org}/doc/1`])
})

test('allows browser calls only from the application origins', async t => {
  const call = await serve(t, fakePool(() => ({ rows: [] })))
  const allowed = await call('OPTIONS', '/orgs', { headers: { Origin: 'https://app.example.test' } })
  assert.equal(allowed.status, 204)
  assert.equal(allowed.headers.get('access-control-allow-origin'), 'https://app.example.test')
  const denied = await call('OPTIONS', '/orgs', { headers: { Origin: 'https://evil.example.test' } })
  assert.equal(denied.status, 403)
  const plain = await call('GET', '/orgs', { headers: { Origin: 'https://evil.example.test' } })
  assert.equal(plain.headers.get('access-control-allow-origin'), null)
})

test('emails invitations through the provider and withholds the token', async t => {
  const sent = []
  const pool = fakePool((sql, args) => ({ rows: [{ id: member, email: args[1], role: args[2], expires_at: 'later', org_name: 'Acme' }] }))
  const call = await serve(t, pool, { invitations: { mailer: { send: async (m, key) => { sent.push({ m, key }) } }, external: 'https://shop-auth.gregale.app', app: 'shop.example.test' } })
  const response = await call('POST', `/orgs/${org}/invitations`, { body: { email: 'bob@example.test', role: 'member' } })
  assert.equal(response.status, 201)
  assert.equal(response.body.delivered, true)
  assert.equal(response.body.token, undefined)
  assert.equal(response.body.org_name, undefined)
  assert.equal(sent[0].m.to, 'bob@example.test')
  assert.match(sent[0].m.text, /^https:\/\/shop-auth\.gregale\.app\/ui\/invitation#token=[A-Za-z0-9_-]{43}$/m)
  // The email goes out before the invitation commits.
  const order = pool.log.map(entry => entry.sql)
  assert.ok(order.indexOf('COMMIT') > order.findIndex(sql => sql.includes('gregale_auth.invite(')))
})

test('a failed invitation email rolls the invitation back', async t => {
  const pool = fakePool((sql, args) => ({ rows: [{ id: member, email: args[1], role: args[2], expires_at: 'later', org_name: 'Acme' }] }))
  const { EmailError } = await import('../email.mjs')
  const call = await serve(t, pool, { invitations: { mailer: { send: async () => { throw new EmailError(503, 'email_provider_unavailable') } }, external: 'https://x.test', app: 'x' } })
  const response = await call('POST', `/orgs/${org}/invitations`, { body: { email: 'bob@example.test', role: 'member' } })
  assert.deepEqual(response.body, { status: 503, code: 'email_provider_unavailable' })
  assert.ok(pool.log.some(entry => entry.sql === 'ROLLBACK'))
  assert.ok(!pool.log.some(entry => entry.sql === 'COMMIT'))
})

test('signs object URLs only after authorize allows the operation', async t => {
  let allowed = true
  const pool = fakePool(() => ({ rows: [{ allowed }] }))
  const storage = new Map([['uploads', { endpoint: new URL('https://s3.gregale.dev'), region: 'us-east-1', bucket: 'b-123', accessKeyId: 'AK', secretAccessKey: 'SK', virtualHosted: false }]])
  const call = await serve(t, pool, { storage })
  const key = `org/${org}/reports/q3.pdf`
  const read = await call('POST', '/storage/sign', { body: { bucket: 'uploads', key } })
  assert.equal(read.status, 200)
  assert.equal(read.body.method, 'GET')
  assert.equal(read.body.expires_in, 60)
  assert.match(read.body.url, /^https:\/\/s3\.gregale\.dev\/b-123\/org\/[0-9a-f-]+\/reports\/q3\.pdf\?X-Amz-Algorithm=AWS4-HMAC-SHA256&.*X-Amz-Expires=60&.*X-Amz-Signature=[0-9a-f]{64}$/)
  assert.doesNotMatch(read.body.url, /SK/)
  assert.deepEqual(pool.log.find(entry => entry.sql.includes('authorize(')).args, ['storage:read', `org/${org}/uploads/reports/q3.pdf`])
  const write = await call('POST', '/storage/sign', { body: { bucket: 'uploads', key, operation: 'write' } })
  assert.equal(write.body.method, 'PUT')
  allowed = false
  assert.equal((await call('POST', '/storage/sign', { body: { bucket: 'uploads', key } })).status, 403)
  assert.equal((await call('POST', '/storage/sign', { body: { bucket: 'other', key } })).body.code, 'unknown_bucket')
  assert.equal((await call('POST', '/storage/sign', { body: { bucket: 'uploads', key: 'no-org/x' } })).body.code, 'invalid_key')
  assert.equal((await call('POST', '/storage/sign', { body: { bucket: 'uploads', key, operation: 'delete' } })).body.code, 'invalid_operation')
})

test('starts workflows only when authorized and configured', async t => {
  let allowed = true
  const starts = []
  const pool = fakePool(() => ({ rows: [{ allowed }] }))
  const call = await serve(t, pool, { workflows: async request => { starts.push(request); return { accepted: true, duplicate: false, source: 'app.x' } } })
  const response = await call('POST', '/workflows/onboard/start', { body: { org, input: { plan: 'pro' }, idempotency_key: 'req-1' } })
  assert.equal(response.status, 202)
  assert.deepEqual(starts[0], { name: 'onboard', org, user: member, input: { plan: 'pro' }, idempotencyKey: 'req-1' })
  assert.deepEqual(pool.log.find(entry => entry.sql.includes('authorize(')).args, ['workflow:start', `org/${org}/workflow/onboard`])
  assert.equal((await call('POST', '/workflows/onboard/start', { body: { org, idempotency_key: 'has space' } })).body.code, 'invalid_idempotency_key')
  assert.equal((await call('POST', '/workflows/Bad Name/start', { body: { org } })).status, 404)
  allowed = false
  assert.equal((await call('POST', '/workflows/onboard/start', { body: { org } })).status, 403)
  assert.equal(starts.length, 1)
  const unconfigured = await serve(t, fakePool(() => ({ rows: [{ allowed: true }] })))
  assert.equal((await unconfigured('POST', '/workflows/onboard/start', { body: { org } })).body.code, 'workflows_unconfigured')
})

test('chooses the session organization only when organization claims are on', async t => {
  const pool = fakePool((sql, args) => sql.includes('set_active_organization') ? { rows: args[0] ? [{ id: args[0], slug: 'acme', role: 'member' }] : [] } : { rows: [] })
  const off = await serve(t, pool)
  assert.equal((await off('POST', '/active-organization', { body: { org } })).status, 404)
  const on = await serve(t, pool, { orgClaims: true })
  assert.deepEqual((await on('POST', '/active-organization', { body: { org } })).body, { organization: { id: org, slug: 'acme', role: 'member' } })
  assert.deepEqual((await on('POST', '/active-organization', { body: { org: null } })).body, { organization: null })
  assert.deepEqual(pool.log.filter(q => q.sql.includes('set_active_organization')).map(q => q.args), [[org], [null]])
  assert.equal((await on('POST', '/active-organization', { body: { org: 'acme' } })).status, 400)
  assert.equal((await on('POST', '/active-organization', { body: {} })).status, 400)
})
