import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { readFile, writeFile, mkdir, mkdtemp, symlink, rm } from 'node:fs/promises'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { command } from './staging/canary.mjs'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

// A probe port is closed before it is used, so the OS can hand the same port
// to the next probe; PostgREST then refuses an admin port equal to its server
// port. Never return a port twice in one test file.
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

test('bulk mutations report isolated partial failures and preserve per-request atomicity', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint, generate } = await import(new URL('types.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `cursor_${suffix}`, role = `cursor_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(r => gateway.close(r)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(await readFile(new URL('../../examples/data-api-bulk/migrations/0001_documents.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../examples/data-api-bulk/migrations/0002_body_validation.sql', import.meta.url), 'utf8'))
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.documents TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}"`)
  await owner.query("INSERT INTO api.documents(owner,tenant,body) VALUES ('alice','team-a','first'),('alice','team-a','second'),('alice','team-a','third'),('alice','team-b','hidden')")
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const typed = await mkdtemp(join(tmpdir(), 'optimistic-client-'))
  t.after(() => rm(typed, { recursive: true, force: true }))
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), generate(snapshot))
  await writeFile(join(typed, 'client.ts'), await readFile(new URL('../../examples/data-api-bulk/client.ts', import.meta.url), 'utf8'))
  await command(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname, ['--noEmit','--strict','--target','ES2022','--module','NodeNext','client.ts'], { cwd: typed, env: process.env })
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = (sub, tenant = 'team-a') => new SignJWT({ sub, tenant, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  const { createDataClient, bulkMutations } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const otherTenant = createDataClient({ url, accessToken: await sign('alice', 'team-b') }).schema('api')


  const visible=await alice.from('documents').select('id,body,version').order('id')
  assert.equal(visible.error,null)
  const [first,second,third]=visible.data
  const hidden=(await owner.query("SELECT id FROM api.documents WHERE tenant='team-b'")).rows[0]
  const edits=[{id:first.id,version:1,body:'updated'},{id:second.id,version:1,body:''},{id:third.id,version:0,body:'stale'},{id:Number(hidden.id),version:1,body:'forbidden'}]
  const progress=[]
  const results=await bulkMutations(edits,edit=>alice.from('documents').update({body:edit.body}).eq('id',edit.id).eq('version',edit.version).select('id,body,version'),{batchSize:2,concurrency:2,onProgress:p=>progress.push(p)})
  assert.deepEqual(results.map(r=>r.outcome),['applied','error','conflict','conflict'])
  assert.equal(results[1].error.code,'23514');assert.equal(progress.at(-1).completed,4)
  const current=await alice.from('documents').select('id,body,version').order('id')
  assert.equal(current.data[0].body,'updated');assert.equal(current.data[0].version,2)
  assert.equal(current.data[1].body,'second');assert.equal(current.data[1].version,1)
  assert.equal(current.data[2].body,'third')
  const foreign=await otherTenant.from('documents').select('body').eq('id',hidden.id)
  assert.deepEqual(foreign.data,[{body:'hidden'}])
  // A single PostgREST array insert remains atomic, unlike a scheduling window.
  const atomic=await alice.from('documents').insert([{owner:'alice',tenant:'team-a',body:'atomic-valid'},{owner:'alice',tenant:'team-a',body:''}]).select('id').retry(false)
  assert.equal(atomic.error.code,'23514')
  assert.deepEqual((await alice.from('documents').select('id').eq('body','atomic-valid')).data,[])
  const imported=await bulkMutations(['imported',''],body=>alice.from('documents').insert({owner:'alice',tenant:'team-a',body}).select('id,body'),{batchSize:2})
  assert.deepEqual(imported.map(r=>r.outcome),['applied','error'])
  assert.equal((await alice.from('documents').select('id').eq('body','imported')).data.length,1)
  const controller=new AbortController()
  const stopped=await bulkMutations([{id:second.id},{id:third.id}],edit=>alice.from('documents').delete().eq('id',edit.id).select('id'),{concurrency:1,batchSize:2,signal:controller.signal,onProgress:()=>controller.abort()})
  assert.deepEqual(stopped.map(r=>r.outcome),['applied','skipped'])
  assert.equal((await alice.from('documents').select('id').eq('id',third.id)).data.length,1)
})
