import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, validatedRpc, RpcValidationError, classifyDataError, dataRequest } from '../dist/index.js'

const bodyParser = { parse(value) {
  if (!value || typeof value.body !== 'string' || !value.body.trim()) throw new Error('private invalid input')
  return { body: value.body.trim(), tags: value.tags ?? [] }
} }

test('invalid untrusted input returns a safe structured failure before query construction or token lookup', async () => {
  let queries = 0, tokens = 0, requests = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: () => { tokens++; return 'token' }, fetch: async () => { requests++; return new Response('1') } })
  for (const input of [null, undefined, false, {}, { body: 42 }, { body: ' ' }]) {
    const outcome = await validatedRpc(input, bodyParser, args => { queries++; return db.rpc('create_note', args) })
    assert.equal(outcome.outcome, 'validation_failed')
    assert.ok(outcome.error instanceof RpcValidationError)
    assert.equal(outcome.error.code, 'RPC_ARGUMENT_VALIDATION_FAILED')
    assert.equal(outcome.error.message, 'RPC argument validation failed')
    assert.equal('cause' in outcome.error, false)
    assert.equal('input' in outcome.error, false)
    assert.equal('result' in outcome, false)
  }
  assert.deepEqual([queries, tokens, requests], [0, 0, 0])
})

test('parser transformations and defaults are sent with schema/auth and explicit query modifiers', async () => {
  const requests = []
  const db = createDataClient({ url: 'https://data.example', accessToken: 'application-token', fetch: async (input, init) => {
    requests.push({ url: String(input), init })
    return new Response('{"id":7,"body":"hello"}', { status: 200 })
  } }).schema('api')
  const input = Object.freeze({ body: ' hello ', privateIgnored: 'secret' })
  const outcome = await validatedRpc(input, bodyParser, args => db.rpc('create_note', args).select('id,body').single().retry(false))
  assert.equal(outcome.outcome, 'completed')
  assert.deepEqual(outcome.result.data, { id: 7, body: 'hello' })
  assert.equal(requests.length, 1)
  assert.equal(new URL(requests[0].url).pathname, '/rest/v1/rpc/create_note')
  assert.equal(new URL(requests[0].url).searchParams.get('select'), 'id,body')
  assert.equal(requests[0].init.method, 'POST')
  assert.equal(requests[0].init.headers.get('Authorization'), 'Bearer application-token')
  assert.equal(requests[0].init.headers.get('Content-Profile'), 'api')
  assert.deepEqual(JSON.parse(requests[0].init.body), { body: 'hello', tags: [] })
  assert.deepEqual(input, { body: ' hello ', privateIgnored: 'secret' })
})

test('preserves parser receiver and original response identity; accepts empty arguments and null results', async () => {
  const result = { data: null, error: null, status: 200, count: 0 }
  const parser = { prefix: 'value', parse(value) { return { body: this.prefix + value } } }
  let args
  const outcome = await validatedRpc('!', parser, parsed => { args = parsed; return Promise.resolve(result) })
  assert.deepEqual(args, { body: 'value!' })
  assert.equal(outcome.result, result)
  const empty = await validatedRpc(undefined, { parse: () => ({}) }, parsed => { assert.deepEqual(parsed, {}); return Promise.resolve(result) })
  assert.equal(empty.result, result)
})

test('rejects malformed or asynchronous parser output without sending a request or leaking rejection', async () => {
  let queries = 0
  for (const value of [null, undefined, 1, 'hello', [], () => {}, Promise.resolve({ body: 'hello' }), Promise.reject(new Error('private rejection'))]) {
    const outcome = await validatedRpc({}, { parse: () => value }, () => { queries++; return Promise.resolve({}) })
    assert.equal(outcome.outcome, 'validation_failed')
  }
  const getter = await validatedRpc({}, { parse: () => ({ get then() { throw new Error('private getter') } }) }, () => { queries++; return Promise.resolve({}) })
  assert.equal(getter.outcome, 'validation_failed')
  assert.equal(queries, 0)
})

test('server and transport failures remain request results, with no extra requests', async () => {
  for (const [code, status] of [['23505', 409], ['schema_contract_mismatch', 409], ['data_api_unavailable', 503]]) {
    let requests = 0
    const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => {
      requests++
      return new Response(JSON.stringify({ code, message: 'private', details: '', hint: '' }), { status })
    } })
    const outcome = await validatedRpc({ body: 'hello' }, bodyParser, args => db.rpc('create_note', args).retry(false))
    assert.equal(outcome.outcome, 'completed')
    assert.equal(outcome.result.error.code, code)
    assert.equal(outcome.result.status, status)
    assert.notEqual(classifyDataError(outcome.result.error, { status }).category, 'unknown')
    assert.equal(requests, 1)
  }
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => { throw new TypeError('network') } })
  const outcome = await validatedRpc({ body: 'hello' }, bodyParser, args => db.rpc('create_note', args).retry(false))
  assert.equal(outcome.outcome, 'completed')
  assert.equal(outcome.result.status, 0)
  assert.ok(outcome.result.error)
})

test('query construction and thrown request errors propagate unchanged instead of becoming validation failures', async () => {
  const original = new Error('request failure')
  await assert.rejects(validatedRpc({}, { parse: () => ({}) }, () => { throw original }), error => error === original)
  await assert.rejects(validatedRpc({}, { parse: () => ({}) }, () => Promise.reject(original)), error => error === original)
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => new Response('{"code":"23503","message":"private","details":"","hint":""}', { status: 409 }) })
  await assert.rejects(validatedRpc({ body: 'hello' }, bodyParser, args => db.rpc('create_note', args).retry(false).throwOnError()), error => error.code === '23503')
})

test('callback can use dataRequest deadlines without changing their result', async () => {
  const db = createDataClient({ url: 'https://data.example', accessToken: () => new Promise(() => {}) })
  const outcome = await validatedRpc({ body: 'hello' }, bodyParser, args => dataRequest(db.rpc('create_note', args), { timeoutMs: 10 }))
  assert.equal(outcome.outcome, 'completed')
  assert.equal(outcome.result.error.code, 'request_timeout')
})
