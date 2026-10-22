import test from 'node:test'
import assert from 'node:assert/strict'
import { createStorageClient, StorageError } from '../dist/index.js'

// File storage (ADR-20261021101510).

function recorder(respond) {
  const requests = []
  const fetch = async (input, init) => {
    requests.push({ url: String(input), method: init.method, headers: new Headers(init.headers), body: init.body, credentials: init.credentials, redirect: init.redirect })
    return respond(requests.at(-1))
  }
  return { requests, fetch }
}

const row = { bucket: 'uploads', name: 'alice/a b.png', owner: 'alice', size: 3, content_type: 'image/png', created_at: 'now', updated_at: 'now' }

test('files go to the storage routes with the caller token', async () => {
  const { requests, fetch } = recorder(request => {
    if (request.method === 'PUT') return Response.json(row)
    if (request.method === 'POST') return Response.json({ url: 'https://s3.example/x?X-Amz-Signature=abc', expires_at: '2026-10-21T00:01:00Z' })
    if (request.method === 'DELETE') return new Response(null, { status: 204 })
    return new Response('png', { status: request.headers.get('Range') ? 206 : 200 })
  })
  const bucket = createStorageClient({ url: 'https://data.example/rest/v1', accessToken: () => 'token', fetch }).from('uploads')
  assert.deepEqual(await bucket.upload('alice/a b.png', new Blob(['png'], { type: 'image/png' })), row)
  assert.equal(await (await bucket.download('alice/a b.png', { range: { start: 0, end: 1 } })).text(), 'png')
  assert.deepEqual(await bucket.signedUrl('alice/a b.png'), { url: 'https://s3.example/x?X-Amz-Signature=abc', expiresAt: '2026-10-21T00:01:00Z' })
  await bucket.remove('alice/a b.png')
  assert.deepEqual(requests.map(r => `${r.method} ${r.url}`), [
    'PUT https://data.example/storage/v1/object/uploads/alice/a%20b.png',
    'GET https://data.example/storage/v1/object/uploads/alice/a%20b.png',
    'POST https://data.example/storage/v1/sign/uploads/alice/a%20b.png',
    'DELETE https://data.example/storage/v1/object/uploads/alice/a%20b.png',
  ])
  for (const request of requests) {
    assert.equal(request.headers.get('Authorization'), 'Bearer token')
    assert.equal(request.credentials, 'omit')
    assert.equal(request.redirect, 'error')
  }
  assert.equal(requests[0].headers.get('Content-Type'), 'image/png', 'a Blob brings its type')
  assert.equal(requests[1].headers.get('Range'), 'bytes=0-1')
})

test('refusals become StorageError with the Data API code', async () => {
  const { fetch } = recorder(() => new Response(JSON.stringify({ code: 'storage_forbidden', status: 403 }), { status: 403, headers: { 'X-Request-Id': '0b8f6f2e-6c1c-4d3a-9f2e-1a2b3c4d5e6f' } }))
  const bucket = createStorageClient({ url: 'https://data.example', accessToken: 'token', fetch }).from('uploads')
  const error = await bucket.upload('a.txt', 'hello', { contentType: 'text/plain' }).catch(e => e)
  assert.ok(error instanceof StorageError)
  assert.deepEqual([error.status, error.code, error.requestId], [403, 'storage_forbidden', '0b8f6f2e-6c1c-4d3a-9f2e-1a2b3c4d5e6f'])
})

test('names and ranges are checked before any request; visitors send no token', async () => {
  const { requests, fetch } = recorder(() => new Response('x'))
  const storage = createStorageClient({ url: 'https://data.example', anonymous: true, fetch })
  for (const name of ['', 'a//b', '../a', 'a/./b']) await assert.rejects(storage.from('uploads').download(name), /Invalid object name/)
  await assert.rejects(storage.from('Uploads').download('a'), /Invalid bucket name/)
  await assert.rejects(storage.from('uploads').download('a', { range: { start: 5, end: 1 } }), /Invalid range/)
  assert.equal(requests.length, 0)
  await storage.from('avatars').download('alice.png')
  assert.equal(requests[0].headers.get('Authorization'), null)
  assert.throws(() => createStorageClient({ url: 'http://data.example', accessToken: 't' }), /HTTPS/)
})
