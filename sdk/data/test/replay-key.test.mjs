import test from 'node:test'
import assert from 'node:assert/strict'
import { createReplayKeyV2, classifyDataError, createDataClient, reconcileMutation } from '../dist/index.js'

test('v2 keys carry the original millisecond timestamp and use UUIDv7 randomness', t => {
  const now = Date.parse('2026-10-17T12:34:56.789Z')
  t.mock.method(Date, 'now', () => now)
  const keys = Array.from({ length: 64 }, () => createReplayKeyV2())
  assert.equal(new Set(keys).size, keys.length)
  for (const key of keys) {
    assert.match(key, /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/)
    assert.equal(parseInt(key.replaceAll('-', '').slice(0, 12), 16), now)
  }
})

test('key generation refuses invalid clocks and never falls back from secure randomness', t => {
  const clock = t.mock.method(Date, 'now', () => -1)
  for (const value of [-1, NaN, Infinity, 0.5, 2 ** 48]) {
    clock.mock.mockImplementation(() => value)
    assert.throws(() => createReplayKeyV2(), RangeError)
  }
  clock.mock.mockImplementation(() => 0)
  assert.match(createReplayKeyV2(), /^00000000-0000-7/)
  t.mock.method(crypto, 'getRandomValues', () => { throw new Error('randomness unavailable') })
  assert.throws(() => createReplayKeyV2(), /randomness unavailable/)
})

test('expiry remains an unresolved outcome with the original error and no automatic replay', async () => {
  const expired = { code: 'PT410', message: 'idempotency_key_expired', details: '', hint: '' }
  let calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
    calls++
    return new Response(JSON.stringify(expired), { status: 410 })
  } }).schema('api')
  const result = await db.rpc('create_note_with_tags_once_v2', { idempotency_key: createReplayKeyV2(), note_body: 'hello' })
  assert.equal(classifyDataError(result.error, { status: result.status }).category, 'replay_expired')
  assert.equal(classifyDataError(result.error).recovery, 'reconcile')
  assert.deepEqual(reconcileMutation(result, () => { assert.fail('must not confirm expired response') }), {
    outcome: 'unresolved', reason: 'replay_expired', error: result.error, status: 410,
  })
  await assert.rejects(async () => await db.rpc('save_v2', {}).throwOnError(), error => {
    assert.equal(classifyDataError(error).category, 'replay_expired')
    assert.equal(classifyDataError(error).original, error)
    return true
  })
  assert.equal(calls, 2)
  for (const error of [{ code: 'PT410', message: 'other' }, { code: 'PT409', message: expired.message }, { message: expired.message }]) {
    assert.equal(classifyDataError(error, { status: 410 }).category, 'unknown')
    assert.equal(reconcileMutation({ data: null, error, status: 410 }, () => true).reason, 'request_failed')
  }
  assert.equal(reconcileMutation({ data: null, error: expired, status: 500 }, () => true).reason, 'request_failed')
  assert.equal(classifyDataError(expired, { status: 500 }).category, 'unknown')
})
