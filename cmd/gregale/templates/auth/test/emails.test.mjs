import test from 'node:test'
import assert from 'node:assert/strict'
import { emailTemplate, emailRoute } from '../emails.mjs'
import { emailTypes, messagesFor, renderEmail } from '../email.mjs'

const api = 'https://shop-auth.gregale.app/auth/v1'
const external = 'https://shop-auth.gregale.app'

test('accepts only known placeholders, bounded text and no link or code in a subject', () => {
  assert.deepEqual(emailTemplate('signup', { subject: ' Welcome to {{ app }} ', body: 'Hi {{email}},\r\n\r\nConfirm: {{link}}\n' }), { subject: 'Welcome to {{ app }}', body: 'Hi {{email}},\n\nConfirm: {{link}}' })
  const refused = {
    'an unknown type': ['newsletter', { subject: 's', body: 'b' }, 404, 'unknown_email_type'],
    'a placeholder of another type': ['signup', { subject: 's', body: 'Join {{org}}' }, 400, 'unknown_placeholder'],
    'a broken placeholder': ['signup', { subject: 's', body: 'Hi {{email}' }, 400, 'invalid_placeholder'],
    'a link in the subject': ['recovery', { subject: 'Reset at {{link}}', body: 'b' }, 400, 'invalid_subject'],
    'a code in the subject': ['code', { subject: 'Your code {{code}}', body: 'b' }, 400, 'invalid_subject'],
    'an empty subject': ['signup', { subject: '  ', body: 'b' }, 400, 'invalid_subject'],
    'a long subject': ['signup', { subject: 'x'.repeat(151), body: 'b' }, 400, 'invalid_subject'],
    'a subject on two lines': ['signup', { subject: 'a\nb', body: 'b' }, 400, 'invalid_subject'],
    'a long body': ['signup', { subject: 's', body: 'é'.repeat(2001) }, 400, 'invalid_body'],
    'a control character': ['signup', { subject: 's', body: 'a\u0007b' }, 400, 'invalid_body'],
    'another field': ['signup', { subject: 's', body: 'b', html: '<b>' }, 400, 'invalid_template'],
  }
  for (const [name, [type, input, status, code]] of Object.entries(refused)) {
    assert.throws(() => emailTemplate(type, input), { status, code }, name)
  }
  // Every default passes its own rules.
  for (const [type, spec] of Object.entries(emailTypes)) emailTemplate(type, { subject: spec.subject, body: spec.body })
})

test('renders escaped values, places link and code where asked and adds them otherwise', () => {
  const template = { subject: 'Join {{org}}', body: 'Hello {{email}},\n{{org}} invited you.\n\nOpen {{link}} to accept.' }
  const m = renderEmail('a@example.test', template, { org: 'Acme <script>\nInc', email: 'a@example.test', link: 'https://x.test/ui/invitation#token=t&x="y"' })
  assert.equal(m.subject, 'Join Acme <script> Inc', 'values in a subject stay on one line; subjects are plain text')
  assert.equal(m.text, 'Hello a@example.test,\nAcme <script>\nInc invited you.\n\nOpen https://x.test/ui/invitation#token=t&x="y" to accept.')
  assert.equal(m.html, '<p>Hello a@example.test,<br>Acme &lt;script&gt;<br>Inc invited you.</p>\n<p>Open <a href="https://x.test/ui/invitation#token=t&amp;x=&quot;y&quot;">https://x.test/ui/invitation#token=t&amp;x=&quot;y&quot;</a> to accept.</p>')
  const appended = renderEmail('a@example.test', { subject: 'Sign in', body: 'Use the link below.' }, { link: 'https://x.test/v', code: '123456' })
  assert.equal(appended.text, 'Use the link below.\n\nhttps://x.test/v\n\nCode: 123456')
  assert.match(appended.html, /<p><a href="https:\/\/x\.test\/v">Sign in<\/a><\/p>\n<p>Code: <strong>123456<\/strong><\/p>$/)
  const inline = renderEmail('a@example.test', { subject: 'Code', body: 'Your code is {{code}}.' }, { code: '987654' })
  assert.equal(inline.text, 'Your code is 987654.')
  assert.equal(inline.html, '<p>Your code is <strong>987654</strong>.</p>')
})

