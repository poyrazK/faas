import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { budgets } from '../attempts.mjs'
import { limits, securityConfig } from '../config.mjs'
import { createSignInSecurity, deviceKind, deviceLabel, sessionFromAnswer, sessionFromLocation } from '../security.mjs'
import { bodyRecipients, createSends, messageBudgets, sendRoute } from '../sends.mjs'
import { routeTeam } from '../team.mjs'

const user = '11111111-2222-4333-8444-555555555555'
const factor = '66666666-7777-4888-9999-000000000000'
const jwt = claims => `${Buffer.from('{"alg":"ES256"}').toString('base64url')}.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.sig`

// memoryAttempts stands in for the database budgets with the same contract.
function memoryAttempts() {
  const counts = new Map()
  const id = key => key.toString('hex')
  const bump = ({ key, budget }) => { counts.set(id(key), (counts.get(id(key)) ?? 0) + 1); return counts.get(id(key)) }
  return {
    counts,
    async reserve(entries) { let wait = 0; for (const entry of entries) if (bump(entry) > entry.budget.limit) wait = entry.budget.seconds; return wait },
    async take(entry) { const count = bump(entry); return { count, wait: count > entry.budget.limit ? entry.budget.seconds : 0 } },
    async release(entries) { for (const { key } of entries) counts.set(id(key), Math.max(0, (counts.get(id(key)) ?? 0) - 1)) },
    async clear(entries) { for (const { key } of entries) counts.delete(id(key)) },
  }
}

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

// The fake GoTrue accepts the password "right", answers every send with 200
// unless the body asks for "refuse", and redirects /verify with a session.
async function harness(t, options) {
  const seen = []
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => {
      seen.push(`${req.method} ${req.url}`)
      const parsed = body ? JSON.parse(body) : {}
      res.setHeader('Content-Type', 'application/json')
      if (req.url.startsWith('/token')) {
        if (parsed.password === 'right' || parsed.refresh_token) return res.end(JSON.stringify({ access_token: 't', user: { id: user, email: 'ann@example.com' } }))
        res.statusCode = 400
        return res.end('{"code":400,"error_code":"invalid_credentials"}')
      }
      if (req.url.startsWith('/verify')) {
        res.writeHead(303, { Location: `https://shop.example.com/#access_token=${jwt({ sub: user, email: 'ann@example.com' })}&refresh_token=r` })
        return res.end()
      }
      if (parsed.email === 'refuse@example.com') { res.statusCode = 422; return res.end('{"error_code":"validation_failed"}') }
      res.end('{}')
    })
  })
  const front = createServer({ upstreamPort: await listen(upstream), ...options })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  return { base: `http://127.0.0.1:${port}`, seen }
}

const post = (base, path, body, address = '203.0.113.7', headers = {}) => fetch(`${base}/auth/v1${path}`, {
  method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Real-IP': address, 'User-Agent': 'Mozilla/5.0 (Macintosh; Intel Mac OS X 14_1) Firefox/120.0', ...headers }, body: JSON.stringify(body),
})

test('names what a request may send and to whom, as GoTrue reads it', () => {
  assert.deepEqual(sendRoute('POST', '/otp'), { kind: 'body' })
  assert.deepEqual(sendRoute('POST', '/recover/'), { kind: 'body' })
  assert.deepEqual(sendRoute('PUT', '/user?x=1'), { kind: 'body' })
  assert.deepEqual(sendRoute('GET', '/reauthenticate'), { kind: 'reauthenticate' })
  assert.deepEqual(sendRoute('POST', `/factors/${factor.toUpperCase()}/challenge`), { kind: 'challenge', factor })
  assert.equal(sendRoute('POST', '/token?grant_type=password'), null)
  assert.equal(sendRoute('GET', '/user'), null)
  assert.deepEqual(bodyRecipients(Buffer.from('{"EMAIL":" Ann@Example.com "}')), ['email:ann@example.com'])
  assert.deepEqual(bodyRecipients(Buffer.from('{"phone":"+1 555 0100"}')), ['phone:15550100'])
  assert.deepEqual(bodyRecipients(Buffer.from('{"password":"x"}')), [])
  assert.deepEqual(bodyRecipients(Buffer.from('not json')), [])
  assert.throws(() => bodyRecipients(Buffer.from('{"email":"a@example.com","Email":"b@example.com"}')), /ambiguous/)
})

