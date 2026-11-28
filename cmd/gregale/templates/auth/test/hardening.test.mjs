import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { budgets } from '../attempts.mjs'
import { limits, securityConfig } from '../config.mjs'
import { createSignInSecurity, deviceKind, deviceLabel, serverAgent, sessionFromAnswer, sessionFromLocation } from '../security.mjs'
import { bodyRecipients, createSends, inbox, messageBudgets, sendRoute } from '../sends.mjs'
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
    async check(entries) { return entries.some(entry => (counts.get(id(entry.key)) ?? 0) >= entry.budget.limit) ? entries[0].budget.seconds : 0 },
    async fail(entries) { for (const entry of entries) bump(entry) },
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
      if (req.url.startsWith('/signup')) return res.end(JSON.stringify({ access_token: 't', user: { id: user, email: parsed.email } }))
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
  assert.deepEqual(sendRoute('PUT', '/user?x=1'), { kind: 'body', user: true })
  assert.deepEqual(sendRoute('GET', '/reauthenticate'), { kind: 'reauthenticate' })
  assert.deepEqual(sendRoute('POST', `/factors/${factor.toUpperCase()}/challenge`), { kind: 'challenge', factor })
  assert.equal(sendRoute('POST', `/factors/${'-'.repeat(36)}/challenge`), null)
  assert.equal(sendRoute('POST', '/token?grant_type=password'), null)
  assert.equal(sendRoute('GET', '/user'), null)
  assert.deepEqual(bodyRecipients(Buffer.from('{"EMAIL":" Ann@Example.com "}')), ['email:ann@example.com'])
  assert.deepEqual(bodyRecipients(Buffer.from('{"phone":"+1 555 0100"}')), ['phone:15550100'])
  assert.deepEqual(bodyRecipients(Buffer.from('{"password":"x"}')), [])
  assert.deepEqual(bodyRecipients(Buffer.from('not json')), [])
  assert.throws(() => bodyRecipients(Buffer.from('{"email":"a@example.com","Email":"b@example.com"}')), /ambiguous/)
})

test('reads message limits and alert settings with bounds', () => {
  assert.deepEqual(securityConfig({}), { newDeviceAlerts: true, messagesPerRecipient: limits.messagesPerRecipientDefault, messagesPerNetwork: null, logDays: limits.securityLogDaysDefault })
  assert.equal(messageBudgets(securityConfig({})).network, null, 'the network budget is off unless set')
  assert.deepEqual(securityConfig({ AUTH_NEW_DEVICE_ALERTS: '0', AUTH_MESSAGES_PER_RECIPIENT: '3', AUTH_MESSAGES_PER_NETWORK: '100' }), { newDeviceAlerts: false, messagesPerRecipient: 3, messagesPerNetwork: 100, logDays: limits.securityLogDaysDefault })
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
  assert.deepEqual(await held.json(), { status: 429, code: 'too_many_messages', error_code: 'too_many_messages', scope: 'recipient', limit: 2, observed: 2, window_seconds: limits.messageWindowSeconds, retry_after: limits.messageWindowSeconds, docs_url: 'https://gregale.dev/docs/application-auth#current-limits' })
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
  // Counted as it arrives, so a parallel burst cannot all pass; GoTrue
  // refusing a forged token gives the count back (forwardSend).
  const first = await sends.take([`user:${user}`], '203.0.113.7')
  assert.equal(first.refused, undefined)
  assert.equal((await sends.take([`user:${user}`], '203.0.113.7')).refused.name, 'recipient')
  await sends.release(first.taken)
  assert.equal((await sends.take([`user:${user}`], '203.0.113.7')).refused, undefined)
  assert.deepEqual(await sends.recipients({ kind: 'body', user: true }, Buffer.from('{"email":"new@example.com"}'), { sub: user }), ['email:new@example.com', `user:${user}`])
  assert.deepEqual(await sends.recipients({ kind: 'body', user: true }, Buffer.from('{"password":"x"}'), { sub: user }), [])
})

test('a spent recipient budget still lets the account\'s trusted network through', async () => {
  const attempts = memoryAttempts()
  const trusted = async (recipient, address) => recipient === 'email:ann@example.com' && address === '198.51.100.9'
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({ messagesPerRecipient: 1 }), trusted })
  assert.deepEqual((await sends.take(['email:ann@example.com'], '203.0.113.1')).taken.map(entry => entry.name), ['recipient'], 'no network budget unless set')
  assert.equal((await sends.take(['email:ann@example.com'], '203.0.113.2')).refused.name, 'recipient')
  assert.equal((await sends.take(['email:ann@example.com'], '198.51.100.9')).refused, undefined)
  assert.equal((await sends.take(['email:bob@example.com'], '198.51.100.9')).refused, undefined)
  assert.equal((await sends.take(['email:bob@example.com'], '198.51.100.9')).refused.name, 'recipient')
})

