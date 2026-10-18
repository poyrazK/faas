import test from 'node:test'
import assert from 'node:assert/strict'
import { once } from 'node:events'
import { allowedRedirect, consentView, errorMessage, fragmentParams, handoffURL, secondFactors, tokenAAL } from '../ui/app.js'
import { createServer } from '../server.mjs'

const site = 'https://shop.example.com'
const redirects = ['https://shop.example.com/callback', 'http://localhost:3000/']

test('hands sessions only to the site or an allowed redirect URL', () => {
  assert.equal(allowedRedirect('', site, redirects), site)
  assert.equal(allowedRedirect('https://shop.example.com/account', site, redirects), 'https://shop.example.com/account')
  assert.equal(allowedRedirect('https://shop.example.com/callback/x?y=1', site, redirects), 'https://shop.example.com/callback/x?y=1')
  assert.equal(allowedRedirect('http://localhost:3000/dev', site, redirects), 'http://localhost:3000/dev')
  for (const hostile of ['https://evil.example.com/', 'https://shop.example.com.evil.test/', 'javascript:alert(1)', 'https://user:pw@shop.example.com/', '//evil.example.com', 'not a url', 'http://localhost:3001/']) {
    assert.equal(allowedRedirect(hostile, site, redirects), site, hostile)
  }
})

test('puts the session in the fragment and reads GoTrue fragments', () => {
  const url = new URL(handoffURL('https://shop.example.com/cb#old', { access_token: 'a', refresh_token: 'r', expires_in: 600, expires_at: 1700000600 }))
  assert.equal(url.search, '')
  assert.deepEqual(fragmentParams(url.hash), { access_token: 'a', refresh_token: 'r', expires_in: '600', expires_at: '1700000600', token_type: 'bearer', type: 'signin' })
  assert.deepEqual(fragmentParams('#access_token=x&type=recovery'), { access_token: 'x', type: 'recovery' })
})

test('explains known errors without echoing server text', () => {
  assert.equal(errorMessage({ error_code: 'invalid_credentials' }, 400), 'Wrong email or password.')
  assert.equal(errorMessage({ code: 'invitation_email_mismatch' }, 403), 'This invitation was sent to a different email address.')
  assert.equal(errorMessage({ msg: '<img src=x onerror=alert(1)>' }, 500), 'Something went wrong. Try again.')
  assert.equal(errorMessage(null, 429), 'Too many attempts. Wait a minute and try again.')
  assert.equal(errorMessage({ error_code: 'otp_expired' }, 403), 'That code is wrong or has expired. Request a new one.')
  assert.equal(errorMessage({ error_code: 'over_sms_send_rate_limit' }, 429), 'Too many codes sent. Wait a minute and try again.')
})

test('serves the hosted pages with a strict policy', async t => {
  const server = createServer({ upstreamPort: 1, ui: { siteURL: site, redirectURLs: redirects, app: 'shop.example.com' } })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  const base = `http://127.0.0.1:${server.address().port}`
  for (const path of ['/ui', '/ui/sign-in', '/ui/sign-up', '/ui/forgot-password', '/ui/reset-password', '/ui/invitation', '/ui/phone', '/ui/sso']) {
    const response = await fetch(base + path)
    assert.equal(response.status, 200, path)
    assert.match(response.headers.get('content-type'), /^text\/html/)
    assert.match(response.headers.get('content-security-policy'), /default-src 'none'; script-src 'self'/)
    assert.equal(response.headers.get('x-frame-options'), 'DENY')
    assert.equal(response.headers.get('referrer-policy'), 'no-referrer')
    assert.match(await response.text(), /<script type="module" src="\/ui\/app\.js">/)
  }
  const script = await fetch(`${base}/ui/app.js`)
  assert.match(script.headers.get('content-type'), /^text\/javascript/)
  const config = await (await fetch(`${base}/ui/config.json`)).json()
  assert.deepEqual(config, { site_url: site, redirect_urls: redirects, app: 'shop.example.com', logo_url: null, captcha: null })
  const brand = await fetch(`${base}/ui/brand.css`)
  assert.match(brand.headers.get('content-type'), /^text\/css/)
  assert.equal(await brand.text(), '')
  assert.equal((await fetch(`${base}/ui/../server.mjs`)).status, 404)
  assert.equal((await fetch(`${base}/ui/%2e%2e/config.mjs`)).status, 404)
  assert.equal((await fetch(`${base}/ui/sign-in`, { method: 'POST' })).status, 405)
})