test('reads message limits and alert settings with bounds', () => {
  assert.deepEqual(securityConfig({}), { newDeviceAlerts: true, messagesPerRecipient: limits.messagesPerRecipientDefault, messagesPerNetwork: limits.messagesPerNetworkDefault })
  assert.deepEqual(securityConfig({ AUTH_NEW_DEVICE_ALERTS: '0', AUTH_MESSAGES_PER_RECIPIENT: '3', AUTH_MESSAGES_PER_NETWORK: '100' }), { newDeviceAlerts: false, messagesPerRecipient: 3, messagesPerNetwork: 100 })
  for (const env of [{ AUTH_NEW_DEVICE_ALERTS: 'yes' }, { AUTH_MESSAGES_PER_RECIPIENT: '0' }, { AUTH_MESSAGES_PER_RECIPIENT: String(limits.messagesPerRecipientMax + 1) }, { AUTH_MESSAGES_PER_NETWORK: '1e3' }]) {
    assert.throws(() => securityConfig(env), JSON.stringify(env))
  }
})

test('message budgets hold one recipient and one network, and refusals give back', async t => {
  const attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({ messagesPerRecipient: 2, messagesPerNetwork: 3 }) })
  const { base, seen } = await harness(t, { attempts, sends })
  assert.equal((await post(base, '/otp', { email: 'ann@example.com' })).status, 200)
  assert.equal((await post(base, '/otp/', { Email: 'ANN@example.com' })).status, 200)
  const held = await post(base, '/magiclink', { email: 'ann@example.com' })
  assert.equal(held.status, 429)
  assert.deepEqual(await held.json(), { status: 429, code: 'too_many_messages', scope: 'recipient', limit: 2, observed: 2, window_seconds: limits.messageWindowSeconds, retry_after: limits.messageWindowSeconds, docs_url: 'https://gregale.dev/docs/application-auth#current-limits' })
  // GoTrue refusing a request gives its count back.
  assert.equal((await post(base, '/otp', { email: 'refuse@example.com' })).status, 422)
  assert.equal((await post(base, '/otp', { email: 'bob@example.com' })).status, 200)
  const network = await post(base, '/otp', { email: 'cat@example.com' })
  assert.equal(network.status, 429)
  assert.equal((await network.json()).scope, 'network')
  assert.equal((await post(base, '/otp', { email: 'cat@example.com' }, '198.51.100.9')).status, 200)
  assert.equal((await post(base, '/otp', { email: 'a@example.com', EMAIL: 'b@example.com' })).status, 400)
  assert.equal(seen.filter(line => line.startsWith('POST /otp') || line.startsWith('POST /magiclink')).length, 5, 'held requests never reach GoTrue')
})

test('a phone factor challenge counts against its user; an authenticator challenge does not', async () => {
  const attempts = memoryAttempts()
  const pool = { query: async (sql, args) => ({ rows: args[0] === factor ? [{ factor_type: 'phone' }] : [{ factor_type: 'totp' }] }) }
  const sends = createSends({ attempts, pool, budgets: messageBudgets({ messagesPerRecipient: 1, messagesPerNetwork: 100 }) })
  assert.deepEqual(await sends.recipients({ kind: 'challenge', factor }, Buffer.alloc(0), { sub: user }), [`user:${user}`])
  assert.deepEqual(await sends.recipients({ kind: 'challenge', factor: user }, Buffer.alloc(0), { sub: user }), [])
  assert.deepEqual(await sends.recipients({ kind: 'reauthenticate' }, Buffer.alloc(0), null), [])
  assert.equal((await sends.take([`user:${user}`], '203.0.113.7')).refused, undefined)
  assert.equal((await sends.take([`user:${user}`], '203.0.113.7')).refused.name, 'recipient')
})

