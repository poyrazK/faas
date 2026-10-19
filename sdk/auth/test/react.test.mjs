import test from 'node:test'
import assert from 'node:assert/strict'
import { createElement as h, StrictMode } from 'react'
import TestRenderer from 'react-test-renderer'
import { createAuthClient } from '../dist/index.js'
import { AuthProvider, Authorized, OrganizationSwitcher, SignedIn, SignedOut, SignIn, SignInButton, UserMenu, useActiveOrganization, useAuth } from '../dist/react.js'

globalThis.IS_REACT_ACT_ENVIRONMENT = true
const { act, create } = TestRenderer
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
  return { calls, fetch, count: key => calls.filter(call => call.key === key).length }
}

const jwt = claims => `eyJhbGciOiJFUzI1NiJ9.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.sig`
const tokens = (n, claims) => ({ access_token: jwt(claims), refresh_token: `refresh-${n}`, expires_in: 600 })
function stored(claims) {
  let value = JSON.stringify({ accessToken: jwt(claims), refreshToken: 'refresh-0', expiresAt: Math.floor(Date.now() / 1000) + 600 })
  return { get: () => value, set: next => { value = next }, remove: () => { value = null } }
}

// Lets fetches, auth events and effects run to completion.
async function settle() {
  for (let i = 0; i < 5; i++) await act(() => new Promise(resolve => setTimeout(resolve, 0)))
}

async function render(element) {
  let renderer
  await act(() => { renderer = create(element) })
  await settle()
  return renderer
}

function text(node) {
  if (node == null) return ''
  if (typeof node === 'string') return node
  if (Array.isArray(node)) return node.map(text).join(' ')
  return text(node.children)
}

const shown = renderer => text(renderer.toJSON())
const type = (renderer, name, value) => act(() => renderer.root.findByProps({ name }).props.onChange({ target: { value } }))
async function submit(renderer) {
  await act(() => renderer.root.findByType('form').props.onSubmit({ preventDefault() {} }))
  await settle()
}
async function click(renderer, label) {
  const button = renderer.root.findAll(node => node.type === 'button' && text(node.children) === label)[0]
  assert.ok(button, `no ${label} button in: ${shown(renderer)}`)
  await act(() => button.props.onClick())
  await settle()
}

function withPage(href) {
  const assigned = []
  const location = { href, assign: url => assigned.push(url) }
  globalThis.location = location
  globalThis.history = { replaceState: (_state, _title, url) => { location.href = url } }
  return { assigned, location, restore: () => { delete globalThis.location; delete globalThis.history } }
}

function Probe() {
  const { status, user } = useAuth()
  const org = useActiveOrganization()
  return h('output', null, `status=${status} user=${user?.email ?? '-'} org=${org ? `${org.slug}/${org.role}` : 'none'}`)
}

