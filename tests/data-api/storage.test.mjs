import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { readFile } from 'node:fs/promises'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

// File storage (ADR-20261021101510) against PostgreSQL and the pinned engine:
// the shipped storage.sql, the owner's RLS policies, PostgREST's tx=rollback
// dry runs and the runtime's streaming, with a bucket stand-in.

const issuedPorts = new Set()
async function port() {
  for (;;) {
    const server = net.createServer()
    await new Promise(r => server.listen(0, '127.0.0.1', r))
    const value = server.address().port
    await new Promise(r => server.close(r))
    if (!issuedPorts.has(value)) {
      issuedPorts.add(value)
      return value
    }
  }
}

// bucketServer stores objects by path; any PUT whose body is "fail-me"
// answers 500, as a bucket outage would.
async function bucketServer() {
  const objects = new Map()
  const server = http.createServer((req, res) => {
    const path = req.url.split('?')[0]
    const signed = req.headers.authorization?.startsWith('AWS4-HMAC-SHA256 ') || /[?&]X-Amz-Signature=[a-f0-9]{64}/.test(req.url)
    if (!signed) { res.writeHead(403); return res.end() }
    if (req.method === 'PUT') {
      const chunks = []
      req.on('data', chunk => chunks.push(chunk))
      req.on('end', () => {
        if (Buffer.concat(chunks).toString() === 'fail-me') { res.writeHead(500); return res.end() }
        objects.set(path, Buffer.concat(chunks))
        res.writeHead(200)
        res.end()
      })
      return
    }
    if (req.method === 'DELETE') { objects.delete(path); res.writeHead(204); return res.end() }
    const body = objects.get(path)
    if (!body) { res.writeHead(404); return res.end() }
    res.writeHead(200, { 'Content-Length': body.length })
    res.end(body)
  })
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  return { server, objects, origin: `http://127.0.0.1:${server.address().port}` }
}

