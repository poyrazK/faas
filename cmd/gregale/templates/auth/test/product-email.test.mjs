import test from 'node:test'
import assert from 'node:assert/strict'
import { once } from 'node:events'
import { randomUUID } from 'node:crypto'
import { createAuthzHandler } from '../authz.mjs'
import { createServer } from '../server.mjs'
import { emailConsent, resolveEmailRecipient } from '../product-email.mjs'

const org = randomUUID(), product = randomUUID(), actor = randomUUID()
async function serve(t, emailEnabled, pool, authenticate) {
  const server = createServer({ upstreamPort: 1, authz: createAuthzHandler({ pool, emailEnabled, authenticate }) })
  server.listen(0, '127.0.0.1'); await once(server, 'listening'); t.after(() => server.close())
  return `http://127.0.0.1:${server.address().port}/authz/v1/orgs/${org}/email-recipient`
}
test('recipient routes disappear before authentication and preflight while disabled', async t => {
  let calls = 0
  const fail = () => { calls++; throw new Error('must not call') }
  const base = await serve(t, false, { query: fail, connect: fail }, fail)
  for (const [method, path] of [['GET', ''], ['POST', ''], ['OPTIONS', ''], ['GET', `/${product}`]]) assert.equal((await fetch(base + path, { method })).status, 404)
  assert.equal(calls, 0)
})
test('worker grant scheme cannot become a login or admin credential', async t => {
  let authenticated = 0, queried = 0
  const base = await serve(t, true, { query: async () => { queried++; return { rows: [{ actor_id: actor, organization_id: org, role: 'member', email: 'fixture@example.test' }] } } }, async () => { authenticated++; throw new Error('must not authenticate grant') })
  const capability = `Product-Recipient ${'A'.repeat(43)}`
  assert.equal((await fetch(base + `/${product}`, { headers: { Authorization: capability } })).status, 200)
  assert.equal((await fetch(base, { headers: { Authorization: capability } })).status, 401)
  assert.equal((await fetch(base + `/${product}`, { headers: { Authorization: 'Bearer a.b.c' } })).status, 401)
  assert.equal((await fetch(base + `/${product}`, { method: 'POST', headers: { Authorization: capability } })).status, 404)
  assert.equal(authenticated, 0); assert.equal(queried, 1)
})
test('consent uses the authenticated transaction and only hashes the opaque token', async () => {
  let stored
  const run = work => work({ query: async (sql, args) => { stored = { sql, args }; return { rows: [{ actor_id: actor, organization_id: org, role: 'member', expires_at: new Date() }] } } })
  const [, value] = await emailConsent(run, 'POST', ['orgs', org, 'email-recipient'], { product_id: product })
  assert.match(stored.sql, /product_email_consent/); assert.equal(stored.args[0], org); assert.equal(stored.args[1], product)
  assert.equal(stored.args[2].length, 32); assert.equal(stored.args[3], 2592000); assert.match(value.token, /^[A-Za-z0-9_-]{43}$/)
  assert.equal(value.product_id, product); assert.equal(value.protocol, 1)
  for (const body of [{ product_id: product, email: 'other@example.test' }, { product_id: 'wrong' }, { product_id: product, actor_id: randomUUID() }]) await assert.rejects(emailConsent(run, 'POST', ['orgs', org, 'email-recipient'], body), e => e.status === 400)
})
test('recipient resolution binds product and org and fails closed on revoked capabilities', async () => {
  let args
  const pool = { query: async (_sql, values) => { args = values; return { rows: [] } } }
  await assert.rejects(resolveEmailRecipient(pool, 'GET', `/orgs/${org}/email-recipient/${product}`, `Product-Recipient ${'A'.repeat(43)}`), e => e.status === 403)
  assert.equal(args[0], org); assert.equal(args[1], product); assert.equal(args[2].length, 32)
})
