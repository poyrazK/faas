// Regression tests for the 2026-11-28 review of the Auth front: every check
// the front makes before GoTrue or route() must read a request the way they
// do, or a request passes the check in a form the other side still serves.
import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { once } from 'node:events'
import { createServer } from '../server.mjs'
import { budgets, goLower, goQueryValue, guardedPath, signInSubject } from '../attempts.mjs'
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

test('dynamic client registration is budgeted per address', async t => {
  const counts = new Map()
  const attempts = {
    async reserve(entries) {
      let wait = 0
      for (const { key, budget } of entries) {
        const n = (counts.get(key.toString('hex')) ?? 0) + 1
        counts.set(key.toString('hex'), n)
        if (n > budget.limit) wait = budget.seconds
      }
      return wait
    },
  }
  let registered = 0
  const upstream = http.createServer((req, res) => { req.resume(); req.on('end', () => { registered++; res.statusCode = 201; res.end('{}') }) })
  const front = createServer({ upstreamPort: await listen(upstream), attempts, oauth: { registrationOpen: async () => true, location: l => l } })
  const port = await listen(front)
  t.after(() => { front.close(); upstream.close() })
  const register = (address, path = '/auth/v1/oauth/clients/register') => fetch(`http://127.0.0.1:${port}${path}`, {
    method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Real-IP': address }, body: '{"redirect_uris":["https://c.example/cb"]}',
  })
  for (let i = 0; i < budgets.oauthRegistration.limit; i++) assert.equal((await register('203.0.113.7')).status, 201)
  const held = await register('203.0.113.7', '/auth/v1/oauth/clients/register/')
  assert.equal(held.status, 429)
  assert.equal((await held.json()).code, 'too_many_registrations')
  assert.equal((await register('198.51.100.9')).status, 201)
  assert.equal(registered, budgets.oauthRegistration.limit + 1)
  assert.equal(limits.oauthRegistrationsPerAddress, budgets.oauthRegistration.limit)
})

test('public settings list no admin keys; one is reported only by its ID', async t => {
  const settings = { app: 'shop', keys: { signing: 'K1', verifying: [] } }
  const adminKeys = [{ id: '0A1B2C3D', scopes: ['users:read'], expires_at: null }]
  const front = createServer({ upstreamPort: 1, settings, adminKeys })
  const port = await listen(front)
  t.after(() => front.close())
  const base = `http://127.0.0.1:${port}`
  const all = await (await fetch(`${base}/settings/v1`)).json()
  assert.equal(all.admin_keys, undefined)
  assert.deepEqual(await (await fetch(`${base}/settings/v1/admin-keys/0A1B2C3D`)).json(), adminKeys[0])
  for (const path of ['/settings/v1/admin-keys/0A1B2C3E', '/settings/v1/admin-keys/', '/settings/v1/admin-keys/0a1b2c3d']) {
    assert.equal((await fetch(`${base}${path}`)).status, 404, path)
  }
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