test('reads who signed in from a token answer or a session redirect', () => {
  assert.deepEqual(sessionFromAnswer({ access_token: 't', user: { id: user.toUpperCase(), email: 'a@example.com' } }), { userId: user, email: 'a@example.com' })
  assert.equal(sessionFromAnswer({ user: { id: user } }), null)
  assert.equal(sessionFromAnswer({ access_token: jwt({ sub: user, act: { sub: 'admin:0A1B2C3D' } }), user: { id: user } }), null, 'support sessions are not the person')
  assert.equal(sessionFromLocation(`https://x.example/#access_token=${jwt({ sub: user, act: { sub: 'admin:0A1B2C3D' } })}`), null)
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
function memorySeen(users = { 'ann@example.com': user }) {
  const rows = new Map(), queries = [], markers = new Set()
  const recent = (row, seconds) => row.at > Date.now() - seconds * 1000
  return {
    rows, queries,
    async query(sql, args) {
      queries.push(sql)
      if (sql.includes('FROM auth.users')) return { rows: users[args[0]] ? [{ id: users[args[0]] }] : [] }
      if (sql.includes('WHERE network = $1 AND trusted')) return { rowCount: [...rows.values()].some(row => row.network?.equals(args[0]) && row.trusted && recent(row, args[1] * 86400)) ? 1 : 0, rows: [] }
      if (sql.includes('key = ANY($1)')) {
        const accounts = new Set([...rows.values()].filter(row => row.network?.equals(args[2]) && recent(row, args[1] * 86400)).map(row => row.user))
        const hit = args[0].some(key => { const row = rows.get(key.toString('hex')); return row?.trusted && recent(row, args[1] * 86400) })
        return { rowCount: hit && accounts.size <= args[3] ? 1 : 0, rows: [] }
      }
      if (sql.startsWith('SELECT')) {
        const mine = [...rows.values()].filter(row => row.user === args[0])
        const has = (key, seconds, trusted = false) => mine.some(row => row.key.equals(key) && recent(row, seconds) && (row.trusted || !trusted))
        const days = args[3] * 86400
        return { rows: [{ known: mine.some(row => row.trusted && recent(row, days)), network_seen: has(args[1], days, true), device_seen: has(args[2], days, true), network_recorded: has(args[1], days), device_recorded: has(args[2], days),
          fresh_trusted: has(args[1], args[4], true) && has(args[2], args[4], true), fresh: has(args[1], args[4]) && has(args[2], args[4]) }] }
      }
      if (sql.startsWith('DELETE FROM gregale_auth.attempt_budgets')) { markers.delete(args[0].toString('hex')); return { rows: [] } }
      if (sql.startsWith('DELETE FROM gregale_auth.sign_in_seen')) { rows.delete(args[0].toString('hex')); return { rows: [] } }
      if (sql.startsWith('UPDATE gregale_auth.sign_in_seen')) { const row = rows.get(args[0].toString('hex')); if (row) row.trusted = false; return { rows: [] } }
      if (sql.includes('INSERT INTO gregale_auth.attempt_budgets')) {
        const fresh = !markers.has(args[0].toString('hex'))
        markers.add(args[0].toString('hex'))
        return { rowCount: fresh ? 1 : 0, rows: [] }
      }
      if (sql.startsWith('INSERT')) {
        for (const [key, network] of [[args[0], args[4]], [args[1], null]]) {
          const trusted = args[5]
          const old = rows.get(key.toString('hex'))
          rows.set(key.toString('hex'), { key, user: args[2], network, trusted: Boolean(old?.trusted || trusted), at: Date.now() })
        }
      }
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
  assert.equal(await security.record({ ...laptop, address: '192.0.2.1', userAgent: 'curl/8.0', alert: false, trust: false }), false, 'a redirect alone alerts no one')
  assert.equal(await security.record({ ...laptop, address: '192.0.2.1', userAgent: 'curl/8.0', trust: 'known' }), true, 'nor counts as seen: the refresh after it alerts')
  const racing = await Promise.all([1, 2].map(() => security.record({ ...laptop, address: '192.0.2.3', userAgent: 'Lynx/2.9' })))
  assert.deepEqual(racing.sort(), [false, true], 'one alert per device and day')
  const quiet = createSignInSecurity({ pool, mailer, alerts: false, app: 'Shop', templates: async () => ({}) })
  const sentBefore = mailer.sent.length
  assert.equal(await quiet.record({ ...laptop, address: '192.0.2.2', userAgent: 'Wget/1.0' }), 'noted', 'with alerts off a new device is noted, not emailed')
  assert.equal(mailer.sent.length, sentBefore)
  const inserts = pool.queries.filter(sql => sql.startsWith('INSERT')).length
  await security.record(laptop)
  assert.equal(pool.queries.filter(sql => sql.startsWith('INSERT')).length, inserts, 'a fresh record is not written again')
  assert.equal(await security.trusted('email:ann@example.com', '203.0.113.7'), true)
  assert.equal(await security.trusted(`user:${user}`, '203.0.113.7'), true)
  assert.equal(await security.trusted('email:ann@example.com', '203.0.113.99'), false)
  assert.equal(await security.trusted('email:nobody@example.com', '203.0.113.7'), false)
})

test('a refresh trusts only a network the account was already seen on', async () => {
  const pool = memorySeen(), mailer = memoryMailer()
  const security = createSignInSecurity({ pool, mailer, app: 'Shop', templates: async () => ({}), log: () => {} })
  const home = { userId: user, email: 'ann@example.com', address: '203.0.113.7', userAgent: 'Firefox/1 (Mac OS X)' }
  await security.record(home)
  assert.equal(await security.record({ ...home, address: '198.51.100.66', userAgent: 'curl/8', trust: 'known' }), true, 'a stolen refresh token from a new network and device alerts')
  assert.equal(await security.trusted('email:ann@example.com', '198.51.100.66'), false, 'and gains no trust')
  await security.record({ ...home, address: '198.51.100.70', trust: false })
  await security.record({ ...home, address: '198.51.100.70', trust: 'known' })
  assert.equal(await security.trusted('email:ann@example.com', '198.51.100.70'), true, 'a session the browser got by redirect and then used')
})

test('two refreshes from a new network still grant no trust', async () => {
  const pool = memorySeen()
  const security = createSignInSecurity({ pool, app: 'Shop', templates: async () => ({}) })
  const home = { userId: user, email: 'ann@example.com', address: '203.0.113.7', userAgent: 'Firefox (Mac)' }
  await security.record(home)
  for (let i = 0; i < 2; i++) await security.record({ ...home, address: '198.51.100.66', userAgent: 'curl/8', trust: 'known' })
  assert.equal(await security.trusted('email:ann@example.com', '198.51.100.66'), false)
})

test('plus tags and Gmail dots reach one inbox and share its budget', () => {
  assert.equal(inbox('email:v.ic.tim+1@gmail.com'), 'email:victim@gmail.com')
  assert.equal(inbox('email:victim@googlemail.com'), 'email:victim@gmail.com')
  assert.equal(inbox('email:a.b+x@example.com'), 'email:a.b@example.com')
  assert.equal(inbox('email:+x@example.com'), 'email:+x@example.com')
  assert.equal(inbox('phone:15550100'), 'phone:15550100')
})

test('a refreshed session from an unrecorded network alerts once, not daily', async () => {
  const pool = memorySeen(), mailer = memoryMailer()
  const security = createSignInSecurity({ pool, mailer, app: 'Shop', templates: async () => ({}), log: () => {} })
  const home = { userId: user, email: 'ann@example.com', address: '203.0.113.7', userAgent: 'Firefox (Mac)' }
  await security.record(home)
  const refresh = { ...home, address: '198.51.100.66', userAgent: 'Chrome (Windows)', trust: 'known' }
  assert.equal(await security.record(refresh), true)
  assert.equal(await security.record(refresh), false)
  assert.equal(mailer.sent.length, 1)
})

test('a device noted while alerts were off is still emailed once they are on', async () => {
  const pool = memorySeen(), mailer = memoryMailer()
  const off = createSignInSecurity({ pool, mailer, alerts: false, app: 'Shop', templates: async () => ({}), log: () => {} })
  const on = createSignInSecurity({ pool, mailer, app: 'Shop', templates: async () => ({}), log: () => {} })
  const home = { userId: user, email: 'ann@example.com', address: '203.0.113.7', userAgent: 'Firefox (Mac)' }
  await off.record(home)
  // A refreshed session from a new network and device is noted but not
  // recorded, so the same refresh after alerts are on is still new.
  const refresh = { ...home, address: '198.51.100.1', userAgent: 'Lynx', trust: 'known' }
  assert.equal(await off.record(refresh), 'noted')
  assert.equal(mailer.sent.length, 0)
  assert.equal(await on.record(refresh), true)
  assert.equal(mailer.sent.length, 1)
})

test('a failed alert lets the next sign-in from that device try again', async () => {
  const pool = memorySeen()
  let failing = true
  const sent = []
  const mailer = { send: async (message, key) => { if (failing) throw new Error('timeout'); sent.push(key) } }
  const security = createSignInSecurity({ pool, mailer, app: 'Shop', templates: async () => ({}), log: () => {} })
  const home = { userId: user, email: 'ann@example.com', address: '203.0.113.7', userAgent: 'Firefox (Mac)' }
  await security.record(home)
  assert.equal(await security.record({ ...home, address: '198.51.100.1', userAgent: 'Lynx' }), false, 'a failed alert does not fail the sign-in')
  assert.equal(await security.trusted('email:ann@example.com', '198.51.100.1'), true, 'trust does not wait on email')
  failing = false
  assert.equal(await security.record({ ...home, address: '198.51.100.2', userAgent: 'Lynx' }), true)
  assert.equal(sent.length, 1)
})

test('sign-ins an app server completes are not the person\'s device', async t => {
  assert.ok(serverAgent(''))
  assert.ok(serverAgent('node'))
  assert.ok(serverAgent('undici'))
  assert.ok(serverAgent('python-requests/2.31'))
  assert.ok(serverAgent('Go-http-client/1.1'))
  assert.ok(!serverAgent('Mozilla/5.0 (iPhone) Safari/604.1'))
  assert.ok(!serverAgent('okhttp/4.12.0 MyApp'))
  assert.ok(!serverAgent('okhttp/4.9.2'), 'Android and React Native apps')
  const recorded = []
  const { base } = await harness(t, { signIns: { trusted: async () => false, record: async entry => { recorded.push(entry) } } })
  await post(base, '/token?grant_type=pkce', { refresh_token: 'r' }, '203.0.113.7', { 'User-Agent': 'node' })
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(recorded.length, 0)
})

test('a correct password does not spend the account\'s failure budget', async t => {
  const attempts = memoryAttempts()
  const { base } = await harness(t, { attempts })
  for (let i = 0; i < limits.passwordFailuresPerAccount + 5; i++) {
    assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'right' }, `192.0.2.${i}`)).status, 200)
  }
  assert.deepEqual([...attempts.counts.values()].filter(Boolean), [])
})

