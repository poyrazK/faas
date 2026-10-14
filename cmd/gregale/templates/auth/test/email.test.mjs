import test from 'node:test'
import assert from 'node:assert/strict'
import { createHmac } from 'node:crypto'
import { once } from 'node:events'
import { createHookServer, createMailer, EmailError, hookEnv, messagesFor, newHookSecret, verifyWebhook } from '../email.mjs'
import { emailConfig } from '../config.mjs'

const api = 'https://shop-auth.gregale.app/auth/v1'

function sign(secret, id, timestamp, body) {
  const key = Buffer.from(secret.replace(/^v1,whsec_/, ''), 'base64')
  return 'v1,' + createHmac('sha256', key).update(`${id}.${timestamp}.${body}`).digest('base64')
}

test('verifies Standard Webhooks signatures from GoTrue', () => {
  const secret = newHookSecret()
  assert.match(secret, /^v1,whsec_[A-Za-z0-9+/]{43}=$/)
  const now = Date.now()
  const timestamp = String(Math.floor(now / 1000))
  const body = '{"email_data":{}}'
  const headers = { 'webhook-id': 'msg_1', 'webhook-timestamp': timestamp, 'webhook-signature': sign(secret, 'msg_1', timestamp, body) }
  assert.equal(verifyWebhook(secret, headers, Buffer.from(body), now), true)
  assert.equal(verifyWebhook(secret, { ...headers, 'webhook-signature': `v1,AAAA, ${headers['webhook-signature']}` }, Buffer.from(body), now), true)
  assert.equal(verifyWebhook(newHookSecret(), headers, Buffer.from(body), now), false)
  assert.equal(verifyWebhook(secret, headers, Buffer.from(body + ' '), now), false)
  assert.equal(verifyWebhook(secret, headers, Buffer.from(body), now + 301000), false)
  assert.equal(verifyWebhook(secret, { ...headers, 'webhook-id': 'msg_2' }, Buffer.from(body), now), false)
  assert.equal(verifyWebhook(secret, {}, Buffer.from(body), now), false)
  assert.deepEqual(Object.keys(hookEnv(secret)).sort(), ['GOTRUE_HOOK_SEND_EMAIL_ENABLED', 'GOTRUE_HOOK_SEND_EMAIL_SECRETS', 'GOTRUE_HOOK_SEND_EMAIL_URI'])
  assert.equal(hookEnv(secret).GOTRUE_HOOK_SEND_EMAIL_URI, 'http://127.0.0.1:9998/send-email')
})

test('builds verification emails per GoTrue action', () => {
  const user = { email: 'alice@example.test', new_email: 'alice@new.example.test' }
  const [signup] = messagesFor({ user, email_data: { email_action_type: 'signup', token: '123456', token_hash: 'pkce_abc', redirect_to: 'https://shop.example.com/welcome' } }, { api, app: 'shop.example.com' })
  assert.equal(signup.to, 'alice@example.test')
  assert.equal(signup.subject, 'Confirm your email')
  const link = new URL(signup.text.split('\n').find(line => line.startsWith('https://')))
  assert.equal(link.origin + link.pathname, `${api}/verify`)
  assert.deepEqual(Object.fromEntries(link.searchParams), { token: 'pkce_abc', type: 'signup', redirect_to: 'https://shop.example.com/welcome' })
  assert.match(signup.text, /Code: 123456/)
  assert.match(signup.html, /&amp;type=signup/)

  const secure = messagesFor({ user, email_data: { email_action_type: 'email_change', token: '111111', token_hash: 'hash-new-address', token_new: '222222', token_hash_new: 'hash-current-address' } }, { api, app: 'x' })
  assert.deepEqual(secure.map(m => m.to), ['alice@example.test', 'alice@new.example.test'])
  assert.match(secure[0].text, /token=hash-current-address/)
  assert.match(secure[1].text, /token=hash-new-address/)
  const single = messagesFor({ user, email_data: { email_action_type: 'email_change', token: '333333', token_hash: 'only' } }, { api, app: 'x' })
  assert.deepEqual(single.map(m => m.to), ['alice@new.example.test'])

  const [otp] = messagesFor({ user, email_data: { email_action_type: 'reauthentication', token: '444444' } }, { api, app: 'x' })
  assert.match(otp.text, /Code: 444444/)
  assert.doesNotMatch(otp.text, /https:/)
  const [notice] = messagesFor({ user, email_data: { email_action_type: 'password_changed_notification' } }, { api, app: 'x' })
  assert.equal(notice.subject, 'Your password was changed')
  assert.throws(() => messagesFor({ user, email_data: { email_action_type: 'surprise' } }, { api, app: 'x' }), EmailError)
  const [hostile] = messagesFor({ user, email_data: { email_action_type: 'magiclink', token_hash: 'h', redirect_to: 'https://x.test/"><script>' } }, { api, app: '<b>app</b>' })
  assert.doesNotMatch(hostile.html, /<script>|<b>/)
})

