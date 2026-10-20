import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { readFile, writeFile, mkdir, mkdtemp, symlink, rm } from 'node:fs/promises'
import { spawn, spawnSync } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { join } from 'node:path'
import { randomUUID } from 'node:crypto'
import { tmpdir } from 'node:os'
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

test('transactional outbox survives retries and worker crashes with fenced tenant-scoped leases', { skip: !enabled, timeout: 60000 }, async t => {
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
  await owner.query(await readFile(new URL('../../examples/data-api-outbox/migrations/0001_orders.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../examples/data-api-outbox/migrations/0002_outbox.sql', import.meta.url), 'utf8'))
  await owner.query(await readFile(new URL('../../examples/data-api-outbox/migrations/0003_dead_letters.sql', import.meta.url), 'utf8'))
  await owner.query(`GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.orders, api.outbox TO "${role}";
    GRANT SELECT ON api.outbox_health TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}";
    GRANT EXECUTE ON FUNCTION api.create_order_once(uuid,integer),api.claim_event(uuid),api.finish_event(bigint,uuid,boolean),api.replay_event(bigint,integer) TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const typed = await mkdtemp(join(tmpdir(), 'optimistic-client-'))
  t.after(() => rm(typed, { recursive: true, force: true }))
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), generate(snapshot))
  await writeFile(join(typed, 'client.ts'), await readFile(new URL('../../examples/data-api-outbox/client.ts', import.meta.url), 'utf8'))
  const compiled = spawnSync(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname, ['--outDir','dist','--strict','--target','ES2022','--module','NodeNext','client.ts'], { cwd: typed, env: process.env, encoding: 'utf8' })
  assert.equal(compiled.status, 0, compiled.stdout + compiled.stderr)

  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = (sub, tenant = 'team-a', worker = false) => new SignJWT({ sub, tenant, outbox_worker: worker, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  const { createDataClient, dataRequest } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const otherTenant = createDataClient({ url, accessToken: await sign('alice', 'team-b') }).schema('api')



  const workerToken = await sign('alice','team-a',true)
  const worker = createDataClient({url,accessToken:workerToken}).schema('api')
  const foreignWorker = createDataClient({url,accessToken:await sign('alice','team-b',true)}).schema('api')
  const invoke = (db, name, args) => dataRequest(db.rpc(name,args), {timeoutMs:5000})
  const key = randomUUID()
  const creation = {request_key:key,order_total:42}
  const orders = await Promise.all([invoke(alice,'create_order_once',creation),invoke(alice,'create_order_once',creation)])
  assert.equal(orders[0].error,null); assert.deepEqual(orders[0].data,orders[1].data)
  const events = await alice.from('outbox').select('id,order_id')
  assert.equal(events.error,null); assert.equal(events.data.length,1)
  assert.equal(events.data[0].order_id,orders[0].data[0].id)
  assert.equal((await invoke(alice,'claim_event',{claim_token:randomUUID()})).status,403)
  assert.deepEqual((await invoke(foreignWorker,'claim_event',{claim_token:randomUUID()})).data,[])
  const tokens = [randomUUID(),randomUUID()]
  const claims = await Promise.all(tokens.map(token=>invoke(worker,'claim_event',{claim_token:token})))
  assert.ok(claims.every(r=>!r.error))
  assert.deepEqual(claims.map(r=>r.data.length).sort(),[0,1])
  const leased = claims.find(r=>r.data.length).data[0]
  assert.equal(leased.attempts,1)
  const finish = (db, token, succeeded) => invoke(db,'finish_event',{event_id:leased.id,claim_token:token,succeeded})
  assert.deepEqual((await finish(foreignWorker,leased.lease_token,true)).data,[])
  assert.deepEqual((await finish(worker,randomUUID(),true)).data,[])
  // A worker crash leaves a lease. Advance only its timestamp with owner test credentials.
  await owner.query("UPDATE api.outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1",[leased.id])
  const replacementToken = randomUUID()
  const reclaimed = await invoke(worker,'claim_event',{claim_token:replacementToken})
  assert.equal(reclaimed.error,null); assert.equal(reclaimed.data[0].id,leased.id); assert.equal(reclaimed.data[0].attempts,2)
  assert.deepEqual((await finish(worker,leased.lease_token,true)).data,[], 'old worker cannot acknowledge a replacement lease')
  // Failed delivery clears the lease and delays the next attempt.
  const failed = await finish(worker,replacementToken,false)
  assert.equal(failed.error,null); assert.equal(failed.data[0].lease_token,null)
  assert.ok(Date.parse(failed.data[0].available_at)>Date.now())
  assert.deepEqual((await invoke(worker,'claim_event',{claim_token:randomUUID()})).data,[])
  await owner.query("UPDATE api.outbox SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1",[leased.id])
  const finalToken=randomUUID()
  const retried=await invoke(worker,'claim_event',{claim_token:finalToken})
  assert.equal(retried.data[0].attempts,3)
  // Crash after external delivery but before acknowledgement causes redelivery;
  // consumers deduplicate the same event ID, regardless of the new lease token.
  const deliveries=new Set([retried.data[0].id])
  await owner.query("UPDATE api.outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1",[leased.id])
  const ackToken=randomUUID()
  const duplicate=await invoke(worker,'claim_event',{claim_token:ackToken})
  deliveries.add(duplicate.data[0].id)
  assert.equal(deliveries.size,1)
  assert.equal((await finish(worker,ackToken,true)).data[0].delivered,true)
  assert.deepEqual((await invoke(worker,'claim_event',{claim_token:randomUUID()})).data,[])
  // A transaction rollback removes both the business row and its event.
  await owner.query('BEGIN')
  const rollbackKey=randomUUID()
  await owner.query("INSERT INTO api.orders(owner,tenant,request_key,total) VALUES('alice','team-a',$1,1)",[rollbackKey])
  await owner.query('ROLLBACK')
  assert.equal((await owner.query('SELECT count(*)::integer AS count FROM api.orders WHERE request_key=$1',[rollbackKey])).rows[0].count,0)
  assert.equal((await owner.query('SELECT count(*)::integer AS count FROM api.outbox')).rows[0].count,1)
  const { dispatchOne } = await import(pathToFileURL(join(typed,'dist/client.js')))
  assert.equal(await dispatchOne(url,workerToken,async()=>{throw new Error('idle should not deliver')}),'idle')
  const next=await invoke(alice,'create_order_once',{request_key:randomUUID(),order_total:99})
  assert.equal(next.error,null)
  assert.equal(await dispatchOne(url,workerToken,async()=>{throw new Error('provider unavailable')}),'retry-scheduled')
  await owner.query("UPDATE api.outbox SET available_at=clock_timestamp()-interval '1 second' WHERE NOT delivered")
  const observed=[]
  assert.equal(await dispatchOne(url,workerToken,async(key,order)=>{observed.push({key,order})}),'delivered')
  assert.equal(observed.length,1); assert.equal(observed[0].order.total,99)
  assert.match(observed[0].key,/^order-created:[0-9]+$/)
  assert.equal(await dispatchOne(url,workerToken,async()=>{throw new Error('delivered should not repeat')}),'idle')

  const { replayEvent, queueHealth, setRetryLimit } = await import(pathToFileURL(join(typed,'dist/client.js')))
  const poison=await invoke(alice,'create_order_once',{request_key:randomUUID(),order_total:123})
  assert.equal(poison.error,null)
  const poisonRow=(await worker.from('outbox').select('id,replay_generation').eq('order_id',poison.data[0].id).single()).data
  assert.equal((await setRetryLimit(url,workerToken,poisonRow.id,2)).error,null)
  const health=async db => { const result=await db.from('outbox_health').select(); assert.equal(result.error,null); return result.data[0] }
  assert.deepEqual(await health(worker),{pending:1,leased:0,delivered:2,dead_lettered:0})
  assert.deepEqual(await health(foreignWorker),{pending:0,leased:0,delivered:0,dead_lettered:0})
  assert.equal(await dispatchOne(url,workerToken,async()=>{throw Error('poison')}),'retry-scheduled')
  await owner.query("UPDATE api.outbox SET available_at=clock_timestamp()-interval '1 second' WHERE id=$1",[poisonRow.id])
  assert.equal(await dispatchOne(url,workerToken,async()=>{throw Error('poison')}),'dead-lettered')
  assert.deepEqual(await health(worker),{pending:0,leased:0,delivered:2,dead_lettered:1})
  assert.equal(await dispatchOne(url,workerToken,async()=>{throw Error('must not deliver dead letter')}),'idle')
  assert.equal((await invoke(alice,'replay_event',{event_id:poisonRow.id,expected_generation:0})).status,403)
  assert.deepEqual((await invoke(foreignWorker,'replay_event',{event_id:poisonRow.id,expected_generation:0})).data,[])
  const replays=await Promise.all([replayEvent(url,workerToken,poisonRow.id,0),replayEvent(url,workerToken,poisonRow.id,0)])
  assert.ok(replays.every(r=>!r.error)); assert.deepEqual(replays.map(r=>r.data.length).sort(),[0,1])
  const replayed=replays.find(r=>r.data.length).data[0]
  assert.equal(replayed.id,poisonRow.id); assert.equal(replayed.replay_generation,1)
  assert.equal(replayed.attempts,0); assert.equal(replayed.total_attempts,2)
  assert.deepEqual((await replayEvent(url,workerToken,poisonRow.id,0)).data,[])
  const replayKeys=[]
  assert.equal(await dispatchOne(url,workerToken,async key=>{replayKeys.push(key)}),'delivered')
  assert.deepEqual(replayKeys,[`order-created:${poisonRow.id}`])
  assert.deepEqual((await replayEvent(url,workerToken,poisonRow.id,1)).data,[], 'delivered events cannot be replayed')
  const counts=await queueHealth(url,workerToken)
  assert.equal(counts.error,null); assert.deepEqual(counts.data,[{pending:0,leased:0,delivered:3,dead_lettered:0}])
  // A crash on the last permitted attempt becomes a dead letter on lease expiry.
  const crashOrder=await invoke(alice,'create_order_once',{request_key:randomUUID(),order_total:124})
  const crashRow=(await worker.from('outbox').select('id').eq('order_id',crashOrder.data[0].id).single()).data
  await setRetryLimit(url,workerToken,crashRow.id,1)
  const crashToken=randomUUID()
  const crashLease=await invoke(worker,'claim_event',{claim_token:crashToken})
  assert.equal(crashLease.data[0].id,crashRow.id)
  assert.deepEqual(await health(worker),{pending:0,leased:1,delivered:3,dead_lettered:0})
  assert.deepEqual((await replayEvent(url,workerToken,crashRow.id,0)).data,[], 'active lease cannot be replayed')
  await owner.query("UPDATE api.outbox SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1",[crashRow.id])
  assert.equal((await health(worker)).dead_lettered,1)
  assert.deepEqual((await invoke(worker,'claim_event',{claim_token:randomUUID()})).data,[])
  const crashed=(await worker.from('outbox').select('dead_lettered,lease_token').eq('id',crashRow.id).single()).data
  assert.equal(crashed.dead_lettered,true); assert.equal(crashed.lease_token,null)
  assert.equal((await replayEvent(url,workerToken,crashRow.id,0)).data.length,1)
  assert.deepEqual((await invoke(worker,'finish_event',{event_id:crashRow.id,claim_token:crashToken,succeeded:true})).data,[])
  assert.equal(await dispatchOne(url,workerToken,async()=>{}),'delivered')
  await assert.rejects(setRetryLimit(url,workerToken,crashRow.id,0),/Retry limit/)
  assert.equal((await worker.from('outbox').update({max_attempts:0}).eq('id',crashRow.id)).error.code,'23514')
  const unauthorized=await alice.from('outbox').update({max_attempts:100}).eq('id',crashRow.id).select('id')
  assert.equal(unauthorized.error,null); assert.deepEqual(unauthorized.data,[])

})
