import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { budgetKey } from '../attempts.mjs'
import { limits, securityConfig } from '../config.mjs'
import { activeHolds, addressDigester, createSecurityLog, networkFilter, networkLabel, releaseHolds, securityEvents, securityKinds } from '../securitylog.mjs'
import { createSends, messageBudgets } from '../sends.mjs'
import { requiredScope } from '../admin.mjs'

const user = '11111111-2222-4333-8444-555555555555'
const jwt = claims => `${Buffer.from('{"alg":"ES256"}').toString('base64url')}.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.sig`

function memoryAttempts() {
  const counts = new Map()
  const id = key => key.toString('hex')
  const bump = ({ key }) => { counts.set(id(key), (counts.get(id(key)) ?? 0) + 1); return counts.get(id(key)) }
  return {
    counts,
    async take(entry) { const count = bump(entry); return { count, wait: count > entry.budget.limit ? entry.budget.seconds : 0 } },
    async release(entries) { for (const { key } of entries) counts.set(id(key), Math.max(0, (counts.get(id(key)) ?? 0) - 1)) },
    async clear(entries) { for (const { key } of entries) counts.delete(id(key)) },
    async check() { return 0 },
    async fail() {},
  }
}

// memoryLog stands in for createSecurityLog and keeps what was recorded.
function memoryLog() {
  const events = []
  return { events, record(kind, fields) { events.push({ kind, ...fields }); return Promise.resolve() } }
}

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

async function harness(t, options) {
  const upstream = http.createServer((req, res) => {
    let body = ''
    req.on('data', chunk => { body += chunk })
    req.on('end', () => {
      const parsed = body ? JSON.parse(body) : {}
      res.setHeader('Content-Type', 'application/json')
      if (req.url.startsWith('/token')) {
        if (parsed.password === 'right') return res.end(JSON.stringify({ access_token: 't', user: { id: user, email: 'ann@example.com' } }))
        if (!parsed.gotrue_meta_security) { res.statusCode = 400; return res.end('{"error_code":"captcha_failed"}') }
        if (parsed.password === 'flood') { res.statusCode = 429; return res.end('{"error_code":"over_request_rate_limit"}') }
        res.statusCode = 400
        return res.end('{"error_code":"invalid_credentials"}')
      }
      if (req.url.startsWith('/factors/')) { res.statusCode = 422; return res.end('{"error_code":"mfa_verification_failed"}') }
      if (parsed.refuse) { res.statusCode = 422; return res.end('{"error_code":"validation_failed"}') }
      res.end('{}')
    })
  })
  const front = createServer({ upstreamPort: await listen(upstream), ...options })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  return `http://127.0.0.1:${port}`
}

// Password sign-ins carry a CAPTCHA token unless they say otherwise; the
// fake GoTrue refuses those without one with captcha_failed.
const post = (base, path, body, address = '203.0.113.7', headers = {}) => fetch(`${base}/auth/v1${path}`, {
  method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Real-IP': address, 'User-Agent': 'Mozilla/5.0 Firefox/120.0', ...headers },
  body: JSON.stringify(body.password && !('gotrue_meta_security' in body) ? { ...body, gotrue_meta_security: { captcha_token: 'ok' } } : body),
})
const settle = () => new Promise(resolve => setTimeout(resolve, 20))

test('names a request\'s network, never its address', () => {
  assert.equal(networkLabel('203.0.113.7'), '203.0.113.0/24')
  assert.equal(networkLabel('::ffff:203.0.113.7'), '203.0.113.0/24')
  assert.equal(networkLabel('2001:db8:1:2:3:4:5:6'), '2001:db8:1::/48')
  assert.equal(networkLabel('[2001:DB8:1::9]'), '2001:db8:1::/48')
  assert.equal(networkLabel('2001:db8::1'), '2001:db8::/48', 'a zero third group is compressed')
  assert.equal(networkLabel('2001::5'), '2001::/48')
  assert.equal(networkFilter('2001:db8:0::/48'), '2001:db8::/48')
  assert.equal(networkFilter('2001:DB8:0:0:0:0:0:0/48'), '2001:db8::/48')
  assert.equal(networkFilter('203.0.113.0/24'), '203.0.113.0/24')
  assert.equal(networkFilter('203.0.113.77'), '203.0.113.0/24')
  for (const wider of ['10.0.0.0/16', '2001:db8::/32', '203.0.113.0/24/1', 'nope']) assert.equal(networkFilter(wider), null, wider)
  assert.equal(networkLabel(''), null)
  assert.equal(networkLabel('not an address'), null)
})

