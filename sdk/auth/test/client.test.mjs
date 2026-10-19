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

test('a sign-up the Auth app refuses carries its code', async () => {
  // GoTrue v2.197.0's body for a hook refusal.
  const api = server({ 'POST /auth/v1/signup': () => [403, { code: 403, error_code: 'unknown', msg: 'waitlisted' }] })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await assert.rejects(auth.signUp({ email: 'a@example.test', password: 'pw' }), err => err.status === 403 && err.code === 'waitlisted')
})

test('a session support staff opened says who acts', async () => {
  const jwt = claims => `eyJhbGciOiJFUzI1NiJ9.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.sig`
  let next = { ...tokens(1), access_token: jwt({ sub: 'u1' }) }
  const api = server({ 'POST /auth/v1/token': () => [200, next] })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal(await auth.actor(), null)
  next = { ...tokens(2), access_token: jwt({ sub: 'u1', act: { sub: 'admin:0A1B2C3D' } }) }
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal(await auth.actor(), 'admin:0A1B2C3D')
})

test('reads an organization audit log a page at a time', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'GET /authz/v1/orgs/o1/audit-log': call => [200, { entries: [{ id: 'e1', action: 'member.added' }], next: call.url.search.includes('cursor') ? null : 'WyIyMDI2Il0' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  const first = await auth.organizations.auditLog('o1', { limit: 20 })
  assert.equal(api.calls.at(-1).url.search, '?limit=20')
  assert.equal((await auth.organizations.auditLog('o1', { cursor: first.next })).next, null)
  assert.equal(api.calls.at(-1).url.search, '?cursor=WyIyMDI2Il0')
})

test('signs in with an Ethereum or Solana wallet, in the message grammar GoTrue parses', async () => {
  const api = server({ 'POST /auth/v1/token': () => [200, tokens(1)] })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const asked = []
  const ethereum = { request: async ({ method, params }) => {
    asked.push(method)
    if (method === 'eth_requestAccounts') return ['0x52908400098527886E0F7030069857D2E4169EE7']
    if (method === 'eth_chainId') return '0x1'
    if (method === 'personal_sign') { asked.push(Buffer.from(params[0].slice(2), 'hex').toString('utf8')); return '0x' + 'ab'.repeat(65) }
  } }
  await auth.signInWithWeb3({ chain: 'ethereum', wallet: ethereum, uri: 'https://shop.example.com' })
  const call = api.calls.at(-1)
  assert.equal(call.url.search, '?grant_type=web3')
  assert.equal(call.body.chain, 'ethereum')
  assert.equal(call.body.signature, '0x' + 'ab'.repeat(65))
  assert.equal(call.body.message, asked.at(-1), 'the signed text is what is sent')
  const lines = call.body.message.split('\n')
  assert.deepEqual(lines.slice(0, 6), ['shop.example.com wants you to sign in with your Ethereum account:', '0x52908400098527886E0F7030069857D2E4169EE7', '', 'Sign in to shop.example.com', '', 'URI: https://shop.example.com'])
  assert.deepEqual(lines.slice(6, 8), ['Version: 1', 'Chain ID: 1'])
  assert.match(lines[8], /^Nonce: [0-9a-f]{32}$/)
  assert.match(lines[9], /^Issued At: \d{4}-\d{2}-\d{2}T/)
  const solana = { publicKey: null, connect: async () => { solana.publicKey = { toBase58: () => '7S3P4HxJpyyigGzodYwHtCxZyUQe9JiBMHyRWXArAaKv' } },
    signMessage: async bytes => ({ signature: new Uint8Array(64).fill(7), signed: bytes }) }
  await auth.signInWithWeb3({ chain: 'solana', wallet: solana, uri: 'https://shop.example.com/app', statement: 'Welcome\nback' })
  const sol = api.calls.at(-1).body
  assert.equal(sol.chain, 'solana')
  assert.equal(Buffer.from(sol.signature, 'base64').length, 64)
  assert.deepEqual(sol.message.split('\n').slice(0, 7), ['shop.example.com wants you to sign in with your Solana account:', '7S3P4HxJpyyigGzodYwHtCxZyUQe9JiBMHyRWXArAaKv', '', 'Welcome back', '', 'URI: https://shop.example.com/app', 'Version: 1'])
  assert.ok(!sol.message.includes('Chain ID'))
  await assert.rejects(auth.signInWithWeb3({ chain: 'ethereum', uri: 'https://shop.example.com' }), err => err.code === 'web3_wallet_not_found')
  await assert.rejects(auth.signInWithWeb3({ chain: 'bitcoin', wallet: ethereum, uri: 'https://shop.example.com' }), err => err.code === 'web3_unsupported_chain')
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

test('second factors enrol, challenge and verify to aal2', async () => {
  const jwt = level => `h.${Buffer.from(JSON.stringify({ aal: level })).toString('base64url')}.s`
  const session = level => ({ access_token: jwt(level), refresh_token: `r-${level}`, expires_in: 600, user: { id: 'u1' } })
  const api = server({
    'POST /auth/v1/token': () => [200, session('aal1')],
    'GET /auth/v1/user': () => [200, { id: 'u1', factors: [{ id: 'f1', factor_type: 'totp', status: 'verified' }, { id: 'rc', factor_type: 'recovery_code', status: 'verified' }] }],
    'POST /auth/v1/factors': call => [200, call.body.factor_type === 'totp' ? { id: 'f2', type: 'totp', totp: { qr_code: '<svg xmlns="http://www.w3.org/2000/svg"/>', secret: 'S', uri: 'otpauth://' } } : { id: 'f3', factor_type: 'phone', status: 'unverified' }],
    'POST /auth/v1/factors/f1/challenge': () => [200, { id: 'c1', type: 'totp', expires_at: 1 }],
    'POST /auth/v1/factors/f1/verify': call => call.body.code === '123456' ? [200, session('aal2')] : [422, { error_code: 'mfa_verification_failed' }],
    'POST /auth/v1/factors/recovery-codes/verify': () => [200, session('aal2')],
    'DELETE /auth/v1/factors/f3': () => [200, {}],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const events = []
  auth.onAuthStateChange(event => events.push(event))
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.deepEqual(await auth.mfa.assuranceLevel(), { current: 'aal1', next: 'aal2' })
  const enrollment = await auth.mfa.enrollTOTP({ friendlyName: 'Phone app' })
  assert.deepEqual(enrollment.totp.uri, 'otpauth://')
  assert.equal(enrollment.totp.qr_code, 'data:image/svg+xml;charset=utf-8,%3Csvg%20xmlns%3D%22http%3A%2F%2Fwww.w3.org%2F2000%2Fsvg%22%2F%3E', 'GoTrue SVG becomes an image URL')
  assert.deepEqual(api.calls.at(-1).body, { factor_type: 'totp', friendly_name: 'Phone app' })
  await auth.mfa.enrollPhone({ phone: '+447700900123' })
  assert.deepEqual(api.calls.at(-1).body, { factor_type: 'phone', phone: '+447700900123' })
  const challenge = await auth.mfa.challenge('f1')
  await assert.rejects(auth.mfa.verify({ factorId: 'f1', challengeId: challenge.id, code: '000000' }), { status: 422, code: 'mfa_verification_failed' })
  await auth.mfa.verify({ factorId: 'f1', challengeId: challenge.id, code: '123456' })
  assert.deepEqual(api.calls.at(-1).body, { challenge_id: 'c1', code: '123456' })
  assert.deepEqual(await auth.mfa.assuranceLevel(), { current: 'aal2', next: 'aal2' })
  await auth.mfa.recoveryCodes.verify('abcd-efgh')
  assert.deepEqual(api.calls.at(-1).body, { code: 'abcd-efgh' })
  await auth.mfa.unenroll('f3')
  assert.deepEqual(events, ['SIGNED_IN', 'MFA_VERIFIED', 'MFA_VERIFIED'])
})

test('organization SSO verifies domains and manages providers', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'POST /authz/v1/orgs/o1/domains': call => [201, { domain: call.body.domain, verified: false, verified_at: null, record: { type: 'TXT', name: '_gregale-auth.acme.com', value: 'gregale-auth-verification=t' } }],
    'POST /authz/v1/orgs/o1/domains/acme.com/verify': () => [200, { domain: 'acme.com', verified: true }],
    'POST /authz/v1/orgs/o1/sso': call => [201, { id: 'p1', domains: call.body.domains, role: call.body.role }],
    'POST /authz/v1/orgs/o1/sso/p1/scim-token': () => [201, { token: 'gscim_x', scim_url: 'https://shop-auth.gregale.app/scim/v2' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal((await auth.orgSSO.domains.add('o1', 'acme.com')).record.name, '_gregale-auth.acme.com')
  assert.equal((await auth.orgSSO.domains.verify('o1', 'acme.com')).verified, true)
  assert.deepEqual(api.calls.at(-1).body, {})
  await auth.orgSSO.providers.add('o1', { metadataUrl: 'https://idp.acme.com/m', domains: ['acme.com'], role: 'member' })
  assert.deepEqual(api.calls.at(-1).body, { metadata_url: 'https://idp.acme.com/m', domains: ['acme.com'], role: 'member' })
  assert.equal((await auth.orgSSO.providers.scimToken('o1', 'p1')).token, 'gscim_x')
  assert.equal(api.calls.at(-1).headers.Authorization, 'Bearer access-1')
})

test('lists and revokes the apps a user connected through the OAuth server', async () => {
  const grant = { client: { id: 'c1', name: 'Claude' }, scopes: ['openid'], granted_at: '2026-10-17T00:00:00Z' }
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'GET /auth/v1/user/oauth/grants': () => [200, [grant]],
    'DELETE /auth/v1/user/oauth/grants': () => [204, undefined],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.deepEqual(await auth.connectedApps.list(), [grant])
  await auth.connectedApps.revoke('c1')
  const revoke = api.calls.at(-1)
  assert.equal(revoke.url.searchParams.get('client_id'), 'c1')
  assert.equal(revoke.headers.Authorization, 'Bearer access-1')
})

test('anonymous users sign in, link a provider and unlink a sign-in method', async () => {
  const api = server({
    'POST /auth/v1/signup': () => [200, { ...tokens(1), user: { id: 'u1', is_anonymous: true } }],
    'GET /auth/v1/user/identities/authorize': () => [200, { url: 'https://github.com/login/oauth/authorize?x=1' }],
    'DELETE /auth/v1/user/identities/i1': () => [204, undefined],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const session = await auth.signInAnonymously({ captchaToken: 'cap' })
  assert.equal(session.accessToken, 'access-1')
  assert.deepEqual(api.calls[0].body, { gotrue_meta_security: { captcha_token: 'cap' } })
  assert.deepEqual(await auth.linkIdentity({ provider: 'github', redirectTo: 'https://shop.example.com/app' }), { url: 'https://github.com/login/oauth/authorize?x=1' })
  const link = api.calls.at(-1)
  assert.equal(link.url.searchParams.get('provider'), 'github')
  assert.equal(link.url.searchParams.get('skip_http_redirect'), 'true')
  assert.equal(link.url.searchParams.get('redirect_to'), 'https://shop.example.com/app')
  assert.equal(link.headers.Authorization, 'Bearer access-1')
  await auth.unlinkIdentity('i1')
  assert.equal(api.calls.at(-1).key, 'DELETE /auth/v1/user/identities/i1')
})

test('lists the user own sessions, ends one and signs out the others', async () => {
  const listed = [{ id: 's2', created_at: '2026-10-18T09:00:00Z', last_active_at: '2026-10-18T10:00:00Z', user_agent: 'Mozilla/5.0', ip: '203.0.113.7', aal: 'aal1', current: false }]
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'GET /authz/v1/sessions': () => [200, { sessions: listed }],
    'DELETE /authz/v1/sessions/s2': () => [204, undefined],
    'POST /auth/v1/logout': () => [204, undefined],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.deepEqual(await auth.sessions.list(), listed)
  await auth.sessions.revoke('s2')
  assert.equal(api.calls.at(-1).headers.Authorization, 'Bearer access-1')
  await auth.signOut({ scope: 'others' })
  assert.equal(api.calls.at(-1).url.searchParams.get('scope'), 'others')
  assert.notEqual(await auth.getSession(), null, 'signing the others out keeps this session')
  assert.equal(auth.hostedAccountUrl({ redirectTo: 'https://shop.example.com/app' }), `${origin}/ui/account?redirect_to=https%3A%2F%2Fshop.example.com%2Fapp`)
})

test('deletes the user own account and forgets the session', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'DELETE /authz/v1/account': () => [200, { deleted: true, organizations_deleted: 1 }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  const events = []
  auth.onAuthStateChange(event => events.push(event))
  assert.deepEqual(await auth.deleteAccount(), { organizations_deleted: 1 })
  assert.equal(api.calls.at(-1).headers.Authorization, 'Bearer access-1')
  assert.equal(await auth.getSession(), null)
  assert.deepEqual(events, ['SIGNED_OUT'])
})

test('chooses the active organization and reads it from the refreshed token', async () => {
  const jwt = claims => `eyJhbGciOiJFUzI1NiJ9.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.sig`
  const org = { org_id: 'o1', org_slug: 'acme', org_role: 'admin', org_permissions: ['org:read', 7] }
  let next = { ...tokens(1), access_token: jwt({ sub: 'u1' }) }
  const api = server({
    'POST /auth/v1/token': () => [200, next],
    'POST /authz/v1/active-organization': call => [200, { organization: call.body.org ? { id: call.body.org, slug: 'acme', role: 'admin' } : null }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal(await auth.organizations.active(), null)
  next = { ...tokens(2), access_token: jwt({ sub: 'u1', ...org }) }
  assert.deepEqual(await auth.organizations.setActive('o1'), { id: 'o1', slug: 'acme', role: 'admin', permissions: ['org:read'] })
  assert.deepEqual(api.calls.map(c => c.key).slice(-2), ['POST /authz/v1/active-organization', 'POST /auth/v1/token'], 'set, then refresh')
  assert.equal((await auth.organizations.active()).slug, 'acme')
  next = { ...tokens(3), access_token: jwt({ sub: 'u1' }) }
  assert.equal(await auth.organizations.setActive(null), null)
  assert.deepEqual(api.calls.at(-2).body, { org: null })
})

test('an organization requires a second factor, and members without one are told so', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1)],
    'PATCH /authz/v1/orgs/o1': call => [200, { id: 'o1', slug: 'acme', name: 'Acme', mfa_required: call.body.mfa_required }],
    'GET /authz/v1/orgs/o2/members': () => [403, { status: 403, code: 'mfa_required' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  await auth.signInWithPassword({ email: 'a@example.test', password: 'pw' })
  assert.equal((await auth.organizations.setMfaRequired('o1', true)).mfa_required, true)
  assert.deepEqual(api.calls.at(-1).body, { mfa_required: true })
  await assert.rejects(auth.members.list('o2'), err => err.status === 403 && err.code === 'mfa_required')
})
