import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { readFile, writeFile, mkdir, mkdtemp, symlink, rm } from 'node:fs/promises'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
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

test('idempotent RPC persists one immutable result across retries and isolates keys by identity', { skip: !enabled, timeout: 60000 }, async t => {
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
  await owner.query(await readFile(new URL('../../examples/data-api-idempotency/migrations/0001_orders.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../examples/data-api-idempotency/migrations/0002_order_receipt_lookup.sql', import.meta.url), 'utf8'))
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.orders TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}";
    GRANT EXECUTE ON FUNCTION api.create_order_once(uuid,integer),api.read_order_receipt(uuid,integer) TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const typed = await mkdtemp(join(tmpdir(), 'optimistic-client-'))
  t.after(() => rm(typed, { recursive: true, force: true }))
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), generate(snapshot))
  await writeFile(join(typed, 'client.ts'), await readFile(new URL('../../examples/data-api-idempotency/client.ts', import.meta.url), 'utf8'))
  await command(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname, ['--strict','--target','ES2022','--module','NodeNext','--outDir','dist','client.ts'], { cwd: typed, env: process.env })
  const example = await import(pathToFileURL(join(typed, 'dist/client.js')))
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
  const { createDataClient, dataRequest, reconcileMutation } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const otherTenant = createDataClient({ url, accessToken: await sign('alice', 'team-b') }).schema('api')


  const key = randomUUID()
  const create = (db, total, requestKey = key) => dataRequest(db.rpc('create_order_once', { request_key: requestKey, order_total: total }), { timeoutMs: 5000 })
  const lookup = (db, total, requestKey = key) => dataRequest(db.rpc('read_order_receipt', { request_key: requestKey, order_total: total }), { timeoutMs: 2000 })
  const match = (total, requestKey = key) => row => row.owner === 'alice' && row.tenant === 'team-a' && row.request_key === requestKey && row.total === total
  const retries = await Promise.all(Array.from({ length: 6 }, () => create(alice, 42)))
  for (const result of retries) { assert.equal(result.error, null); assert.deepEqual(result.data, retries[0].data) }
  const receipt = retries[0].data[0]
  assert.equal(receipt.total,42)
  assert.deepEqual(reconcileMutation(await lookup(alice,42),match(42)),{outcome:'confirmed',row:receipt,status:200})
  assert.equal(reconcileMutation(await lookup(alice,43),match(43)).outcome,'payload_conflict')
  assert.equal(reconcileMutation(await lookup(alice,42,randomUUID()),match(42)).reason,'not_found')
  assert.deepEqual((await lookup(otherTenant,43)).data,[],'another tenant cannot infer a hidden receipt or its payload')
  assert.equal((await alice.from('orders').select('id')).data.length,1)
  const mismatch = await create(alice,43)
  assert.equal(mismatch.status,409); assert.equal(mismatch.error.code,'PT409'); assert.equal(mismatch.error.message,'idempotency_payload_conflict')
  assert.deepEqual((await create(alice,42)).data,[receipt])
  const hidden = await otherTenant.from('orders').select('id').eq('id',receipt.id)
  assert.equal(hidden.error,null); assert.deepEqual(hidden.data,[])
  const separate = await create(otherTenant,7)
  assert.equal(separate.error,null); assert.notEqual(separate.data[0].id,receipt.id)
  const bob = createDataClient({url,accessToken:await sign('bob')}).schema('api')
  assert.deepEqual((await lookup(bob,43)).data,[],'another subject cannot infer a hidden receipt or its payload')
  const separateSubject = await create(bob,9)
  assert.equal(separateSubject.error,null); assert.notEqual(separateSubject.data[0].id,receipt.id)
  assert.deepEqual((await bob.from('orders').select('id').eq('id',receipt.id)).data,[])
  for (const operation of [alice.from('orders').update({total:0}).eq('id',receipt.id),alice.from('orders').delete().eq('id',receipt.id)]) {
    const result = await operation.retry(false)
    assert.equal(result.status,409); assert.equal(result.error.message,'idempotency_receipt_immutable')
  }
  // Failed payload validation consumes no key; a corrected request can succeed.
  const invalidKey = randomUUID()
  assert.equal((await create(alice,-1,invalidKey)).status,400)
  assert.equal((await create(alice,1,invalidKey)).error,null)
  // Conflicting concurrent payloads choose one winner, never two orders.
  const raceKey = randomUUID()
  const race = await Promise.all([create(alice,100,raceKey),create(alice,200,raceKey)])
  assert.deepEqual(race.map(r=>r.status).sort(),[200,409])
  const winner = race.find(r=>!r.error).data[0]
  assert.deepEqual((await create(alice,winner.total,raceKey)).data,[winner])
  // A lost response after commit is reconciled without replaying the operation.
  let dropped = false
  const uncertain = createDataClient({url,accessToken:await sign('alice'),fetch:async (input,init) => {
    const response = await fetch(input,init)
    await response.text(); dropped = true
    throw new Error('response lost after commit')
  }}).schema('api')
  const lostKey = randomUUID()
  const lost = await create(uncertain,55,lostKey)
  assert.ok(dropped); assert.equal(lost.status,0)
  const recovered = reconcileMutation(await lookup(alice,55,lostKey),match(55,lostKey))
  assert.equal(recovered.outcome,'confirmed'); assert.equal(recovered.row.total,55)
  const persisted = await owner.query('SELECT count(*)::integer AS count FROM api.orders WHERE owner=$1 AND tenant=$2 AND request_key=$3',['alice','team-a',lostKey])
  assert.equal(persisted.rows[0].count,1)
  const operation={key:lostKey,total:55,subject:'alice',tenant:'team-a'},token=await sign('alice')
  assert.deepEqual(await example.reconcileOrder(url,token,operation),recovered)
  assert.equal((await example.reconcileOrder(url,token,{...operation,total:56})).outcome,'payload_conflict')
  assert.equal((await example.reconcileOrder(url,token,{...operation,tenant:'team-b'})).reason,'invalid_receipt')
  const direct = new pg.Client({connectionString:loginURL.toString()});await direct.connect()
  try {
    await direct.query('BEGIN READ ONLY')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice',tenant:'team-a'})])
    const readOnly=(await direct.query('SELECT * FROM api.read_order_receipt($1::uuid,$2)',[lostKey,55])).rows[0]
    assert.equal(String(readOnly.id),String(recovered.row.id));assert.equal(readOnly.total,55);assert.equal(readOnly.owner,'alice');assert.equal(readOnly.tenant,'team-a')
    await direct.query('ROLLBACK')
    await direct.query('BEGIN')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true)",[JSON.stringify({sub:'alice',tenant:'team-a'})])
    const inFlightKey=randomUUID()
    const writing=await direct.query('SELECT * FROM api.create_order_once($1::uuid,$2)',[inFlightKey,123])
    assert.equal(reconcileMutation(await lookup(alice,123,inFlightKey),match(123,inFlightKey)).reason,'not_found')
    await direct.query('COMMIT')
    const finished=reconcileMutation(await lookup(alice,123,inFlightKey),match(123,inFlightKey))
    assert.equal(finished.outcome,'confirmed');assert.equal(String(finished.row.id),String(writing.rows[0].id))
  } finally {await direct.query('ROLLBACK');await direct.end()}
})