test('reads who signed in from a token answer or a session redirect', () => {
  assert.deepEqual(sessionFromAnswer({ access_token: 't', user: { id: user.toUpperCase(), email: 'a@example.com' } }), { userId: user, email: 'a@example.com' })
  assert.equal(sessionFromAnswer({ user: { id: user } }), null)
  assert.deepEqual(sessionFromLocation(`https://x.example/#access_token=${jwt({ sub: user, email: '' })}&type=magiclink`), { userId: user, email: null })
  assert.equal(sessionFromLocation('https://x.example/?code=abc'), null)
  assert.equal(sessionFromLocation('https://x.example/#access_token=garbage'), null)
  assert.equal(deviceKind('Firefox/120.0 (Mac OS X 14_1)'), deviceKind('Firefox/121.0 (Mac OS X 14_2)'))
  assert.equal(deviceLabel('Mozilla/5.0 (iPhone; CPU iPhone OS 17_1) Version/17.1 Mobile Safari/604.1'), 'Safari on iOS')
  assert.equal(deviceLabel('Mozilla/5.0 (Windows NT 10.0) Chrome/120.0 Safari/537.36 Edg/120.0'), 'Edge on Windows')
  assert.equal(deviceLabel(''), 'an unknown device')
  assert.equal(deviceLabel('my-app/1.0\r\nX: y'), 'my-app/1.0 X: y')
})

// memorySeen stands in for gregale_auth.sign_in_seen and auth.users.
function memorySeen() {
  const rows = new Map(), queries = []
  const age = (key, seconds) => rows.has(key.toString('hex')) && rows.get(key.toString('hex')).at > Date.now() - seconds * 1000
  return {
    rows, queries,
    async query(sql, args) {
      queries.push(sql)
      if (sql.includes('FROM auth.users')) return { rows: args[0] === 'ann@example.com' ? [{ id: user }] : [] }
      if (sql.includes('key = ANY($1)')) return { rowCount: args[0].some(key => age(key, args[1] * 86400)) ? 1 : 0, rows: [] }
      if (sql.startsWith('SELECT')) {
        const mine = [...rows.values()].filter(row => row.user === args[0])
        const recent = (key, seconds) => mine.some(row => row.key.equals(key) && row.at > Date.now() - seconds * 1000)
        return { rows: [{ known: mine.some(row => row.at > Date.now() - args[3] * 86400000), network_seen: recent(args[1], args[3] * 86400), device_seen: recent(args[2], args[3] * 86400), fresh: recent(args[1], args[4]) && recent(args[2], args[4]) }] }
      }
      if (sql.startsWith('INSERT')) for (const key of [args[0], args[1]]) rows.set(key.toString('hex'), { key, user: args[2], at: Date.now() })
      return { rows: [] }
    },
  }
}

function memoryMailer() {
  const sent = []
  return { sent, send: async (message, key) => { sent.push({ message, key }) } }
}

test('a sign-in from a new network and a new device emails the person, once there is history', async () => {
  const pool = memorySeen(), mailer = memoryMailer()
  const security = createSignInSecurity({ pool, mailer, app: 'Shop', templates: async () => ({}), log: () => {} })
  const laptop = { userId: user, email: 'ann@example.com', address: '203.0.113.7', userAgent: 'Mozilla/5.0 (Macintosh; Mac OS X 14_1) Firefox/120.0' }
  assert.equal(await security.record(laptop), false, 'a first sign-in has nothing to compare with')
  assert.equal(await security.record({ ...laptop, address: '198.51.100.9' }), false, 'a known device on a new network')
  assert.equal(await security.record({ ...laptop, userAgent: 'Mozilla/5.0 (iPhone) Safari/604.1' }), false, 'a new device on a known network')
  assert.equal(await security.record({ ...laptop, address: '2001:db8:1:2::9', userAgent: 'Mozilla/5.0 (Windows NT 10.0) Chrome/120.0' }), true)
  assert.equal(mailer.sent.length, 1)
  assert.equal(mailer.sent[0].message.to, 'ann@example.com')
  assert.match(mailer.sent[0].message.text, /Chrome on Windows/)
  assert.match(mailer.sent[0].message.text, /2001:db8:1:2::\/64/)
  assert.match(mailer.sent[0].key, new RegExp(`^new-device-${user}-[0-9a-f]{16}-\\d{4}-\\d{2}-\\d{2}$`))
  assert.equal(await security.record({ ...laptop, address: '192.0.2.1', userAgent: 'curl/8.0', alert: false }), false, 'a refreshed session alerts no one')
  const quiet = createSignInSecurity({ pool, mailer, alerts: false, app: 'Shop', templates: async () => ({}) })
  assert.equal(await quiet.record({ ...laptop, address: '192.0.2.2', userAgent: 'Wget/1.0' }), false)
  const inserts = pool.queries.filter(sql => sql.startsWith('INSERT')).length
  await security.record(laptop)
  assert.equal(pool.queries.filter(sql => sql.startsWith('INSERT')).length, inserts, 'a fresh record is not written again')
  assert.equal(await security.trusted('email:ann@example.com', '203.0.113.7'), true)
  assert.equal(await security.trusted('email:ann@example.com', '203.0.113.99'), false)
  assert.equal(await security.trusted('email:nobody@example.com', '203.0.113.7'), false)
})

