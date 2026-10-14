import test from 'node:test'
import assert from 'node:assert/strict'
import { once } from 'node:events'
import { allowedRedirect, errorMessage, fragmentParams, handoffURL } from '../ui/app.js'
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
})

test('serves the hosted pages with a strict policy', async t => {
  const server = createServer({ upstreamPort: 1, ui: { siteURL: site, redirectURLs: redirects, app: 'shop.example.com' } })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  const base = `http://127.0.0.1:${server.address().port}`
  for (const path of ['/ui', '/ui/sign-in', '/ui/sign-up', '/ui/forgot-password', '/ui/reset-password', '/ui/invitation']) {
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
  assert.deepEqual(config, { site_url: site, redirect_urls: redirects, app: 'shop.example.com' })
  assert.equal((await fetch(`${base}/ui/../server.mjs`)).status, 404)
  assert.equal((await fetch(`${base}/ui/%2e%2e/config.mjs`)).status, 404)
  assert.equal((await fetch(`${base}/ui/sign-in`, { method: 'POST' })).status, 405)
})
