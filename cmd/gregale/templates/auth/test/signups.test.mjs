import test from 'node:test'
import assert from 'node:assert/strict'
import { createHmac } from 'node:crypto'
import { disposableDomain, signupPolicy, signupRefusal, signupsRoute } from '../signups.mjs'
import { createHookServer, hookAddress, hookEnv, newHookSecret } from '../email.mjs'

const user = (email, provider = 'email') => ({ user: { email, app_metadata: { provider } } })

// fakeDB answers the access and waitlist queries from in-memory rows.
function fakeDB({ approved = [], invited = [], pending = [], capacity = 10000 } = {}) {
  const requests = new Set(pending)
  const log = []
  return {
    requests, log,
    async query(sql, args) {
      log.push(sql.trim().split(/\s+/).slice(0, 3).join(' '))
      if (sql.includes('AS allowed')) return { rows: [{ allowed: approved.includes(args[0]) || invited.includes(args[0]) }] }
      if (sql.startsWith('INSERT INTO gregale_auth.signup_access (email, requested_at)')) {
        if (requests.has(args[0]) || requests.size >= Math.min(capacity, args[1])) return { rowCount: 0 }
        requests.add(args[0]); return { rowCount: 1 }
      }
      if (sql.startsWith('SELECT 1 FROM gregale_auth.signup_access')) return { rowCount: requests.has(args[0]) ? 1 : 0 }
      throw new Error('unexpected SQL ' + sql)
    },
  }
}

test('no restrictions leave the hook off; invalid settings fail the start', () => {
  assert.equal(signupPolicy({}), null)
  assert.equal(signupPolicy({ AUTH_SIGNUP_ACCESS: 'open', AUTH_SIGNUP_BLOCK_DISPOSABLE: '0' }), null)
  const policy = signupPolicy({ AUTH_SIGNUP_ALLOWED_DOMAINS: 'Acme.com, acme.io,acme.com', AUTH_SIGNUP_ACCESS: 'waitlist' })
  assert.deepEqual([[...policy.allowed], policy.access], [['acme.com', 'acme.io'], 'waitlist'])
  for (const env of [{ AUTH_SIGNUP_ACCESS: 'closed' }, { AUTH_SIGNUP_BLOCK_DISPOSABLE: 'yes' }, { AUTH_SIGNUP_ALLOWED_DOMAINS: 'acme' }, { AUTH_SIGNUP_BLOCKED_DOMAINS: 'a b.com' },
    { AUTH_SIGNUP_BLOCKED_DOMAINS: Array.from({ length: 101 }, (_, i) => `d${i}.com`).join(',') }]) {
    assert.throws(() => signupPolicy(env), /Invalid AUTH_SIGNUP_/, JSON.stringify(env).slice(0, 60))
  }
})

test('domains: blocked with subdomains, throwaway, allowlist exact', async () => {
  const policy = signupPolicy({ AUTH_SIGNUP_BLOCKED_DOMAINS: 'spam.example', AUTH_SIGNUP_BLOCK_DISPOSABLE: '1', AUTH_SIGNUP_ALLOWED_DOMAINS: 'acme.com,mailinator.com,spam.example' })
  const db = fakeDB()
  assert.equal(disposableDomain('mailinator.com'), true)
  assert.equal(disposableDomain('eu.mailinator.com'), true, 'subdomains of a throwaway domain')
  assert.equal(disposableDomain('acme.com'), false)
  for (const [email, code] of [['ada@ACME.com', null], ['ada@x.spam.example', 'email_domain_blocked'], ['ada@mailinator.com', 'disposable_email'],
    ['ada@eng.acme.com', 'email_domain_not_allowed'], ['ada@globex.com', 'email_domain_not_allowed']]) {
    assert.deepEqual(await signupRefusal(db, policy, user(email)), code && { status: 403, code }, email)
  }
  assert.deepEqual(await signupRefusal(db, policy, { user: { phone: '15555550100' } }), { status: 403, code: 'signup_restricted' }, 'no address under an allowlist')
  assert.equal(await signupRefusal(db, signupPolicy({ AUTH_SIGNUP_BLOCK_DISPOSABLE: '1' }), { user: { is_anonymous: true } }), null, 'anonymous allowed without allowlist or access rules')
  assert.equal(db.log.length, 0, 'domain rules need no database')
  await assert.rejects(signupRefusal(db, policy, {}), { status: 400 })
})