test('converts WebAuthn options and credentials between JSON and buffers', async () => {
  const { fromBase64url, toBase64url, requestOptions, creationOptions, credentialJSON } = await import('../ui/app.js')
  const bytes = Uint8Array.from([0, 1, 250, 251, 252, 253, 254, 255])
  assert.equal(toBase64url(bytes.buffer), 'AAH6-_z9_v8')
  assert.deepEqual(new Uint8Array(fromBase64url('AAH6-_z9_v8')), bytes)
  for (const length of [1, 2, 3, 4, 31, 32, 33]) {
    const sample = Uint8Array.from({ length }, (_, i) => (i * 37) % 256)
    assert.deepEqual(new Uint8Array(fromBase64url(toBase64url(sample.buffer))), sample, `length ${length}`)
  }
  const request = requestOptions({ challenge: 'AAH6', rpId: 'auth.example.com', allowCredentials: [{ type: 'public-key', id: 'AQI' }] })
  assert.deepEqual([...new Uint8Array(request.challenge)], [0, 1, 250])
  assert.deepEqual([...new Uint8Array(request.allowCredentials[0].id)], [1, 2])
  assert.equal(request.rpId, 'auth.example.com')
  assert.equal('allowCredentials' in requestOptions({ challenge: 'AAH6' }), false)
  const creation = creationOptions({ challenge: 'AAH6', user: { id: 'AQI', name: 'a@example.test' }, rp: { id: 'auth.example.com', name: 'Shop' } })
  assert.deepEqual([...new Uint8Array(creation.user.id)], [1, 2])
  assert.equal(creation.user.name, 'a@example.test')
  // Without PublicKeyCredential.toJSON, the response is serialized by hand.
  const buf = values => Uint8Array.from(values).buffer
  const assertion = credentialJSON({ id: 'AQI', rawId: buf([1, 2]), type: 'public-key', authenticatorAttachment: 'platform', getClientExtensionResults: () => ({}),
    response: { clientDataJSON: buf([3]), authenticatorData: buf([4]), signature: buf([5]), userHandle: buf([6]) } })
  assert.deepEqual(assertion, { id: 'AQI', rawId: 'AQI', type: 'public-key', authenticatorAttachment: 'platform', clientExtensionResults: {},
    response: { clientDataJSON: 'Aw', authenticatorData: 'BA', signature: 'BQ', userHandle: 'Bg' } })
  const attestation = credentialJSON({ id: 'AQI', rawId: buf([1, 2]), type: 'public-key', response: { clientDataJSON: buf([3]), attestationObject: buf([7]), getTransports: () => ['internal'] } })
  assert.deepEqual(attestation.response, { clientDataJSON: 'Aw', attestationObject: 'Bw', transports: ['internal'] })
  assert.deepEqual(credentialJSON({ toJSON: () => ({ native: true }) }), { native: true })
})