test('an address digest groups one address, is keyed, and is not the address', () => {
  const digest = addressDigester('a'.repeat(32)), other = addressDigester('b'.repeat(32))
  assert.equal(digest('203.0.113.7').length, 16)
  assert.deepEqual(digest('203.0.113.7'), digest(' 203.0.113.7 '))
  assert.notDeepEqual(digest('203.0.113.7'), digest('203.0.113.8'))
  assert.notDeepEqual(digest('203.0.113.7'), other('203.0.113.7'))
  assert.equal(digest(''), null)
})

test('wrong passwords, holds and a CAPTCHA pass are recorded by network', async t => {
  const securityLog = memoryLog(), attempts = memoryAttempts()
  const base = await harness(t, { attempts, securityLog, captcha: true })
  for (let i = 0; i < limits.passwordFailuresPerAddress + 1; i++) await post(base, '/token?grant_type=password', { email: 'Ann@Example.com', password: 'wrong' })
  await settle()
  const failed = securityLog.events.filter(event => event.kind === 'password_failed')
  assert.equal(failed.length, limits.passwordFailuresPerAddress)
  assert.equal(failed[0].subject, 'email:ann@example.com')
  assert.equal(failed[0].address, '203.0.113.7')
  assert.deepEqual(failed[0].alike, ['email:ann@example.com', '203.0.113.0/24'])
  const [held] = securityLog.events.filter(event => event.kind === 'held')
  assert.equal(held.detail.budget, 'password_address')
  assert.equal(held.detail.limit, limits.passwordFailuresPerAddress)
  assert.ok(Date.parse(held.detail.until) > Date.now())
  assert.deepEqual(held.hold, budgetKey('password-address', 'email:ann@example.com', '203.0.113.7'))
  // Past the account budget from other networks, a CAPTCHA passes.
  for (let i = 0; i < limits.passwordFailuresPerAccount; i++) await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' }, `198.51.${Math.floor(i / 5)}.${i % 5 + 1}`)
  await settle()
  const passes = securityLog.events.filter(event => event.kind === 'captcha_pass')
  assert.ok(passes.length > 0)
  assert.equal(passes[0].detail.budget, 'password_account')
  assert.equal(passes[0].subject, 'email:ann@example.com')
  // GoTrue refusing the CAPTCHA, or refusing before it checks one, is no pass.
  await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong', gotrue_meta_security: null }, '192.0.2.200')
  await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'flood' }, '192.0.2.201')
  await settle()
  assert.equal(securityLog.events.filter(event => event.kind === 'captcha_pass').length, passes.length)
})

test('a wrong second-step code is recorded against the token\'s user', async t => {
  const securityLog = memoryLog()
  const base = await harness(t, { attempts: memoryAttempts(), securityLog })
  await post(base, '/factors/recovery-codes/verify', { code: '000000' }, '203.0.113.7', { Authorization: `Bearer ${jwt({ sub: user.toUpperCase(), aal: 'aal1' })}` })
  await settle()
  const [code] = securityLog.events.filter(event => event.kind === 'code_failed')
  assert.equal(code.userId, user.toUpperCase())
  assert.equal(code.subject, undefined)
  assert.deepEqual(code.alike, [user.toUpperCase(), '203.0.113.0/24'])
})

test('a spent message budget records the recipient it held', async t => {
  const securityLog = memoryLog(), attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({ messagesPerRecipient: 1 }), events: securityLog })
  const base = await harness(t, { attempts, sends, securityLog })
  await post(base, '/otp', { email: 'ann@example.com' })
  const refused = await post(base, '/otp', { email: 'ann@example.com' })
  assert.equal(refused.status, 429)
  const [limited] = securityLog.events.filter(event => event.kind === 'message_limited')
  assert.equal(limited.subject, 'email:ann@example.com')
  assert.equal(limited.detail.budget, 'message_recipient')
  assert.equal(limited.detail.limit, 1)
  assert.deepEqual(limited.hold, budgetKey('message-recipient', 'email:ann@example.com'))
})

test('a new-device alert is recorded with the device, not the address', async t => {
  const securityLog = memoryLog()
  const signIns = { record: async () => true, trusted: async () => false }
  const base = await harness(t, { attempts: memoryAttempts(), securityLog, signIns })
  await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'right' })
  await settle()
  const [alert] = securityLog.events.filter(event => event.kind === 'new_device_alert')
  assert.equal(alert.userId, user)
  assert.equal(alert.detail.device, 'Firefox')
  assert.equal(alert.detail.emailed, true)
})

