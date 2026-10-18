import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn, execFile } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { promisify } from 'node:util'
import { createDataClient, classifyDataError } from '../../sdk/data/dist/index.js'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function port() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port
  await new Promise(resolve => server.close(resolve))
  return value
}
async function until(check) {
  const end = Date.now() + 10000
  while (!await check()) {
    if (Date.now() >= end) throw Error('User concurrency SQL fixture did not settle')
    await new Promise(resolve => setTimeout(resolve, 20))
  }
}

test('real SQL concurrency limits reject execution, isolate users, retain uncertain cancellations and recover after deadlines', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `concurrency_${suffix}`, role = `concurrency_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null && child.signalCode === null) { child.kill(); await once(child, 'exit') }
    if (owner) { await owner.query('ROLLBACK'); await owner.end() }
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=30000`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.notes(id text PRIMARY KEY, subject text NOT NULL);
    CREATE TABLE api.blocked(id integer);
    CREATE SEQUENCE api.invocations;
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT ON api.notes TO "${role}";
    GRANT SELECT ON api.blocked TO "${role}";
    GRANT USAGE,SELECT ON SEQUENCE api.invocations TO "${role}";
    CREATE FUNCTION api.add_note(note_id text,delay_ms integer DEFAULT 0) RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
    BEGIN
      PERFORM nextval('api.invocations');
      PERFORM pg_sleep(delay_ms / 1000.0);
      INSERT INTO api.notes VALUES(note_id,current_setting('request.jwt.claims',true)::jsonb->>'sub');
      RETURN 1;
    END $$;
    COMMENT ON FUNCTION api.add_note(text,integer) IS '@gregale:rpc';
    REVOKE EXECUTE ON FUNCTION api.add_note(text,integer) FROM PUBLIC;
    GRANT EXECUTE ON FUNCTION api.add_note(text,integer) TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_USER_CONCURRENCY: '1' })
  config.snapshot = await inspect(config.connection, config.schemas)
  config.fingerprint = fingerprint(config.snapshot); config.functions = config.snapshot.functions
  const upstreamPort = await port(), readyPort = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
  await until(async () => { try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false } })
  let offset = 0
  const actualClock = performance.now.bind(performance)
  t.mock.method(performance, 'now', () => actualClock() + offset)
  const records = [], infos = []
  gateway = createServer(config, async token => {
    if (!['alice-old', 'alice-new', 'bob'].includes(token)) throw Error('Invalid fixture JWT')
    return { sub: token.startsWith('alice-') ? 'alice' : 'bob', exp: 9999999999 }
  }, upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  let accessToken = 'alice-old', calls = 0
  const db = createDataClient({ url: base, accessToken: () => accessToken, onResponse: info => infos.push(info), fetch: (...args) => { calls++; return fetch(...args) } }).schema('api')
  const bob = createDataClient({ url: base, accessToken: 'bob' }).schema('api')
  const active = () => admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND state='active'", [role]).then(result => Number(result.rows[0].count))
  const locked = () => admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role]).then(result => Number(result.rows[0].count))

  await owner.query('BEGIN; LOCK TABLE api.blocked IN ACCESS EXCLUSIVE MODE')
  const first = fetch(base + '/rest/v1/blocked', { headers: { Authorization: 'Bearer alice-old' } })
  await until(async () => await locked() === 1)
  const denied = await db.rpc('add_note', { note_id: 'blocked-rpc' })
  assert.equal(denied.status, 429); assert.equal(classifyDataError(denied.error).category, 'concurrency_limited')
  assert.equal(classifyDataError(denied.error).recovery, 'wait')
  assert.equal((await db.from('notes').insert({ id: 'blocked-rest', subject: 'alice' })).status, 429)
  accessToken = 'alice-new'
  await assert.rejects(async () => await db.rpc('add_note', { note_id: 'blocked-rotated' }).throwOnError(), error => classifyDataError(error).category === 'concurrency_limited')
  assert.equal(calls, 3, 'concurrency rejection must not replay mutations')
  assert.equal((await owner.query('SELECT is_called FROM api.invocations')).rows[0].is_called, false, 'even rolled-back RPC execution would advance this sequence')
  assert.equal((await owner.query('SELECT count(*) FROM api.notes')).rows[0].count, '0')
  assert.equal((await bob.rpc('add_note', { note_id: 'bob-note' })).status, 200, 'another user must still reach SQL')
  assert.ok(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename=$1', [role])).rows[0].count) <= limits.pool)
  await owner.query('COMMIT')
  const firstResponse = await first; assert.equal(firstResponse.status, 200); await firstResponse.text()
  assert.equal((await db.rpc('add_note', { note_id: 'recovered' })).status, 200)
  assert.deepEqual((await db.from('notes').select('id')).data, [{ id: 'recovered' }])

  const abort = new AbortController()
  const pending = fetch(base + '/rest/v1/rpc/add_note', { method: 'POST', headers: { Authorization: 'Bearer alice-old', 'Content-Type': 'application/json' }, body: '{"note_id":"cancelled-note","delay_ms":1000}', signal: abort.signal })
  const rejection = assert.rejects(pending, error => error.name === 'AbortError')
  await until(async () => (await owner.query('SELECT last_value FROM api.invocations')).rows[0].last_value === '3' && await active() > 0)
  abort.abort(); await rejection
  await until(() => records.some(record => record.outcome === 'aborted'))
  const cooling = await db.from('notes').select('id')
  assert.equal(cooling.status, 429)
  assert.equal(records.at(-1).concurrency_limit.active, 0)
  assert.equal(records.at(-1).concurrency_limit.cooling_down, 1)
  assert.deepEqual((await bob.from('notes').select('id')).data, [{ id: 'bob-note' }])
  await until(async () => await active() === 0)
  assert.equal((await db.from('notes').select('id')).status, 429, 'HTTP cancellation without engine acknowledgement must retain cooldown')
  assert.equal((await owner.query('SELECT last_value FROM api.invocations')).rows[0].last_value, '3')
  offset += limits.userConcurrencyCooldownMs
  const afterCancel = await db.from('notes').select('id')
  assert.equal(afterCancel.status, 200); assert.ok(afterCancel.data.every(row => row.id !== 'bob-note'))

  await owner.query('BEGIN; LOCK TABLE api.blocked IN ACCESS EXCLUSIVE MODE')
  const started = actualClock()
  const expired = fetch(base + '/rest/v1/blocked', { headers: { Authorization: 'Bearer alice-old' } })
  await until(async () => await locked() === 1)
  const expiredResponse = await expired
  assert.equal(expiredResponse.status, 504); assert.equal((await expiredResponse.json()).code, 'request_deadline_exceeded')
  assert.ok(actualClock() - started >= limits.queryMs - 100)
  await owner.query('COMMIT')
  assert.equal((await db.rpc('add_note', { note_id: 'blocked-after-deadline' })).status, 429)
  assert.equal((await owner.query('SELECT last_value FROM api.invocations')).rows[0].last_value, '3')
  assert.equal((await bob.rpc('add_note', { note_id: 'bob-after-deadline' })).status, 200)
  await until(async () => await active() === 0)
  offset += limits.userConcurrencyCooldownMs
  assert.equal((await db.rpc('add_note', { note_id: 'after-deadline' })).status, 200)
  assert.equal((await owner.query('SELECT last_value FROM api.invocations')).rows[0].last_value, '5')
  assert.equal((await fetch(base + '/livez')).status, 200)

  const rejections = records.filter(record => record.code === 'data_api_concurrency_limited')
  assert.equal(rejections.length, 6)
  assert.ok(rejections.some(record => infos.some(info => info.requestId === record.request_id)))
  for (const record of rejections) {
    assert.equal(record.status, 429); assert.equal(record.outcome, 'rejected')
    assert.equal(record.timings.upstream, undefined); assert.equal(record.database_timings, undefined)
  }
  assert.doesNotMatch(JSON.stringify(records), /alice|bob|test-only|blocked-|postgres:/)
  if (process.env.DATA_API_DEV_CLI) {
    const directory = await mkdtemp(join(tmpdir(), 'gregale-concurrency-diagnostics-'))
    t.after(() => rm(directory, { recursive: true, force: true }))
    const file = join(directory, 'logs.ndjson'); await writeFile(file, records.map(record => JSON.stringify(record)).join('\n'))
    const args = ['data-api', 'diagnostics', '--file', file], options = { env: { ...process.env, FAAS_TOKEN: '' }, timeout: 10000 }
    const { stdout } = await promisify(execFile)(process.env.DATA_API_DEV_CLI, ['--json', ...args], options)
    const report = JSON.parse(stdout)
    assert.equal(report.partial, false); assert.equal(report.failures.concurrency_limited, rejections.length)
    assert.equal(report.failures.rate_limited, 0)
    const lookup = await promisify(execFile)(process.env.DATA_API_DEV_CLI, ['--json', ...args, '--request-id', rejections[0].request_id], options)
    assert.equal(JSON.parse(lookup.stdout).matches[0].concurrency_limit.reason, 'subject')
    const human = await promisify(execFile)(process.env.DATA_API_DEV_CLI, args, options)
    assert.match(human.stdout, /User concurrency limits: 6 observed/)
    assert.doesNotMatch(stdout + lookup.stdout + human.stdout, /alice|bob|test-only|blocked-|postgres:/)
  }
})
