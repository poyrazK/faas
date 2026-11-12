import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, parseLosslessJSON } from '../dist/index.js'
import { liveRead } from '../dist/live/index.js'

test('lossless REST and live values agree for large integers, decimals, arrays and JSON', async () => {
  const wire = '[{"id":9007199254740993,"price":1.234567890123456789,"count":2,"values":[9007199254740993,null],"json":{"v":1e100,"text":"123"}}]'
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', numericMode: 'lossless', fetch: async () => new Response(wire, { headers: { 'content-type': 'application/json' } }) }).schema('api')
  const query = db.from('items').select('*')
  const { data, error } = await query
  assert.equal(error, null)
  assert.deepEqual(data, [{ id: '9007199254740993', price: '1.234567890123456789', count: '2', values: ['9007199254740993', null], json: { v: '1e100', text: '123' } }])
  const columns = ['int8', 'numeric', 'int4', '_int8', 'jsonb'].map((type, i) => ({ name: ['id', 'price', 'count', 'values', 'json'][i], type }))
  assert.deepEqual(liveRead(query).parse(['9007199254740993', '1.234567890123456789', '2', '{9007199254740993,NULL}', '{"v":1e100,"text":"123"}'], columns), data[0])
})

test('lossless decoder preserves lexemes and treats keys and escaped strings as text', () => {
  const data = parseLosslessJSON('{"123":-0,"a":"a\\\"123","b":[1e-1000,0.1000000000000000001]}')
  assert.deepEqual(data, { 123: '-0', a: 'a"123', b: ['1e-1000', '0.1000000000000000001'] })
  assert.throws(() => parseLosslessJSON('[01]'))
  assert.throws(() => parseLosslessJSON('[1.]'))
})

test('default numbers and lossless error status codes retain existing behavior', async () => {
  const fetch = async () => new Response('[{"n":42}]', { headers: { 'content-type': 'application/json' } })
  assert.equal((await createDataClient({ url: 'https://data.example', accessToken: 'token', fetch }).from('x').select()).data[0].n, 42)
  const error = { code: '42501', message: 'denied', details: null, hint: null }
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', numericMode: 'lossless', fetch: async () => new Response(JSON.stringify(error), { status: 403, headers: { 'content-type': 'application/json' } }) })
  assert.equal((await db.from('x').select()).error.code, '42501')
})
