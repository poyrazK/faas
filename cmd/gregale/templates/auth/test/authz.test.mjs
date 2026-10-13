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

async function serve(t, pool, { authenticate = async () => ({ sub: member, session_id: 'session' }), origins = ['https://app.example.test'], invitations } = {}) {
  const server = createServer({ upstreamPort: 1, authz: createAuthzHandler({ pool, authenticate, origins, invitations }) })
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
