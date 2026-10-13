import test from 'node:test'
import assert from 'node:assert/strict'
import { AuthError, createAuthClient, resource } from '../dist/index.js'

const origin = 'https://shop-auth.gregale.app'

function server(routes) {
  const calls = []
  const fetch = async (url, init) => {
    const u = new URL(url)
    const key = `${init.method} ${u.pathname}`
    calls.push({ key, url: u, headers: init.headers, body: init.body ? JSON.parse(init.body) : undefined })
    const handler = routes[key]
    if (!handler) return new Response(JSON.stringify({ code: 'not_found' }), { status: 404 })
    const [status, body] = await handler(calls.at(-1))
    return new Response(body === undefined ? null : JSON.stringify(body), { status })
  }
  return { calls, fetch }
}

const tokens = (n, expiresIn = 600) => ({ access_token: `access-${n}`, refresh_token: `refresh-${n}`, expires_in: expiresIn, user: { id: 'u1', email: 'a@example.test' } })

test('rejects unsafe Auth URLs', () => {
  for (const url of ['http://shop-auth.gregale.app', 'https://user:pw@shop-auth.gregale.app', 'https://shop-auth.gregale.app/?x=1', 'https://shop-auth.gregale.app/#x']) {
    assert.throws(() => createAuthClient({ url, fetch: async () => {} }), undefined, url)
  }
  assert.doesNotThrow(() => createAuthClient({ url: 'http://localhost:8080', fetch: async () => {} }))
})

test('signs in, stores the session and authenticates authz calls', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'GET /authz/v1/orgs': () => [200, { organizations: [{ id: 'o1', slug: 'acme', name: 'Acme', role: 'owner', created_at: 'now' }] }],
    'POST /authz/v1/authorize': call => [200, { allowed: call.body.action === 'read' }],
  })
  const events = []
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  auth.onAuthStateChange(event => events.push(event))
  const session = await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal(session.accessToken, 'access-1')
  assert.equal(api.calls[0].url.search, '?grant_type=password')
  assert.equal((await auth.organizations.list())[0].slug, 'acme')
  assert.equal(api.calls[1].headers.Authorization, 'Bearer access-1')
  assert.equal(await auth.authorize('read', resource('o1', 'doc', 7)), true)
  assert.deepEqual(api.calls[2].body, { action: 'read', resource: 'org/o1/doc/7' })
  assert.equal(await auth.authorize('write', resource('o1', 'doc', 7)), false)
  assert.deepEqual(events, ['SIGNED_IN'])
})

