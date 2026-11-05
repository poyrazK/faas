import test from 'node:test'
import assert from 'node:assert/strict'
import { once } from 'node:events'
import { callbackTokens, channelResource } from '../realtime.mjs'
import { createRealtimeAuthorizer } from '../realtime.mjs'
import { createServer } from '../server.mjs'

const org = '6f1c1a52-34c0-4a8e-9c55-2f5d7c0e4a11'
const user = '0b5f2f0e-7a51-4d8f-8a3a-5c1b9d2e6f70'
const session = 'd654628b-965b-4f84-965b-cc994ff0411b'
const otherSession = '05af6001-a967-4d3f-8fbf-393e827ade46'
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

function sessionPool(respond) {
  const queries = []
  return {
    queries,
    async connect() {
      let claims
      return {
        async query(sql, args) {
          queries.push({ sql, args })
          if (sql.includes('set_config')) { claims = JSON.parse(args[0]); return { rows: [] } }
          if (/^(BEGIN|COMMIT|ROLLBACK)$/.test(sql)) return { rows: [] }
          return respond(sql, args, claims)
        },
        release() { queries.push({ sql: 'release' }) },
      }
    },
  }
}

test('grants channels through session-aware authorization and fails closed', async t => {
  let allowed = true
  const pool = sessionPool(() => { if (allowed === 'error') throw Object.assign(new Error('x'), { code: '57P01' }); return { rows: [{ allowed }] } })
  const { queries } = pool
  const call = await serve(t, pool)
  const event = { type: 'realtime.authorize_channel', principal: user, session_id: session, channel: `org:${org}:room:lobby`, permission: 'read' }
  assert.equal((await call(event)).status, 204)
  assert.deepEqual(queries.map(q => q.sql), ['BEGIN', "SELECT set_config('request.jwt.claims', $1, true)", 'SELECT gregale_auth.authorize($1, $2) AS allowed', 'COMMIT', 'release'])
  assert.deepEqual(JSON.parse(queries[1].args[0]), { sub: user, session_id: session })
  assert.deepEqual(queries[2].args, ['realtime:read', `org/${org}/room/lobby`])
  assert.equal((await call({ ...event, permission: 'read_activity', channel: '__activity.x', activity_parent_channel: `org:${org}:room:lobby` })).status, 204)
  assert.deepEqual(queries[7].args, ['realtime:activity', `org/${org}/room/lobby`])
  assert.equal((await call({ ...event, permission: 'presence' })).status, 204)
  assert.deepEqual(queries[12].args, ['realtime:presence', `org/${org}/room/lobby`])
  allowed = false
  assert.equal((await call(event)).status, 403)
  allowed = 'error'
  assert.equal((await call(event)).status, 503)
  assert.deepEqual(queries.slice(-2).map(q => q.sql), ['ROLLBACK', 'release'])
  const before = queries.length
  assert.equal((await call(event, `Bearer ${'u'.repeat(40)}`)).status, 401)
  assert.equal((await call(event, '')).status, 401)
  assert.equal((await call({ ...event, channel: 'lobby' })).status, 403)
  assert.equal((await call({ ...event, principal: 'not-a-uuid' })).status, 403)
  for (const session_id of [undefined, '', 'not-a-uuid', [session], { id: session }]) {
    assert.equal((await call({ ...event, session_id })).status, 403)
  }
  assert.equal((await call({ ...event, type: 'realtime.message' })).status, 403)
  assert.equal(queries.length, before)
})

test('logging out one session cannot borrow another active session', async t => {
  const sessions = new Map([[session, user], [otherSession, user]])
  const pool = sessionPool((_sql, _args, claims) => ({ rows: [{ allowed: sessions.get(claims.session_id) === claims.sub }] }))
  const call = await serve(t, pool)
  const event = { type: 'realtime.authorize_channel', principal: user, session_id: session, channel: `org:${org}:room:lobby`, permission: 'read' }
  assert.equal((await call(event)).status, 204)
  sessions.delete(session)
  assert.equal((await call(event)).status, 403)
  assert.equal((await call({ ...event, session_id: otherSession })).status, 204)
  assert.equal((await call({ ...event, principal: org, session_id: otherSession })).status, 403)
})

test('is absent until a realtime endpoint is wired', async t => {
  const call = await serve(t, { query: async () => ({ rows: [] }) }, [])
  assert.equal((await call({})).status, 404)
})
