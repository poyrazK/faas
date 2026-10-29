import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

// A VM snapshot must not hold the engine's SQL sessions: after a restore they
// are dead. before_checkpoint stops the engine, after_restore starts it again
// and the first request after the restore waits for it instead of failing.

async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  await new Promise(resolve => server.close(resolve))
  return port
}

test('a checkpoint leaves no SQL session and the first request after a restore succeeds', { skip: !enabled, timeout: 90000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime))
  const { default: pg } = await import(pathToFileURL(require.resolve('pg')))
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const { createEngine, lifecyclePaths } = await import(new URL('engine.mjs', runtime))

  const suffix = `${process.pid}_${Date.now()}`, database = `lifecycle_${suffix}`, role = `lifecycle_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let engine, gateway
  t.after(async () => {
    gateway?.close()
    await engine?.stop()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  const owner = new pg.Client({ connectionString: ownerURL.toString() })
  await owner.connect()
  await owner.query(`CREATE SCHEMA api; CREATE TABLE api.ping (id int); INSERT INTO api.ping VALUES (1); GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT ON api.ping TO "${role}"`)
  await owner.end()
  const sessions = async () => Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename = $1', [role])).rows[0].count)

  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const upstream = await freePort(), ready = await freePort()
  // The same wiring as server.mjs main(), on free ports.
  engine = createEngine({ bin: process.env.DATA_API_POSTGREST_BIN, env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, readyPort: ready,
    onExit: exit => assert.fail(`engine exited on its own: ${JSON.stringify(exit)}`) })
  config.lifecycle = { ready: waitMs => engine.ready(waitMs), beforeCheckpoint: () => engine.stop(), afterRestore: () => engine.start() }
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const token = await new SignJWT({ sub: 'alice' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${gateway.address().port}`
  const read = () => fetch(url + '/rest/v1/ping', { headers: { Authorization: `Bearer ${token}` } })
  const hook = (path, header) => fetch(url + path, { method: 'POST', headers: { [header]: '1' } })

  engine.start()
  const first = await read()
  assert.deepEqual([first.status, await first.json()], [200, [{ id: 1 }]], 'the first request waited for the engine')
  assert.ok(await sessions() > 0, 'the engine holds a SQL session while serving')

  assert.equal((await hook(lifecyclePaths.beforeCheckpoint, 'x-faas-before-checkpoint')).status, 204)
  for (let i = 0; i < 50 && await sessions() > 0; i++) await new Promise(resolve => setTimeout(resolve, 100))
  assert.equal(await sessions(), 0, 'nothing for a snapshot to capture')

  assert.equal((await hook(lifecyclePaths.afterRestore, 'x-faas-after-restore')).status, 204)
  const restored = await read()
  assert.deepEqual([restored.status, await restored.json()], [200, [{ id: 1 }]], 'the first request after the restore succeeds')
})
