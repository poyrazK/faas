// Regression tests for the 2026-11-28 review of the Auth front: every check
// the front makes before GoTrue or route() must read a request the way they
// do, or a request passes the check in a form the other side still serves.
import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { addressBucket, budgets, goLower, goQueryValue, guardedPath, signInSubject } from '../attempts.mjs'
import { smsSendPath } from '../sms.mjs'
import { impersonationBlocked } from '../impersonation.mjs'
import { createAuthzHandler } from '../authz.mjs'
import { allowedRedirect } from '../ui/app.js'
import { limits } from '../config.mjs'

async function listen(server) {
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  return server.address().port
}

test('the password grant is read from the query as Go reads it', () => {
  // Go drops a pair with a semicolon; the second pair is the grant.
  assert.equal(guardedPath('POST', '/token?grant_type=x;&grant_type=password'), 'password')
  assert.equal(guardedPath('POST', '/token?grant_type=password;x'), null)
  assert.equal(guardedPath('POST', '/token?grant_type=pass%77ord'), 'password')
  assert.equal(guardedPath('POST', '/token?grant_type=%zz&grant_type=password'), 'password')
  assert.equal(guardedPath('POST', '/token?grant_type=refresh_token&grant_type=password'), null)
  assert.equal(goQueryValue('a=1&a=2', 'a'), '1')
  assert.equal(goQueryValue('a+b=c+d', 'a b'), 'c d')
  assert.equal(goQueryValue('', 'a'), null)
})

test('an account is named as Go lowercases it, so İ cannot open a new budget', () => {
  assert.equal(goLower('ALİCE@X.COM'), 'alice@x.com')
  assert.equal(signInSubject(Buffer.from(JSON.stringify({ email: 'alİce@example.com', password: 'p' }))), 'email:alice@example.com')
  assert.equal(signInSubject(Buffer.from(JSON.stringify({ email: 'ALICE@example.com', password: 'p' }))), 'email:alice@example.com')
})

test('a trailing slash still meets the SMS country check', () => {
  for (const path of ['/auth/v1/otp/', '/auth/v1/signup//', '/auth/v1/resend/?x=1', '/auth/v1/factors/']) assert.ok(smsSendPath('POST', path), path)
  assert.ok(smsSendPath('PUT', '/auth/v1/user/'))
})

test('an impersonated session is refused however the path repeats slashes', () => {
  for (const [method, url] of [
    ['DELETE', '/authz/v1//account'], ['DELETE', '/authz/v1/account//'], ['DELETE', '/authz/v1//sessions/x'],
    ['POST', '/authz/v1/orgs/o//service-accounts'], ['POST', '/authz/v1//orgs/o/invitations'],
    ['PATCH', '/authz/v1/orgs/o/members//u'], ['PATCH', '/authz/v1//orgs/o'], ['POST', '/authz/v1/orgs/o//sso'],
  ]) assert.ok(impersonationBlocked(method, url), `${method} ${url}`)
})

test('an impersonated session takes no team actions', () => {
  assert.ok(impersonationBlocked('POST', '/authz/v1/orgs/o/team-actions'))
  assert.ok(impersonationBlocked('POST', '/authz/v1/orgs/o/team-actions/'))
  assert.ok(!impersonationBlocked('GET', '/authz/v1/orgs/o/team-actions'))
  assert.ok(!impersonationBlocked('GET', '/authz/v1/orgs/o/team'))
})

test('the authz router refuses a path with an empty segment before reading a token', async t => {
  let authenticated = 0
  const handler = createAuthzHandler({ pool: { query: async () => ({ rows: [] }) }, authenticate: async () => { authenticated++; return { sub: 'u' } } })
  const server = http.createServer((req, res) => handler(req, res, '/' + req.url.slice('/authz/v1/'.length).split('?')[0]))
  const port = await listen(server)
  t.after(() => server.close())
  for (const path of ['/authz/v1//account', '/authz/v1/orgs//x', '/authz/v1/orgs/x//members']) {
    const res = await fetch(`http://127.0.0.1:${port}${path}`, { method: 'DELETE', headers: { Authorization: 'Bearer a.b.c' } })
    assert.equal(res.status, 404, path)
  }
  assert.equal(authenticated, 0)
})