test('signs in with the form, shows signed-in content and signs out', async () => {
  const api = server({
    'POST /auth/v1/token': call => call.body.password === 'right' ? [200, { ...tokens(1, { sub: 'u1', aal: 'aal1', email: 'a@example.test' }), user: { id: 'u1', email: 'a@example.test' } }] : [400, { error_code: 'invalid_credentials' }],
    'GET /auth/v1/user': () => [200, { id: 'u1', factors: [] }],
    'POST /auth/v1/logout': () => [204],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const r = await render(h(AuthProvider, { client: auth }, h(SignedOut, null, h(SignIn)), h(SignedIn, null, h(UserMenu), 'app-content')))
  assert.match(shown(r), /Sign in/)
  assert.doesNotMatch(shown(r), /app-content/)

  await type(r, 'email', ' a@example.test ')
  await type(r, 'password', 'wrong')
  await submit(r)
  assert.match(shown(r), /Wrong email or password\./)
  assert.equal(api.calls[0].body.email, 'a@example.test')

  await type(r, 'password', 'right')
  await submit(r)
  assert.match(shown(r), /app-content/)
  assert.match(shown(r), /a@example\.test/)
  assert.doesNotMatch(shown(r), /Wrong email/)
  // A user without a second factor is checked once and let in.
  assert.equal(api.count('GET /auth/v1/user'), 1)

  await click(r, 'Sign out')
  assert.doesNotMatch(shown(r), /app-content/)
  assert.match(shown(r), /New here\? Create an account/)
  assert.equal(api.count('POST /auth/v1/logout'), 1)
})

test('renders nothing while loading, then completes a redirect in the URL (strict mode)', async () => {
  const token = jwt({ sub: 'u2', aal: 'aal2', email: 'b@example.test', user_metadata: { name: 'B' } })
  const page = withPage(`https://shop.example/app?tab=1#access_token=${token}&refresh_token=r1&expires_in=600`)
  try {
    const api = server({})
    const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
    let r
    const events = []
    auth.onAuthStateChange(event => events.push(event))
    act(() => { r = create(h(StrictMode, null, h(AuthProvider, { client: auth }, h(SignedOut, null, 'signed-out'), h(SignedIn, null, 'signed-in'), h(Probe)))) })
    assert.equal(shown(r), 'status=loading user=- org=none')
    await settle()
    assert.equal(shown(r), 'signed-in status=signed-in user=b@example.test org=none')
    assert.equal(page.location.href, 'https://shop.example/app?tab=1')
    assert.deepEqual(events, ['SIGNED_IN'])
    // An aal2 session needs no second-step check.
    assert.equal(api.calls.length, 0)
  } finally {
    page.restore()
  }
})

test('asks for the second step before showing signed-in content', async () => {
  const factors = [{ id: 'f1', factor_type: 'totp', status: 'verified', friendly_name: 'Phone app' }, { id: 'f2', factor_type: 'phone', status: 'verified', phone: '+15550100' }, { id: 'rc', factor_type: 'recovery_code', status: 'verified' }, { id: 'f3', factor_type: 'totp', status: 'unverified' }]
  const aal2 = tokens(2, { sub: 'u1', aal: 'aal2', email: 'a@example.test' })
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1, { sub: 'u1', aal: 'aal1', email: 'a@example.test' })],
    'GET /auth/v1/user': () => [200, { id: 'u1', factors }],
    'POST /auth/v1/factors/f1/challenge': () => [200, { id: 'c1', type: 'totp' }],
    'POST /auth/v1/factors/f2/challenge': () => [200, { id: 'c2', type: 'phone' }],
    'POST /auth/v1/factors/f1/verify': call => call.body.code === '123456' && call.body.challenge_id === 'c1' ? [200, aal2] : [422, { error_code: 'mfa_verification_failed' }],
    'POST /auth/v1/factors/recovery-codes/verify': () => [400, { error_code: 'mfa_verification_failed' }],
    'POST /auth/v1/logout': () => [204],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const r = await render(h(AuthProvider, { client: auth }, h(SignedOut, null, h(SignIn)), h(SignedIn, null, 'app-content'), h(Probe)))
  await type(r, 'email', 'a@example.test')
  await type(r, 'password', 'pw')
  await submit(r)
  assert.match(shown(r), /status=second-step/)
  assert.match(shown(r), /Enter the code from Phone app\./)
  assert.doesNotMatch(shown(r), /app-content/)
  assert.match(shown(r), /Text me a code instead/)
  assert.match(shown(r), /Use a recovery code/)

  await click(r, 'Text me a code instead')
  assert.match(shown(r), /We texted a code to \+15550100\./)
  await click(r, 'Use Phone app instead')
  await click(r, 'Use a recovery code')
  assert.match(shown(r), /Recovery code/)
  await type(r, 'code', 'abcd-efgh')
  await submit(r)
  assert.match(shown(r), /That code is wrong\. Try again\./)

  await click(r, 'Use Phone app instead')
  await type(r, 'code', '000000')
  await submit(r)
  assert.match(shown(r), /That code is wrong/)
  await type(r, 'code', ' 123456 ')
  await submit(r)
  assert.match(shown(r), /app-content/)
  assert.match(shown(r), /status=signed-in/)
})

