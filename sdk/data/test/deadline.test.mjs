import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, dataRequest } from '../dist/index.js'
const client = fetch => createDataClient({ url: 'https://data.example', accessToken: 'jwt', fetch }).schema('api')
const waiting = async (_input, init) => new Promise((_resolve, reject) => {
  if (init.signal.aborted) reject(init.signal.reason)
  else init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true })
})
test('deadlines and caller cancellation have distinct stable codes and no retries', async () => {
  let calls = 0
  const db = client((...args) => { calls++; return waiting(...args) })
  const expired = await dataRequest(db.from('notes').select(), { timeoutMs: 20 })
  assert.equal(expired.status, 0); assert.equal(expired.data, null); assert.equal(expired.error.code, 'request_timeout')
  const controller = new AbortController()
  const pending = dataRequest(db.rpc('slow'), { timeoutMs: 1000, signal: controller.signal })
  setTimeout(() => controller.abort(), 10)
  const cancelled = await pending
  assert.equal(cancelled.error.code, 'request_cancelled'); assert.equal(calls, 2)
})
test('pending token lookup is bounded and never starts transport after cancellation', async () => {
  let resolveToken, calls = 0
  const db = createDataClient({ url: 'https://data.example', accessToken: () => new Promise(resolve => { resolveToken = resolve }), fetch: async () => { calls++; return new Response('[]') } })
  const result = await dataRequest(db.from('notes').select(), { timeoutMs: 20 })
  assert.equal(result.error.code, 'request_timeout')
  resolveToken('late-token')
  await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(calls, 0)
})
test('pre-cancelled requests and invalid deadlines do not send data', async () => {
  let calls = 0
  const db = client(async () => { calls++; return new Response('[]') })
  const signal = AbortSignal.abort()
  assert.equal((await dataRequest(db.from('notes').select(), { timeoutMs: 100, signal })).error.code, 'request_cancelled')
  for (const timeoutMs of [0, -1, Infinity, 1.5, 2147483648]) await assert.rejects(dataRequest(db.from('notes').select(), { timeoutMs }))
  assert.equal(calls, 0)
})
test('success and server failures preserve responses and do not become timeouts', async () => {
  const result = await dataRequest(client(async () => new Response('[{"id":1}]')).from('notes').select('id'), { timeoutMs: 1000 })
  assert.deepEqual(result.data, [{ id: 1 }]); assert.equal(result.error, null)
  const error = await dataRequest(client(async () => new Response('{"code":"42501"}', { status: 403 })).from('notes').select(), { timeoutMs: 1000 })
  assert.equal(error.status, 403); assert.equal(error.error.code, '42501')
})
test('deadline remains active while consuming response bodies', async () => {
  const db = client(async (_input, init) => new Response(new ReadableStream({
    start(controller) { init.signal.addEventListener('abort', () => controller.error(init.signal.reason), { once: true }) },
  })))
  const result = await dataRequest(db.from('notes').select(), { timeoutMs: 20 })
  assert.equal(result.error.code, 'request_timeout')
  assert.equal(result.data, null)
})
