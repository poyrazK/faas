import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient } from '../dist/index.js'

test('client evaluates rotating tokens per request and preserves PostgREST behavior', async () => {
  let token = 'first'
  const requests = []
  const client = createDataClient({ url: 'https://data.example', accessToken: async () => token,
    fetch: async (url, init) => {
      requests.push({url: String(url), ...init})
      return new Response('[{"id":7,"body":"hello"}]', {status:200,headers:{'Content-Type':'application/json','Content-Range':'0-0/1'}})
    } })
  const first = await client.schema('api').from('notes').select('id,body', {count:'exact'}).eq('id',7)
  assert.equal(first.data[0].body, 'hello')
  assert.equal(first.count, 1)
  token = 'second'
  await client.schema('api').from('notes').insert({body:'next'})
  assert.equal(requests[0].headers.get('Authorization'), 'Bearer first')
  assert.equal(requests[1].headers.get('Authorization'), 'Bearer second')
  assert.equal(requests[0].headers.get('Accept-Profile'), 'api')
  assert.equal(requests[1].headers.get('Content-Profile'), 'api')
  assert.equal(requests[0].credentials, 'omit')
  assert.equal(requests[0].redirect, 'error')
  assert.match(requests[0].url, /\/rest\/v1\/notes\?select=id%2Cbody&id=eq.7$/)
})

test('client validates URLs and never sends missing tokens', async () => {
  for (const url of ['http://remote.example', 'https://u:p@data.example', 'https://data.example?token=x']) assert.throws(() => createDataClient({url, accessToken:'x'}))
  let sent = false
  const client = createDataClient({url:'https://data.example',accessToken:()=>'',fetch:async()=>{sent=true;return new Response('[]')}})
  await assert.rejects(async () => await client.schema('api').from('notes').select().throwOnError(), /application access token/)
  assert.equal(sent, false)
})
