import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto'
import http from 'node:http'
import { limits } from './config.mjs'

// Email for application Auth (ADR-1190). Tenant egress blocks SMTP, so
// GoTrue's send-email hook calls this module on loopback and it delivers
// through the customer's provider over HTTPS. Links and codes never reach
// a log line.

export const hookAddress = Object.freeze({ host: '127.0.0.1', port: 9998, path: '/send-email', accessTokenPath: '/custom-access-token' })

// A fresh secret per boot: GoTrue and the front start together, so nothing
// needs sealing, and a snapshot restores both halves of the pair.
export function newHookSecret() {
  return `v1,whsec_${randomBytes(32).toString('base64')}`
}

// The custom access token hook runs on every token GoTrue issues, so it is
// on only when it changes tokens: with the OAuth server (client tokens,
// oauth.mjs) or organization claims (AUTH_ORG_CLAIMS, authz.mjs).
export function hookEnv(secret, { accessToken = false } = {}) {
  return {
    GOTRUE_HOOK_SEND_EMAIL_ENABLED: 'true',
    GOTRUE_HOOK_SEND_EMAIL_URI: `http://${hookAddress.host}:${hookAddress.port}${hookAddress.path}`,
    GOTRUE_HOOK_SEND_EMAIL_SECRETS: secret,
    ...(accessToken ? {
      GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_ENABLED: 'true',
      GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_URI: `http://${hookAddress.host}:${hookAddress.port}${hookAddress.accessTokenPath}`,
      GOTRUE_HOOK_CUSTOM_ACCESS_TOKEN_SECRETS: secret,
    } : {}),
  }
}

// verifyWebhook checks a Standard Webhooks signature: base64 HMAC-SHA256 of
// "id.timestamp.body" under the whsec_ key, within five minutes.
export function verifyWebhook(secret, headers, body, now = Date.now()) {
  const id = headers['webhook-id']
  const timestamp = headers['webhook-timestamp']
  const signatures = headers['webhook-signature']
  if (typeof id !== 'string' || typeof timestamp !== 'string' || typeof signatures !== 'string' || !/^\d{1,12}$/.test(timestamp)) return false
  if (Math.abs(now / 1000 - Number(timestamp)) > 300) return false
  const key = Buffer.from(secret.replace(/^v1,whsec_/, ''), 'base64')
  const expected = createHmac('sha256', key).update(`${id}.${timestamp}.`).update(body).digest()
  return signatures.split(/[\s,]+/).some((part, i, parts) => {
    if (part !== 'v1' || i + 1 >= parts.length) return false
    const candidate = Buffer.from(parts[i + 1], 'base64')
    return candidate.length === expected.length && timingSafeEqual(candidate, expected)
  })
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c])
}

function verifyLink(api, tokenHash, type, redirectTo) {
  const url = new URL(`${api}/verify`)
  url.searchParams.set('token', tokenHash)
  url.searchParams.set('type', type)
  if (redirectTo) url.searchParams.set('redirect_to', redirectTo)
  return url.toString()
}

function message(to, subject, lines, link, code) {
  const text = [...lines, ...(link ? ['', link] : []), ...(code ? ['', `Code: ${code}`] : [])].join('\n')
  const html = [...lines.map(line => `<p>${escapeHTML(line)}</p>`),
    ...(link ? [`<p><a href="${escapeHTML(link)}">${escapeHTML(subject)}</a></p>`] : []),
    ...(code ? [`<p>Code: <strong>${escapeHTML(code)}</strong></p>`] : [])].join('\n')
  return { to, subject, text, html }
}

const linkActions = {
  signup: ['Confirm your email', 'Confirm your email address to finish signing up for {app}.'],
  invite: ['You have been invited', 'You have been invited to create an account on {app}.'],
  magiclink: ['Your sign-in link', 'Use this link to sign in to {app}. It expires soon and works once.'],
  recovery: ['Reset your password', 'Use this link to choose a new password for {app}. If you did not ask for this, ignore this email.'],
}

const notifications = {
  password_changed_notification: 'Your password was changed',
  email_changed_notification: 'Your email address was changed',
  phone_changed_notification: 'Your phone number was changed',
  identity_linked_notification: 'A sign-in method was linked',
  identity_unlinked_notification: 'A sign-in method was removed',
  mfa_factor_enrolled_notification: 'A second factor was added',
  mfa_factor_unenrolled_notification: 'A second factor was removed',
}

// messagesFor turns one GoTrue send-email hook payload into the emails to
// deliver. For a secure email change GoTrue pairs token_hash_new with the
// current address and token_hash with the new one.
export function messagesFor(payload, { api, app }) {
  const user = payload?.user ?? {}
  const data = payload?.email_data ?? {}
  const action = data.email_action_type
  const say = text => text.replace('{app}', app)
  if (linkActions[action]) {
    const [subject, line] = linkActions[action]
    return [message(user.email, subject, [say(line)], verifyLink(api, data.token_hash, action, data.redirect_to), data.token)]
  }
  if (action === 'email_change') {
    const lines = [say('Confirm the change of your email address on {app}.')]
    if (data.token_hash_new) {
      return [
        message(user.email, 'Confirm your email change', lines, verifyLink(api, data.token_hash_new, 'email_change', data.redirect_to), data.token),
        message(user.new_email, 'Confirm your new email', lines, verifyLink(api, data.token_hash, 'email_change', data.redirect_to), data.token_new),
      ]
    }
    return [message(user.new_email, 'Confirm your new email', lines, verifyLink(api, data.token_hash, 'email_change', data.redirect_to), data.token)]
  }
  if (action === 'email' || action === 'reauthentication') {
    return [message(user.email, 'Your verification code', [say('Enter this code in {app}. It expires soon.')], null, data.token)]
  }
  if (notifications[action]) {
    return [message(user.email, notifications[action], [say('This is a security notice from {app}. If this was not you, reset your password.')])]
  }
  throw new EmailError(400, 'unsupported_email_action')
}