test('GoTrue emails use the owner template for their type', () => {
  const templates = { signup: { subject: 'Bienvenue sur {{app}}', body: 'Confirmez votre adresse : {{link}}' } }
  const [signup] = messagesFor({ user: { email: 'a@example.test' }, email_data: { email_action_type: 'signup', token_hash: 'h', token: '111111' } }, { api, app: 'Shop', templates })
  assert.equal(signup.subject, 'Bienvenue sur Shop')
  assert.equal(signup.text.match(/https:\/\//g).length, 1, 'a placed link is not added again')
  assert.match(signup.text, /\n\nCode: 111111$/, 'an unplaced code is still sent')
  const [recovery] = messagesFor({ user: { email: 'a@example.test' }, email_data: { email_action_type: 'recovery', token_hash: 'h' } }, { api, app: 'Shop', templates })
  assert.equal(recovery.subject, 'Reset your password', 'types without a template keep the default')
  const [notice] = messagesFor({ user: { email: 'a@example.test' }, email_data: { email_action_type: 'identity_linked_notification' } }, { api, app: 'Shop', templates: { security_notice: { subject: 'Shop: {{event}}', body: 'Was it you? {{event}}.' } } })
  assert.equal(notice.subject, 'Shop: A sign-in method was linked')
})

function fakePool() {
  const rows = new Map()
  const log = []
  return {
    rows, log,
    async query(sql, args) {
      log.push(sql)
      if (sql.startsWith('SELECT type, subject, body')) return { rows: [...rows.values()] }
      if (sql.startsWith('INSERT INTO gregale_auth.email_templates')) {
        const row = { type: args[0], subject: args[1], body: args[2], updated_at: '2026-10-18T12:00:00Z' }
        rows.set(args[0], row)
        return { rows: [row] }
      }
      if (sql.startsWith('DELETE FROM gregale_auth.email_templates')) { rows.delete(args[0]); return { rows: [] } }
      throw new Error(`unexpected ${sql}`)
    },
  }
}

test('the admin API lists, changes, resets and previews templates', async () => {
  const pool = fakePool()
  const sent = []
  const context = { pool, mailer: { send: async (m, key) => { sent.push({ m, key }) } }, api, external, app: 'Shop' }
  const route = (method, path, body) => emailRoute(context, method, path.split('/').filter(Boolean), body)
  const [, list] = await route('GET', 'emails')
  assert.deepEqual(list.emails.map(e => e.type), Object.keys(emailTypes))
  assert.ok(list.emails.every(e => e.custom === false))
  // Changing only the subject keeps the default body.
  const [, changed] = await route('PATCH', 'emails/magiclink', { subject: 'Your {{app}} link' })
  assert.deepEqual([changed.subject, changed.body, changed.custom], ['Your {{app}} link', emailTypes.magiclink.body, true])
  const [, body] = await route('PATCH', 'emails/magiclink', { body: 'Tap {{link}}' })
  assert.deepEqual([body.subject, body.body], ['Your {{app}} link', 'Tap {{link}}'])
  await assert.rejects(route('PATCH', 'emails/magiclink', { body: '{{org}}' }), { status: 400, code: 'unknown_placeholder' })
  await assert.rejects(route('PATCH', 'emails/magiclink', null), { status: 400, code: 'invalid_template' })
  const [, preview] = await route('POST', 'emails/magiclink/preview', {})
  assert.equal(preview.subject, 'Your Shop link')
  assert.equal(preview.text, `Tap ${api}/verify?token=preview&type=magiclink\n\nCode: 123456`, 'magic-link emails carry a code too')
  assert.equal(preview.sent, false)
  const [, draft] = await route('POST', 'emails/organization_invitation/preview', { subject: 'Join {{org}}', body: 'Hi {{email}}', send_to: 'owner@example.test' })
  assert.deepEqual([draft.subject, draft.sent], ['Join Acme', true])
  assert.equal(sent.length, 1)
  assert.equal(sent[0].m.to, 'owner@example.test')
  assert.match(sent[0].m.text, /^Hi owner@example\.test\n\nhttps:\/\/shop-auth\.gregale\.app\/ui\/invitation#token=preview$/)
  assert.equal(pool.rows.has('organization_invitation'), false, 'a preview saves nothing')
  await assert.rejects(route('POST', 'emails/code/preview', { send_to: 'not an address' }), { status: 400, code: 'invalid_send_to' })
  await assert.rejects(emailRoute({ ...context, mailer: null }, 'POST', ['emails', 'code', 'preview'], { send_to: 'o@example.test' }), { status: 409, code: 'email_provider_unconfigured' })
  const [, reset] = await route('DELETE', 'emails/magiclink')
  assert.deepEqual([reset.subject, reset.custom], [emailTypes.magiclink.subject, false])
  await assert.rejects(route('GET', 'emails/newsletter'), { status: 404, code: 'unknown_email_type' })
})