test('a new device noted without email is recorded as not emailed', async t => {
  const securityLog = memoryLog()
  const signIns = { record: async () => 'noted', trusted: async () => false }
  const base = await harness(t, { attempts: memoryAttempts(), securityLog, signIns })
  await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'right' })
  await settle()
  const [alert] = securityLog.events.filter(event => event.kind === 'new_device_alert')
  assert.equal(alert.detail.emailed, false)
})

test('releasing holds clears every budget that may hold the user and records who did', async () => {
  const calls = []
  const query = async (sql, args) => {
    calls.push({ sql, args })
    if (sql.startsWith('SELECT email, phone')) return args[0] === user ? [{ email: 'Ann.Smith+x@Gmail.com', phone: '15551234567' }] : []
    if (sql.includes('DELETE FROM gregale_auth.attempt_budgets')) return [{ cleared: 4, ended: 2 }]
    return []
  }
  assert.deepEqual(await releaseHolds(query, user, 'ABCD1234'), { budgets_cleared: 4 })
  const keys = calls[1].args[1].map(key => key.toString('hex'))
  for (const expected of [budgetKey('second-step', user), budgetKey('message-recipient', `user:${user}`), budgetKey('password-account', 'email:ann.smith+x@gmail.com'),
    budgetKey('message-recipient', 'email:annsmith@gmail.com'), budgetKey('password-account', 'phone:15551234567'), budgetKey('message-recipient', 'phone:15551234567')]) {
    assert.ok(keys.includes(expected.toString('hex')), expected.toString('hex'))
  }
  assert.match(calls[1].sql, /user_id = \$1 AND budget_key IS NOT NULL/, 'and the address budgets its events name')
  assert.deepEqual(calls[2].args, [user, '{"budgets":4,"admin_key":"ABCD1234"}'])
  await assert.rejects(releaseHolds(query, '22222222-2222-4333-8444-555555555555'), /user_not_found/)
  assert.equal(requiredScope('POST', ['users', user, 'release-holds']), 'users:write')
})

test('recording writes one row per alike events and never fails a request', async () => {
  const calls = [], logged = []
  let fail = false
  const pool = { query: async (sql, args) => { calls.push(args); if (fail) throw Object.assign(new Error('down'), { code: '57P01' }); return { rowCount: 1 } } }
  const log = createSecurityLog({ pool, secret: 'x'.repeat(32), log: line => logged.push(JSON.parse(line)), now: () => 120000 })
  await log.record('password_failed', { subject: 'email:ann@example.com', address: '203.0.113.7', alike: ['email:ann@example.com', '203.0.113.0/24'] })
  await log.record('password_failed', { subject: 'email:ann@example.com', address: '203.0.113.9', alike: ['email:ann@example.com', '203.0.113.0/24'] })
  const [first, second] = calls
  assert.deepEqual(first.slice(0, 5), ['password_failed', null, 'email', 'ann@example.com', '203.0.113.0/24'])
  assert.deepEqual(first[7], second[7], 'alike events in one minute share a dedupe key')
  assert.notDeepEqual(first[5], second[5], 'their addresses differ')
  await log.record('held', { userId: user.toUpperCase(), address: '203.0.113.7', hold: Buffer.alloc(32, 1), detail: { budget: 'second_step' } })
  assert.equal(calls[2][1], user)
  assert.equal(calls[2][7], null, 'a hold is grouped by its budget in SQL')
  assert.equal(calls[2][6], '{"budget":"second_step"}')
  // One budget's refusals of different accounts or networks are rows apart.
  await log.record('held', { subject: 'email:bob@example.com', address: '203.0.113.7', hold: Buffer.alloc(32, 1) })
  await log.record('held', { userId: user, address: '198.51.100.7', hold: Buffer.alloc(32, 1) })
  await log.record('held', { userId: user, address: '203.0.113.99', hold: Buffer.alloc(32, 1) })
  assert.notDeepEqual(calls[2][9], calls[3][9])
  assert.notDeepEqual(calls[2][9], calls[4][9])
  assert.deepEqual(calls[2][9], calls[5][9], 'one account from one network shares a row')
  fail = true
  await log.record('code_failed', { userId: user })
  assert.equal(logged[0].event, 'security_event_failed')
  assert.equal(logged[0].sqlstate, '57P01')
})

