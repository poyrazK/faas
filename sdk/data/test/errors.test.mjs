import test from 'node:test'
import assert from 'node:assert/strict'
import { classifyDataError, createDataClient, dataRequest } from '../dist/index.js'

test('classifies stable codes while preserving original identity and private details', () => {
  const cases = {
    '23505': ['unique_violation', 'change_input'], '23503': ['foreign_key_violation', 'change_input'],
    '42501': ['permission_denied', 'check_permissions'], token_required: ['authentication', 'authenticate'],
    token_invalid: ['authentication', 'authenticate'], PGRST301: ['authentication', 'authenticate'],
    PGRST302: ['authentication', 'authenticate'], PGRST303: ['authentication', 'authenticate'],
    origin_not_allowed: ['permission_denied', 'check_permissions'],
    schema_contract_mismatch: ['contract_mismatch', 'refresh_contract'],
    request_timeout: ['timeout', 'reconcile'], query_timeout: ['timeout', 'reconcile'],
    request_cancelled: ['cancelled', 'reconcile'], '57014': ['cancelled', 'reconcile'],
    data_api_unavailable: ['unavailable', 'check_service'], serving_contract_unavailable: ['unavailable', 'check_service'],
    data_api_refresh_unavailable: ['unavailable', 'check_service'],
  }
  for (const [code, [category, recovery]] of Object.entries(cases)) {
    const original = Object.freeze({ code, message: 'private row value', details: 'private constraint', hint: 'private hint' })
    const result = classifyDataError(original, { status: 500 })
    assert.deepEqual(result, { category, recovery, code, status: 500, original })
    assert.equal(result.original, original)
    assert.ok(Object.isFrozen(result))
    assert.equal('message' in result, false)
  }
})

test('status fallback is bounded and generic conflicts or messages do not imply contract mismatch', () => {
  for (const [status, category] of [[401, 'authentication'], [403, 'permission_denied'], [503, 'unavailable'], [504, 'timeout'], [0, 'no_response'], [409, 'unknown'], [500, 'unknown']]) {
    assert.equal(classifyDataError({}, { status }).category, category)
  }
  for (const code of ['PT409', 'PT503', 'constructor', '__proto__', '', 23505]) {
    assert.equal(classifyDataError({ code, message: 'schema_contract_mismatch', details: '23505' }).category, 'unknown')
  }
  for (const status of [-1, 600, 1.5, NaN, Infinity, '503']) assert.equal(classifyDataError({}, { status }).status, null)
  for (const error of [false, 0, '', new Error('23505'), new DOMException('cancelled', 'AbortError')]) {
    assert.equal(classifyDataError(error).category, 'unknown')
  }
  assert.equal(classifyDataError(null), null)
  assert.equal(classifyDataError(undefined), null)
})

test('real client result and throwOnError retain SQL and gateway classification without extra requests', async () => {
  for (const [code, status, category] of [['23505', 409, 'unique_violation'], ['23503', 409, 'foreign_key_violation'], ['42501', 403, 'permission_denied'], ['schema_contract_mismatch', 409, 'contract_mismatch'], ['data_api_unavailable', 503, 'unavailable']]) {
    let calls = 0
    const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
      calls++
      return new Response(JSON.stringify({ code, message: 'private', details: '', hint: '' }), { status })
    } })
    const result = await db.from('notes').insert({ body: 'hello' }).retry(false)
    assert.equal(classifyDataError(result.error, { status: result.status }).category, category)
    await assert.rejects(async () => await db.from('notes').insert({ body: 'hello' }).retry(false).throwOnError(), error => {
      const classified = classifyDataError(error)
      assert.equal(classified.category, category)
      assert.equal(classified.original, error)
      assert.equal(classified.status, null)
      return true
    })
    assert.equal(calls, 2)
  }
})

test('transport, deadline and cancellation remain explicit uncertain outcomes', async () => {
  let calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => { calls++; throw new TypeError('network unavailable') } })
  const failed = await db.from('notes').insert({ body: 'hello' }).retry(false)
  assert.equal(classifyDataError(failed.error, { status: failed.status }).category, 'no_response')
  assert.equal(calls, 1)
  const waiting = createDataClient({ url: 'https://data.example', accessToken: () => new Promise(() => {}) })
  const timeout = await dataRequest(waiting.from('notes').insert({ body: 'hello' }), { timeoutMs: 10 })
  assert.equal(classifyDataError(timeout.error, { status: timeout.status }).category, 'timeout')
  const controller = new AbortController()
  controller.abort()
  const cancelled = await dataRequest(waiting.from('notes').insert({ body: 'hello' }), { timeoutMs: 100, signal: controller.signal })
  assert.equal(classifyDataError(cancelled.error, { status: cancelled.status }).category, 'cancelled')
})
