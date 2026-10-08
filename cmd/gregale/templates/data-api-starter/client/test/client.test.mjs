import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient, noteCursor } from '../dist/notes.js'

test('typed notes use renewed application tokens and schema projections', async () => {
  let token = 'application-token-1'
  const requests = []
  const client = notesClient({
    url: 'https://notes.example', subject: 'user-subject', accessToken: () => token,
    fetch: async (url, init) => {
      requests.push({ url: String(url), init })
      return new Response('[]', { status: 200, headers: { 'Content-Type': 'application/json' } })
    },
  })
  await client.list()
  token = 'application-token-2'
  await client.list()
  assert.equal(requests[0].init.headers.get('Authorization'), 'Bearer application-token-1')
  assert.equal(requests[1].init.headers.get('Authorization'), 'Bearer application-token-2')
  assert.equal(requests[0].init.headers.get('Accept-Profile'), 'api')
  assert.equal(new URL(requests[0].url).pathname, '/rest/v1/notes')
  assert.equal(new URL(requests[0].url).searchParams.get('select'), 'id,body,priority,created_at,version')
  assert.equal(requests[0].init.redirect, 'error')
  assert.equal(requests[0].init.credentials, 'omit')
})

test('invalid pagination bounds fail before a request', () => {
  const client = notesClient({ url: 'https://notes.example', subject: 'user', accessToken: 'token', fetch: () => { throw new Error('unexpected request') } })
  for (const options of [{ offset: -1 }, { size: 0 }, { size: 1.5 }, { offset: NaN }, { size: Infinity }, { offset: Number.MAX_SAFE_INTEGER, size: 2 }]) {
    assert.throws(() => client.page(options), RangeError)
  }
})

test('cursor validation rejects malformed values before requesting and retains microseconds', () => {
  const client = notesClient({ url: 'https://notes.example', subject: 'user', accessToken: 'token', fetch: () => { throw new Error('unexpected request') } })
  const valid = { created_at: '2026-10-08T12:00:00.123456+00:00', id: 1 }
  assert.deepEqual(noteCursor(valid), valid)
  assert.equal(noteCursor({ ...valid, created_at: '2000-02-29T00:00:00Z' }).created_at, '2000-02-29T00:00:00Z')
  for (const after of [null, {}, { ...valid, id: 0 }, { ...valid, id: 1.5 }, { ...valid, id: 2147483648 }, { ...valid, created_at: '2026-02-30T00:00:00Z' }, { ...valid, created_at: '1900-02-29T00:00:00Z' }, { ...valid, created_at: '0000-01-01T00:00:00Z' }, { ...valid, created_at: '2026-10-08T00:00:00Z,id.gt.0' }]) {
    assert.throws(() => client.cursorPage({ after }), TypeError)
  }
  for (const size of [0, -1, NaN, Infinity, 1.5]) assert.throws(() => client.cursorPage({ size }), RangeError)
})

test('session renewal retries a read once with a newly resolved token', async () => {
  const { readCursorPageWithSession } = await import('../dist/session.js')
  let token = 'expired'
  let renewed = 0
  const tokens = []
  const client = notesClient({ url: 'https://notes.example', subject: 'user', accessToken: () => token,
    fetch: async (_, init) => {
      tokens.push(init.headers.get('Authorization'))
      return token === 'expired' ? new Response(JSON.stringify({ code: 'token_invalid' }), { status: 401 }) : new Response('[]', { status: 200 })
    },
  })
  const result = await readCursorPageWithSession({ client, signal: new AbortController().signal, renewSession: async () => { renewed++; token = 'renewed' } })
  assert.equal(result.error, null)
  assert.equal(renewed, 1)
  assert.deepEqual(tokens, ['Bearer expired', 'Bearer renewed'])
  token = 'expired'
  const rejected = await readCursorPageWithSession({ client, signal: new AbortController().signal, renewSession: async () => { renewed++ } })
  assert.equal(rejected.status, 401)
  assert.equal(renewed, 2, 'renewal_loop_detected')
})

test('renewal is skipped for non-auth errors and cancellation prevents a renewed read', async () => {
  const { readCursorPageWithSession } = await import('../dist/session.js')
  let status = 503
  let renewals = 0
  const controller = new AbortController()
  const client = notesClient({ url: 'https://notes.example', subject: 'user', accessToken: 'token', fetch: async (_, init) => {
    if (init.signal.aborted) throw new DOMException('Aborted', 'AbortError')
    return new Response(JSON.stringify({ code: status === 401 ? 'token_invalid' : 'data_api_unavailable' }), { status })
  } })
  const renewSession = async signal => { renewals++; assert.equal(signal, controller.signal); controller.abort() }
  const unavailable = await readCursorPageWithSession({ client, signal: controller.signal, renewSession })
  assert.equal(unavailable.status, 503)
  assert.equal(renewals, 0)
  status = 401
  const canceled = await readCursorPageWithSession({ client, signal: controller.signal, renewSession })
  assert.equal(canceled.status, 0)
  assert.equal(renewals, 1)
})

test('empty write batches are rejected before requesting', () => {
  const client = notesClient({ url: 'https://notes.example', subject: 'user', accessToken: 'token', fetch: () => { throw new Error('unexpected request') } })
  assert.throws(() => client.createMany([]), RangeError)
  assert.throws(() => client.saveDetails([]), RangeError)
})

test('invalid expected versions fail before requesting', async () => {
  const client = notesClient({ url: 'https://notes.example', subject: 'user', accessToken: 'token', fetch: () => { throw new Error('unexpected request') } })
  for (const version of [0, -1, 1.5, NaN, Infinity, 2147483648]) await assert.rejects(client.update(1, version, { body: 'edit' }), RangeError)
})
