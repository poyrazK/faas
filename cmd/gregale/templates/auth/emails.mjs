import { randomBytes } from 'node:crypto'
import { HTTPError } from './authz.mjs'
import { limits } from './config.mjs'
import { emailTypes, placeholder, renderEmail, templateFor } from './email.mjs'

// Email templates (ADR-1190): the owner edits the wording of the emails the
// Auth app sends through /admin/v1/emails; email.mjs holds the defaults and
// renders, gregale_auth.email_templates (sql/0014) keeps the owner's.

// emailTemplate checks an owner's template for one type and returns it
// normalized: line endings as \n, no surrounding blank space.
export function emailTemplate(type, input) {
  const spec = emailTypes[type]
  if (!spec) throw new HTTPError(404, 'unknown_email_type')
  if (input === null || typeof input !== 'object' || Array.isArray(input) || Object.keys(input).some(k => k !== 'subject' && k !== 'body')) throw new HTTPError(400, 'invalid_template')
  const subject = typeof input.subject === 'string' ? input.subject.trim() : ''
  if (!subject || [...subject].length > limits.emailSubjectChars || /[\u0000-\u001f\u007f]/.test(subject)) throw new HTTPError(400, 'invalid_subject')
  const body = typeof input.body === 'string' ? input.body.replace(/\r\n?/g, '\n').trim() : ''
  if (!body || Buffer.byteLength(body) > limits.emailBodyBytes || /[\u0000-\u0008\u000b-\u001f\u007f]/.test(body)) throw new HTTPError(400, 'invalid_body')
  for (const text of [subject, body]) {
    for (const [, name] of text.matchAll(placeholder)) if (!spec.placeholders.includes(name)) throw new HTTPError(400, 'unknown_placeholder')
    if (/\{\{|\}\}/.test(text.replace(placeholder, ''))) throw new HTTPError(400, 'invalid_placeholder')
  }
  // A link or code in a subject would show in inbox previews and logs.
  if ([...subject.matchAll(placeholder)].some(([, name]) => name === 'link' || name === 'code')) throw new HTTPError(400, 'invalid_subject')
  return { subject, body }
}

function view(type, stored) {
  const spec = emailTypes[type]
  return { type, subject: (stored ?? spec).subject, body: (stored ?? spec).body, custom: Boolean(stored), placeholders: spec.placeholders, updated_at: stored?.updated_at ?? null }
}

// Example values for previews, only those the type's emails carry; links
// point at the Auth app but hold no working token.
function sampleValues(type, { api, external, app }, to) {
  const all = {
    app, email: to, new_email: 'new.address@example.com', org: 'Acme', event: 'Your password was changed', code: '123456',
    link: type === 'organization_invitation' ? `${external}/ui/invitation#token=preview` : type === 'signup_approved' ? `${external}/ui/sign-up` : `${api}/verify?token=preview&type=${type}`,
  }
  return Object.fromEntries(emailTypes[type].placeholders.map(name => [name, all[name]]))
}

const recipient = /^[^\s@<>"]+@[^\s@<>"]+\.[^\s@<>"]+$/

// emailRoute serves /admin/v1/emails: list, read, change and reset
// templates, and preview one with example values, optionally sent to an
// address through the configured provider.
export async function emailRoute({ pool, mailer, api, external, app }, method, parts, body) {
  const type = parts[1]
  if (type !== undefined && !emailTypes[type]) throw new HTTPError(404, 'unknown_email_type')
  const stored = async () => Object.fromEntries((await pool.query('SELECT type, subject, body, updated_at FROM gregale_auth.email_templates')).rows.map(row => [row.type, { subject: row.subject, body: row.body, updated_at: row.updated_at }]))
  if (parts.length === 1 && method === 'GET') {
    const rows = await stored()
    return [200, { emails: Object.keys(emailTypes).map(name => view(name, rows[name])) }]
  }
  if (parts.length === 2 && method === 'GET') return [200, view(type, (await stored())[type])]
  // PATCH changes the subject, the body or both; the rest stays as it is.
  if (parts.length === 2 && method === 'PATCH') {
    if (body === null || typeof body !== 'object' || Array.isArray(body)) throw new HTTPError(400, 'invalid_template')
    const current = templateFor(await stored(), type)
    const { subject, body: text } = emailTemplate(type, { subject: current.subject, body: current.body, ...body })
    const { rows: [row] } = await pool.query(`INSERT INTO gregale_auth.email_templates (type, subject, body) VALUES ($1, $2, $3)
      ON CONFLICT (type) DO UPDATE SET subject = EXCLUDED.subject, body = EXCLUDED.body, updated_at = now() RETURNING type, subject, body, updated_at`, [type, subject, text])
    return [200, view(type, row)]
  }
  if (parts.length === 2 && method === 'DELETE') {
    await pool.query('DELETE FROM gregale_auth.email_templates WHERE type = $1', [type])
    return [200, view(type, null)]
  }
  if (parts.length === 3 && parts[2] === 'preview' && method === 'POST') {
    const { send_to: sendTo, subject, body: text } = body ?? {}
    if (sendTo !== undefined && (typeof sendTo !== 'string' || sendTo.length > 320 || !recipient.test(sendTo))) throw new HTTPError(400, 'invalid_send_to')
    const template = subject === undefined && text === undefined ? templateFor(await stored(), type) : emailTemplate(type, { subject, body: text })
    const message = renderEmail(sendTo ?? 'person@example.com', template, sampleValues(type, { api, external, app }, sendTo ?? 'person@example.com'))
    if (sendTo !== undefined) {
      if (!mailer) throw new HTTPError(409, 'email_provider_unconfigured')
      await mailer.send(message, `preview-${randomBytes(8).toString('hex')}`)
    }
    return [200, { subject: message.subject, text: message.text, html: message.html, sent: sendTo !== undefined }]
  }
  throw new HTTPError(404, 'not_found')
}
