import test from 'node:test'
import assert from 'node:assert/strict'
import { notesClient } from '../dist/notes.js'
import { createNoteForm } from '../dist/note-form.js'
const key = '11111111-1111-4111-8111-111111111111'
const row = { id: 7, body: 'hello', priority: 0, version: 1 }
const make = fetch => notesClient({ url: 'https://notes.example', subject: 'alice', accessToken: 'token', fetch })

test('v2 recovery keeps the original UUIDv7 and payload after replay expiry', async () => {
  const calls = []
  const form = createNoteForm(make(async (url, init) => {
    calls.push({ path: new URL(url).pathname, body: JSON.parse(init.body) })
    if (calls.length === 1) throw new TypeError('lost response')
    return new Response(JSON.stringify({ code: 'PT410', message: 'idempotency_key_expired' }), { status: 410 })
  }), { replayContract: 'v2' })
  assert.equal((await form.submit({ body: 'hello', tags: 'one,two' })).uncertain, true)
  const pending = form.pending
  assert.match(pending.idempotency_key, /^[0-9a-f-]{14}7[0-9a-f-]{21}$/)
  const lookup = await form.reconcile()
  assert.equal(lookup.uncertain, true); assert.match(lookup.message, /window has closed/)
  assert.equal(form.pending, pending)
  const retry = await form.retry()
  assert.equal(retry.uncertain, true); assert.match(retry.message, /window has closed/)
  assert.equal(form.pending, pending)
  assert.deepEqual(await form.submit({ body: 'replacement', tags: '' }), { outcome: 'pending' })
  assert.deepEqual(calls.map(call => call.path), ['/rest/v1/rpc/create_note_with_tags_once_v2', '/rest/v1/rpc/read_note_create_receipt_v2', '/rest/v1/rpc/create_note_with_tags_once_v2'])
  for (const call of calls) assert.deepEqual(call.body, pending)
})

test('form feedback rejects invalid fields before token lookup or transport', async () => {
  let requests = 0, tokens = 0, keys = 0
  const client = notesClient({ url: 'https://notes.example', subject: 'alice', accessToken: () => { tokens++; return 'token' }, fetch: async () => { requests++; return new Response('[]') } })
  const form = createNoteForm(client, { createKey: () => { keys++; return key } })
  const invalid = await form.submit({ body: ' ', tags: 'one,,two' })
  assert.equal(invalid.outcome, 'validation_failed')
  assert.deepEqual(invalid.fields, { body: 'Enter a note body.', tags: 'Enter a name for each tag; remove extra commas.' })
  assert.deepEqual([requests, tokens, keys], [0, 0, 0])
  assert.equal(form.pending, null)
})

test('valid form uses the approved typed RPC, normalization and selected result', async () => {
  const requests = []
  const form = createNoteForm(make(async (url, init) => {
    requests.push({ url: String(url), init })
    return new Response(JSON.stringify([{ ...row, subject: 'alice' }]))
  }), { createKey: () => key })
  const outcome = await form.submit({ body: ' hello ', tags: ' one, two ' })
  assert.deepEqual(outcome, { outcome: 'saved', note: row })
  assert.equal(form.pending, null)
  assert.equal(requests.length, 1)
  assert.equal(new URL(requests[0].url).pathname, '/rest/v1/rpc/create_note_with_tags_once')
  assert.equal(new URL(requests[0].url).searchParams.get('select'), 'id,body,priority,version,subject')
  assert.deepEqual(JSON.parse(requests[0].init.body), { idempotency_key: key, note_body: 'hello', tag_names: ['one', 'two'] })
  assert.equal(requests[0].init.headers.get('Content-Profile'), 'api')
})

