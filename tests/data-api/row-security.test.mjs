import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)

// ADR 20261026140223: the Data API login may read and write every relation in
// its schema, so signed-in requests reach only relations whose rows PostgreSQL
// decides (RLS tables, security-invoker views) or that the owner exempts.

async function freePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address()
  await new Promise(resolve => server.close(resolve))
  return port
}

test('signed-in requests need row-level security on every relation they read or write', { skip: !enabled, timeout: 90000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime))
  const { default: pg } = await import(pathToFileURL(require.resolve('pg')))
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const { createEngine } = await import(new URL('engine.mjs', runtime))
  const { loadRowSecurity } = await import(new URL('access.mjs', runtime))

  const suffix = `${process.pid}_${Date.now()}`, database = `row_security_${suffix}`, role = `row_security_login_${suffix}`
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
  await owner.query(`CREATE SCHEMA api;
    CREATE TABLE api.notes (id int PRIMARY KEY, subject text NOT NULL);
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own ON api.notes USING (subject = current_setting('request.jwt.claims', true)::json->>'sub');
    CREATE TABLE api.secrets (id int PRIMARY KEY, note_id int REFERENCES api.notes, body text);
    CREATE TABLE api.countries (code text PRIMARY KEY);
    CREATE VIEW api.own_notes WITH (security_invoker = true) AS SELECT id FROM api.notes;
    CREATE VIEW api.all_notes AS SELECT id, subject FROM api.notes;
    CREATE MATERIALIZED VIEW api.note_counts AS SELECT count(*) AS n FROM api.notes;
    INSERT INTO api.notes VALUES (1, 'alice'), (2, 'bob'); INSERT INTO api.secrets VALUES (1, 1, 'x'); INSERT INTO api.countries VALUES ('TR');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA api TO "${role}"`)
  await owner.end()

  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes',
    DATA_API_ALLOW_WITHOUT_RLS: 'api.countries' })
  config.rowSecurity = await loadRowSecurity(loginURL.toString(), config.schemas)
  assert.deepEqual(Object.fromEntries(config.rowSecurity), { 'api.all_notes': false, 'api.countries': false, 'api.note_counts': false, 'api.notes': true, 'api.own_notes': true, 'api.secrets': false })

  const upstream = await freePort(), ready = await freePort()
  engine = createEngine({ bin: process.env.DATA_API_POSTGREST_BIN, env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, readyPort: ready,
    onExit: exit => assert.fail(`engine exited on its own: ${JSON.stringify(exit)}`) })
  config.lifecycle = { ready: waitMs => engine.ready(waitMs), beforeCheckpoint: () => engine.stop(), afterRestore: () => engine.start() }
  engine.start()
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const token = await new SignJWT({ sub: 'alice' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const request = async (path, init = {}) => {
    const response = await fetch(`http://127.0.0.1:${gateway.address().port}${path}`, { ...init, headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json', ...init.headers } })
    const body = await response.json().catch(() => null)
    return [response.status, response.ok ? body : body?.code]
  }

  assert.deepEqual(await request('/rest/v1/notes?select=id'), [200, [{ id: 1 }]], 'RLS decides the rows')
  assert.deepEqual(await request('/rest/v1/own_notes'), [200, [{ id: 1 }]], 'a security-invoker view')
  assert.deepEqual(await request('/rest/v1/secrets'), [403, 'rls_required'])
  assert.deepEqual(await request('/rest/v1/secrets', { method: 'POST', body: '{"id":2,"body":"y"}' }), [403, 'rls_required'], 'no write either')
  assert.deepEqual(await request('/rest/v1/all_notes'), [403, 'rls_required'], 'a view running as its owner bypasses RLS')
  assert.deepEqual(await request('/rest/v1/note_counts'), [403, 'rls_required'], 'materialized views cannot have RLS')
  assert.deepEqual(await request('/rest/v1/notes?select=id,secrets(body)'), [403, 'rls_required'], 'an embedding reads the relation too')
  assert.deepEqual(await request('/rest/v1/countries'), [200, [{ code: 'TR' }]], 'an owner exemption')
  assert.deepEqual(await request('/rest/v1/not_there'), [404, 'not_found'])
})