test('a redirect alone, or a network many accounts share, grants no trust', async () => {
  const users = Object.fromEntries(Array.from({ length: limits.trustedNetworkMaxAccounts + 1 }, (_, i) => [`u${i}@example.com`, `11111111-2222-4333-8444-${String(i).padStart(12, '0')}`]))
  const pool = memorySeen(users)
  const security = createSignInSecurity({ pool, app: 'Shop', templates: async () => ({}) })
  const first = { userId: users['u0@example.com'], email: 'u0@example.com', address: '203.0.113.7', userAgent: 'UA' }
  await security.record({ ...first, trust: false })
  assert.equal(await security.trusted('email:u0@example.com', '203.0.113.7'), false, 'a link scanner may have followed the redirect')
  await security.record(first)
  assert.equal(await security.trusted('email:u0@example.com', '203.0.113.7'), true, 'the client used the session')
  for (const [email, userId] of Object.entries(users)) await security.record({ userId, email, address: '192.0.2.10', userAgent: 'node' })
  assert.equal(await security.trusted('email:u0@example.com', '192.0.2.10'), false, 'an app server many accounts sign in through')
  assert.equal(await security.trusted('email:u0@example.com', '203.0.113.7'), true)
})

test('a CAPTCHA passes a spent account budget only up to a ceiling', async t => {
  const attempts = memoryAttempts()
  const { base } = await harness(t, { attempts, signIns: { trusted: async () => false, record: async () => false }, captcha: true })
  const ceiling = limits.passwordFailuresPerAccount * limits.trustedBudgetMultiplier
  for (let i = 0; i < ceiling; i++) {
    assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' }, `192.0.2.${i}`)).status, 400, String(i))
  }
  assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' }, '198.51.100.1')).status, 429)
})