test('unknown write outcome blocks new submissions and explicit recovery retains immutable key and payload', async () => {
  const payloads = []
  const paths = []
  let token = 'first'
  const tokens = []
  const client = notesClient({ url: 'https://notes.example', subject: 'alice', accessToken: () => token, fetch: async (url, init) => {
    paths.push(new URL(url).pathname)
    payloads.push(JSON.parse(init.body)); tokens.push(init.headers.get('Authorization'))
    if (payloads.length === 1) throw new TypeError('private network failure')
    return new Response(JSON.stringify([{ ...row, subject: 'alice' }]))
  } })
  const form = createNoteForm(client, { createKey: () => key })
  const first = await form.submit({ body: 'hello', tags: 'one' })
  assert.equal(first.outcome, 'failed'); assert.equal(first.uncertain, true)
  assert.equal(payloads.length, 1)
  assert.ok(Object.isFrozen(form.pending)); assert.ok(Object.isFrozen(form.pending.tag_names))
  assert.throws(() => form.pending.tag_names.push('changed'), TypeError)
  assert.deepEqual(await form.submit({ body: 'different', tags: '' }), { outcome: 'pending' })
  assert.equal(payloads.length, 1)
  token = 'renewed'
  assert.deepEqual(await form.reconcile(), { outcome: 'saved', note: row })
  assert.deepEqual(payloads[0], payloads[1])
  assert.deepEqual(paths, ['/rest/v1/rpc/create_note_with_tags_once', '/rest/v1/rpc/read_note_create_receipt'])
  assert.deepEqual(tokens, ['Bearer first', 'Bearer renewed'])
  assert.equal(form.pending, null)
  assert.deepEqual(await form.reconcile(), { outcome: 'empty' })
})

test('missing or purged receipts preserve the operation and replay needs an explicit retry call', async () => {
  const calls = []
  let keys = 0
  const form = createNoteForm(make(async (url, init) => {
    const path = new URL(url).pathname
    calls.push({ path, payload: JSON.parse(init.body) })
    if (calls.length === 1) throw new TypeError('lost response')
    return new Response(JSON.stringify(path.endsWith('/read_note_create_receipt') ? [] : [{ ...row, subject: 'alice' }]))
  }), { createKey: () => { keys++; return key } })
  assert.equal((await form.submit({ body: 'hello', tags: '' })).uncertain, true)
  const operation = form.pending
  const absent = await form.reconcile()
  assert.equal(absent.outcome, 'failed'); assert.equal(absent.uncertain, true)
  assert.match(absent.message, /still be running|expired/)
  assert.equal(form.pending, operation)
  assert.equal(calls.length, 2)
  assert.deepEqual(await form.submit({ body: 'other', tags: '' }), { outcome: 'pending' })
  assert.deepEqual(await form.retry(), { outcome: 'saved', note: row })
  assert.deepEqual(calls.map(call => call.path), ['/rest/v1/rpc/create_note_with_tags_once', '/rest/v1/rpc/read_note_create_receipt', '/rest/v1/rpc/create_note_with_tags_once'])
  for (const call of calls) assert.deepEqual(call.payload, operation)
  assert.equal(keys, 1); assert.equal(form.pending, null)
})

test('conflicts, lookup failures and changed identity cannot discard or confirm a pending operation', async () => {
  for (const result of [
    { status: 409, body: { code: 'PT409', message: 'idempotency_payload_conflict' } },
    { status: 401, body: { code: 'token_invalid', message: 'private' } },
    { status: 200, body: [{ ...row, subject: 'bob' }] },
    { status: 200, body: null },
  ]) {
    let calls = 0
    const form = createNoteForm(make(async () => {
      if (++calls === 1) throw new TypeError('lost response')
      return new Response(JSON.stringify(result.body), { status: result.status })
    }), { createKey: () => key })
    assert.equal((await form.submit({ body: 'hello', tags: '' })).uncertain, true)
    const pending = form.pending
    const recovered = await form.reconcile()
    assert.equal(recovered.outcome, 'failed'); assert.equal(recovered.uncertain, true)
    assert.equal(form.pending, pending); assert.equal(calls, 2)
    assert.doesNotMatch(recovered.message, /private/)
    if (result.status === 401) {
      assert.equal((await form.retry()).uncertain, true, 'a refused retry cannot settle the original attempt')
      assert.equal(form.pending, pending)
    }
  }
})

test('SQL, authentication and permission errors use curated feedback without raw details', async () => {
  for (const [code, status, message] of [
    ['23505', 409, 'That value already exists. Review the note and tags.'],
    ['23503', 409, 'A related item is unavailable. Review the note and tags.'],
    ['token_invalid', 401, 'Session expired or invalid. Sign in again before saving.'],
    ['42501', 403, 'Your session cannot save this note. Check your access.'],
    ['schema_contract_mismatch', 409, 'The application needs an update before saving.'],
  ]) {
    let calls = 0
    const form = createNoteForm(make(async () => { calls++; return new Response(JSON.stringify({ code, message: 'private row', details: 'private constraint' }), { status }) }), { createKey: () => key })
    assert.deepEqual(await form.submit({ body: 'hello', tags: '' }), { outcome: 'failed', message, uncertain: false })
    assert.equal(form.pending, null); assert.equal(calls, 1)
  }
})