test('waiting alike events merge into one write, and a full queue drops and reports', async () => {
  let release
  const blocked = new Promise(resolve => { release = resolve })
  const calls = [], logged = []
  let clock = 0
  const pool = { query: async (sql, args) => { calls.push(args); await blocked; return { rowCount: 1 } } }
  const log = createSecurityLog({ pool, secret: 'x'.repeat(32), log: line => logged.push(JSON.parse(line)), now: () => clock })
  const alike = [user, '203.0.113.0/24']
  const first = log.record('password_failed', { userId: user, alike: ['first'] })
  await Promise.resolve()
  const merged = []
  for (let i = 0; i < 5; i++) merged.push(log.record('password_failed', { userId: user, alike }))
  assert.equal(new Set(merged).size, 1, 'merged events share one promise')
  for (let i = 0; i < limits.securityEventsQueued; i++) log.record('password_failed', { userId: user, alike: [String(i)] })
  assert.equal(calls.length, 1, 'one writer')
  clock = 61000
  release()
  await first
  await Promise.all(merged)
  await log.settled()
  const write = calls.find(args => args[7] && args[10] === 5)
  assert.ok(write, 'five alike events became one write with count 5')
  // The first write, the merged row, and the distinct rows that fit beside it.
  assert.equal(calls.length, 1 + 1 + limits.securityEventsQueued - 1)
  assert.deepEqual(logged.map(line => [line.event, line.count]), [['security_events_dropped', 1]])
})

test('a CAPTCHA pass and its wrong password are both recorded', async t => {
  const rows = []
  const pool = { query: async (sql, args) => { rows.push(args[0]); return { rowCount: 1 } } }
  const securityLog = createSecurityLog({ pool, secret: 'x'.repeat(32) })
  const base = await harness(t, { attempts: memoryAttempts(), securityLog, captcha: true })
  for (let i = 0; i < limits.passwordFailuresPerAccount + 1; i++) await post(base, '/token?grant_type=password', { email: 'ann@example.com', password: 'wrong' }, `198.51.${Math.floor(i / 5)}.${i % 5 + 1}`)
  await settle()
  await securityLog.settled()
  assert.ok(rows.includes('captcha_pass'))
  assert.equal(rows.filter(kind => kind === 'password_failed').length, limits.passwordFailuresPerAccount + 1)
})

test('the admin API pages security events and checks its filters', async () => {
  let seen
  const query = async (sql, args) => { seen = { sql, args }; return [{ id: '2' }, { id: '1' }] }
  const page = await securityEvents(query, new URLSearchParams({ per_page: '1', kind: 'held', user: user.toUpperCase(), network: '203.0.113.0/24' }))
  assert.deepEqual(page, { entries: [{ id: '2' }], more: true, page: 1, per_page: 1 })
  assert.deepEqual(seen.args, [user, 'held', '203.0.113.0/24', 2, 0])
  assert.match(seen.sql, /e\.user_id = \$1::uuid/, 'the user filter can use its index')
  for (const bad of [{ kind: 'nope' }, { user: 'x' }, { network: "1'; DROP" }, { network: '10.0.0.0/16' }, { per_page: '0' }, { page: '0' }]) {
    await assert.rejects(securityEvents(query, new URLSearchParams(bad)), JSON.stringify(bad))
  }
  assert.equal(requiredScope('GET', ['security-events']), 'users:read')
  assert.deepEqual(await activeHolds(async (sql, args) => { assert.match(sql, /window_ends_at > now\(\)/); return [{ budget: 'password_account', args }] }, user), [{ budget: 'password_account', args: [user] }])
  assert.equal(securityKinds.length, 8)
})

test('reads how long security events are kept, with bounds', () => {
  assert.equal(securityConfig({}).logDays, 30)
  assert.equal(securityConfig({ AUTH_SECURITY_LOG_DAYS: '365' }).logDays, 365)
  for (const value of ['0', '366', '1e2', '-1']) assert.throws(() => securityConfig({ AUTH_SECURITY_LOG_DAYS: value }), value)
})

test('a trusted network\'s pass of a message budget is recorded only once GoTrue takes the request', async t => {
  const securityLog = memoryLog(), attempts = memoryAttempts()
  const sends = createSends({ attempts, pool: null, budgets: messageBudgets({ messagesPerRecipient: 1 }), trusted: async () => true })
  const base = await harness(t, { attempts, sends, securityLog })
  await post(base, '/otp', { email: 'ann@example.com' })
  await post(base, '/otp', { email: 'ann@example.com' })
  await settle()
  assert.equal(securityLog.events.filter(event => event.kind === 'trusted_pass').length, 1)
  await post(base, '/otp', { email: 'ann@example.com', refuse: true })
  await settle()
  assert.equal(securityLog.events.filter(event => event.kind === 'trusted_pass').length, 1, 'a request GoTrue refused passed nothing')
})
