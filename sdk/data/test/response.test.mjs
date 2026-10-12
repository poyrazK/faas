import test from 'node:test'
import assert from 'node:assert/strict'
import { validateRpcResponse, RpcResponseValidationError, createDataClient, validatedRpc } from '../dist/index.js'

const response = data => ({ data, error: null, status: 200, statusText: 'OK', count: 3 })
const numberParser = { parse(value) { if (typeof value !== 'number') throw new Error('private value'); return value } }

test('parses scalar, JSON and row responses and preserves metadata without replacing the original data', () => {
  const examples = [
    [42, numberParser, 42],
    [{ amount: '42' }, { parse: value => ({ amount: Number(value.amount) }) }, { amount: 42 }],
    [[{ id: 7, body: 'hello' }], { parse: rows => rows.map(row => ({ ...row, label: row.body.toUpperCase() })) }, [{ id: 7, body: 'hello', label: 'HELLO' }]],
  ]
  for (const [input, parser, expected] of examples) {
    const original = Object.freeze(response(input))
    const checked = validateRpcResponse(original, parser)
    assert.equal(checked.outcome, 'validated')
    assert.deepEqual(checked.result, { ...original, data: expected })
    assert.notEqual(checked.result, original)
    assert.equal(original.data, input)
  }
})

test('null, undefined and falsy successful data are passed to the parser without being treated as failures', () => {
  for (const input of [null, undefined, 0, false, '']) {
    const seen = []
    const result = validateRpcResponse({ ...response(input), status: input === undefined ? 204 : 200 }, { parse(value) { seen.push(value); return { value } } })
    assert.equal(result.outcome, 'validated')
    assert.deepEqual(seen, [input]); assert.deepEqual(result.result.data, { value: input })
  }
  assert.equal(validateRpcResponse(response(null), numberParser).outcome, 'validation_failed')
})

test('request and server errors preserve exact result identity and never invoke the parser', () => {
  let calls = 0
  const parser = { parse() { calls++; throw new Error('unexpected') } }
  for (const result of [
    { ...response(null), error: { code: '23505', message: 'private row' }, status: 409 },
    { ...response(null), error: { code: 'request_timeout' }, status: 0 },
    { ...response(null), error: null, status: 503 },
    { ...response(null), error: { code: 'unexpected' }, status: 200 },
  ]) {
    const checked = validateRpcResponse(result, parser)
    assert.equal(checked.outcome, 'request_failed'); assert.equal(checked.result, result)
  }
  assert.equal(calls, 0)
})

test('validation failures omit rejected data and parser exceptions and preserve original HTTP status', () => {
  const privateException = Object.assign(new Error('private parser details'), { input: 'private data' })
  const result = response({ secret: 'private value' })
  const checked = validateRpcResponse(result, { parse() { throw privateException } })
  assert.equal(checked.outcome, 'validation_failed')
  assert.equal(checked.status, 200)
  assert.ok(checked.error instanceof RpcResponseValidationError)
  assert.equal(checked.error.code, 'RPC_RESPONSE_VALIDATION_FAILED')
  assert.equal(checked.error.message, 'RPC response validation failed')
  for (const key of ['cause', 'input', 'data', 'original']) assert.equal(key in checked.error, false)
  assert.equal('result' in checked, false)
  assert.doesNotMatch(JSON.stringify(checked), /private/)
  assert.deepEqual(result.data, { secret: 'private value' })
})

test('preserves parser receivers and rejects async outputs without unhandled rejection', () => {
  const parser = { factor: 2, parse(value) { return value * this.factor } }
  assert.equal(validateRpcResponse(response(21), parser).result.data, 42)
  for (const value of [Promise.resolve(42), Promise.reject(new Error('private async rejection')), { get then() { throw new Error('private getter') } }]) {
    assert.equal(validateRpcResponse(response(42), { parse: () => value }).outcome, 'validation_failed')
  }
})

test('actual RPC result validation and argument validation compose without extra requests', async () => {
  let requests = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => { requests++; return new Response('"unexpected scalar"') } })
  const completed = await validatedRpc({}, { parse: () => ({}) }, args => db.rpc('count_notes', args).retry(false))
  assert.equal(completed.outcome, 'completed')
  assert.equal(validateRpcResponse(completed.result, numberParser).outcome, 'validation_failed')
  assert.equal(requests, 1)
  assert.equal(completed.result.data, 'unexpected scalar')
})
