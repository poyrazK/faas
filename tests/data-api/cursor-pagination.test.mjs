import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

async function port() {
  const server = net.createServer()
  await new Promise(r => server.listen(0, '127.0.0.1', r))
  const value = server.address().port
  await new Promise(r => server.close(r))
  return value
}

test('cursor pagination respects composite order, nulls, changing data and RLS', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
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
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.orders(subject text NOT NULL,id integer NOT NULL,rank integer,label text,PRIMARY KEY(subject,id));
    ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_orders ON api.orders USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT ON api.orders TO "${role}"`)
  for (const [subject,id,rank,label] of [['alice',1,1,'a,"\\z'],['alice',2,1,'a,"\\z'],['alice',3,2,'b'],['alice',4,null,'c'],['alice',5,null,'d'],['bob',1,0,'private']]) {
    await owner.query('INSERT INTO api.orders VALUES($1,$2,$3,$4)',[subject,id,rank,label])
  }
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', b => { logs += b }); child.stderr.on('data', b => { logs += b })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = sub => new SignJWT({ sub, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(r => gateway.listen(0, '127.0.0.1', r))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(r => setTimeout(r, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  const { createDataClient, cursorPage, cursorFrom } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const bob = createDataClient({ url, accessToken: await sign('bob') }).schema('api')
  for (const ascending of [true,false]) for (const nullsFirst of [true,false]) {
    const order = [{ column:'rank',ascending,nullsFirst }, { column:'id',ascending,unique:true }]
    const expected = await alice.from('orders').select('id,rank').order('rank',{ascending,nullsFirst}).order('id',{ascending})
    assert.equal(expected.error,null)
    let after, ids=[]
    for (let i=0;i<6;i++) {
      const page=await cursorPage(alice.from('orders').select('id,rank'),{order,size:2,after})
      assert.equal(page.error,null)
      if (!page.data.length) break
      ids.push(...page.data.map(row=>row.id)); after=cursorFrom(page.data.at(-1),order)
    }
    assert.deepEqual(ids,expected.data.map(row=>row.id))
  }
  const textOrder=[{column:'label'},{column:'id',unique:true}]
  const first=await cursorPage(alice.from('orders').select('id,label'),{order:textOrder,size:1})
  const escaped=await cursorPage(alice.from('orders').select('id,label'),{order:textOrder,size:1,after:cursorFrom(first.data[0],textOrder)})
  assert.equal(escaped.error,null); assert.equal(escaped.data[0].id,2)
  const order=[{column:'rank'},{column:'id',unique:true}]
  const page=await cursorPage(alice.from('orders').select('id,rank'),{order,size:2})
  const after=cursorFrom(page.data.at(-1),order)
  await owner.query("DELETE FROM api.orders WHERE subject='alice' AND id=2; INSERT INTO api.orders VALUES('alice',6,0,'before'),('alice',7,3,'after')")
  const continued=await cursorPage(alice.from('orders').select('id,rank'),{order,size:10,after})
  assert.equal(continued.error,null); assert.deepEqual(continued.data.map(row=>row.id),[3,7,4,5])
  const other=await cursorPage(bob.from('orders').select('id,rank'),{order,size:10,after})
  assert.equal(other.error,null); assert.deepEqual(other.data,[])
})
