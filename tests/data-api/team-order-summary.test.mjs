import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { mkdtemp, mkdir, readFile, readdir, writeFile, symlink, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { pathToFileURL } from 'node:url'
import { command } from './staging/canary.mjs'
import { verifyTeamOrderSummary } from './team-order-summary.mjs'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function probePort() {
  const server = net.createServer(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port; await new Promise(resolve => server.close(resolve)); return value
}
// A probe port is closed before it is used, so the OS can hand the same port
// to the next probe; PostgREST then refuses an admin port equal to its server
// port. Never return a port twice in one test file.
const issuedPorts = new Set()
async function port() {
  for (;;) {
    const value = await probePort()
    if (!issuedPorts.has(value)) {
      issuedPorts.add(value)
      return value
    }
  }
}

test('team order summaries retain RLS, exact large totals and the serving query budget', { skip: !enabled, timeout: 120000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { inspect, generate, fingerprint } = await import(new URL('types.mjs', runtime))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `summary_${suffix}`, schemaOwner = `summary_owner_${suffix}`, role = `summary_api_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL }); await admin.connect()
  const typed = await mkdtemp(join(tmpdir(), 'gregale-team-summary-client-'))
  let owner, child, gateway, direct
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await direct?.end(); await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"; DROP ROLE IF EXISTS "${schemaOwner}"`)
    await admin.end(); await rm(typed, { recursive: true, force: true })
  })
  await admin.query(`CREATE ROLE "${schemaOwner}" NOLOGIN NOBYPASSRLS;
    CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    ALTER ROLE "${role}" SET statement_timeout=15000`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = '/' + database
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON DATABASE "${database}" FROM PUBLIC;
    GRANT CONNECT ON DATABASE "${database}" TO "${schemaOwner}", "${role}";
    GRANT CREATE ON DATABASE "${database}" TO "${schemaOwner}";
    REVOKE ALL ON SCHEMA public FROM PUBLIC;
    SET ROLE "${schemaOwner}"; CREATE SCHEMA api;
    GRANT USAGE ON SCHEMA api TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO "${role}"`)
  const source = new URL('../../cmd/gregale/templates/data-api-teams/', import.meta.url)
  const migrations = new URL('migrations/sql/', source)
  for (const file of (await readdir(migrations)).filter(file => file.endsWith('.sql')).sort()) await owner.query(await readFile(new URL(file, migrations), 'utf8'))
  await owner.query(await readFile(new URL('seed.sql', source), 'utf8'))
  assert.equal((await owner.query("SELECT has_function_privilege('public','api.summarize_team_orders(text,api.order_status,integer,integer)','EXECUTE') AS allowed")).rows[0].allowed, false)
  assert.deepEqual((await owner.query("SELECT prosecdef,provolatile,proconfig FROM pg_proc WHERE oid='api.summarize_team_orders(text,api.order_status,integer,integer)'::regprocedure")).rows[0], { prosecdef: false, provolatile: 's', proconfig: ['search_path=pg_catalog'] })
  for (const row of (await owner.query("SELECT p.oid::regprocedure::text AS identity FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='api' AND obj_description(p.oid,'pg_proc')='@gregale:rpc'")).rows) await owner.query(`GRANT EXECUTE ON FUNCTION ${row.identity} TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api']), types = generate(snapshot)
  const fixture = new URL('client/src/database.types.ts', source)
  if (process.env.DATA_API_TEAMS_UPDATE_FIXTURE === '1') await writeFile(fixture, types)
  else assert.equal(types, await readFile(fixture, 'utf8'), 'committed teams types must match all migrations')
  await mkdir(join(typed, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data/', import.meta.url), join(typed, 'node_modules/@gregale/data'))
  await writeFile(join(typed, 'package.json'), '{"type":"module"}')
  await writeFile(join(typed, 'database.types.ts'), types)
  for (const name of ['orders.ts', 'order-summary.ts']) await writeFile(join(typed, name), await readFile(new URL('client/src/' + name, source), 'utf8'))
  await command(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname, ['--strict', '--target', 'ES2022', '--module', 'NodeNext', '--lib', 'ES2022,DOM', '--outDir', 'dist', 'orders.ts', 'order-summary.ts'], { cwd: typed, env: process.env })
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'orders' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', value => { logs += value }); child.stderr.on('data', value => { logs += value })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = (sub, tenant) => new SignJWT({ sub, tenant, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200; i++) await new Promise(resolve => setTimeout(resolve, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  const { teamsClient } = await import(pathToFileURL(join(typed, 'dist/orders.js')))
  const { parseOrderSummary } = await import(pathToFileURL(join(typed, 'dist/order-summary.js')))
  const token = await sign('alice', 'shop-b')
  await verifyTeamOrderSummary({ owner, pg, loginURL: loginURL.toString(), url, typed, teamsClient, token,
    adminToken: await sign('amy', 'untrusted'), memberToken: await sign('ann', 'untrusted'), otherToken: await sign('bob', 'shop-a') })
  assert.equal((await fetch(url + '/rest/v1/rpc/summarize_team_orders', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{"team_id":"shop-a"}' })).status, 401)
  await owner.query("INSERT INTO api.team_members(tenant_id,subject,role) VALUES('summary-empty','alice','owner')")
  const empty = await teamsClient({ url, accessToken: token }, 'summary-empty').summary()
  assert.equal(empty.error, null); assert.equal(parseOrderSummary(empty.data[0], 'summary-empty').total_cents, '0')

  // A security-invoker synthetic view models 5,000,001 valid int4 amounts
  // without storing millions of rows. Its source retains authoritative RLS.
  // This DDL is confined to this disposable database after normal-table checks.
  await owner.query(`ALTER TABLE api.team_orders RENAME TO summary_physical_orders;
    CREATE TABLE api.summary_precision_source(tenant_id text, copies integer, total_cents integer);
    ALTER TABLE api.summary_precision_source ENABLE ROW LEVEL SECURITY;
    CREATE POLICY precision_members ON api.summary_precision_source FOR SELECT USING(
      EXISTS(SELECT 1 FROM api.team_members m WHERE m.tenant_id=summary_precision_source.tenant_id AND m.subject=current_setting('request.jwt.claims',true)::jsonb->>'sub'));
    INSERT INTO api.summary_precision_source VALUES('shop-a',5000001,2147483647),('shop-b',3,1);
    CREATE VIEW api.team_orders WITH(security_invoker=true) AS SELECT
      lpad(to_hex(n),32,'0')::uuid AS id,p.tenant_id,'alice'::text AS created_by,p.total_cents,
      'draft'::api.order_status AS status,1::integer AS version
      FROM api.summary_precision_source p CROSS JOIN LATERAL generate_series(1,p.copies) n`)
  const started = performance.now()
  const response = await teamsClient({ url, accessToken: token }, 'shop-a').summary()
  assert.equal(response.error, null, JSON.stringify(response.error))
  const large = parseOrderSummary(response.data[0], 'shop-a')
  assert.equal(large.order_count, '5000001'); assert.equal(large.total_cents, '10737420382483647')
  assert.ok(BigInt(large.total_cents) > 9007199254740991n && BigInt(large.total_cents) % 2n === 1n)
  assert.equal(large.by_status[0].total_cents, large.total_cents)
  t.diagnostic(`exact 5,000,001-row aggregate returned in ${Math.round(performance.now() - started)}ms`)
  direct = new pg.Client({ connectionString: loginURL.toString() }); await direct.connect()
  await direct.query("SELECT set_config('request.jwt.claims','{\"sub\":\"alice\"}',false)")
  await direct.query('SET statement_timeout=5')
  await assert.rejects(direct.query("SELECT * FROM api.summarize_team_orders('shop-a')"), error => error.code === '57014')
  await direct.query('SET statement_timeout=15000')
  assert.equal((await direct.query("SELECT * FROM api.summarize_team_orders('shop-a')")).rows[0].total_cents, large.total_cents)
})