test('a spent account budget holds strangers but not a trusted network or a CAPTCHA', async t => {
  const trustedAt = new Set(['198.51.100.9'])
  const signIns = { trusted: async (subject, address) => subject === 'email:ann@example.com' && trustedAt.has(address), record: async () => false }
  for (const captcha of [false, true]) {
    const attempts = memoryAttempts()
    const { base } = await harness(t, { attempts, signIns, captcha })
    for (let i = 0; i < limits.passwordFailuresPerAccount; i++) {
      assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' }, `192.0.2.${i}`)).status, 400)
    }
    assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'right' }, '203.0.113.50')).status, captcha ? 200 : 429)
    assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'right' }, '198.51.100.9')).status, 200)
    // Signing in does not clear the account's budget for the guesser.
    assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' }, '203.0.113.51')).status, captcha ? 400 : 429)
  }
})

test('a refused attempt is given back, so held requests do not spend the account budget', async t => {
  const attempts = memoryAttempts()
  const { base } = await harness(t, { attempts })
  for (let i = 0; i < limits.passwordFailuresPerAddress + 20; i++) await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' })
  const account = [...attempts.counts.values()].sort((a, b) => b - a)
  assert.deepEqual(account, [limits.passwordFailuresPerAddress, limits.passwordFailuresPerAddress])
  assert.ok(budgets.passwordAccount.limit > limits.passwordFailuresPerAddress)
})

test('records sign-ins from token answers and session redirects, alerting except on refresh', async t => {
  const recorded = []
  const signIns = { trusted: async () => false, record: async entry => { recorded.push(entry) } }
  const { base } = await harness(t, { signIns })
  await post(base, '/token?grant_type=refresh_token', { refresh_token: 'r' })
  await post(base, '/token?grant_type=pkce', { refresh_token: 'r' })
  await fetch(`${base}/auth/v1/verify?token=x&type=magiclink`, { redirect: 'manual', headers: { 'X-Real-IP': '198.51.100.9', 'User-Agent': 'UA' } })
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(recorded.map(entry => [entry.userId, entry.alert]), [[user, false], [user, true], [user, true]])
  assert.equal(recorded[2].address, '198.51.100.9')
  assert.equal(recorded[2].email, 'ann@example.com')
})

test('an impersonated session may remove team access but not grant it', async () => {
  const org = '22222222-3333-4444-8555-666666666666'
  const run = async () => { throw new Error('must not reach SQL') }
  for (const action of ['invite', 'set_role']) {
    await assert.rejects(routeTeam({ run, method: 'POST', parts: ['orgs', org, 'team-actions'], body: { action }, impersonated: true }), { status: 403, code: 'impersonation_restricted' })
  }
  await assert.rejects(routeTeam({ run, method: 'POST', parts: ['orgs', org, 'team-actions'], body: { action: 'remove_member' }, impersonated: true }), { code: 'invalid_team_action' })
})