test('storage follows RLS through PostgREST: own files, public avatars, guarded rows and rolled-back dry runs', { skip: !enabled, timeout: 90000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime))
  const pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const { checkStorageTable } = await import(new URL('storage.mjs', runtime))
  const { createStorageClient, StorageError } = await import(new URL('../../sdk/data/dist/index.js', import.meta.url))
  const suffix = `${process.pid}_${Date.now()}`, database = `storage_${suffix}`, role = `storage_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  const bucket = await bucketServer()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(r => gateway.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    bucket.server.closeAllConnections(); await new Promise(r => bucket.server.close(r))
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  const sql = await readFile(new URL('../../cmd/gregale/templates/data-api/storage.sql', import.meta.url), 'utf8')
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api; CREATE SCHEMA bare;`)
  await owner.query(sql.replaceAll('{{schema}}', 'api'))
  await owner.query(`CREATE POLICY own_uploads ON api.storage_objects
      USING (bucket = 'uploads' AND owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub')
      WITH CHECK (bucket = 'uploads' AND owner = current_setting('request.jwt.claims', true)::jsonb ->> 'sub' AND size <= 64);
    CREATE POLICY read_avatars ON api.storage_objects FOR SELECT USING (bucket = 'avatars');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT, INSERT, UPDATE, DELETE ON api.storage_objects TO "${role}";
    CREATE TABLE bare.storage_objects (bucket text, name text);
    ALTER TABLE bare.storage_objects ENABLE ROW LEVEL SECURITY;`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  await checkStorageTable(loginURL.toString(), 'api')
  await assert.rejects(checkStorageTable(loginURL.toString(), 'bare'), /guard trigger/)
  await assert.rejects(checkStorageTable(loginURL.toString(), 'public'), /missing/)
  // A table created before object keys refuses storage until its upgrade.
  await owner.query(`CREATE SCHEMA old; ${sql.replaceAll('{{schema}}', 'old')}; ALTER TABLE old.storage_objects DROP COLUMN object_key; GRANT USAGE ON SCHEMA old TO "${role}"`)
  await assert.rejects(checkStorageTable(loginURL.toString(), 'old'), /storage sql --upgrade/)
  await owner.query(`ALTER TABLE old.storage_objects ADD COLUMN IF NOT EXISTS object_key text CHECK (object_key ~ '^_gregale/objects/[0-9a-f]{32}$')`)
  await checkStorageTable(loginURL.toString(), 'old')

  const s3 = name => ({ [`GREGALE_S3_${name}_ENDPOINT`]: bucket.origin, [`GREGALE_S3_${name}_REGION`]: 'us-east-1', [`GREGALE_S3_${name}_BUCKET`]: `real-${name.toLowerCase()}`, [`GREGALE_S3_${name}_ACCESS_KEY_ID`]: 'AKID', [`GREGALE_S3_${name}_SECRET_ACCESS_KEY`]: 'secret' })
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'files',
    DATA_API_STORAGE: 'on', DATA_API_PUBLIC_READ: 'api.storage_objects', ...s3('UPLOADS'), ...s3('AVATARS') })
  const snapshot = await inspect(loginURL.toString(), ['api'])
  config.snapshot = snapshot; config.functions = snapshot.functions
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = sub => new SignJWT({ sub, gregale_storage: 'write' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)

  const alice = await sign('alice'), bob = await sign('bob')
  const files = token => createStorageClient({ url, accessToken: token }).from('uploads')
  const rows = async () => (await owner.query('SELECT bucket, name, owner, size, content_type, created_at, updated_at FROM api.storage_objects ORDER BY bucket, name')).rows
  const stored = async name => `/real-uploads/${(await owner.query('SELECT object_key FROM api.storage_objects WHERE name = $1', [name])).rows[0].object_key}`
  const refusal = promise => promise.then(() => null, error => { assert.ok(error instanceof StorageError, String(error)); return [error.status, error.code] })

  // Alice's token claims gregale_storage itself; the gateway strips it, so only
  // the storage endpoints can write rows.
  const uploaded = await files(alice).upload('alice/hello.txt', 'hello', { contentType: 'text/plain' })
  assert.deepEqual([uploaded.owner, uploaded.size, uploaded.content_type, uploaded.object_key], ['alice', 5, 'text/plain', undefined])
  const firstVersion = await stored('alice/hello.txt')
  assert.match(firstVersion, /^\/real-uploads\/_gregale\/objects\/[0-9a-f]{32}$/)
  assert.equal(bucket.objects.get(firstVersion).toString(), 'hello')
  assert.equal(await (await files(alice).download('alice/hello.txt')).text(), 'hello')
  assert.deepEqual(await refusal(files(bob).download('alice/hello.txt')), [404, 'storage_not_found'])

  const rest = (token, init = {}) => fetch(url + '/rest/v1/storage_objects', { ...init, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', ...init.headers } })
  const forged = await rest(alice, { method: 'POST', body: JSON.stringify({ bucket: 'uploads', name: 'alice/forged.txt', size: 1, content_type: 'text/plain' }) })
  assert.equal(forged.status, 403, 'REST writes stop at the guard trigger')
  assert.deepEqual((await (await rest(alice)).json()).map(r => r.name), ['alice/hello.txt'], 'REST reads follow RLS')
  assert.deepEqual(await (await rest(bob)).json(), [])

  // Refusals and bucket failures leave no row: dry runs really roll back.
  assert.deepEqual(await refusal(files(alice).upload('alice/big.bin', 'x'.repeat(65))), [403, 'storage_forbidden'])
  assert.deepEqual(await refusal(files(bob).upload('alice/hello.txt', 'mine now')), [403, 'storage_forbidden'])
  assert.deepEqual(await refusal(files(alice).upload('alice/fail.txt', 'fail-me')), [502, 'storage_backend_unavailable'])
  assert.deepEqual((await rows()).map(r => r.name), ['alice/hello.txt'])
  assert.deepEqual([...bucket.objects.keys()], [firstVersion], 'refused and failed uploads leave no bytes')

  const first = (await rows())[0]
  const replaced = await files(alice).upload('alice/hello.txt', 'hello again', { contentType: 'text/plain' })
  const second = (await rows())[0]
  assert.deepEqual([replaced.size, second.owner, second.created_at.getTime()], [11, 'alice', first.created_at.getTime()], 'overwrites keep the owner and creation time')
  const secondVersion = await stored('alice/hello.txt')
  assert.notEqual(secondVersion, firstVersion)
  assert.deepEqual([...bucket.objects.keys()], [secondVersion], 'the replaced version is deleted after the commit')

  // Visitors read public avatars only; RLS hides Alice's uploads from them.
  await owner.query(`SELECT set_config('request.jwt.claims', '{"gregale_storage":"write","sub":"alice"}', false)`)
  await owner.query(`INSERT INTO api.storage_objects (bucket, name, size, content_type) VALUES ('avatars', 'alice.png', 3, 'image/png')`)
  bucket.objects.set('/real-avatars/alice.png', Buffer.from('png'))
  const visitor = createStorageClient({ url, anonymous: true })
  assert.equal(await (await visitor.from('avatars').download('alice.png')).text(), 'png')
  assert.deepEqual(await refusal(visitor.from('uploads').download('alice/hello.txt')), [404, 'storage_not_found'])
  assert.deepEqual(await refusal(visitor.from('uploads').upload('anon.txt', 'x')), [401, 'token_required'])

  const link = await files(alice).signedUrl('alice/hello.txt')
  assert.ok(link.url.startsWith(`${bucket.origin}${secondVersion}?`))
  assert.equal(await (await fetch(link.url)).text(), 'hello again')

  assert.deepEqual(await refusal(files(bob).remove('alice/hello.txt')), [404, 'storage_not_found'])
  await files(alice).remove('alice/hello.txt')
  assert.ok(!bucket.objects.has(secondVersion))
  assert.deepEqual((await rows()).map(r => `${r.bucket}/${r.name}`), ['avatars/alice.png'])
})