export class EmailError extends Error {
  constructor(status, code) { super(code); this.status = status; this.code = code }
}

const providers = {
  resend: (config, m, idempotencyKey) => ({
    url: 'https://api.resend.com/emails',
    headers: { Authorization: `Bearer ${config.apiKey}`, ...(idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : {}) },
    body: { from: config.from, to: [m.to], subject: m.subject, text: m.text, html: m.html },
  }),
  postmark: (config, m) => ({
    url: 'https://api.postmarkapp.com/email',
    headers: { 'X-Postmark-Server-Token': config.apiKey, Accept: 'application/json' },
    body: { From: config.from, To: m.to, Subject: m.subject, TextBody: m.text, HtmlBody: m.html, MessageStream: 'outbound' },
  }),
}

// createMailer returns null when no provider is configured.
export function createMailer(config, fetchImpl = fetch) {
  if (!config?.provider) return null
  const build = providers[config.provider]
  return {
    async send(m, idempotencyKey) {
      if (typeof m.to !== 'string' || !m.to.includes('@')) throw new EmailError(400, 'missing_recipient')
      const request = build(config, m, idempotencyKey)
      let response
      try {
        response = await fetchImpl(request.url, { method: 'POST', headers: { 'Content-Type': 'application/json', ...request.headers }, body: JSON.stringify(request.body), signal: AbortSignal.timeout(limits.emailTimeoutMs), redirect: 'error' })
      } catch {
        throw new EmailError(503, 'email_provider_unavailable')
      }
      await response.arrayBuffer().catch(() => {})
      if (response.status === 429 || response.status >= 500) throw new EmailError(503, 'email_provider_unavailable')
      if (!response.ok) throw new EmailError(502, 'email_provider_rejected')
    },
  }
}

function hookReply(res, error) {
  res.writeHead(200, { 'Content-Type': 'application/json' })
  res.end(JSON.stringify(error ? { error: { http_code: error.status, message: error.code } } : {}))
}

// createHookServer serves only GoTrue on loopback. It is a separate listener
// from the public front, so the gateway can never route to it.
export function createHookServer({ secret, mailer, api, app, accessToken = null }) {
  return http.createServer((req, res) => {
    const tokenHook = accessToken && req.url === hookAddress.accessTokenPath
    if (req.method !== 'POST' || (req.url !== hookAddress.path && !tokenHook)) { res.writeHead(404); return res.end() }
    const chunks = []
    let size = 0
    req.on('data', chunk => {
      size += chunk.length
      if (size > limits.hookBodyBytes) { res.writeHead(413); res.end(); req.destroy() } else chunks.push(chunk)
    })
    req.on('end', async () => {
      if (res.writableEnded) return
      const body = Buffer.concat(chunks)
      if (!verifyWebhook(secret, req.headers, body)) { res.writeHead(401); return res.end() }
      if (tokenHook) return answerTokenHook(res, accessToken, body)
      try {
        if (!mailer) throw new EmailError(503, 'email_provider_unconfigured')
        const messages = messagesFor(JSON.parse(body.toString('utf8')), { api, app })
        // One idempotency key per message lets GoTrue's retries reuse it.
        for (const [i, m] of messages.entries()) await mailer.send(m, `${req.headers['webhook-id']}-${i}`)
        hookReply(res)
      } catch (err) {
        const failure = err instanceof EmailError ? err : new EmailError(400, 'invalid_email_payload')
        console.error(JSON.stringify({ level: 'error', event: 'auth_email_failed', code: failure.code }))
        hookReply(res, failure)
      }
    })
  })
}

// answerTokenHook replies to the custom access token hook. A failure fails
// the token request, which the client retries.
async function answerTokenHook(res, accessToken, body) {
  try {
    const reply = await accessToken(JSON.parse(body.toString('utf8')))
    res.writeHead(200, { 'Content-Type': 'application/json' })
    res.end(JSON.stringify(reply))
  } catch (err) {
    const status = Number.isInteger(err?.status) ? err.status : 500
    const code = typeof err?.code === 'string' && /^[a-z_]+$/.test(err.code) ? err.code : 'access_token_hook_failed'
    console.error(JSON.stringify({ level: 'error', event: 'auth_token_hook_failed', code }))
    hookReply(res, { status, code })
  }
}

export function invitationMessage(to, { external, app, orgName, token }) {
  const link = `${external}/ui/invitation#token=${encodeURIComponent(token)}`
  return message(to, `Join ${orgName} on ${app}`, [`You have been invited to join ${orgName} on ${app}.`, 'Sign in or create an account with this email address to accept.'], link)
}