function fakeFetch(status = 200) {
  const calls = []
  const impl = async (url, init) => {
    calls.push({ url, init, body: JSON.parse(init.body) })
    if (status === 'throw') throw new Error('network')
    return new Response('{}', { status })
  }
  return { calls, impl }
}

test('sends through Resend and Postmark HTTPS APIs', async () => {
  const m = { to: 'bob@example.test', subject: 'S', text: 'T', html: '<p>T</p>' }
  const resend = fakeFetch()
  await createMailer({ provider: 'resend', apiKey: 're_key_123', from: 'Shop <auth@shop.example.com>' }, resend.impl).send(m, 'msg_1-0')
  assert.equal(resend.calls[0].url, 'https://api.resend.com/emails')
  assert.equal(resend.calls[0].init.headers.Authorization, 'Bearer re_key_123')
  assert.equal(resend.calls[0].init.headers['Idempotency-Key'], 'msg_1-0')
  assert.equal(resend.calls[0].init.redirect, 'error')
  assert.deepEqual(resend.calls[0].body, { from: 'Shop <auth@shop.example.com>', to: ['bob@example.test'], subject: 'S', text: 'T', html: '<p>T</p>' })
  const postmark = fakeFetch()
  await createMailer({ provider: 'postmark', apiKey: 'pm-token-1', from: 'auth@shop.example.com' }, postmark.impl).send(m)
  assert.equal(postmark.calls[0].url, 'https://api.postmarkapp.com/email')
  assert.equal(postmark.calls[0].init.headers['X-Postmark-Server-Token'], 'pm-token-1')
  assert.equal(postmark.calls[0].body.MessageStream, 'outbound')
  for (const [status, code] of [[429, 'email_provider_unavailable'], [503, 'email_provider_unavailable'], [422, 'email_provider_rejected'], ['throw', 'email_provider_unavailable']]) {
    await assert.rejects(createMailer({ provider: 'resend', apiKey: 'k_12345678', from: 'a@b.co' }, fakeFetch(status).impl).send(m), { code })
  }
  assert.equal(createMailer(null), null)
})

test('validates the email provider settings', () => {
  assert.equal(emailConfig({}), null)
  assert.deepEqual(emailConfig({ AUTH_EMAIL_PROVIDER: 'resend', AUTH_EMAIL_API_KEY: 're_12345678', AUTH_EMAIL_FROM: 'Shop <auth@shop.example.com>' }), { provider: 'resend', apiKey: 're_12345678', from: 'Shop <auth@shop.example.com>' })
  for (const env of [
    { AUTH_EMAIL_PROVIDER: 'smtp', AUTH_EMAIL_API_KEY: 're_12345678', AUTH_EMAIL_FROM: 'a@b.co' },
    { AUTH_EMAIL_PROVIDER: 'resend', AUTH_EMAIL_FROM: 'a@b.co' },
    { AUTH_EMAIL_PROVIDER: 'resend', AUTH_EMAIL_API_KEY: 're_12345678', AUTH_EMAIL_FROM: 'not an address' },
    { AUTH_EMAIL_PROVIDER: 'resend', AUTH_EMAIL_API_KEY: 're_12345678', AUTH_EMAIL_FROM: 'a@b.co\r\nBcc: x@y.z' },
    { AUTH_EMAIL_API_KEY: 're_12345678' },
  ]) {
    assert.throws(() => emailConfig(env), undefined, JSON.stringify(env))
  }
})

test('the loopback hook delivers signed payloads and reports failures to GoTrue', async t => {
  const secret = newHookSecret()
  const sent = []
  for (const [mailer, expect] of [
    [{ send: async (m, key) => { sent.push({ m, key }) } }, {}],
    [null, { error: { http_code: 503, message: 'email_provider_unconfigured' } }],
    [{ send: async () => { throw new EmailError(502, 'email_provider_rejected') } }, { error: { http_code: 502, message: 'email_provider_rejected' } }],
  ]) {
    const server = createHookServer({ secret, mailer, api, app: 'shop.example.com' })
    server.listen(0, '127.0.0.1')
    await once(server, 'listening')
    t.after(() => server.close())
    const url = `http://127.0.0.1:${server.address().port}/send-email`
    const body = JSON.stringify({ user: { email: 'alice@example.test' }, email_data: { email_action_type: 'magiclink', token_hash: 'h', token: '1' } })
    const timestamp = String(Math.floor(Date.now() / 1000))
    const response = await fetch(url, { method: 'POST', body, headers: { 'webhook-id': 'msg_9', 'webhook-timestamp': timestamp, 'webhook-signature': sign(secret, 'msg_9', timestamp, body) } })
    assert.equal(response.status, 200)
    assert.deepEqual(await response.json(), expect)
    const forged = await fetch(url, { method: 'POST', body, headers: { 'webhook-id': 'msg_9', 'webhook-timestamp': timestamp, 'webhook-signature': 'v1,AAAA' } })
    assert.equal(forged.status, 401)
  }
  assert.equal(sent.length, 1)
  assert.equal(sent[0].key, 'msg_9-0')
  assert.equal(sent[0].m.to, 'alice@example.test')
})
