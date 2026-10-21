import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { createHash } from 'node:crypto'
import { createServer } from '../server.mjs'
import { runtimeConfig, storageConfig, storageBindings, limits } from '../config.mjs'
import { storageRoute, storageContentType, presign, signHeaders, objectURL, unsafeStorageTable } from '../storage.mjs'

// File storage (ADR-20261021101510).

const baseEnv = { DATABASE_URL: 'postgres://restricted:private-password@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_ALLOWED_ORIGINS: 'https://app.example' }
const binding = name => ({ [`GREGALE_S3_${name}_ENDPOINT`]: 'https://s3.example', [`GREGALE_S3_${name}_REGION`]: 'us-east-1', [`GREGALE_S3_${name}_BUCKET`]: `real-${name.toLowerCase()}`, [`GREGALE_S3_${name}_ACCESS_KEY_ID`]: 'AKID', [`GREGALE_S3_${name}_SECRET_ACCESS_KEY`]: 'secret' })
const claimsOf = authorization => JSON.parse(Buffer.from(authorization.split(' ')[1].split('.')[1], 'base64url').toString())
const keyA = 'gdsk_' + 'A'.repeat(43), keyB = 'gdsk_' + 'B'.repeat(43)
const hash = key => createHash('sha256').update(key).digest('hex')

test('storage turns on with DATA_API_STORAGE=on, serves bound buckets and bounds object sizes', () => {
  assert.equal(storageConfig(baseEnv, ['api']), null)
  assert.throws(() => storageConfig({ DATA_API_STORAGE: 'on' }, ['api']), /bucket bound/)
  assert.throws(() => storageConfig({ DATA_API_STORAGE: 'yes', ...binding('UPLOADS') }, ['api']), /Invalid DATA_API_STORAGE/)
  assert.throws(() => storageConfig({ DATA_API_STORAGE_MAX_OBJECT_BYTES: '10' }, ['api']), /needs DATA_API_STORAGE/)
  for (const bad of ['0', '-1', '1.5', '01', String(limits.storageObjectMaxBytes + 1)]) {
    assert.throws(() => storageConfig({ DATA_API_STORAGE: 'on', DATA_API_STORAGE_MAX_OBJECT_BYTES: bad, ...binding('UPLOADS') }, ['api']), /MAX_OBJECT_BYTES/, bad)
  }
  const storage = storageConfig({ DATA_API_STORAGE: 'on', ...binding('UPLOADS'), ...binding('MY_FILES') }, ['catalog', 'api'])
  assert.deepEqual([...storage.buckets.keys()].sort(), ['my_files', 'uploads'])
  assert.equal(storage.maxObjectBytes, 25 << 20)
  assert.equal(storage.schema, 'catalog', 'the first exposed schema holds storage_objects')
  assert.equal(storageBindings({ ...binding('PLAIN'), GREGALE_S3_PLAIN_ENDPOINT: 'http://s3.example' }).size, 0, 'remote endpoints must be HTTPS')
  const config = runtimeConfig({ ...baseEnv, DATA_API_STORAGE: 'on', ...binding('UPLOADS') })
  assert.equal(config.postgrestEnv.PGRST_DB_TX_END, 'commit-allow-override')
  assert.equal(runtimeConfig(baseEnv).postgrestEnv.PGRST_DB_TX_END, undefined, 'dry runs are enabled only with storage')
})

test('routes name a bound bucket and a clean object name', () => {
  const storage = storageConfig({ DATA_API_STORAGE: 'on', ...binding('UPLOADS') }, ['api'])
  const route = (url, method = 'GET') => storageRoute({ url, method }, storage)
  assert.deepEqual(route('/storage/v1/object/uploads/alice/2026/report%20(final).pdf', 'PUT'), { action: 'upload', bucket: 'uploads', name: 'alice/2026/report (final).pdf', write: true })
  assert.equal(route('/storage/v1/object/uploads/a.png', 'DELETE').action, 'remove')
  assert.equal(route('/storage/v1/object/uploads/a.png', 'HEAD').action, 'head')
  assert.equal(route('/storage/v1/sign/uploads/a.png', 'POST').action, 'sign')
  for (const [url, method, code] of [
    ['/storage/v1/object/other/a.png', 'GET', 'storage_bucket_not_found'],
    ['/storage/v1/object/uploads/a/../b', 'GET', 'storage_invalid_name'],
    ['/storage/v1/object/uploads/a//b', 'GET', 'storage_invalid_name'],
    ['/storage/v1/object/uploads/a%2Fb', 'GET', 'storage_invalid_name'],
    ['/storage/v1/object/uploads/a%00b', 'GET', 'storage_invalid_name'],
    ['/storage/v1/object/uploads/a%ZZ', 'GET', 'storage_invalid_name'],
    ['/storage/v1/object/uploads/' + 'x'.repeat(1025), 'GET', 'storage_invalid_name'],
    ['/storage/v1/object/uploads/a.png?download=1', 'GET', 'not_found'],
    ['/storage/v1/object/uploads/a.png', 'POST', 'method_not_allowed'],
    ['/storage/v1/sign/uploads/a.png', 'GET', 'method_not_allowed'],
  ]) assert.equal(route(url, method).code, code, url)
  assert.equal(storageContentType(undefined), 'application/octet-stream')
  assert.equal(storageContentType('image/png'), 'image/png')
  assert.equal(storageContentType('text/plain; charset=utf-8'), 'text/plain; charset=utf-8')
  for (const bad of ['png', 'image/', 'a/b\nc', 'x/' + 'y'.repeat(300)]) assert.equal(storageContentType(bad), null, bad)
})

test('SigV4 signing matches the AWS S3 examples', () => {
  const credentials = { region: 'us-east-1', accessKeyId: 'AKIAIOSFODNN7EXAMPLE', secretAccessKey: 'wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY', now: new Date('2013-05-24T00:00:00Z') }
  const url = new URL('https://examplebucket.s3.amazonaws.com/test.txt')
  assert.equal(presign({ method: 'GET', url, expires: 86400, ...credentials }),
    'https://examplebucket.s3.amazonaws.com/test.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request&X-Amz-Date=20130524T000000Z&X-Amz-Expires=86400&X-Amz-SignedHeaders=host&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404')
  const signed = signHeaders({ method: 'GET', url, headers: { Range: 'bytes=0-9' }, payloadHash: 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', ...credentials })
  assert.equal(signed.authorization, 'AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41')
  const path = { endpoint: new URL('https://s3.example/base/'), bucket: 'real-uploads', virtualHosted: false }
  assert.equal(objectURL(path, 'alice/a b+(1).png').href, 'https://s3.example/base/real-uploads/alice/a%20b%2B%281%29.png')
  assert.equal(objectURL({ ...path, virtualHosted: true }, 'a.png').href, 'https://real-uploads.s3.example/a.png')
})

test('startup refuses a storage table without RLS or its guard trigger', () => {
  assert.match(unsafeStorageTable(undefined), /missing/)
  assert.match(unsafeStorageTable({ kind: 'v', rls: true, guarded: true }), /not a table/)
  assert.match(unsafeStorageTable({ kind: 'r', rls: false, guarded: true }), /row-level security/)
  assert.match(unsafeStorageTable({ kind: 'r', rls: true, guarded: false }), /guard trigger/)
  assert.equal(unsafeStorageTable({ kind: 'r', rls: true, guarded: true }), null)
})

// fakeS3 keeps objects in memory and checks that PUTs sign their length.
async function fakeS3(t) {
  const state = { objects: new Map(), requests: [], hold: null }
  const server = http.createServer((req, res) => {
    const auth = req.headers.authorization ?? ''
    const signedHeaders = /SignedHeaders=([^,]+)/.exec(auth)?.[1].split(';') ?? []
    state.requests.push({ method: req.method, path: req.url, signedHeaders })
    if (!auth.startsWith('AWS4-HMAC-SHA256 Credential=AKID/')) { res.writeHead(403); return res.end() }
    if (req.method === 'PUT') {
      if (!signedHeaders.includes('content-length') || !signedHeaders.includes('content-type')) { res.writeHead(403); return res.end() }
      state.onPut?.()
      const chunks = []
      req.on('data', chunk => chunks.push(chunk))
      req.on('end', () => {
        const body = Buffer.concat(chunks)
        if (body.length !== Number(req.headers['content-length'])) { res.writeHead(400); return res.end() }
        state.objects.set(req.url, { body, type: req.headers['content-type'] })
        res.writeHead(200, { ETag: '"etag"' })
        res.end()
      })
      return
    }
    const object = state.objects.get(req.url)
    if (req.method === 'DELETE') { state.objects.delete(req.url); res.writeHead(204); return res.end() }
    if (!object) { res.writeHead(404); return res.end() }
    const range = /^bytes=(\d+)-(\d+)$/.exec(req.headers.range ?? '')
    if (range) {
      const part = object.body.subarray(Number(range[1]), Number(range[2]) + 1)
      res.writeHead(206, { 'Content-Length': part.length, 'Content-Range': `bytes ${range[1]}-${range[2]}/${object.body.length}`, ETag: '"etag"' })
      return res.end(part)
    }
    res.writeHead(200, { 'Content-Length': object.body.length, ETag: '"etag"', 'Content-Type': 'application/should-not-leak' })
    res.end(object.body)
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  t.after(() => { server.closeAllConnections(); return new Promise(resolve => server.close(resolve)) })
  return { state, port: server.address().port }
}

// fakePostgREST models storage_objects: the guard trigger refuses writes
// without the gateway's gregale_storage claim, and the policy lets a user own
// files in "uploads" and everyone read "avatars".
function fakePostgREST(state) {
  const visible = (row, claims) => row.bucket === 'avatars' || (claims.sub && row.owner === claims.sub)
  const filter = url => {
    const params = new URL(url, 'http://x').searchParams
    const value = key => JSON.parse(params.get(key).slice(3))
    return [value('bucket'), value('name')]
  }
  return http.createServer((req, res) => {
    const chunks = []
    req.on('data', chunk => chunks.push(chunk))
    req.on('end', () => {
      const claims = claimsOf(req.headers.authorization)
      const prefer = req.headers.prefer ?? ''
      state.calls.push({ method: req.method, path: req.url, prefer, claims, profile: req.headers['content-profile'] ?? req.headers['accept-profile'] })
      const json = (status, body) => { res.writeHead(status, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(body)) }
      if (!req.url.startsWith('/storage_objects?')) return json(200, [])
      if (req.method !== 'GET' && claims.gregale_storage !== 'write') return json(403, { code: '42501', message: 'storage objects change only through the Data API storage endpoints' })
      const rollback = prefer.includes('tx=rollback')
      if (req.method === 'POST') {
        const body = JSON.parse(Buffer.concat(chunks).toString())
        const key = `${body.bucket}/${body.name}`
        const existing = state.rows.get(key)
        const row = { ...body, owner: existing?.owner ?? claims.sub ?? null, created_at: 'now', updated_at: 'now' }
        if (body.bucket !== 'uploads' || !claims.sub || row.owner !== claims.sub || body.size > 64) return json(403, { code: '42501', message: 'new row violates row-level security policy' })
        if (!rollback) state.rows.set(key, row)
        return json(201, [row])
      }
      const [bucket, name] = filter(req.url)
      const row = state.rows.get(`${bucket}/${name}`)
      const found = row && visible(row, claims) && (req.method === 'GET' || row.owner === claims.sub) ? [row] : []
      if (req.method === 'DELETE' && found.length && !rollback) state.rows.delete(`${bucket}/${name}`)
      return json(200, found)
    })
  })
}

async function fixture(t, env = {}) {
  const s3 = await fakeS3(t)
  const s3Env = name => ({ [`GREGALE_S3_${name}_ENDPOINT`]: `http://127.0.0.1:${s3.port}`, [`GREGALE_S3_${name}_REGION`]: 'us-east-1', [`GREGALE_S3_${name}_BUCKET`]: `real-${name.toLowerCase()}`, [`GREGALE_S3_${name}_ACCESS_KEY_ID`]: 'AKID', [`GREGALE_S3_${name}_SECRET_ACCESS_KEY`]: 'secret' })
  const config = runtimeConfig({ ...baseEnv, DATA_API_STORAGE: 'on', DATA_API_STORAGE_MAX_OBJECT_BYTES: '1000', ...s3Env('UPLOADS'), ...s3Env('AVATARS'),
    DATA_API_SERVER_KEY_A_SHA256: hash(keyA), DATA_API_SERVER_KEY_B_SHA256: hash(keyB), DATA_API_SERVER_KEY_B_PERMISSIONS: '{"version":1,"access":"read","relations":["api.notes"]}', ...env })
  config.fingerprint = 'a'.repeat(64)
  config.functions = []
  const state = { calls: [], rows: new Map(), logs: [] }
  const upstream = fakePostgREST(state)
  const ready = http.createServer((_req, res) => res.end('{}'))
  await Promise.all([upstream, ready].map(server => new Promise(resolve => server.listen(0, '127.0.0.1', resolve))))
  const verify = async token => {
    if (token === 'forged') return { sub: 'alice', exp: 9999999999, gregale_storage: 'write' }
    return { sub: token, exp: 9999999999 }
  }
  const gateway = createServer(config, verify, upstream.address().port, ready.address().port, record => state.logs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(async () => {
    for (const server of [gateway, upstream, ready]) server.closeAllConnections()
    await Promise.all([gateway, upstream, ready].map(server => new Promise(resolve => server.close(resolve))))
  })
  const open = (path, { method = 'GET', headers = {} } = {}) => http.request({ host: '127.0.0.1', port: gateway.address().port, path, method, headers })
  const send = (path, { method = 'GET', headers = {}, body } = {}) => new Promise((resolve, reject) => {
    const client = open(path, { method, headers: body === undefined ? headers : { 'content-length': Buffer.byteLength(body), ...headers } })
    client.on('response', res => {
      const chunks = []
      res.on('data', chunk => chunks.push(chunk))
      res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks).toString() }))
    })
    client.on('error', reject)
    client.end(body)
  })
  return { state, s3: s3.state, send, open }
}

const as = user => ({ authorization: `Bearer ${user}` })

test('an upload is authorized as the caller, streamed with its signed length, then committed', async t => {
  const { state, s3, send } = await fixture(t)
  const uploaded = await send('/storage/v1/object/uploads/alice/notes.txt', { method: 'PUT', headers: { ...as('alice'), 'content-type': 'text/plain' }, body: 'hello world' })
  assert.equal(uploaded.status, 200, uploaded.body)
  assert.deepEqual(JSON.parse(uploaded.body), { bucket: 'uploads', name: 'alice/notes.txt', size: 11, content_type: 'text/plain', owner: 'alice', created_at: 'now', updated_at: 'now' })
  const [dry, commit] = state.calls
  assert.match(dry.prefer, /tx=rollback/)
  assert.doesNotMatch(commit.prefer, /tx=rollback/)
  for (const call of [dry, commit]) {
    assert.equal(call.method, 'POST')
    assert.equal(call.profile, 'api')
    assert.equal(call.claims.gregale_storage, 'write')
    assert.equal(call.claims.sub, 'alice')
    assert.equal(call.claims.gregale_access, 'user')
  }
  assert.equal(s3.objects.get('/real-uploads/alice/notes.txt').body.toString(), 'hello world')
  assert.equal(s3.objects.get('/real-uploads/alice/notes.txt').type, 'text/plain')
  assert.ok(state.rows.has('uploads/alice/notes.txt'))
})

test('refused uploads never reach the bucket', async t => {
  const { s3, send } = await fixture(t)
  const put = (path, headers, body = 'data') => send(path, { method: 'PUT', headers, body })
  for (const [response, status, code] of [
    [await put('/storage/v1/object/avatars/a.png', as('alice')), 403, 'storage_forbidden'],
    [await put('/storage/v1/object/uploads/big.bin', as('alice'), 'x'.repeat(100)), 403, 'storage_forbidden'],
    [await put('/storage/v1/object/uploads/a.png', {}), 401, 'token_required'],
    [await put('/storage/v1/object/uploads/a.png', { ...as('alice'), 'content-type': 'not a type' }), 400, 'storage_invalid_content_type'],
    [await put('/storage/v1/object/uploads/huge.bin', as('alice'), 'x'.repeat(1001)), 413, 'storage_object_too_large'],
    [await put('/storage/v1/object/uploads/a/../b', as('alice')), 400, 'storage_invalid_name'],
    [await put('/storage/v1/object/photos/a.png', as('alice')), 404, 'storage_bucket_not_found'],
    [await put('/storage/v1/object/uploads/a.png', { ...as(keyB) }), 403, 'server_key_permission_denied'],
  ]) {
    assert.equal(response.status, status, response.body)
    assert.equal(JSON.parse(response.body).code, code)
  }
  const tooBig = await put('/storage/v1/object/uploads/huge.bin', as('alice'), 'x'.repeat(1001))
  assert.deepEqual([JSON.parse(tooBig.body).limit, JSON.parse(tooBig.body).observed], [1000, 1001])
  const chunked = await send('/storage/v1/object/uploads/a.png', { method: 'PUT', headers: { ...as('alice'), 'transfer-encoding': 'chunked' } })
  assert.equal(JSON.parse(chunked.body).code, 'storage_length_required')
  assert.equal(s3.requests.length, 0)
})

test('downloads stream the object as the row describes it, sandboxed, with ranges', async t => {
  const { send } = await fixture(t)
  await send('/storage/v1/object/uploads/alice/page.html', { method: 'PUT', headers: { ...as('alice'), 'content-type': 'text/html' }, body: '<script>x</script>' })
  const whole = await send('/storage/v1/object/uploads/alice/page.html', { headers: as('alice') })
  assert.equal(whole.status, 200)
  assert.equal(whole.body, '<script>x</script>')
  assert.equal(whole.headers['content-type'], 'text/html')
  assert.match(whole.headers['content-security-policy'], /^sandbox/)
  assert.equal(whole.headers['x-content-type-options'], 'nosniff')
  assert.equal(whole.headers['cache-control'], 'private, no-store')
  const part = await send('/storage/v1/object/uploads/alice/page.html', { headers: { ...as('alice'), range: 'bytes=0-7' } })
  assert.equal(part.status, 206)
  assert.equal(part.body, '<script>')
  assert.equal(part.headers['content-range'], 'bytes 0-7/18')
  const head = await send('/storage/v1/object/uploads/alice/page.html', { method: 'HEAD', headers: as('alice') })
  assert.equal(head.headers['content-length'], '18')
  const hidden = await send('/storage/v1/object/uploads/alice/page.html', { headers: as('mallory') })
  assert.equal(hidden.status, 404)
  assert.equal(JSON.parse(hidden.body).code, 'storage_not_found', 'a row the caller may not see looks missing')
})

test('signed links and anonymous reads follow the public-read allowlist and RLS', async t => {
  const { state, send } = await fixture(t)
  await send('/storage/v1/object/uploads/alice/a.png', { method: 'PUT', headers: { ...as('alice'), 'content-type': 'image/png' }, body: 'png' })
  state.rows.set('avatars/alice.png', { bucket: 'avatars', name: 'alice.png', owner: 'alice', size: 3, content_type: 'image/png' })
  const signed = await send('/storage/v1/sign/uploads/alice/a.png', { method: 'POST', headers: as('alice') })
  assert.equal(signed.status, 200)
  const { url, expires_at: expiresAt } = JSON.parse(signed.body)
  assert.match(url, /^http:\/\/127\.0\.0\.1:\d+\/real-uploads\/alice\/a\.png\?X-Amz-Algorithm=AWS4-HMAC-SHA256&.*X-Amz-Expires=60&.*X-Amz-Signature=[a-f0-9]{64}$/)
  assert.ok(Math.abs(Date.parse(expiresAt) - Date.now() - 60000) < 5000)
  assert.equal((await send('/storage/v1/sign/uploads/alice/a.png', { method: 'POST', headers: as('mallory') })).status, 404)
  const unlisted = await send('/storage/v1/object/avatars/alice.png')
  assert.equal(JSON.parse(unlisted.body).code, 'public_read_not_allowed')
  const listed = await fixture(t, { DATA_API_PUBLIC_READ: 'api.storage_objects' })
  listed.state.rows.set('avatars/alice.png', { bucket: 'avatars', name: 'alice.png', owner: 'alice', size: 3, content_type: 'image/png' })
  listed.state.rows.set('uploads/alice/a.png', { bucket: 'uploads', name: 'alice/a.png', owner: 'alice', size: 3, content_type: 'image/png' })
  const visitorSign = await listed.send('/storage/v1/sign/avatars/alice.png', { method: 'POST' })
  assert.equal(visitorSign.status, 200)
  assert.equal(listed.state.calls.at(-1).claims.gregale_access, 'anonymous')
  assert.equal((await listed.send('/storage/v1/sign/uploads/alice/a.png', { method: 'POST' })).status, 404, 'RLS still decides')
  assert.equal((await listed.send('/storage/v1/object/avatars/alice.png', { method: 'DELETE' })).status, 401)
})

test('removal checks the caller, deletes the object, then the row', async t => {
  const { state, s3, send } = await fixture(t)
  await send('/storage/v1/object/uploads/alice/old.txt', { method: 'PUT', headers: as('alice'), body: 'old' })
  assert.equal((await send('/storage/v1/object/uploads/alice/old.txt', { method: 'DELETE', headers: as('mallory') })).status, 404)
  assert.ok(s3.objects.has('/real-uploads/alice/old.txt'))
  const removed = await send('/storage/v1/object/uploads/alice/old.txt', { method: 'DELETE', headers: as('alice') })
  assert.equal(removed.status, 204)
  assert.ok(!s3.objects.has('/real-uploads/alice/old.txt'))
  assert.ok(!state.rows.has('uploads/alice/old.txt'))
  const deletes = state.calls.filter(call => call.method === 'DELETE')
  assert.deepEqual(deletes.map(call => /tx=rollback/.test(call.prefer)), [true, true, false], 'mallory and alice dry-run; only alice commits')
})

test('no token can claim storage writes for itself', async t => {
  const { state, send } = await fixture(t)
  await send('/rest/v1/notes', { headers: as('forged') })
  assert.equal(state.calls[0].claims.gregale_storage, undefined, 'REST writes stay behind the guard')
  await send('/storage/v1/object/uploads/alice/a.txt', { headers: as('forged') })
  assert.equal(state.calls[1].claims.gregale_storage, undefined, 'downloads read without the write claim')
})

test('read-only mode and read-only keys may only download', async t => {
  const { send } = await fixture(t, { DATA_API_READ_ONLY: 'true' })
  const upload = await send('/storage/v1/object/uploads/a.txt', { method: 'PUT', headers: as('alice'), body: 'x' })
  assert.equal(JSON.parse(upload.body).code, 'read_only_identity')
  assert.equal((await send('/storage/v1/object/uploads/a.txt', { method: 'DELETE', headers: as(keyA) })).status, 403)
  assert.equal((await send('/storage/v1/object/uploads/a.txt', { headers: as('alice') })).status, 404, 'downloads still run')
})

test('transfers release the SQL slot and are bounded per instance', async t => {
  const { s3, send, open } = await fixture(t, { DATA_API_STORAGE_MAX_OBJECT_BYTES: '100000' })
  const started = []
  let putStarted
  s3.onPut = () => putStarted?.()
  // Each upload sends half its body, then waits inside the bucket transfer.
  const slow = []
  for (let i = 0; i < limits.storageTransfers; i++) {
    const request = open(`/storage/v1/object/uploads/alice/slow-${i}.bin`, { method: 'PUT', headers: { ...as('alice'), 'content-length': '10' } })
    request.on('error', () => {})
    const reached = new Promise(resolve => { putStarted = resolve })
    request.write('12345')
    await reached
    started.push(request)
    slow.push(new Promise(resolve => request.on('response', res => { res.resume(); res.on('end', () => resolve(res.statusCode)) })))
  }
  const rest = await send('/rest/v1/notes', { headers: as('alice') })
  assert.equal(rest.status, 200, 'REST is served while every transfer slot is busy')
  const busy = await send('/storage/v1/object/uploads/alice/one-more.bin', { method: 'PUT', headers: as('alice'), body: 'x' })
  assert.equal(busy.status, 503)
  assert.equal(JSON.parse(busy.body).code, 'storage_busy')
  for (const request of started) request.end('67890')
  assert.deepEqual(await Promise.all(slow), Array(limits.storageTransfers).fill(200))
})

test('preflights allow PUT only when storage is on', async t => {
  const { send } = await fixture(t)
  const preflight = await send('/storage/v1/object/uploads/a.png', { method: 'OPTIONS', headers: { origin: 'https://app.example' } })
  assert.match(preflight.headers['access-control-allow-methods'], /PUT/)
})
