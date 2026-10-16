import test from 'node:test'
import assert from 'node:assert/strict'
import { backoffSeconds, createEventPublisher, lifecycleConfig, publishEvent, publishesAfter, setLifecycleEvents } from '../events.mjs'
import { limits } from '../config.mjs'

const config = { api: 'https://api.gregale.dev', slug: 'shop-auth', key: 'fp_live_events_only' }
const user = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'

// fakeOutbox models gregale_auth.event_outbox for the statements events.mjs
// sends; clock is in seconds.
function fakeOutbox(rows = []) {
  const state = { rows, enabled: null, clock: 1_700_000_000 }
  const at = row => row.next_attempt_at ?? row.occurred_at
  const pool = {
    async query(sql, args = []) {
      if (sql.startsWith('UPDATE gregale_auth.event_settings')) { state.enabled = args[0]; return { rowCount: 1, rows: [] } }
      if (sql === 'DELETE FROM gregale_auth.event_outbox') { const n = state.rows.length; state.rows = []; return { rowCount: n, rows: [] } }
      if (sql.includes('occurred_at < now()')) {
        const before = state.rows.length
        state.rows = state.rows.filter(row => row.occurred_at >= state.clock - args[0])
        return { rowCount: before - state.rows.length, rows: [] }
      }
      if (sql.startsWith('WITH due AS')) {
        const due = state.rows.filter(row => at(row) <= state.clock).sort((a, b) => a.occurred_at - b.occurred_at).slice(0, args[0])
        for (const row of due) { row.attempts = (row.attempts ?? 0) + 1; row.next_attempt_at = state.clock + args[1] }
        return { rowCount: due.length, rows: due.map(row => ({ ...row, occurred_at: new Date(row.occurred_at * 1000) })) }
      }
      if (sql.startsWith('UPDATE gregale_auth.event_outbox SET next_attempt_at')) {
        const row = state.rows.find(r => r.id === args[0]); row.next_attempt_at = state.clock + args[1]
        return { rowCount: 1, rows: [] }
      }
      if (sql.includes('id = ANY')) {
        const before = state.rows.length
        state.rows = state.rows.filter(row => !args[0].includes(row.id))
        return { rowCount: before - state.rows.length, rows: [] }
      }
      if (sql.includes('min(next_attempt_at)')) {
        return { rows: [{ seconds: state.rows.length ? Math.min(...state.rows.map(at)) - state.clock : null }] }
      }
      throw new Error('unexpected SQL ' + sql)
    },
  }
  return { pool, state }
}

const row = (id, type, extra = {}) => ({ id, type, user_id: user, data: {}, occurred_at: 1_700_000_000 - 10, attempts: 0, ...extra })

function fakeAPI(statuses = {}) {
  const calls = []
  const fetchImpl = async (url, init) => {
    const body = JSON.parse(init.body)
    calls.push({ url, headers: init.headers, body })
    const status = statuses[body.key] ?? 202
    return new Response(status === 202 ? '{}' : '{"code":"x"}', { status })
  }
  return { calls, fetchImpl }
}

test('lifecycle events need the flag and the events key', () => {
  assert.equal(lifecycleConfig({}), null)
  assert.equal(lifecycleConfig({ AUTH_LIFECYCLE_EVENTS: '0', AUTH_EVENTS_KEY: 'k' }), null)
  assert.throws(() => lifecycleConfig({ AUTH_LIFECYCLE_EVENTS: 'yes' }), /Invalid/)
  assert.throws(() => lifecycleConfig({ AUTH_LIFECYCLE_EVENTS: '1' }), /requires AUTH_EVENTS_KEY/)
  assert.deepEqual(lifecycleConfig({ AUTH_LIFECYCLE_EVENTS: '1', AUTH_EVENTS_KEY: 'k', AUTH_GREGALE_API_URL: 'https://api.gregale.dev', AUTH_APP_SLUG: 'shop-auth' }), { api: 'https://api.gregale.dev', slug: 'shop-auth', key: 'k' })
})

test('publishes outbox rows as app-scoped events keyed by row and deletes them', async () => {
  const { pool, state } = fakeOutbox([row('e1', 'auth.user.created', { data: { provider: 'google', anonymous: false } }), row('e2', 'auth.user.signed_in', { data: { aal: 'aal1' } })])
  const api = fakeAPI()
  await createEventPublisher({ pool, config, fetchImpl: api.fetchImpl, log: () => {} }).poke()
  assert.equal(state.rows.length, 0)
  assert.deepEqual(api.calls.map(c => c.body.key).sort(), ['auth-event.e1', 'auth-event.e2'])
  const created = api.calls.find(c => c.body.key === 'auth-event.e1')
  assert.equal(created.url, 'https://api.gregale.dev/v1/apps/shop-auth/events:publish')
  assert.equal(created.headers.Authorization, 'Bearer fp_live_events_only')
  assert.deepEqual(created.body, { key: 'auth-event.e1', type: 'auth.user.created', time: new Date((1_700_000_000 - 10) * 1000).toISOString(), data: { user_id: user, provider: 'google', anonymous: false } })
})

