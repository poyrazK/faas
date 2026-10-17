import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { createDataClient, classifyDataError, dataRequest } from '../../sdk/data/dist/index.js'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
const command = promisify(execFile)
async function port() {
  const server = net.createServer(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port; await new Promise(resolve => server.close(resolve)); return value
}
async function until(check) {
  const end = performance.now() + 10000
  while (!await check()) { if (performance.now() >= end) throw Error('Response limit fixture did not settle'); await new Promise(resolve => setTimeout(resolve, 20)) }
}

test('real JSON and committed RPC results exceed the cap without retries, then recover with RLS', { skip: !enabled, timeout: 45000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `response_${suffix}`, role = `response_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null && child.signalCode === null) { child.kill(); await once(child, 'exit') }
    if (owner) await owner.end()
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
    CREATE TABLE api.notes(id text PRIMARY KEY, subject text NOT NULL, body text NOT NULL);
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT,INSERT ON api.notes TO "${role}";
    CREATE FUNCTION api.make_large(note_id text) RETURNS jsonb LANGUAGE plpgsql AS $$
    BEGIN
      INSERT INTO api.notes(id,subject,body) VALUES(note_id,current_setting('request.jwt.claims',true)::jsonb->>'sub','committed');
      RETURN jsonb_build_object('payload', repeat('x',${limits.responseBytes + 1}));
    END $$;
    COMMENT ON FUNCTION api.make_large(text) IS '@gregale:rpc';
    REVOKE ALL ON FUNCTION api.make_large(text) FROM PUBLIC;
    GRANT EXECUTE ON FUNCTION api.make_large(text) TO "${role}"`)
  await owner.query("INSERT INTO api.notes VALUES('huge','alice',repeat('🙂',$1))", [limits.responseBytes / 4])
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  config.snapshot = await inspect(config.connection, config.schemas); config.fingerprint = fingerprint(config.snapshot); config.functions = config.snapshot.functions
  assert.ok(config.functions.some(fn => fn.name === 'make_large'), 'RPC fixture must be catalog-recognized')
  const upstreamPort = await port(), readyPort = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
  await until(async () => { try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false } })
  const records = [], infos = []
  gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  let calls = 0
  const db = createDataClient({ url: base, accessToken: 'alice', onResponse: info => infos.push(info), fetch: (...args) => { calls++; return fetch(...args) } }).schema('api')
  const large = await db.from('notes').select('body').eq('id', 'huge').retry(false)
  assert.ok([0, 502].includes(large.status))
  assert.ok(['no_response', 'response_too_large'].includes(classifyDataError(large.error, { status: large.status }).category))
  assert.equal(classifyDataError(large.error, { status: large.status }).recovery, 'reconcile')
  assert.equal(calls, 1)
  const mutation = await dataRequest(db.rpc('make_large', { note_id: 'once' }), { timeoutMs: 15000 })
  assert.ok([0, 502].includes(mutation.status), `RPC status=${mutation.status} code=${mutation.error?.code}`)
  assert.equal(classifyDataError(mutation.error, { status: mutation.status }).recovery, 'reconcile')
  assert.equal(calls, 2)
  assert.equal((await owner.query("SELECT count(*) FROM api.notes WHERE id='once'")).rows[0].count, '1', 'the RPC committed before the oversized result was interrupted')
  const recovered = await db.from('notes').select('id').order('id').retry(false)
  assert.equal(recovered.status, 200)
  assert.deepEqual(recovered.data.map(row => row.id), ['huge', 'once'])
  const bob = createDataClient({ url: base, accessToken: 'bob' }).schema('api')
  assert.deepEqual((await bob.from('notes').select('id').retry(false)).data, [])
  await until(() => records.filter(record => record.code === 'response_too_large').length === 2)
  for (const record of records.filter(record => record.response_limit)) {
    assert.ok(infos.some(info => info.requestId === record.request_id))
    assert.equal(record.response_limit.limit_bytes, limits.responseBytes)
    assert.equal(record.response_limit.observed_at_least_bytes, limits.responseBytes + 1)
    assert.equal(record.timings.streaming.complete, false)
  }
  assert.doesNotMatch(JSON.stringify(records), /test-only|alice|bob|payload|🙂|postgres:/)
  if (process.env.DATA_API_DEV_CLI) {
    const directory = await mkdtemp(join(tmpdir(), 'gregale-response-diagnostics-')); t.after(() => rm(directory, { recursive: true, force: true }))
    const file = join(directory, 'logs.ndjson'); await writeFile(file, records.map(record => JSON.stringify(record)).join('\n'))
    const { stdout } = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file], { env: { ...process.env, FAAS_TOKEN: '' } })
    const report = JSON.parse(stdout)
    assert.equal(report.partial, false); assert.equal(report.failures.response_too_large, 2)
    assert.doesNotMatch(stdout, /test-only|alice|bob|payload|🙂|postgres:/)
  }
})