test('a second step can be abandoned for another account', async () => {
  const api = server({
    'POST /auth/v1/token': () => [200, tokens(1, { sub: 'u1', aal: 'aal1' })],
    'GET /auth/v1/user': () => [200, { id: 'u1', factors: [{ id: 'f1', factor_type: 'totp', status: 'verified' }] }],
    'POST /auth/v1/factors/f1/challenge': () => [200, { id: 'c1', type: 'totp' }],
    'POST /auth/v1/logout': () => [204],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const r = await render(h(AuthProvider, { client: auth }, h(SignIn), h(Probe)))
  await type(r, 'email', 'a@example.test')
  await type(r, 'password', 'pw')
  await submit(r)
  assert.match(shown(r), /Enter the code from your authenticator app\./)
  await click(r, 'Use another account')
  assert.match(shown(r), /status=signed-out/)
  assert.ok(r.root.findByProps({ name: 'password' }))
})

test('without requireSecondStep an aal1 session is signed in at once', async () => {
  const api = server({})
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: stored({ sub: 'u1', aal: 'aal1', email: 'a@example.test' }) })
  const r = await render(h(AuthProvider, { client: auth, requireSecondStep: false }, h(Probe)))
  assert.equal(shown(r), 'status=signed-in user=a@example.test org=none')
  assert.equal(api.calls.length, 0)
})

test('switches organization and re-asks authorization with the new token', async () => {
  const orgToken = jwt({ sub: 'u1', aal: 'aal2', email: 'a@example.test', org_id: 'o2', org_slug: 'beta', org_role: 'admin', org_permissions: ['doc:write'] })
  const api = server({
    'GET /authz/v1/orgs': () => [200, { organizations: [{ id: 'o1', slug: 'acme', name: 'Acme', created_at: 'now' }, { id: 'o2', slug: 'beta', name: 'Beta', created_at: 'now' }] }],
    'POST /authz/v1/active-organization': call => call.body.org === 'o2' ? [200, { organization: { id: 'o2', slug: 'beta', role: 'admin' } }] : [404, { code: 'not_found' }],
    'POST /auth/v1/token': () => [200, { access_token: orgToken, refresh_token: 'refresh-1', expires_in: 600 }],
    'POST /authz/v1/authorize': call => [200, { allowed: call.headers.Authorization === `Bearer ${orgToken}` && call.body.action === 'write' }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: stored({ sub: 'u1', aal: 'aal2', email: 'a@example.test' }) })
  const changes = []
  const r = await render(h(AuthProvider, { client: auth },
    h(OrganizationSwitcher, { onChange: org => changes.push(org?.slug ?? null) }), h(Probe),
    h(Authorized, { action: 'write', resource: 'org/o2/doc/1', fallback: 'read-only' }, 'can-write')))
  const select = () => r.root.findByType('select')
  assert.equal(select().props.value, '')
  assert.deepEqual(select().findAllByType('option').map(option => option.props.value), ['', 'o1', 'o2'])
  assert.match(shown(r), /org=none/)
  assert.match(shown(r), /read-only/)

  await act(() => select().props.onChange({ target: { value: 'o2' } }))
  await settle()
  assert.equal(select().props.value, 'o2')
  assert.match(shown(r), /user=a@example\.test org=beta\/admin/)
  assert.match(shown(r), /can-write/)
  assert.deepEqual(changes, ['beta'])
  assert.equal(api.count('POST /authz/v1/authorize'), 2)

  await act(() => select().props.onChange({ target: { value: 'o1' } }))
  await settle()
  assert.match(shown(r), /That organization is not available/)
  assert.equal(select().props.value, 'o2')
})

test('switches organization without an onChange handler', async () => {
  const orgToken = jwt({ sub: 'u1', aal: 'aal2', org_id: 'o1', org_slug: 'acme', org_role: 'owner', org_permissions: ['*'] })
  const api = server({
    'GET /authz/v1/orgs': () => [200, { organizations: [{ id: 'o1', slug: 'acme', name: 'Acme', created_at: 'now' }] }],
    'POST /authz/v1/active-organization': () => [200, { organization: { id: 'o1', slug: 'acme', role: 'owner' } }],
    'POST /auth/v1/token': () => [200, { access_token: orgToken, refresh_token: 'refresh-1', expires_in: 600 }],
  })
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: stored({ sub: 'u1', aal: 'aal2' }) })
  const r = await render(h(AuthProvider, { client: auth }, h(OrganizationSwitcher), h(Probe)))
  await act(() => r.root.findByType('select').props.onChange({ target: { value: 'o1' } }))
  await settle()
  assert.equal(api.count('POST /authz/v1/active-organization'), 1)
  assert.match(shown(r), /org=acme\/owner/)
})

