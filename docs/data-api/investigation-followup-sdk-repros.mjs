// Negative assertions for the 2026-11-13 audit. These reproduce current defects;
// they are evidence, not acceptance tests for a future fix.
// npm run build --prefix sdk/data
// DATA_API_RUNTIME_DIR=/tmp/prepared-runtime node docs/data-api/investigation-followup-sdk-repros.mjs
import assert from 'node:assert/strict'
import http from 'node:http'
import { spawnSync } from 'node:child_process'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { createDataClient, createStorageClient, createMutationJournal, createReplayKeyV2, ExactNumber } from '../../sdk/data/dist/index.js'

async function progressUpload(onProgress) {
  const id = '00000000-0000-4000-8000-000000000001'
  const session = { id, bucket: 'uploads', name: 'file', size: 1, content_type: 'application/octet-stream',
    state: 'uploading', expires_at: '2099-01-01T00:00:00Z', chunk_size: 5242880, parts: [] }
  const object = { bucket: 'uploads', name: 'file', size: 1, owner: 'alice',
    content_type: session.content_type, created_at: '2026-11-13T00:00:00Z', updated_at: '2026-11-13T00:00:00Z' }
  const client = createStorageClient({ url: 'https://data.example', accessToken: 'synthetic-token', fetch: async (url, init) => {
    if (String(url).endsWith('/complete')) return Response.json({ ...session, state: 'complete', object })
    if (String(url).includes('/parts/')) {
      session.parts = [{ number: 1, size: 1, sha256: new Headers(init.headers).get('x-content-sha256') }]
    }
    return Response.json(session)
  } })
  return client.from('uploads').uploadResumable('file', 'x', { id, onProgress })
}

if (process.argv.includes('--progress-child')) {
  await progressUpload(async () => { throw new Error('progress-observer-failed') })
  await new Promise(resolve => setTimeout(resolve, 20))
} else {
  const runtime = process.env.DATA_API_RUNTIME_DIR
  if (!runtime) throw new Error('DATA_API_RUNTIME_DIR must point to an unchanged runtime copy with its dependencies')
  const { runtimeConfig } = await import(pathToFileURL(runtime + '/config.mjs'))
  const { createChangeRelay } = await import(pathToFileURL(runtime + '/changes.mjs'))
  let published = null
  const raw = '[{"id":1,"event_key":"00000000-0000-4000-8000-000000000001","relation_schema":"api","relation_name":"orders","op":"insert","row_key":{"id":9007199254740993},"new_row":{"id":9007199254740993,"amount":0.123456789012345678901},"old_row":null,"txid":1,"captured_at":"2026-11-13T00:00:00Z"}]'
  const upstream = http.createServer(async (req, res) => {
    for await (const _ of req) { /* consume synthetic loopback arguments */ }
    if (req.url === '/rpc/gregale_change_claim') { res.setHeader('content-type', 'application/json'); res.end(raw) }
    else if (req.method === 'GET') { res.setHeader('content-range', '*/0'); res.end('[]') }
    else { res.writeHead(204); res.end() }
  })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db',
    DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'audit',
    DATA_API_CHANGE_EVENTS_URL: 'https://api.gregale.test/', FAAS_WORKLOAD_IDENTITY_ENDPOINT: 'http://127.0.0.1:9/oidc/token' })
  const relay = createChangeRelay(config, {
    upstreamPort: upstream.address().port, tryAcquire: () => () => {}, setTimer: () => ({}), clearTimer: () => {},
    fetchImpl: async (url, init) => {
      if (String(url).startsWith('http://127.0.0.1:9/')) return Response.json({ token_type: 'Bearer', access_token: 'synthetic-workload-token' })
      if (String(url) === config.changeEvents.status) return new Response(null, { status: 204 })
      published = init.body
      return Response.json({ results: [{ index: 0, status: 'accepted' }] })
    },
  })
  try {
    await relay.cycle()
    assert.ok(published)
    assert.ok(raw.includes('9007199254740993'))
    assert.ok(!published.includes('9007199254740993'))
    assert.ok(published.includes('9007199254740992'))
    assert.ok(!published.includes('0.123456789012345678901'))
    console.log(JSON.stringify({ id: 'DAPI031', observed: 'relay rounded key and row before publication', publishedKey: JSON.parse(published).events[0].data.key }))
  } finally {
    relay.stop()
    upstream.closeAllConnections()
    await new Promise(resolve => upstream.close(resolve))
  }

  const records = new Map()
  const store = { async get(slot) { return records.get(slot) ?? null }, async compareExchange(slot, expected, next) {
    if ((records.get(slot) ?? null) !== expected) return false
    if (next === null) records.delete(slot); else records.set(slot, next)
    return true
  } }
  const context = { apiURL: 'https://data.example', subject: 'alice', tenant: 'team', contract: 'audit', schemaFingerprint: null }
  const journal = createMutationJournal({ store, context, parser: { parse: value => value } })
  await journal.prepare('control', { ...context, key: createReplayKeyV2(), amount: '9007199254740993' })
  await assert.rejects(journal.prepare('exact', { ...context, key: createReplayKeyV2(), amount: new ExactNumber('9007199254740993') }),
    error => error.code === 'invalid_operation')
  assert.equal(records.has('exact'), false)
  console.log(JSON.stringify({ id: 'DAPI035', observed: 'ExactNumber payload rejected by durable journal', code: 'invalid_operation' }))

  const targets = []
  for (const suffix of ['', '?', '#']) {
    const client = createDataClient({ url: 'https://data.example/' + suffix, accessToken: 'synthetic-token',
      fetch: async url => { targets.push({ suffix, url: String(url), path: new URL(url).pathname }); return Response.json([]) } })
    await client.from('notes').select('id').retry(false)
  }
  assert.equal(targets[0].path, '/rest/v1/notes')
  assert.equal(targets[1].path, '/')
  assert.equal(targets[2].path, '/')
  console.log(JSON.stringify({ id: 'DAPI036', observed: 'bare delimiters accepted and REST path lost', targets }))

  assert.equal((await progressUpload(() => {})).name, 'file')
  const child = spawnSync(process.execPath, ['--unhandled-rejections=strict', fileURLToPath(import.meta.url), '--progress-child'], { encoding: 'utf8', timeout: 10000 })
  assert.equal(child.status, 1)
  assert.match(child.stderr, /progress-observer-failed/)
  console.log(JSON.stringify({ id: 'DAPI037', observed: 'async progress rejection escapes upload promise and terminates Node child', exitStatus: child.status }))
}
