import test from 'node:test'
import assert from 'node:assert/strict'
import { createWorkflowStarter, workflowsConfig } from '../workflows.mjs'

test('requires the API URL and app slug with an events key', () => {
  assert.equal(workflowsConfig({}), null)
  assert.deepEqual(workflowsConfig({ AUTH_EVENTS_KEY: 'k', AUTH_GREGALE_API_URL: 'https://api.gregale.dev', AUTH_APP_SLUG: 'shop-auth' }), { api: 'https://api.gregale.dev', slug: 'shop-auth', key: 'k' })
  assert.throws(() => workflowsConfig({ AUTH_EVENTS_KEY: 'k', AUTH_APP_SLUG: 'shop-auth' }))
  assert.throws(() => workflowsConfig({ AUTH_EVENTS_KEY: 'k', AUTH_GREGALE_API_URL: 'http://api.gregale.dev', AUTH_APP_SLUG: 'shop-auth' }))
  assert.throws(() => workflowsConfig({ AUTH_EVENTS_KEY: 'k', AUTH_GREGALE_API_URL: 'https://api.gregale.dev', AUTH_APP_SLUG: 'Shop Auth' }))
})

test('publishes an app-scoped start event with a stable key', async () => {
  const calls = []
  let status = 202
  const start = createWorkflowStarter({ api: 'https://api.gregale.dev', slug: 'shop-auth', key: 'fp_live_events' }, async (url, init) => {
    calls.push({ url, init, body: JSON.parse(init.body) })
    return new Response(JSON.stringify({ duplicate: false, source: 'app.1234' }), { status })
  })
  const result = await start({ name: 'onboard', org: 'o1', user: 'u1', input: { plan: 'pro' }, idempotencyKey: 'abc' })
  assert.deepEqual(result, { accepted: true, duplicate: false, source: 'app.1234' })
  assert.equal(calls[0].url, 'https://api.gregale.dev/v1/apps/shop-auth/events:publish')
  assert.equal(calls[0].init.headers.Authorization, 'Bearer fp_live_events')
  assert.equal(calls[0].init.redirect, 'error')
  assert.deepEqual(calls[0].body, { key: 'workflow-start.onboard.u1.abc', type: 'auth.workflow.start', data: { workflow: 'onboard', org_id: 'o1', user_id: 'u1', input: { plan: 'pro' } } })
  for (const [code, expected] of [[409, 'idempotency_conflict'], [429, 'workflow_start_unavailable'], [500, 'workflow_start_unavailable'], [403, 'workflow_start_rejected']]) {
    status = code
    await assert.rejects(start({ name: 'onboard', org: 'o1', user: 'u1', idempotencyKey: 'abc' }), { code: expected })
  }
  const offline = createWorkflowStarter({ api: 'https://api.gregale.dev', slug: 's', key: 'k' }, async () => { throw new Error('down') })
  await assert.rejects(offline({ name: 'n', org: 'o', user: 'u', idempotencyKey: 'k' }), { code: 'workflow_start_unavailable' })
})