test('invitation-only lets approved and invited addresses and SSO people in', async () => {
  const policy = signupPolicy({ AUTH_SIGNUP_ACCESS: 'invite' })
  const db = fakeDB({ approved: ['ada@acme.com'], invited: ['bob@acme.com'] })
  assert.equal(await signupRefusal(db, policy, user('Ada@acme.com')), null)
  assert.equal(await signupRefusal(db, policy, user('bob@acme.com', 'google')), null, 'OAuth sign-ups are checked by address too')
  assert.deepEqual(await signupRefusal(db, policy, user('eve@acme.com')), { status: 403, code: 'signup_invitation_required' })
  assert.equal(await signupRefusal(db, policy, user('carol@acme.com', 'sso:6a1b2c3d-0000-4000-8000-000000000001')), null, 'an identity provider vouches')
  assert.deepEqual(await signupRefusal(db, policy, { user: { is_anonymous: true } }), { status: 403, code: 'signup_restricted' })
  assert.equal(db.requests.size, 0, 'nothing joins a waitlist')
})

test('the waitlist records one request per address until it is full', async () => {
  const policy = signupPolicy({ AUTH_SIGNUP_ACCESS: 'waitlist' })
  const db = fakeDB({ capacity: 2 })
  for (const email of ['a@x.com', 'a@x.com', 'b@x.com']) assert.deepEqual(await signupRefusal(db, policy, user(email)), { status: 403, code: 'waitlisted' })
  assert.deepEqual([...db.requests], ['a@x.com', 'b@x.com'])
  assert.deepEqual(await signupRefusal(db, policy, user('c@x.com')), { status: 403, code: 'waitlist_full' })
  assert.deepEqual(await signupRefusal(db, policy, user('a@x.com')), { status: 403, code: 'waitlisted' }, 'already waiting')
})

function signed(secret, body) {
  const id = 'msg_1', timestamp = String(Math.floor(Date.now() / 1000))
  const key = Buffer.from(secret.replace(/^v1,whsec_/, ''), 'base64')
  const signature = createHmac('sha256', key).update(`${id}.${timestamp}.${body}`).digest('base64')
  return { 'webhook-id': id, 'webhook-timestamp': timestamp, 'webhook-signature': `v1,${signature}`, 'content-type': 'application/json' }
}

test('GoTrue reaches the hook only while restrictions exist, and a failure refuses', async t => {
  const secret = newHookSecret()
  assert.equal(hookEnv(secret).GOTRUE_HOOK_BEFORE_USER_CREATED_ENABLED, undefined)
  assert.equal(hookEnv(secret, { beforeUserCreated: true }).GOTRUE_HOOK_BEFORE_USER_CREATED_URI, `http://127.0.0.1:9998${hookAddress.beforeUserCreatedPath}`)
  let answer = async () => null
  const server = createHookServer({ secret, mailer: null, api: 'https://a.example/auth/v1', app: 'Shop', beforeUserCreated: payload => answer(payload) })
  server.listen(0, '127.0.0.1')
  await new Promise(resolve => server.once('listening', resolve))
  t.after(() => server.close())
  const post = async (body, headers = signed(secret, body)) => {
    const r = await fetch(`http://127.0.0.1:${server.address().port}${hookAddress.beforeUserCreatedPath}`, { method: 'POST', headers, body })
    return [r.status, await r.text()]
  }
  const body = JSON.stringify(user('ada@acme.com'))
  assert.deepEqual(await post(body), [200, '{}'])
  answer = async () => ({ status: 403, code: 'waitlisted' })
  assert.deepEqual(await post(body), [200, '{"error":{"http_code":403,"message":"waitlisted"}}'])
  answer = async () => { throw new Error('database down') }
  assert.deepEqual(await post(body), [200, '{"error":{"http_code":503,"message":"signup_check_unavailable"}}'])
  assert.equal((await post(body, { 'content-type': 'application/json' }))[0], 401, 'unsigned calls are refused')
})

