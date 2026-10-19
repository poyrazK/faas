import test from 'node:test'
import assert from 'node:assert/strict'
import { classifyVersionedMutation, createDataClient, createReplayKeyV2 } from '../dist/index.js'

const row = { id: 7, subject: 'alice', body: 'changed', version: 2 }
const matches = value => value.id === 7 && value.subject === 'alice' && value.version === 2

test('versioned writes confirm one matching snapshot and preserve original error contracts', () => {
  assert.deepEqual(classifyVersionedMutation({ data: [row], error: null, status: 200 }, matches), { outcome: 'applied', row, status: 200 })
  for (const [message, outcome] of [['idempotency_version_conflict', 'version_conflict'], ['idempotency_payload_conflict', 'payload_conflict']]) {
    const error = Object.freeze({ code: 'PT409', message, details: 'private constraint' })
    const result = classifyVersionedMutation({ data: null, error, status: 409 }, () => { assert.fail('conflict cannot confirm a row') })
    assert.deepEqual(result, { outcome, error, status: 409 }); assert.equal(result.error, error)
    assert.equal(classifyVersionedMutation({ data: null, error, status: 500 }, matches).reason, 'request_failed')
    assert.equal(classifyVersionedMutation({ data: null, error: { code: '23505', message }, status: 409 }, matches).reason, 'request_failed')
  }
})

test('empty, mismatched and expired write results preserve uncertainty', () => {
  assert.equal(classifyVersionedMutation({ data: [], error: null, status: 200 }, matches).outcome, 'unresolved')
  const mismatched = classifyVersionedMutation({ data: [{ ...row, subject: 'bob' }], error: null, status: 200 }, matches)
  assert.equal(mismatched.reason, 'invalid_receipt'); assert.equal('row' in mismatched, false)
  const error = { code: 'PT410', message: 'idempotency_key_expired' }
  assert.deepEqual(classifyVersionedMutation({ data: null, error, status: 410 }, matches), { outcome: 'unresolved', reason: 'replay_expired', error, status: 410 })
  assert.equal(classifyVersionedMutation({ data: null, error: { code: 'request_timeout' }, status: 0 }, matches).reason, 'request_failed')
})

test('classification neither executes a query nor retries a versioned write', async () => {
  const requests = []
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async (url, init) => {
    requests.push({ path: new URL(url).pathname, body: JSON.parse(init.body) })
    return new Response(JSON.stringify([row]))
  } }).schema('api')
  const pending = Object.freeze({ idempotency_key: createReplayKeyV2(), note_id: 7, expected_version: 1, note_patch: { body: 'changed' } })
  const result = await db.rpc('update_note_once_v2', pending).retry(false)
  assert.equal(classifyVersionedMutation(result, matches).outcome, 'applied')
  assert.equal(classifyVersionedMutation(result, matches).outcome, 'applied')
  assert.deepEqual(requests, [{ path: '/rest/v1/rpc/update_note_once_v2', body: pending }])
  let executed = false
  classifyVersionedMutation({ then() { executed = true } }, matches)
  assert.equal(executed, false)
})