// memoryBudgets stands in for attempt_budgets with take's contract.
function memoryBudgets() {
  const counts = new Map()
  return {
    counts,
    async take({ key, budget }) {
      const count = (counts.get(key.toString('hex')) ?? 0) + 1
      counts.set(key.toString('hex'), count)
      return { count, wait: count > budget.limit ? budget.seconds : 0 }
    },
  }
}

test('dynamic client registration is budgeted per address', async t => {
  let registered = 0
  const upstream = http.createServer((req, res) => { req.resume(); req.on('end', () => { registered++; res.statusCode = 201; res.end('{}') }) })
  const front = createServer({ upstreamPort: await listen(upstream), attempts: memoryBudgets(), oauth: { registeredClients: async () => ({ clients: 0, unused: 0 }), location: l => l } })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  const register = (address, path = '/auth/v1/oauth/clients/register') => fetch(`http://127.0.0.1:${port}${path}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Real-IP': address }, body: '{"redirect_uris":["https://c.example/cb"]}',
  })
  // Addresses in one IPv6 /64 share a budget.
  for (let i = 0; i < budgets.oauthRegistration.limit; i++) assert.equal((await register(`2001:db8:1:2::${i + 1}`)).status, 201)
  const held = await register('2001:db8:1:2:ffff::1', '/auth/v1/oauth/clients/register/')
  assert.equal(held.status, 429)
  assert.deepEqual(await held.json(), { status: 429, code: 'too_many_registrations', limit: budgets.oauthRegistration.limit, observed: budgets.oauthRegistration.limit + 1,
    window_seconds: budgets.oauthRegistration.seconds, retry_after: budgets.oauthRegistration.seconds, docs_url: 'https://gregale.dev/docs/application-auth#current-limits' })
  assert.equal((await register('198.51.100.9')).status, 201)
  assert.equal(registered, budgets.oauthRegistration.limit + 1)
  assert.equal(limits.oauthRegistrationsPerAddress, budgets.oauthRegistration.limit)
})

test('registration pauses at the unused-client cap and stops at the client limit; a failing store is logged', async t => {
  const logs = []
  const original = console.error
  console.error = line => logs.push(JSON.parse(line))
  t.after(() => { console.error = original })
  let counts = { clients: limits.oauthClients, unused: 0 }
  let failing = false
  const front = createServer({ upstreamPort: 1, attempts: { take: async () => { if (failing) throw Object.assign(new Error('x'), { code: '42P01' }); return { count: 1, wait: 0 } } },
    oauth: { registeredClients: async () => counts, location: l => l } })
  const port = await listen(front)
  t.after(() => front.close())
  const register = () => fetch(`http://127.0.0.1:${port}/auth/v1/oauth/clients/register`, { method: 'POST', body: '{}' })
  const docs = 'https://gregale.dev/docs/application-auth#current-limits'
  const full = await register()
  assert.equal(full.status, 403)
  assert.deepEqual(await full.json(), { status: 403, code: 'oauth_client_limit', limit: limits.oauthClients, observed: limits.oauthClients, docs_url: docs })
  counts = { clients: limits.oauthUnusedClients + 10, unused: limits.oauthUnusedClients }
  const paused = await register()
  assert.equal(paused.status, 429)
  assert.ok(Number(paused.headers.get('retry-after')) > 0)
  assert.deepEqual(await paused.json(), { status: 429, code: 'too_many_unused_clients', limit: limits.oauthUnusedClients, observed: limits.oauthUnusedClients, docs_url: docs })
  assert.ok(limits.oauthUnusedClients < limits.oauthClients, 'used and owner clients keep places')
  failing = true
  assert.equal((await register()).status, 503)
  assert.deepEqual(logs, [{ level: 'error', event: 'oauth_registration_failed', sqlstate: '42P01' }])
})

test('an IPv6 client counts by its /64; IPv4 counts as itself however it is written', () => {
  assert.equal(addressBucket('2001:db8:1:2:3:4:5:6'), '2001:db8:1:2::/64')
  assert.equal(addressBucket('2001:DB8:0001:0002::9'), '2001:db8:1:2::/64')
  assert.equal(addressBucket('2001:db8::1'), '2001:db8:0:0::/64')
  assert.equal(addressBucket('[2001:db8::1]'), '2001:db8:0:0::/64')
  assert.equal(addressBucket('fe80::1%en0'), 'fe80:0:0:0::/64')
  assert.equal(addressBucket('64:ff9b::203.0.113.7'), '64:ff9b:0:0::/64')
  assert.equal(addressBucket('::ffff:203.0.113.7'), '203.0.113.7')
  assert.equal(addressBucket('::ffff:cb00:7107'), '203.0.113.7')
  assert.equal(addressBucket('203.0.113.7'), '203.0.113.7')
  assert.equal(addressBucket('not an address'), 'not an address')
})

test('an admin key is reported by its ID, a budgeted number of times, never to a browser', async t => {
  const settings = { app: 'shop', keys: { signing: 'K1', verifying: [] } }
  const adminKeys = [{ id: '0A1B2C3D', scopes: ['users:read'], expires_at: null }]
  const budgetsUsed = memoryBudgets()
  const front = createServer({ upstreamPort: 1, settings, adminKeys, attempts: budgetsUsed })
  const port = await listen(front)
  t.after(() => front.close())
  const base = `http://127.0.0.1:${port}`
  assert.equal((await (await fetch(`${base}/settings/v1`)).json()).admin_keys, undefined, 'public settings list no admin keys')
  const one = await fetch(`${base}/settings/v1/admin-keys/0A1B2C3D`)
  assert.equal(one.headers.get('access-control-allow-origin'), null, 'no browser origin may read a key')
  assert.deepEqual(await one.json(), adminKeys[0])
  assert.equal((await fetch(`${base}/settings/v1/admin-keys/0A1B2C3E`)).status, 404)
  // Paths that are not IDs never spend the budget (or reach the database).
  for (const path of ['/settings/v1/admin-keys/', '/settings/v1/admin-keys/0a1b2c3d', '/settings/v1/admin-keys/x/../y']) {
    assert.equal((await fetch(`${base}${path}`)).status, 404, path)
  }
  assert.equal([...budgetsUsed.counts.values()][0], 2)
  for (let i = 2; i < budgets.adminKeyLookup.limit; i++) assert.equal((await fetch(`${base}/settings/v1/admin-keys/0A1B2C3D`)).status, 200)
  const held = await fetch(`${base}/settings/v1/admin-keys/0A1B2C3D`)
  assert.equal(held.status, 429)
  assert.equal((await held.json()).code, 'too_many_lookups')
})

test('a redirect URL allows paths under it only at a segment boundary', () => {
  const site = 'https://app.example'
  const allowed = ['https://app.example/callback', 'https://other.example/auth/']
  assert.equal(allowedRedirect('https://app.example/callback', site, allowed), 'https://app.example/callback')
  assert.equal(allowedRedirect('https://app.example/callback/x?y=1', site, allowed), 'https://app.example/callback/x?y=1')
  assert.equal(allowedRedirect('https://other.example/auth/done', site, allowed), 'https://other.example/auth/done')
  assert.equal(allowedRedirect('https://other.example/authx', site, allowed), site)
  assert.equal(allowedRedirect('https://other.example/auth', site, allowed), site)
  // The site URL is the origin root, so every path on it stays allowed.
  assert.equal(allowedRedirect('https://app.example/callback-other', site, allowed), 'https://app.example/callback-other')
  assert.equal(allowedRedirect('https://app.example/callback-other', 'https://app.example/app', allowed), 'https://app.example/app')
})