test('signed out, authorization is false and the switcher renders nothing', async () => {
  const api = server({})
  const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
  const r = await render(h(AuthProvider, { client: auth }, h(OrganizationSwitcher), h(Authorized, { action: 'read', resource: 'org/o1/doc/1', fallback: 'no' }, 'yes')))
  assert.equal(shown(r), 'no')
  assert.equal(api.calls.length, 0)
})

test('OAuth, sign-in links, sign-up confirmation and the hosted page', async () => {
  const page = withPage('https://shop.example/login#stale')
  try {
    const api = server({
      'POST /auth/v1/otp': () => [200, {}],
      'POST /auth/v1/signup': () => [200, { id: 'u3', email: 'c@example.test' }],
    })
    const auth = createAuthClient({ url: origin, fetch: api.fetch, store: null })
    const links = await render(h(AuthProvider, { client: auth, handleRedirect: false }, h(SignIn, { providers: ['google', 'github'], password: false, magicLink: true, captchaToken: 'cap' }), h(SignInButton, { redirectTo: 'https://shop.example/app' })))
    assert.equal(links.root.findAllByProps({ name: 'password' }).length, 0)
    await click(links, 'Continue with GitHub')
    assert.equal(page.assigned.at(-1), `${origin}/auth/v1/authorize?provider=github&redirect_to=${encodeURIComponent('https://shop.example/login')}`)
    await type(links, 'email', 'c@example.test')
    await submit(links)
    const otp = api.calls.find(call => call.key === 'POST /auth/v1/otp')
    assert.deepEqual(otp.body, { email: 'c@example.test', create_user: true, gotrue_meta_security: { captcha_token: 'cap' } })
    assert.equal(otp.url.searchParams.get('redirect_to'), 'https://shop.example/login')
    assert.match(shown(links), /Check your email for a sign-in link\./)
    await click(links, 'Sign in')
    assert.equal(page.assigned.at(-1), `${origin}/ui/sign-in?redirect_to=${encodeURIComponent('https://shop.example/app')}`)

    const signUp = await render(h(AuthProvider, { client: auth, handleRedirect: false }, h(SignIn, { redirectTo: 'https://shop.example/welcome' })))
    await click(signUp, 'New here? Create an account')
    assert.equal(signUp.root.findByProps({ name: 'password' }).props.autoComplete, 'new-password')
    await type(signUp, 'email', 'c@example.test')
    await type(signUp, 'password', 'long password')
    await submit(signUp)
    const created = api.calls.find(call => call.key === 'POST /auth/v1/signup')
    assert.equal(created.url.searchParams.get('redirect_to'), 'https://shop.example/welcome')
    assert.match(shown(signUp), /Check your email to confirm your address, then sign in\./)
  } finally {
    page.restore()
  }
})
