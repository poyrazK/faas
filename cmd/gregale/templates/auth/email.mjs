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
// on only with the OAuth server, whose client tokens it changes (oauth.mjs).
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

// Email templates (ADR-1190): the owner may replace the subject and body of
// each type (emails.mjs, sql/0014). A body is plain text with {{placeholder}}
// values; every value is escaped and the HTML layout is fixed, so a template
// cannot add markup, and an email that carries a link or code always has it.
const verification = ['app', 'email', 'link', 'code']
export const emailTypes = Object.freeze({
  signup: { placeholders: verification, subject: 'Confirm your email', body: 'Confirm your email address to finish signing up for {{app}}.' },
  invite: { placeholders: verification, subject: 'You have been invited', body: 'You have been invited to create an account on {{app}}.' },
  magiclink: { placeholders: verification, subject: 'Your sign-in link', body: 'Use this link to sign in to {{app}}. It expires soon and works once.' },
  recovery: { placeholders: verification, subject: 'Reset your password', body: 'Use this link to choose a new password for {{app}}. If you did not ask for this, ignore this email.' },
  email_change: { placeholders: ['app', 'email', 'new_email', 'link', 'code'], subject: 'Confirm your email change', body: 'Confirm the change of your email address on {{app}} to {{new_email}}.' },
  code: { placeholders: ['app', 'email', 'code'], subject: 'Your verification code', body: 'Enter this code in {{app}}. It expires soon.' },
  security_notice: { placeholders: ['app', 'email', 'event'], subject: '{{event}}', body: 'This is a security notice from {{app}}. If this was not you, reset your password.' },
  organization_invitation: { placeholders: ['app', 'email', 'org', 'link'], subject: 'Join {{org}} on {{app}}', body: 'You have been invited to join {{org}} on {{app}}.\n\nSign in or create an account with this email address to accept.' },
})

export const placeholder = /\{\{\s*([a-z_]+)\s*\}\}/g

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c])
}

const oneLine = value => String(value ?? '').replace(/[\r\n]+/g, ' ')

// renderEmail fills a template. values.link and values.code are added at
// the end when the body does not place them.
export function renderEmail(to, template, values) {
  const subject = template.subject.replace(placeholder, (_, name) => oneLine(values[name]))
  const placed = new Set([...template.body.matchAll(placeholder)].map(([, name]) => name))
  const text = [template.body.replace(placeholder, (_, name) => String(values[name] ?? ''))]
  const html = template.body.split(/\n{2,}/).map(paragraph => {
    let out = '', last = 0
    for (const match of paragraph.matchAll(placeholder)) {
      out += escapeHTML(paragraph.slice(last, match.index))
      const value = values[match[1]] ?? ''
      out += match[1] === 'link' && value ? `<a href="${escapeHTML(value)}">${escapeHTML(value)}</a>` : match[1] === 'code' && value ? `<strong>${escapeHTML(value)}</strong>` : escapeHTML(value)
      last = match.index + match[0].length
    }
    return `<p>${(out + escapeHTML(paragraph.slice(last))).replace(/\n/g, '<br>')}</p>`
  })
  if (values.link && !placed.has('link')) { text.push('', values.link); html.push(`<p><a href="${escapeHTML(values.link)}">${escapeHTML(subject)}</a></p>`) }
  if (values.code && !placed.has('code')) { text.push('', `Code: ${values.code}`); html.push(`<p>Code: <strong>${escapeHTML(values.code)}</strong></p>`) }
  return { to, subject, text: text.join('\n'), html: html.join('\n') }
}

// loadEmailTemplates returns the owner's templates by type; db is a pool or
// a client already in a transaction.
export async function loadEmailTemplates(db) {
  const { rows } = await db.query('SELECT type, subject, body FROM gregale_auth.email_templates')
  return Object.fromEntries(rows.filter(row => emailTypes[row.type]).map(row => [row.type, { subject: row.subject, body: row.body }]))
}

export function templateFor(templates, type) {
  return templates?.[type] ?? emailTypes[type]
}


function verifyLink(api, tokenHash, type, redirectTo) {
  const url = new URL(`${api}/verify`)
  url.searchParams.set('token', tokenHash)
  url.searchParams.set('type', type)
  if (redirectTo) url.searchParams.set('redirect_to', redirectTo)
  return url.toString()
}

const linkActions = new Set(['signup', 'invite', 'magiclink', 'recovery'])

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
// deliver, in the owner's templates where set. For a secure email change
// GoTrue pairs token_hash_new with the current address and token_hash with
// the new one.
export function messagesFor(payload, { api, app, templates = {} }) {
  const user = payload?.user ?? {}
  const data = payload?.email_data ?? {}
  const action = data.email_action_type
  const render = (type, to, values) => renderEmail(to, templateFor(templates, type), { app, email: to, ...values })
  if (linkActions.has(action)) return [render(action, user.email, { link: verifyLink(api, data.token_hash, action, data.redirect_to), code: data.token })]
  if (action === 'email_change') {
    const addresses = { email: user.email, new_email: user.new_email }
    const link = hash => verifyLink(api, hash, 'email_change', data.redirect_to)
    if (data.token_hash_new) {
      return [render('email_change', user.email, { ...addresses, link: link(data.token_hash_new), code: data.token }),
        render('email_change', user.new_email, { ...addresses, link: link(data.token_hash), code: data.token_new })]
    }
    return [render('email_change', user.new_email, { ...addresses, link: link(data.token_hash), code: data.token })]
  }
  if (action === 'email' || action === 'reauthentication') return [render('code', user.email, { code: data.token })]
  if (notifications[action]) return [render('security_notice', user.email, { event: notifications[action] })]
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
export function createHookServer({ secret, mailer, api, app, accessToken = null, templates = async () => ({}) }) {
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
        // The defaults stand in when the owner's templates cannot be read.
        const owned = await templates().catch(() => {
          console.error(JSON.stringify({ level: 'warning', event: 'auth_email_templates_unavailable' }))
          return {}
        })
        const messages = messagesFor(JSON.parse(body.toString('utf8')), { api, app, templates: owned })
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

export function invitationMessage(to, { external, app, orgName, token, templates = {} }) {
  const link = `${external}/ui/invitation#token=${encodeURIComponent(token)}`
  return renderEmail(to, templateFor(templates, 'organization_invitation'), { app, email: to, org: orgName, link })
}
