import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, optimisticMutation } from '../dist/index.js'
const query = (body, status = 200) => createDataClient({ url: 'https://data.example', accessToken: 'jwt', fetch: async () => new Response(body, { status }) }).from('documents').update({ body: 'edited' }).eq('id',1).eq('version',1).select('id,version')
test('conditional mutations distinguish applied, conflict and server errors', async () => {
  assert.deepEqual(await optimisticMutation(query('[{"id":1,"version":2}]')), { outcome:'applied', row:{id:1,version:2} })
  assert.deepEqual(await optimisticMutation(query('[]')), { outcome:'conflict' })
  const failed = await optimisticMutation(query('{"code":"42501","message":"denied"}',403))
  assert.equal(failed.outcome,'error'); assert.equal(failed.status,403); assert.equal(failed.error.code,'42501')
})
test('transport failures never become write conflicts or replay mutations', async () => {
  let calls = 0
  const db = createDataClient({ url:'https://data.example',accessToken:'jwt',fetch:async () => { calls++; throw new Error('connection lost') } })
  const result = await optimisticMutation(db.from('documents').delete().eq('id',1).eq('version',1).select('id'))
  assert.equal(result.outcome,'error'); assert.equal(result.status,0); assert.equal(calls,1)
})
test('missing selection and multiple rows reject ambiguous results', async () => {
  await assert.rejects(optimisticMutation(query('null')), /select/)
  await assert.rejects(optimisticMutation(query('[{"id":1},{"id":2}]')), /primary key/)
})
