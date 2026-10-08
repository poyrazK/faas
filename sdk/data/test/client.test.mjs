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

test('abort signals stop pending client reads and network failures retain status zero', async t => {
  const { createServer } = await import('node:http')
  let opened
  const opening = new Promise(resolve => { opened = resolve })
  const server = createServer(() => opened())
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); server.close() })
  const client = createDataClient({ url: `http://127.0.0.1:${server.address().port}`, accessToken: 'token' })
  const controller = new AbortController()
  const pending = Promise.resolve(client.schema('api').from('notes').select().abortSignal(controller.signal).retry(false))
  await opening
  controller.abort()
  const canceled = await pending
  assert.equal(canceled.status, 0)
  assert.equal(canceled.data, null)
  assert.match(canceled.error.message, /AbortError/)
  const offline = createDataClient({ url: 'https://data.example', accessToken: 'token', fetch: async () => { throw new TypeError('fetch failed') } })
  const failed = await offline.schema('api').from('notes').select().retry(false)
  assert.equal(failed.status, 0)
  assert.equal(failed.data, null)
  assert.ok(failed.error)
  await assert.rejects(Promise.resolve(offline.schema('api').from('notes').select().retry(false).throwOnError()), TypeError)
})

test('RPC uses application authorization and named POST arguments without replaying a failed mutation', async () => {
  const requests = []
  const db = createDataClient({ url: 'https://data.example', accessToken: async () => 'application-token',
    fetch: async (url, init) => {
      requests.push({ url: String(url), ...init })
      return new Response('{"code":"data_api_unavailable","message":"unavailable"}', { status: 503, headers: { 'Content-Type': 'application/json' } })
    } }).schema('api')
  const result = await db.rpc('create_note_with_tags', { note_body: 'atomic', tag_names: ['one'] }).retry(false)
  assert.equal(result.status, 503)
  assert.equal(requests.length, 1)
  assert.equal(requests[0].method, 'POST')
  assert.equal(requests[0].headers.get('Authorization'), 'Bearer application-token')
  assert.equal(requests[0].headers.get('Content-Profile'), 'api')
  assert.equal(requests[0].credentials, 'omit')
  assert.equal(requests[0].redirect, 'error')
  assert.deepEqual(JSON.parse(requests[0].body), { note_body: 'atomic', tag_names: ['one'] })
  assert.equal(new URL(requests[0].url).search, '')
})