test('operators list, approve with an email, and remove addresses', async () => {
  const rows = new Map([['wait@x.com', { email: 'wait@x.com', requested_at: 't', approved_at: null, notified_at: null }]])
  const sent = []
  const pool = {
    async query(sql, args) {
      if (sql.includes('FROM gregale_auth.signup_access s')) return { rows: [...rows.values()].filter(r => args[0] === 'all' || (args[0] === 'pending') === (r.approved_at === null)).map(r => ({ ...r, signed_up: false, total: 1 })) }
      if (sql.startsWith('INSERT INTO gregale_auth.signup_access (email, approved_at)')) {
        return { rows: [...new Set(args[0].map(e => e.toLowerCase()))].map(email => { const r = rows.get(email) ?? { email, requested_at: null, notified_at: null }; r.approved_at ??= 'now'; rows.set(email, r); return { email, notified_at: r.notified_at } }) }
      }
      if (sql.startsWith('UPDATE gregale_auth.signup_access SET notified_at')) { rows.get(args[0]).notified_at = 'now'; return { rows: [] } }
      if (sql.includes('FROM gregale_auth.email_templates')) return { rows: [] }
      if (sql.startsWith('DELETE FROM gregale_auth.signup_access')) return { rowCount: rows.delete(args[0]) ? 1 : 0 }
      throw new Error('unexpected SQL ' + sql)
    },
  }
  const pageParams = q => ({ page: Number(q.get('page') ?? 1), perPage: 50, search: q.get('search') ?? '' })
  const ctx = { pool, mailer: { send: async (m, id) => sent.push([m.to, m.subject, id]) }, external: 'https://shop-auth.example', app: 'Shop', pageParams, likePattern: s => `%${s}%` }
  const route = (method, path, body) => signupsRoute(ctx, method, path.split('/').filter(Boolean), new URL(`http://x${path}`), body)
  assert.deepEqual((await route('GET', '/signups?status=pending'))[1].signups.map(r => r.email), ['wait@x.com'])
  await assert.rejects(route('GET', '/signups?status=old'), { status: 400 })
  assert.deepEqual(await route('POST', '/signups/approve', { emails: ['Wait@x.com', 'new@x.com'] }), [200, { approved: 2, notified: 2 }])
  assert.deepEqual(sent.map(([to, subject]) => [to, subject]), [['wait@x.com', 'You can sign up for Shop now'], ['new@x.com', 'You can sign up for Shop now']])
  assert.deepEqual(await route('POST', '/signups/approve', { emails: ['wait@x.com'] }), [200, { approved: 1, notified: 0 }], 'notified once')
  assert.deepEqual(await route('POST', '/signups/approve', { emails: ['later@x.com'], notify: false }), [200, { approved: 1, notified: 0 }])
  for (const body of [{}, { emails: [] }, { emails: ['not an address'] }, { emails: ['a@x.com'], notify: 'yes' }, { emails: Array.from({ length: 101 }, (_, i) => `u${i}@x.com`) }]) {
    await assert.rejects(route('POST', '/signups/approve', body), { status: 400 }, JSON.stringify(body).slice(0, 40))
  }
  assert.deepEqual(await route('DELETE', '/signups/NEW%40x.com'), [204, undefined])
  await assert.rejects(route('DELETE', '/signups/new%40x.com'), { status: 404 })
})
