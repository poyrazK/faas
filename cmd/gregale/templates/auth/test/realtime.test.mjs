import test from 'node:test'
import assert from 'node:assert/strict'
import { once } from 'node:events'
import { callbackTokens, channelResource } from '../realtime.mjs'
import { createRealtimeAuthorizer } from '../realtime.mjs'
import { createServer } from '../server.mjs'

const org = '6f1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'
const user = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'
const token = 't'.repeat(40)

test('maps channels to authorization resources', () => {
  assert.equal(channelResource(`org:${org}:room:lobby`), `org/${org}/room/lobby`)
  assert.equal(channelResource(`org:${org}:room:a:b`), `org/${org}/room/a:b`)
  for (const channel of ['lobby', `org:${org}:room:`, `org:nope:room:x`, `team:${org}:room:x`, `org:${org}:Room:x`, undefined]) {
    assert.equal(channelResource(channel), null, String(channel))
  }
  assert.equal(callbackTokens({ AUTH_REALTIME_CALLBACK_TOKEN_AB12: token, AUTH_REALTIME_CALLBACK_TOKEN_SHORT: 'x', OTHER: token }).length, 1)
})

async function serve(t, pool, tokens = [Buffer.from(token)]) {
  const server = createServer({ upstreamPort: 1, realtime: createRealtimeAuthorizer({ pool, tokens }) })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  t.after(() => server.close())
  return (body, auth = `Bearer ${token}`) => fetch(`http://127.0.0.1:${server.address().port}/realtime/authorize-channel`, { method: 'POST', headers: { Authorization: auth, 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
}

test('grants channels through authorize_subject and fails closed', async t => {
  const queries = []
  let allowed = true
  const pool = { query: async (sql, args) => { queries.push({ sql, args }); if (allowed === 'error') throw Object.assign(new Error('x'), { code: '57P01' }); return { rows: [{ allowed }] } } }
  const call = await serve(t, pool)
  const event = { type: 'realtime.authorize_channel', principal: user, channel: `org:${org}:room:lobby`, permission: 'read' }
  assert.equal((await call(event)).status, 204)
  assert.deepEqual(queries[0].args, [user, 'realtime:read', `org/${org}/room/lobby`])
  assert.match(queries[0].sql, /gregale_auth\.authorize_subject/)
  assert.equal((await call({ ...event, permission: 'read_activity', channel: '__activity.x', activity_parent_channel: `org:${org}:room:lobby` })).status, 204)
  assert.deepEqual(queries[1].args, [user, 'realtime:activity', `org/${org}/room/lobby`])
  allowed = false
  assert.equal((await call(event)).status, 403)
  allowed = 'error'
  assert.equal((await call(event)).status, 503)
  const before = queries.length
  assert.equal((await call(event, `Bearer ${'u'.repeat(40)}`)).status, 401)
  assert.equal((await call(event, '')).status, 401)
  assert.equal((await call({ ...event, channel: 'lobby' })).status, 403)
  assert.equal((await call({ ...event, principal: 'not-a-uuid' })).status, 403)
  assert.equal((await call({ ...event, type: 'realtime.message' })).status, 403)
  assert.equal(queries.length, before)
})

test('is absent until a realtime endpoint is wired', async t => {
  const call = await serve(t, { query: async () => ({ rows: [] }) }, [])
  assert.equal((await call({})).status, 404)
})