test('retries failures with backoff, treats a known key as delivered and drops rejected events', async () => {
  const { pool, state } = fakeOutbox([row('busy', 'auth.user.created'), row('dup', 'auth.user.deleted'), row('bad', 'auth.user.banned')])
  const logs = []
  const api = fakeAPI({ 'auth-event.busy': 503, 'auth-event.dup': 409, 'auth-event.bad': 400 })
  await createEventPublisher({ pool, config, fetchImpl: api.fetchImpl, log: line => logs.push(JSON.parse(line)) }).poke()
  assert.deepEqual(state.rows.map(r => [r.id, r.attempts, r.next_attempt_at - state.clock]), [['busy', 1, backoffSeconds(1)]])
  assert.deepEqual(logs, [{ level: 'error', event: 'lifecycle_event_rejected', type: 'auth.user.banned' }])
  // Not due yet: a new drain does not publish it again.
  await createEventPublisher({ pool, config, fetchImpl: api.fetchImpl, log: () => {} }).poke()
  assert.equal(api.calls.filter(c => c.body.key === 'auth-event.busy').length, 1)
  state.clock += backoffSeconds(1)
  await createEventPublisher({ pool, config, fetchImpl: fakeAPI().fetchImpl, log: () => {} }).poke()
  assert.equal(state.rows.length, 0)
  assert.equal(backoffSeconds(1), 10)
  assert.equal(backoffSeconds(30), limits.eventMaxBackoffSeconds)
})

test('drops events older than the retention period', async () => {
  const old = row('old', 'auth.user.created', { occurred_at: 1_700_000_000 - limits.eventRetentionSeconds - 1 })
  const { pool, state } = fakeOutbox([old, row('new', 'auth.user.created')])
  const logs = []
  const api = fakeAPI()
  await createEventPublisher({ pool, config, fetchImpl: api.fetchImpl, log: line => logs.push(JSON.parse(line)) }).poke()
  assert.deepEqual(api.calls.map(c => c.body.key), ['auth-event.new'])
  assert.equal(state.rows.length, 0)
  assert.deepEqual(logs, [{ level: 'warning', event: 'lifecycle_events_expired', count: 1 }])
})

test('concurrent pokes share one drain and run once more after it', async () => {
  const { pool } = fakeOutbox([row('e1', 'auth.user.created')])
  let drains = 0
  const counting = { query: (sql, args) => { if (sql.startsWith('WITH due AS')) drains++; return pool.query(sql, args) } }
  const publisher = createEventPublisher({ pool: counting, config, fetchImpl: fakeAPI().fetchImpl, log: () => {} })
  const first = publisher.poke()
  assert.equal(publisher.poke(), first)
  publisher.poke()
  await first
  await new Promise(resolve => setTimeout(resolve, 20))
  assert.equal(drains, 2)
})

test('turning lifecycle events off clears the outbox', async () => {
  const { pool, state } = fakeOutbox([row('e1', 'auth.user.created')])
  await setLifecycleEvents(pool, true)
  assert.equal(state.enabled, true)
  assert.equal(state.rows.length, 1)
  await setLifecycleEvents(pool, false)
  assert.equal(state.enabled, false)
  assert.equal(state.rows.length, 0)
})

test('publishes only after requests that may change users', () => {
  for (const [method, url, expected] of [
    ['POST', '/auth/v1/signup', true], ['POST', '/auth/v1/token?grant_type=password', true], ['GET', '/auth/v1/callback?code=x', true],
    ['GET', '/auth/v1/verify?token=x', true], ['PUT', '/auth/v1/user', true], ['GET', '/auth/v1/user', false],
    ['GET', '/auth/v1/.well-known/jwks.json', false], ['GET', '/auth/v1/settings', false], ['GET', '/healthz', false],
    ['GET', '/settings/v1', false], ['GET', '/ui/sign-in', false], ['POST', '/admin/v1/users/x/ban', true], ['GET', '/admin/v1/users', false],
  ]) {
    assert.equal(publishesAfter({ method, url }), expected, `${method} ${url}`)
  }
})

test('a network failure is retried, not dropped', async () => {
  assert.equal(await publishEvent(config, row('e1', 'auth.user.created'), async () => { throw new Error('reset') }), 'retry')
})