test('branding and CAPTCHA widen the page policy by exact origins only', async t => {
  const branding = { name: 'Shop', color: '#4f46e5', logoURL: 'https://cdn.example.com/logo.svg', logoOrigin: 'https://cdn.example.com' }
  const captcha = { provider: 'turnstile', siteKey: '1x00000000000000000000AA', script: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit', sources: ['https://challenges.cloudflare.com'] }
  const server = createServer({ upstreamPort: 1, ui: { siteURL: site, redirectURLs: redirects, app: 'Shop', branding, captcha } })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  const base = `http://127.0.0.1:${server.address().port}`
  const page = await fetch(`${base}/ui/sign-in`)
  assert.equal(page.headers.get('content-security-policy'), "default-src 'none'; script-src 'self' https://challenges.cloudflare.com; style-src 'self' https://challenges.cloudflare.com; connect-src 'self' https://challenges.cloudflare.com; img-src 'self' https://cdn.example.com; frame-src https://challenges.cloudflare.com; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
  assert.deepEqual(await (await fetch(`${base}/ui/config.json`)).json(), { site_url: site, redirect_urls: redirects, app: 'Shop', logo_url: 'https://cdn.example.com/logo.svg',
    captcha: { provider: 'turnstile', site_key: '1x00000000000000000000AA', script: 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit' } })
  assert.equal(await (await fetch(`${base}/ui/brand.css`)).text(), ':root { --accent: #4f46e5; }\n')
})

test('a CAPTCHA token is required when the check is shown, and sent once', async () => {
  const { captchaBody } = await import('../ui/app.js')
  assert.deepEqual(captchaBody({ email: 'a@example.test' }, undefined), { email: 'a@example.test' })
  assert.deepEqual(captchaBody({ email: 'a@example.test' }, 'tok'), { email: 'a@example.test', gotrue_meta_security: { captcha_token: 'tok' } })
  assert.throws(() => captchaBody({}, ''), /security check/)
})

test('a sign-in asks for a verified second factor until the session is aal2', () => {
  const token = claims => `x.${Buffer.from(JSON.stringify(claims)).toString('base64url')}.y`
  assert.equal(tokenAAL(token({ aal: 'aal2' })), 'aal2')
  assert.equal(tokenAAL(token({ aal: 'aal1' })), 'aal1')
  assert.equal(tokenAAL(token({})), 'aal1')
  assert.equal(tokenAAL('garbage'), 'aal1')
  const user = { factors: [
    { id: 'f1', factor_type: 'totp', status: 'verified' }, { id: 'f2', factor_type: 'phone', status: 'unverified' },
    { id: 'f3', factor_type: 'phone', status: 'verified', phone: '447700900123' }, { id: 'f4', factor_type: 'recovery_code', status: 'verified' },
    { id: 'f5', factor_type: 'webauthn', status: 'verified' },
  ] }
  assert.deepEqual(secondFactors(user).factors.map(f => f.id), ['f1', 'f3'])
  assert.equal(secondFactors(user).recovery, true)
  assert.deepEqual(secondFactors({}), { factors: [], recovery: false })
  assert.equal(errorMessage({ error_code: 'mfa_verification_failed' }, 422), 'That code is wrong. Try again.')
})

test('the consent page names the client, its permissions, its resource and where it returns', () => {
  const view = consentView({ client: { name: 'Claude' }, user: { email: 'ada@example.com' }, scope: 'openid email custom', redirect_uri: 'http://localhost:6274/oauth/callback', resource: 'https://mcp.shop.example.com/mcp' })
  assert.deepEqual(view, { name: 'Claude', account: 'ada@example.com', abilities: ['Know who you are', 'See your email address', 'Use the custom permission'], resource: 'https://mcp.shop.example.com/mcp', destination: 'localhost:6274' })
  assert.equal(consentView({ redirect_uri: 'cursor://anysphere.cursor-mcp/oauth/callback' }).destination, 'cursor app')
  assert.equal(consentView({ redirect_uri: 'not a url' }).destination, 'an unknown address')
  assert.equal(consentView({ client: { name: '' } }).name, 'An application')
  assert.equal(consentView({ client: { name: 'x'.repeat(500) } }).name.length, 100)
  assert.equal(allowedRedirect('https://shop-auth.gregale.app/ui/consent?authorization_id=a', 'https://shop.example.com', ['https://shop-auth.gregale.app/ui/consent']), 'https://shop-auth.gregale.app/ui/consent?authorization_id=a')
})
