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
    request_timeout: ['timeout', 'reconcile'], query_timeout: ['timeout', 'reconcile'], request_deadline_exceeded: ['timeout', 'reconcile'],
    response_too_large: ['response_too_large', 'reconcile'], request_cancelled: ['cancelled', 'reconcile'], '57014': ['cancelled', 'reconcile'],
    data_api_rate_limited: ['rate_limited', 'wait'], data_api_unavailable: ['unavailable', 'check_service'], serving_contract_unavailable: ['unavailable', 'check_service'],
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
  for (const [status, category] of [[401, 'authentication'], [403, 'permission_denied'], [429, 'rate_limited'], [503, 'unavailable'], [504, 'timeout'], [0, 'no_response'], [409, 'unknown'], [500, 'unknown']]) {
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
  for (const [code, status, category] of [['23505', 409, 'unique_violation'], ['23503', 409, 'foreign_key_violation'], ['42501', 403, 'permission_denied'], ['schema_contract_mismatch', 409, 'contract_mismatch'], ['data_api_unavailable', 503, 'unavailable'], ['data_api_overloaded', 503, 'unavailable'], ['request_deadline_exceeded', 504, 'timeout'], ['response_too_large', 502, 'response_too_large'], ['data_api_rate_limited', 429, 'rate_limited']]) {
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

test('an overloaded ordinary mutation makes one request without enabling retries', async () => {
  let calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
    calls++
    return new Response(JSON.stringify({ code: 'data_api_overloaded' }), { status: 503, headers: { 'Retry-After': '1' } })
  } })
  const result = await db.from('notes').insert({ body: 'hello' })
  assert.equal(classifyDataError(result.error).category, 'unavailable')
  assert.equal(calls, 1)
})

test('a server deadline does not retry an ordinary mutation', async () => {
  let calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
    calls++
    return new Response(JSON.stringify({ code: 'request_deadline_exceeded' }), { status: 504 })
  } })
  const result = await db.from('notes').insert({ body: 'hello' })
  assert.equal(classifyDataError(result.error).category, 'timeout')
  assert.equal(classifyDataError(result.error).recovery, 'reconcile')
  assert.equal(calls, 1)
})

test('response limits preserve reconciliation guidance without retrying GET, POST or thrown errors', async () => {
  let calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
    calls++
    return new Response(JSON.stringify({ code: 'response_too_large', limit: 16777216, observed: 16777217, observed_is_lower_bound: true }), { status: 502 })
  } })
  for (const query of [db.from('notes').select(), db.from('notes').insert({ body: 'hello' })]) {
    const result = await query
    assert.equal(classifyDataError(result.error, { status: result.status }).category, 'response_too_large')
    assert.equal(classifyDataError(result.error).recovery, 'reconcile')
  }
  await assert.rejects(async () => await db.from('notes').insert({ body: 'hello' }).throwOnError(), error => classifyDataError(error).category === 'response_too_large')
  assert.equal(calls, 3)
  assert.equal(classifyDataError({}, { status: 502 }).category, 'unknown')
})

test('an interrupted body remains an uncertain transport outcome, with header diagnostics retained', async () => {
  let calls = 0
  const diagnostics = []
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse: info => diagnostics.push(info), fetch: async () => {
    calls++
    const body = new ReadableStream({ start(controller) { controller.enqueue(new TextEncoder().encode('[{"id":')); queueMicrotask(() => controller.error(new TypeError('private-stream-error'))) } })
    return new Response(body, { headers: { 'X-Request-Id': '11111111-1111-4111-8111-111111111111' } })
  } })
  const result = await db.from('notes').insert({ body: 'hello' }).select()
  assert.equal(result.status, 0)
  assert.equal(classifyDataError(result.error, { status: result.status }).category, 'no_response')
  assert.equal(classifyDataError(result.error, { status: result.status }).recovery, 'reconcile')
  assert.equal(calls, 1)
  assert.equal(diagnostics[0].status, 200)
  assert.equal(diagnostics[0].requestId, '11111111-1111-4111-8111-111111111111')
})


test('rate-limited GET, mutation and throwOnError requests wait without automatic replay', async () => {
  let calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
    calls++
    return new Response(JSON.stringify({ code: 'data_api_rate_limited', retry_after_seconds: 1 }), { status: 429, headers: { 'Retry-After': '1' } })
  } })
  for (const query of [db.from('notes').select(), db.from('notes').insert({ body: 'hello' })]) {
    const result = await query
    assert.equal(result.status, 429)
    assert.equal(classifyDataError(result.error).category, 'rate_limited')
    assert.equal(classifyDataError(result.error).recovery, 'wait')
  }
  await assert.rejects(async () => await db.from('notes').insert({ body: 'hello' }).throwOnError(), error => classifyDataError(error).recovery === 'wait')
  assert.equal(calls, 3)
  assert.equal(classifyDataError({ code: 'PT429' }, { status: 429 }).category, 'rate_limited')
  assert.equal(classifyDataError({ code: '23505' }, { status: 429 }).category, 'unique_violation')
})
