import { test } from 'node:test'
import assert from 'node:assert/strict'
import { validateJson, JsonValidationError } from '../dist/index.js'
const details = { parse(value) {
  if (!value || typeof value.count !== 'number') throw new Error('secret payload: ' + JSON.stringify(value))
  return { count: value.count }
} }
test('validates arrays and single rows without mutating data', () => {
  const input = [{ id: 1, details: { count: 2, extra: 'private' } }]
  assert.deepEqual(validateJson(input, { details }), [{ id: 1, details: { count: 2 } }])
  assert.equal(input[0].details.extra, 'private')
  assert.deepEqual(validateJson(input[0], { details }), { id: 1, details: { count: 2 } })
  assert.deepEqual(validateJson([], { details }), [])
  assert.equal(validateJson(null, { details }), null)
})
test('null fields require explicit parser acceptance', () => {
  assert.throws(() => validateJson({ details: null }, { details }), JsonValidationError)
  const nullable = { parse: value => value === null ? null : details.parse(value) }
  assert.deepEqual(validateJson({ details: null }, { details: nullable }), { details: null })
})
test('malformed data and changed schemas fail with safe row and field context', () => {
  for (const bad of [null, [], 'private', { count: 'private' }, { renamed: 1 }]) {
    assert.throws(() => validateJson([{ details: { count: 1 } }, { details: bad }], { details }), error => {
      assert.equal(error.code, 'JSON_VALIDATION_FAILED')
      assert.equal(error.rowIndex, 1)
      assert.equal(error.field, 'details')
      assert.equal(error.cause, undefined)
      assert.doesNotMatch(JSON.stringify(error) + error.message, /private|secret payload/)
      return true
    })
  }
  assert.throws(() => validateJson({}, { details }), error => error.rowIndex === null)
})
test('preserves parser receivers and safely rejects asynchronous parsers', () => {
  const parser = { expected: 2, parse(value) { assert.equal(value, this.expected); return String(value) } }
  assert.equal(validateJson({ value: 2 }, { value: parser }).value, '2')
  assert.throws(() => validateJson({ value: 2 }, { value: { parse: async () => { throw new Error('private') } } }), JsonValidationError)
})

test('validates SDK query results while leaving transport errors intact', async () => {
  const { createDataClient, selectJson, jsonValue } = await import('../dist/index.js')
  let fail = false
  const db = createDataClient({ url: 'https://data.example', accessToken: 'jwt', fetch: async () =>
    fail ? new Response(JSON.stringify({ code: '42501', message: 'permission denied' }), { status: 403 })
      : new Response(JSON.stringify([{ id: 1, details: { count: 2 } }]), { status: 200 }) })
  const response = await selectJson(db.from('orders'), 'id', { details: jsonValue('metadata', 'details') })
  assert.equal(response.error, null)
  assert.equal(validateJson(response.data, { details })[0].details.count, 2)
  fail = true
  const denied = await db.from('orders').select()
  assert.equal(denied.error.code, '42501')
  assert.equal(denied.status, 403)
  assert.equal(denied.data, null)
})
