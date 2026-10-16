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

test('passkeys: sign in, register, list, rename and remove through WebAuthn', async () => {
  const buf = values => Uint8Array.from(values).buffer
  const api = server({
    'POST /auth/v1/passkeys/authentication/options': () => [200, { challenge_id: 'c1', options: { challenge: 'AAH6', rpId: 'shop-auth.gregale.app', allowCredentials: [{ type: 'public-key', id: 'AQI' }] } }],
    'POST /auth/v1/passkeys/authentication/verify': () => [200, tokens(1)],
    'POST /auth/v1/passkeys/registration/options': () => [200, { challenge_id: 'c2', options: { challenge: 'AAH6', user: { id: 'AQI', name: 'a@example.test' }, rp: { id: 'shop-auth.gregale.app' } } }],
    'POST /auth/v1/passkeys/registration/verify': () => [200, { id: 'pk1' }],
    'GET /auth/v1/passkeys': () => [200, [{ id: 'pk1', friendly_name: 'Laptop' }]],
    'PATCH /auth/v1/passkeys/pk1': () => [200, { id: 'pk1' }],
    'DELETE /auth/v1/passkeys/pk1': () => [200, {}],
  })
  const seen = {}
  const credentials = {
    get: async ({ publicKey }) => {
      seen.get = publicKey
      return { id: 'AQI', rawId: buf([1, 2]), type: 'public-key', getClientExtensionResults: () => ({}), response: { clientDataJSON: buf([3]), authenticatorData: buf([4]), signature: buf([5]), userHandle: buf([6]) } }
    },
    create: async ({ publicKey }) => {
      seen.create = publicKey
      return { id: 'AQI', rawId: buf([1, 2]), type: 'public-key', response: { clientDataJSON: buf([3]), attestationObject: buf([7]), getTransports: () => ['internal'] } }
    },
  }
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null, credentials })
  const session = await auth.signInWithPasskey({ captchaToken: 'tok' })
  assert.equal(session.accessToken, 'access-1')
  assert.deepEqual(api.calls[0].body, { gotrue_meta_security: { captcha_token: 'tok' } })
  assert.deepEqual([...new Uint8Array(seen.get.challenge)], [0, 1, 250])
  assert.deepEqual([...new Uint8Array(seen.get.allowCredentials[0].id)], [1, 2])
  const verify = api.calls.find(c => c.key === 'POST /auth/v1/passkeys/authentication/verify')
  assert.deepEqual(verify.body, { challenge_id: 'c1', credential: { id: 'AQI', rawId: 'AQI', type: 'public-key', clientExtensionResults: {}, response: { clientDataJSON: 'Aw', authenticatorData: 'BA', signature: 'BQ', userHandle: 'Bg' } } })
  await auth.passkeys.register()
  assert.deepEqual([...new Uint8Array(seen.create.user.id)], [1, 2])
  const registration = api.calls.find(c => c.key === 'POST /auth/v1/passkeys/registration/verify')
  assert.equal(registration.headers.Authorization, 'Bearer access-1')
  assert.deepEqual(registration.body.credential.response, { clientDataJSON: 'Aw', attestationObject: 'Bw', transports: ['internal'] })
  assert.deepEqual(await auth.passkeys.list(), [{ id: 'pk1', friendly_name: 'Laptop' }])
  await auth.passkeys.rename('pk1', 'Phone')
  assert.deepEqual(api.calls.at(-1).body, { friendly_name: 'Phone' })
  await auth.passkeys.remove('pk1')
  assert.equal(api.calls.at(-1).key, 'DELETE /auth/v1/passkeys/pk1')
  // No WebAuthn outside a browser unless one is supplied.
  await assert.rejects(createAuthClient({ url: origin, fetch: api.fetch, store: null }).signInWithPasskey(), err => err instanceof AuthError && err.code === 'passkeys_unsupported')
})

test('sends the CAPTCHA token the Auth app requires', async () => {
  const api = server({
    'POST /auth/v1/signup': () => [200, tokens(1)],
    'POST /auth/v1/token': () => [200, tokens(2)],
    'POST /auth/v1/otp': () => [200, {}],
    'POST /auth/v1/recover': () => [200, {}],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signUp({ email: 'a@example.test', password: 'pw', captchaToken: 't1' })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw', captchaToken: 't2' })
  await auth.signInWithMagicLink({ email: 'a@example.test', captchaToken: 't3' })
  await auth.resetPasswordForEmail('a@example.test', { captchaToken: 't4' })
  assert.deepEqual(api.calls.map(c => c.body.gotrue_meta_security?.captcha_token), ['t1', 't2', 't3', 't4'])
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal('gotrue_meta_security' in api.calls.at(-1).body, false)
})

test('phone sign-in texts a code and verifies it', async () => {
  const api = server({
    'POST /auth/v1/otp': () => [200, {}],
    'POST /auth/v1/verify': call => call.body.token === '123456' ? [200, tokens(1)] : [403, { error_code: 'otp_expired' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPhone({ phone: '+447700900123', captchaToken: 't1' })
  assert.deepEqual(api.calls[0].body, { phone: '+447700900123', channel: 'sms', create_user: false, gotrue_meta_security: { captcha_token: 't1' } })
  await assert.rejects(auth.verifyPhone({ phone: '+447700900123', code: '000000' }), { status: 403, code: 'otp_expired' })
  const session = await auth.verifyPhone({ phone: '+447700900123', code: '123456' })
  assert.equal(session.accessToken, 'access-1')
  assert.deepEqual(api.calls[2].body, { type: 'sms', phone: '+447700900123', token: '123456' })
  assert.equal((await auth.getSession()).accessToken, 'access-1')
  await auth.signInWithPhone({ phone: '+15555550100', createUser: true })
  assert.equal(api.calls[3].body.create_user, true)
})

test('organization single sign-on returns the identity provider URL', async () => {
  const api = server({ 'POST /auth/v1/sso': call => call.body.domain === 'acme.com' || call.body.provider_id ? [200, { url: 'https://idp.example.com/sso?SAMLRequest=x' }] : [404, { error_code: 'sso_provider_not_found' }] })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  assert.deepEqual(await auth.signInWithSSO({ domain: ' Acme.com ', redirectTo: 'https://shop.example.com/', captchaToken: 't1' }), { url: 'https://idp.example.com/sso?SAMLRequest=x' })
  assert.deepEqual(api.calls[0].body, { domain: 'acme.com', redirect_to: 'https://shop.example.com/', skip_http_redirect: true, gotrue_meta_security: { captcha_token: 't1' } })
  await auth.signInWithSSO({ providerId: 'p1' })
  assert.deepEqual(api.calls[1].body, { provider_id: 'p1', skip_http_redirect: true })
  await assert.rejects(auth.signInWithSSO({ domain: 'globex.com' }), { status: 404, code: 'sso_provider_not_found' })
})