test('a trusted network passes a spent recipient budget only up to a ceiling', async () => {
  const attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({ messagesPerRecipient: 1 }), trusted: async () => true })
  const outcomes = []
  for (let i = 0; i < limits.trustedBudgetMultiplier + 1; i++) outcomes.push((await sends.take(['phone:15550100'], '203.0.113.1')).refused?.name ?? 'sent')
  assert.deepEqual(outcomes, [...Array(limits.trustedBudgetMultiplier).fill('sent'), 'recipient'])
})

test('a profile save that changes no address sends nothing and spends nothing', async t => {
  const attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({ messagesPerRecipient: 1 }) })
  const { base } = await harness(t, { attempts, sends })
  const bearer = `Bearer ${jwt({ sub: user, session_id: user })}`
  for (let i = 0; i < 3; i++) {
    const saved = await fetch(`${base}/auth/v1/user`, { method: 'PUT', headers: { 'Content-Type': 'application/json', Authorization: bearer, 'User-Agent': 'Mozilla/5.0' }, body: JSON.stringify({ email: 'ann@example.com', data: { name: 'Ann' } }) })
    assert.equal(saved.status, 200, String(i))
  }
  assert.deepEqual([...attempts.counts.values()].filter(Boolean), [])
})

test('a held password attempt reports its limit, count and docs', async t => {
  const attempts = memoryAttempts()
  const { base } = await harness(t, { attempts })
  for (let i = 0; i < limits.passwordFailuresPerAddress; i++) await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' })
  const held = await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' })
  const body = await held.json()
  assert.equal(held.status, 429)
  assert.equal(body.error_code, 'too_many_attempts')
  assert.equal(body.limit, limits.passwordFailuresPerAddress)
  assert.equal(body.observed, limits.passwordFailuresPerAddress)
  assert.match(body.docs_url, /current-limits/)
})

