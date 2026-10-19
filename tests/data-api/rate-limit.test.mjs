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
import { createDataClient, classifyDataError } from '../../sdk/data/dist/index.js'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
const command = promisify(execFile)
async function port() {
  const server = net.createServer(); await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port; await new Promise(resolve => server.close(resolve)); return value
}
async function until(check) {
  const end = Date.now() + 10000
  while (!await check()) { if (Date.now() >= end) throw Error('Rate limit fixture did not settle'); await new Promise(resolve => setTimeout(resolve, 20)) }
}

test('real SQL rate limits reject writes before execution, preserve user isolation and recover without retries', { skip: !enabled, timeout: 30000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `rate_${suffix}`, role = `rate_login_${suffix}`
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
    CREATE TABLE api.notes(id text PRIMARY KEY, subject text NOT NULL);
    CREATE SEQUENCE api.invocations;
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}"; GRANT SELECT,INSERT ON api.notes TO "${role}";
    GRANT USAGE,SELECT ON SEQUENCE api.invocations TO "${role}";
    CREATE FUNCTION api.add_note(note_id text) RETURNS integer LANGUAGE plpgsql AS $$
    BEGIN
      PERFORM nextval('api.invocations');
      INSERT INTO api.notes VALUES(note_id,current_setting('request.jwt.claims',true)::jsonb->>'sub');
      RETURN 1;
    END $$;
    COMMENT ON FUNCTION api.add_note(text) IS '@gregale:rpc';
    REVOKE ALL ON FUNCTION api.add_note(text) FROM PUBLIC;
    GRANT EXECUTE ON FUNCTION api.add_note(text) TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_RATE_LIMIT_PER_SECOND: '1', DATA_API_RATE_LIMIT_BURST: '1' })
  config.snapshot = await inspect(config.connection, config.schemas); config.fingerprint = fingerprint(config.snapshot); config.functions = config.snapshot.functions
  assert.ok(config.functions.some(fn => fn.name === 'add_note'))
  const upstreamPort = await port(), readyPort = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
  await until(async () => { try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false } })
  let now = 0
  t.mock.method(performance, 'now', () => now)
  const records = [], infos = []
  gateway = createServer(config, async token => {
    if (!['alice-old', 'alice-new', 'bob'].includes(token)) throw Error('Invalid fixture JWT')
    return { sub: token.startsWith('alice-') ? 'alice' : 'bob', exp: 9999999999 }
  }, upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  let calls = 0, accessToken = 'alice-old'
  const db = createDataClient({ url: base, accessToken: () => accessToken, onResponse: info => infos.push(info), fetch: (...args) => { calls++; return fetch(...args) } }).schema('api')
  assert.equal((await db.from('notes').select('id')).status, 200)
  const blocked = await db.rpc('add_note', { note_id: 'blocked-rpc' })
  assert.equal(blocked.status, 429)
  assert.equal(classifyDataError(blocked.error).category, 'rate_limited')
  assert.equal(classifyDataError(blocked.error).recovery, 'wait')
  assert.equal((await db.from('notes').insert({ id: 'blocked-rest', subject: 'alice' })).status, 429)
  accessToken = 'alice-new'
  await assert.rejects(async () => await db.rpc('add_note', { note_id: 'blocked-rotated' }).throwOnError(), error => classifyDataError(error).category === 'rate_limited')
  assert.equal(calls, 4, 'rate rejections must not replay requests')
  assert.equal((await owner.query('SELECT is_called FROM api.invocations')).rows[0].is_called, false, 'even a rolled-back RPC invocation would advance this sequence')
  assert.equal((await owner.query('SELECT count(*) FROM api.notes')).rows[0].count, '0')
  const bob = createDataClient({ url: base, accessToken: 'bob' }).schema('api')
  assert.equal((await bob.rpc('add_note', { note_id: 'bob-note' })).status, 200)
  now = 1000
  assert.equal((await db.rpc('add_note', { note_id: 'recovered' })).status, 200)
  now = 2000
  assert.deepEqual((await db.from('notes').select('id')).data, [{ id: 'recovered' }])
  assert.deepEqual((await bob.from('notes').select('id')).data, [{ id: 'bob-note' }])
  assert.equal((await owner.query('SELECT last_value FROM api.invocations')).rows[0].last_value, '2')
  await until(() => records.filter(record => record.code === 'data_api_rate_limited').length === 3)
  for (const record of records.filter(record => record.rate_limit)) {
    assert.ok(infos.some(info => info.requestId === record.request_id))
    assert.equal(record.status, 429); assert.equal(record.rate_limit.reason, 'subject')
    assert.equal(record.timings.upstream, undefined)
  }
  assert.doesNotMatch(JSON.stringify(records), /test-only|alice|bob|blocked-|postgres:/)
  if (process.env.DATA_API_DEV_CLI) {
    const directory = await mkdtemp(join(tmpdir(), 'gregale-rate-diagnostics-')); t.after(() => rm(directory, { recursive: true, force: true }))
    const file = join(directory, 'logs.ndjson'); await writeFile(file, records.map(record => JSON.stringify(record)).join('\n'))
    const options = { env: { ...process.env, FAAS_TOKEN: '' } }
    const { stdout } = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file], options)
    const report = JSON.parse(stdout)
    assert.equal(report.partial, false); assert.equal(report.failures.rate_limited, 3)
    assert.equal(report.failures.overloaded, 0); assert.equal(report.failures.unavailable, 0)
    const id = records.find(record => record.rate_limit).request_id
    const lookup = await command(process.env.DATA_API_DEV_CLI, ['--json', 'data-api', 'diagnostics', '--file', file, '--request-id', id], options)
    assert.equal(JSON.parse(lookup.stdout).matches[0].rate_limit.reason, 'subject')
    const human = await command(process.env.DATA_API_DEV_CLI, ['data-api', 'diagnostics', '--file', file], options)
    assert.match(human.stdout, /User rate limits: 3 observed/)
    assert.doesNotMatch(stdout + lookup.stdout + human.stdout, /test-only|alice|bob|blocked-|postgres:/)
  }
})