test('refreshes once for concurrent callers before the token expires', async () => {
  let refreshes = 0
  const api = server({
    'POST /auth/v1/token': async call => {
      if (call.url.search === '?grant_type=password') return [200, tokens(1, 30)]
      refreshes++
      assert.equal(call.body.refresh_token, 'refresh-1')
      await new Promise(resolve => setTimeout(resolve, 20))
      return [200, tokens(2)]
    },
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  const results = await Promise.all([auth.getAccessToken(), auth.getAccessToken(), auth.getAccessToken()])
  assert.deepEqual(results, ['access-2', 'access-2', 'access-2'])
  assert.equal(refreshes, 1)
})

test('a rejected refresh token signs the user out', async () => {
  const api = server({
    'POST /auth/v1/token': call => call.url.search === '?grant_type=password' ? [200, tokens(1, 1)] : [400, { error_code: 'refresh_token_not_found' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  await assert.rejects(auth.getSession(), err => err instanceof AuthError && err.code === 'refresh_token_not_found' && err.status === 400)
  assert.equal(await auth.getSession(), null)
  await assert.rejects(auth.getAccessToken(), { code: 'not_authenticated' })
})

test('completes redirects from the hosted pages', async () => {
  const stored = new Map()
  const store = { get: () => stored.get('s') ?? null, set: v => { stored.set('s', v) }, remove: () => { stored.delete('s') } }
  const auth = createAuthClient({ url: origin, fetch: async () => { throw new Error('no network') }, store })
  assert.equal(await auth.sessionFromUrl('https://shop.example.com/app'), null)
  const expiresAt = Math.floor(Date.now() / 1000) + 600
  const session = await auth.sessionFromUrl(`https://shop.example.com/app#access_token=a&refresh_token=r&expires_in=600&expires_at=${expiresAt}&token_type=bearer&type=signin`)
  assert.deepEqual({ ...session }, { accessToken: 'a', refreshToken: 'r', expiresAt })
  assert.equal((await auth.getSession()).accessToken, 'a')
  assert.ok(stored.has('s'))
  assert.equal(auth.hostedSignInUrl({ redirectTo: 'https://shop.example.com/app' }), `${origin}/ui/sign-in?redirect_to=https%3A%2F%2Fshop.example.com%2Fapp`)
  assert.equal(auth.oauthUrl({ provider: 'github', redirectTo: 'https://shop.example.com/' }), `${origin}/auth/v1/authorize?provider=github&redirect_to=https%3A%2F%2Fshop.example.com%2F`)
})

test('sign-out clears the session even when the server already ended it', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'POST /auth/v1/logout': () => [401, { error_code: 'session_not_found' }],
  })
  const events = []
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  auth.onAuthStateChange(event => events.push(event))
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  await auth.signOut({ scope: 'global' })
  assert.equal(api.calls[1].url.search, '?scope=global')
  assert.equal(await auth.getSession(), null)
  assert.deepEqual(events, ['SIGNED_IN', 'SIGNED_OUT'])
})

test('sign-up returns a session only when confirmation is not required', async () => {
  let confirm = true
  const api = server({ 'POST /auth/v1/signup': () => [200, confirm ? { id: 'u1', email: 'a@example.test' } : tokens(1)] })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const pending = await auth.signUp({ email: 'a@example.test', password: 'pw', redirectTo: 'https://shop.example.com/welcome' })
  assert.equal(pending.session, null)
  assert.equal(pending.user.id, 'u1')
  assert.equal(api.calls[0].url.searchParams.get('redirect_to'), 'https://shop.example.com/welcome')
  confirm = false
  assert.equal((await auth.signUp({ email: 'a@example.test', password: 'pw' })).session.accessToken, 'access-1')
})

test('surfaces stable error codes and hides server text', async () => {
  const api = server({
    'POST /auth/v1/token': () => [400, { error_code: 'invalid_credentials', msg: 'Invalid login credentials' }],
    'POST /auth/v1/otp': () => [500, { msg: '<script>' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await assert.rejects(auth.signInWithPassword({ email: 'a', password: 'b' }), { status: 400, code: 'invalid_credentials' })
  await assert.rejects(auth.signInWithMagicLink({ email: 'a' }), { status: 500, code: 'http_500' })
  assert.deepEqual(api.calls[1].body, { email: 'a', create_user: false })
})

test('organization, member and invitation calls use encoded paths', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'POST /authz/v1/orgs/o%2F1/invitations': call => [201, { id: 'i1', email: call.body.email, role: call.body.role, expires_at: 'later', delivered: false, token: 't' }],
    'DELETE /authz/v1/orgs/o%2F1/members/u%3F1': () => [204],
    'POST /authz/v1/invitations/accept': () => [200, { org_id: 'o1', role: 'member' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  const invitation = await auth.invitations.create('o/1', { email: 'b@example.test', role: 'member', expiresIn: 3600 })
  assert.equal(invitation.token, 't')
  assert.deepEqual(api.calls[1].body, { email: 'b@example.test', role: 'member', expires_in: 3600 })
  assert.equal(await auth.members.remove('o/1', 'u?1'), null)
  assert.deepEqual(await auth.invitations.accept('tok'), { org_id: 'o1', role: 'member' })
})
