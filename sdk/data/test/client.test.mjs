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
  let observations = 0
  const onResponse = () => { observations++ }
  const client = createDataClient({ url: `http://127.0.0.1:${server.address().port}`, accessToken: 'token', onResponse })
  const controller = new AbortController()
  const pending = Promise.resolve(client.schema('api').from('notes').select().abortSignal(controller.signal).retry(false))
  await opening
  controller.abort()
  const canceled = await pending
  assert.equal(canceled.status, 0)
  assert.equal(canceled.data, null)
  assert.match(canceled.error.message, /AbortError/)
  const offline = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse, fetch: async () => { throw new TypeError('fetch failed') } })
  const failed = await offline.schema('api').from('notes').select().retry(false)
  assert.equal(failed.status, 0)
  assert.equal(failed.data, null)
  assert.ok(failed.error)
  await assert.rejects(Promise.resolve(offline.schema('api').from('notes').select().retry(false).throwOnError()), TypeError)
  assert.equal(observations, 0)
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

test('response diagnostics cover reads, writes and RPCs without consuming bodies or exposing data', async () => {
  const observations = []
  let response
  const ids = ['11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222', '33333333-3333-4333-8333-333333333333']
  let calls = 0
  const client = createDataClient({ url: 'https://data.example', accessToken: 'secret-token-sentinel',
    onResponse: info => {
      assert.equal(response.bodyUsed, false)
      assert.ok(Object.isFrozen(info))
      observations.push(info)
    },
    fetch: async () => {
      const index = calls++
      response = new Response(index === 2 ? '{"code":"P0001","message":"body-secret-sentinel"}' : '[{"id":7,"body":"body-secret-sentinel"}]', {
        status: [200, 201, 400][index], headers: { 'Content-Type': 'application/json', 'Content-Range': '0-0/1', 'X-Request-Id': ids[index] }
      })
      return response
    } }).schema('api')
  const read = await client.from('notes').select('id,body', { count: 'exact' }).eq('body', 'query-secret-sentinel').retry(false)
  assert.equal(read.count, 1)
  assert.equal(read.data[0].body, 'body-secret-sentinel')
  const write = await client.from('notes').insert({ body: 'payload-secret-sentinel' }).select().retry(false)
  assert.equal(write.status, 201)
  assert.equal(write.data[0].id, 7)
  const rpc = await client.rpc('private_function_sentinel', { value: 'argument-secret-sentinel' }).retry(false)
  assert.equal(rpc.status, 400)
  assert.equal(rpc.error.code, 'P0001')
  assert.equal(observations.length, 3)
  assert.deepEqual(observations.map(info => info.requestId), ids)
  assert.deepEqual(observations.map(info => info.status), [200, 201, 400])
  for (const info of observations) {
    assert.deepEqual(Object.keys(info).sort(), ['durationMs', 'requestId', 'status'])
    assert.ok(Number.isFinite(info.durationMs) && info.durationMs >= 0)
  }
  assert.doesNotMatch(JSON.stringify(observations), /sentinel|Bearer|https:/)
})

test('concurrent response diagnostics stay associated with their own responses', async () => {
  const observations = []
  const releases = new Map()
  let started
  const bothStarted = new Promise(resolve => { started = resolve })
  const ids = ['11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222']
  const client = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse: info => observations.push(info),
    fetch: async url => new Promise(resolve => {
      const id = Number(new URL(url).searchParams.get('id').slice(3))
      releases.set(id, () => resolve(new Response(`[{"id":${id}}]`, { status: 200, headers: { 'X-Request-Id': ids[id - 1] } })))
      if (releases.size === 2) started()
    }) }).schema('api')
  const first = Promise.resolve(client.from('notes').select('id').eq('id', 1).retry(false))
  const second = Promise.resolve(client.from('notes').select('id').eq('id', 2).retry(false))
  await bothStarted
  releases.get(2)()
  assert.equal((await second).data[0].id, 2)
  releases.get(1)()
  assert.equal((await first).data[0].id, 1)
  assert.deepEqual(observations.map(info => info.requestId), [ids[1], ids[0]])
})

test('missing and malformed response IDs are null diagnostics', async () => {
  for (const id of [null, '', 'caller-secret-sentinel', '11111111-1111-7111-8111-111111111111']) {
    let observed
    const client = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse: info => { observed = info },
      fetch: async () => new Response(null, { status: 204, headers: id === null ? {} : { 'X-Request-Id': id } }) })
    const result = await client.schema('api').from('notes').insert({ body: 'hello' }).retry(false)
    assert.equal(result.status, 204)
    assert.equal(result.error, null)
    assert.equal(observed.requestId, null)
    assert.equal(observed.status, 204)
  }
})

test('throwing, rejecting and pending diagnostic callbacks do not alter or delay results', async () => {
  for (const callback of [() => { throw new Error('diagnostics failed') }, async () => { throw new Error('diagnostics failed') }, () => new Promise(() => {})]) {
    let calls = 0, observations = 0
    const client = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse: info => { observations++; return callback(info) },
      fetch: async () => { calls++; return new Response('[{"id":7}]', { status: 200 }) } })
    let timer
    try {
      const result = await Promise.race([
        Promise.resolve(client.schema('api').from('notes').select('id').retry(false)),
        new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('diagnostics blocked request')), 1000) }),
      ])
      assert.deepEqual(result.data, [{ id: 7 }])
      assert.equal(result.error, null)
      assert.equal(observations, 1)
      assert.equal(calls, 1)
    } finally { clearTimeout(timer) }
  }
  await new Promise(setImmediate) // Rejected callback promises must be handled.
})

test('each HTTP retry response has its own diagnostic record', async () => {
  let calls = 0
  const observations = []
  const ids = ['11111111-1111-4111-8111-111111111111', '22222222-2222-4222-8222-222222222222']
  const client = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse: info => observations.push(info),
    fetch: async () => {
      const index = calls++
      return new Response(index === 0 ? '{"code":"unavailable","message":"try later"}' : '[{"id":7}]', {
        status: index === 0 ? 503 : 200, headers: { 'X-Request-Id': ids[index] }
      })
    } })
  const result = await client.schema('api').from('notes').select('id').retry(true)
  assert.deepEqual(result.data, [{ id: 7 }])
  assert.equal(calls, 2)
  assert.deepEqual(observations.map(info => [info.requestId, info.status]), [[ids[0], 503], [ids[1], 200]])
})

test('rejected callback promises preserve mutation errors without replay', async () => {
  let calls = 0
  const client = createDataClient({ url: 'https://data.example', accessToken: 'token', onResponse: async () => { throw new Error('telemetry failed') },
    fetch: async () => { calls++; return new Response('{"code":"idempotency_conflict","message":"conflict"}', { status: 409 }) } })
  const result = await client.schema('api').rpc('create_note_with_tags_once', { idempotency_key: 'key', note_body: 'body' }).retry(false)
  assert.equal(result.status, 409)
  assert.equal(result.error.code, 'idempotency_conflict')
  assert.equal(calls, 1)
  await new Promise(setImmediate)
})