test('database contention retains the same receipt key for explicit recovery', async () => {
  for (const code of ['55P03', '40P01']) {
    const payloads = []
    const form = createNoteForm(make(async (url, init) => {
      payloads.push(JSON.parse(init.body))
      return payloads.length === 1
        ? new Response(JSON.stringify({ code, message: 'private relation and SQL' }), { status: 500 })
        : new Response(JSON.stringify([{ ...row, subject: 'alice' }]))
    }), { createKey: () => key })
    const failed = await form.submit({ body: 'hello', tags: '' })
    assert.equal(failed.outcome, 'failed'); assert.equal(failed.uncertain, true)
    assert.doesNotMatch(failed.message, /private|relation|SQL/)
    assert.equal(payloads.length, 1); assert.equal(form.pending.idempotency_key, key)
    assert.deepEqual(await form.submit({ body: 'changed', tags: '' }), { outcome: 'pending' })
    assert.deepEqual(await form.reconcile(), { outcome: 'saved', note: row })
    assert.deepEqual(payloads[0], payloads[1]); assert.equal(form.pending, null)
  }
})

test('service failure and deadlines retain a pending request and never automatically replay', async () => {
  let calls = 0
  const unavailable = createNoteForm(make(async () => { calls++; return new Response('{"code":"data_api_unavailable"}', { status: 503 }) }), { createKey: () => key })
  assert.equal((await unavailable.submit({ body: 'hello', tags: '' })).uncertain, true)
  assert.equal(calls, 1); assert.ok(unavailable.pending)
  const client = notesClient({ url: 'https://notes.example', subject: 'alice', accessToken: () => new Promise(() => {}) })
  const timeout = createNoteForm(client, { timeoutMs: 10, createKey: () => key })
  assert.deepEqual(await timeout.submit({ body: 'hello', tags: '' }), { outcome: 'failed', message: 'The save timed out. Its outcome is uncertain.', uncertain: true })
  assert.ok(timeout.pending)
})

test('in-flight submissions and recovery cannot overlap', async () => {
  let resolve
  const form = createNoteForm(make(() => new Promise(done => { resolve = done })), { createKey: () => key })
  const first = form.submit({ body: 'hello', tags: '' })
  await new Promise(done => setImmediate(done))
  assert.deepEqual(await form.submit({ body: 'changed', tags: '' }), { outcome: 'busy' })
  assert.deepEqual(await form.reconcile(), { outcome: 'busy' })
  resolve(new Response(JSON.stringify([{ ...row, subject: 'alice' }])))
  assert.equal((await first).outcome, 'saved')
})

test('response limits and rate responses preserve the receipt until explicit same-key recovery', async () => {
  for (const [code, status, message] of [
    ['response_too_large', 502, 'The save response was too large. Its outcome is uncertain.'],
    ['data_api_sql_deadline_exceeded', 504, 'The save timed out. Its outcome is uncertain.'],
    ['data_api_rate_limited', 429, 'Too many requests. Wait before checking the save outcome.'],
    ['data_api_concurrency_limited', 429, 'Too many requests are still running. Wait before checking the save outcome.'],
    ['custom_rate_response', 429, 'Too many requests. Wait before checking the save outcome.'],
  ]) {
    const payloads = []
    const form = createNoteForm(make(async (_, init) => {
      payloads.push(JSON.parse(init.body))
      if (payloads.length === 1) return new Response(JSON.stringify({ code, message: 'private failure detail' }), { status })
      return new Response(JSON.stringify([{ ...row, subject: 'alice' }]))
    }), { createKey: () => key })
    assert.deepEqual(await form.submit({ body: 'hello', tags: '' }), { outcome: 'failed', message, uncertain: true })
    assert.equal(form.pending.idempotency_key, key)
    assert.deepEqual(await form.submit({ body: 'different', tags: '' }), { outcome: 'pending' })
    assert.equal(payloads.length, 1)
    assert.deepEqual(await form.reconcile(), { outcome: 'saved', note: row })
    assert.deepEqual(payloads[1], payloads[0])
    assert.equal(form.pending, null)
  }
})
