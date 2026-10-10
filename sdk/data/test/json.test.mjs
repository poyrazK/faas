import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, selectJson, jsonText, jsonValue } from '../dist/index.js'

test('JSON projections quote names, retain SDK filters/authentication and preserve null responses', async () => {
  let request
  const db = createDataClient({ url: 'https://data.example', accessToken: 'application-token', fetch: async (url, init) => {
    request = { url: new URL(url), init }
    return Response.json([{ id: 1, status: null, 'delivery city': 'Paris', last: 'item', details: null }])
  } }).schema('api')
  const result = await selectJson(db.from('orders'), 'id', {
    status: jsonText('metadata', 'status'),
    'delivery city': jsonText('metadata', 'delivery,address', 'city'),
    last: jsonText('metadata', 'items', -1),
    details: jsonValue('metadata', 'details'),
  }).eq('metadata->>status', 'paid').order('id').limit(10)
  assert.equal(request.url.searchParams.get('select'), 'id,"status":"metadata"->>"status","delivery city":"metadata"->"delivery,address"->>"city","last":"metadata"->"items"->>-1,"details":"metadata"->"details"')
  assert.equal(request.url.searchParams.get('metadata->>status'), 'eq.paid')
  assert.equal(request.init.headers.get('Authorization'), 'Bearer application-token')
  assert.deepEqual(result.data, [{ id: 1, status: null, 'delivery city': 'Paris', last: 'item', details: null }])
})

test('invalid projection fragments fail before sending and constructed paths cannot be mutated', () => {
  let requests = 0
  const source = createDataClient({ url: 'https://data.example', accessToken: 'jwt', fetch: async () => { requests++; return Response.json([]) } }).from('orders')
  for (const key of ['', 'key"),secret', 'key\\', '\u0000']) assert.throws(() => jsonText('metadata', key), /projection names/)
  for (const index of [1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1]) assert.throws(() => jsonValue('metadata', index), /safe integers/)
  assert.throws(() => jsonText('metadata'), /Invalid JSON projection/)
  assert.throws(() => selectJson(source, 'id', {}), /At least one/)
  assert.throws(() => selectJson(source, 'id', { value: { column: 'metadata', path: [], text: true } }), /Invalid JSON projection/)
  assert.throws(() => selectJson(source, 'id', { 'bad"alias': jsonText('metadata', 'key') }), /projection names/)
  const path = jsonText('metadata', 'status')
  assert.throws(() => { path.path[0] = 'secret' }, TypeError)
  assert.equal(requests, 0)
})