test('an answer GoTrue drops midway gives back and answers 502', async t => {
  const upstream = http.createServer((req, res) => {
    req.resume()
    req.on('end', () => { res.writeHead(200, { 'Content-Type': 'application/json', 'Content-Length': '100' }); res.write('{"partial":'); setTimeout(() => res.socket.destroy(), 20) })
  })
  const attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({}) })
  const front = createServer({ upstreamPort: await listen(upstream), attempts, sends })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  const answer = await post(`http://127.0.0.1:${port}`, '/otp', { email: 'ann@example.com' })
  assert.equal(answer.status, 502)
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.deepEqual([...attempts.counts.values()].filter(Boolean), [])
})

test('budgets are given back when GoTrue never answers', async t => {
  const attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({}) })
  const front = createServer({ upstreamPort: 1, attempts, sends })
  const port = await listen(front)
  t.after(() => front.close())
  const base = `http://127.0.0.1:${port}`
  assert.equal((await post(base, '/otp', { email: 'ann@example.com' })).status, 502)
  assert.equal((await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'x' })).status, 502)
  assert.deepEqual([...attempts.counts.values()].filter(Boolean), [])
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

test('records sign-ins from token answers, sign-ups and session redirects', async t => {
  const recorded = []
  const signIns = { trusted: async () => false, record: async entry => { recorded.push(entry) } }
  const { base } = await harness(t, { signIns })
  await post(base, '/token?grant_type=refresh_token', { refresh_token: 'r' })
  await post(base, '/token?grant_type=pkce', { refresh_token: 'r' })
  await fetch(`${base}/auth/v1/verify?token=x&type=magiclink`, { redirect: 'manual', headers: { 'X-Real-IP': '198.51.100.9', 'User-Agent': 'UA' } })
  await post(base, '/signup', { email: 'ann@example.com', password: 'p' })
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(recorded.map(entry => [entry.userId, entry.alert, entry.trust]), [[user, true, 'known'], [user, true, true], [user, false, false], [user, true, true]])
  assert.equal(recorded[2].address, '198.51.100.9')
  assert.equal(recorded[2].email, 'ann@example.com')
})

test('an impersonated session may remove team access but not grant it', async () => {
  const org = '22222222-3333-4444-8555-666666666666'
  const run = async () => { throw new Error('must not reach SQL') }
  for (const action of ['invite', 'set_role']) {
    await assert.rejects(routeTeam({ run, method: 'POST', parts: ['orgs', org, 'team-actions'], body: { action }, impersonator: 'admin:0A1B2C3D' }), { status: 403, code: 'impersonation_restricted' })
  }
  await assert.rejects(routeTeam({ run, method: 'POST', parts: ['orgs', org, 'team-actions'], body: { action: 'remove_member' }, impersonator: 'admin:0A1B2C3D' }), { code: 'invalid_team_action' })
})
