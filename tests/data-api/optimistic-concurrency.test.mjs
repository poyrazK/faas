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

test('conditional mutations serialize competing writers and preserve tenant isolation', { skip: !enabled, timeout: 60000 }, async t => {
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
  await owner.query(await readFile(new URL('../../examples/data-api-concurrency/migrations/0001_documents.sql', import.meta.url), 'utf8'))
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.documents TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}"`)
  await owner.query("INSERT INTO api.documents(owner,tenant,body) VALUES ('alice','team-a','original')")
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const typed = await mkdtemp(join(tmpdir(), 'optimistic-client-'))
  t.after(() => rm(typed, { recursive: true, force: true }))
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), generate(snapshot))
  await writeFile(join(typed, 'client.ts'), await readFile(new URL('../../examples/data-api-concurrency/client.ts', import.meta.url), 'utf8'))
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
  const { createDataClient, optimisticMutation } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const otherTenant = createDataClient({ url, accessToken: await sign('alice', 'team-b') }).schema('api')

  const initial = await alice.from('documents').select('id,body,version').single()
  assert.equal(initial.error, null)
  const row = initial.data
  const edit = (db, body, version = row.version) => optimisticMutation(db.from('documents').update({body}).eq('id', row.id).eq('version', version).select('id,body,version'))
  const raced = await Promise.all([edit(alice, 'first'), edit(alice, 'second')])
  assert.deepEqual(raced.map(r => r.outcome).sort(), ['applied','conflict'])
  const winner = raced.find(r => r.outcome === 'applied').row
  assert.equal(winner.version, row.version + 1)
  const hidden = await otherTenant.from('documents').select('id,version').eq('id', row.id)
  assert.equal(hidden.error, null); assert.deepEqual(hidden.data, [])
  assert.equal((await edit(otherTenant, 'forbidden', winner.version)).outcome, 'conflict')
  const hiddenDelete = await optimisticMutation(otherTenant.from('documents').delete().eq('id', row.id).eq('version', winner.version).select('id'))
  assert.equal(hiddenDelete.outcome, 'conflict')
  // Ordinary updates cannot keep or reset a stale version.
  const ordinary = await alice.from('documents').update({body:'ordinary',version:1}).eq('id',row.id).select('version').single()
  assert.equal(ordinary.error, null); assert.equal(ordinary.data.version, winner.version + 1)
  assert.equal((await edit(alice,'stale',winner.version)).outcome,'conflict')
  const current = ordinary.data.version
  const editAndDelete = await Promise.all([
    edit(alice, 'last edit', current),
    optimisticMutation(alice.from('documents').delete().eq('id',row.id).eq('version',current).select('id')),
  ])
  assert.deepEqual(editAndDelete.map(r => r.outcome).sort(), ['applied','conflict'])
  const remaining = await alice.from('documents').select('version').eq('id', row.id)
  assert.equal(remaining.error, null)
  if (remaining.data.length) {
    const removed = await optimisticMutation(alice.from('documents').delete().eq('id',row.id).eq('version',remaining.data[0].version).select('id'))
    assert.equal(removed.outcome,'applied')
  }
  assert.equal((await edit(alice,'deleted',current)).outcome,'conflict')
})
