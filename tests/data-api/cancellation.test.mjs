import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
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

test('cancelled concurrent RPCs release SQL connections within the server statement timeout', { skip: !enabled, timeout: 60000 }, async t => {
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
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=15000`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.orders(subject text NOT NULL,id integer NOT NULL,rank integer,label text,PRIMARY KEY(subject,id));
    ALTER TABLE api.orders ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_orders ON api.orders USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT ON api.orders TO "${role}";
    CREATE FUNCTION api.cancellation_probe() RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS
      'BEGIN PERFORM pg_sleep(60); RETURN 1; END';
    REVOKE EXECUTE ON FUNCTION api.cancellation_probe() FROM PUBLIC;
    GRANT EXECUTE ON FUNCTION api.cancellation_probe() TO "${role}";
    COMMENT ON FUNCTION api.cancellation_probe() IS '@gregale:rpc'`)
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
  const { createDataClient, cursorPage, cursorFrom, dataRequest } = await import('../../sdk/data/dist/index.js')
  const alice = createDataClient({ url, accessToken: await sign('alice') }).schema('api')
  const bob = createDataClient({ url, accessToken: await sign('bob') }).schema('api')
  const controller = new AbortController()
  const secondController = new AbortController()
  const second = dataRequest(alice.rpc('cancellation_probe'), { timeoutMs: 5000, signal: secondController.signal })
  const slow = dataRequest(alice.rpc('cancellation_probe'), { timeoutMs: 5000, signal: controller.signal })
  const active = async () => (await admin.query("SELECT count(*)::integer AS count FROM pg_stat_activity WHERE usename=$1 AND state='active' AND query LIKE '%cancellation_probe%'", [role])).rows[0].count
  let started = false
  for (let i = 0; i < 100; i++) {
    if (await active() === 2) { started = true; break }
    await new Promise(resolve => setTimeout(resolve, 10))
  }
  assert.ok(started, 'RPC must reach PostgreSQL before cancellation')
  const cancelledAt = Date.now()
  controller.abort()
  secondController.abort()
  assert.equal((await slow).error.code, 'request_cancelled')
  assert.equal((await second).error.code, 'request_cancelled')
  for (let i = 0; i < 1600 && await active(); i++) await new Promise(resolve => setTimeout(resolve, 10))
  assert.equal(await active(), 0, 'Server statement timeout must release cancelled SQL connections')


  assert.ok(Date.now() - cancelledAt < 17000, 'SQL connections must be released within the server timeout margin')
  const healthy = await dataRequest(alice.from('orders').select('id'), { timeoutMs: 1000 })
  assert.equal(healthy.error, null)
  assert.equal(healthy.data.length, 5)

})
