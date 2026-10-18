import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL } from 'node:url'
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
    if (Date.now() >= end) throw Error('Lock contention fixture did not settle')
    await new Promise(resolve => setTimeout(resolve, 20))
  }
}

test('real lock timeouts and deadlocks roll back REST/RPC writes and recover with RLS', { skip: !enabled, timeout: 45000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
  const { inspect, fingerprint } = await import(new URL('types.mjs', runtime))
  const { createServer } = await import(new URL('server.mjs', runtime))
  const { permissions } = await import(new URL('rpc-permissions.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `lock_policy_${suffix}`, role = `lock_login_${suffix}`
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
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=${limits.queryMs}; ALTER ROLE "${role}" SET lock_timeout=${limits.lockMs}`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE CREATE,TEMPORARY ON DATABASE "${database}" FROM PUBLIC;
    REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.notes(id integer PRIMARY KEY, subject text NOT NULL, value integer NOT NULL DEFAULT 0);
    CREATE TABLE api.events(subject text NOT NULL, note_id integer NOT NULL);
    CREATE SEQUENCE api.deadlock_invocations;
    INSERT INTO api.notes(id,subject) VALUES(1,'alice'),(2,'alice'),(3,'bob');
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY; ALTER TABLE api.events ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE POLICY own_events ON api.events USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT CONNECT ON DATABASE "${database}" TO "${role}";
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO "${role}";
    GRANT USAGE,SELECT ON SEQUENCE api.deadlock_invocations TO "${role}";
    CREATE FUNCTION api.blocked_save(note_id integer,new_value integer) RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
    BEGIN
      INSERT INTO api.events VALUES(current_setting('request.jwt.claims',true)::jsonb->>'sub',note_id);
      UPDATE api.notes SET value=new_value WHERE id=note_id;
      RETURN 1;
    END $$;
    CREATE FUNCTION api.lock_pair(first_id integer,second_id integer) RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
    BEGIN
      PERFORM nextval('api.deadlock_invocations');
      UPDATE api.notes SET value=value+1 WHERE id=first_id;
      PERFORM pg_advisory_xact_lock(734928149);
      UPDATE api.notes SET value=value+1 WHERE id=second_id;
      RETURN 1;
    END $$;
    CREATE FUNCTION api.slow_unlocked() RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
    BEGIN PERFORM pg_sleep(5.1); RETURN 1; END $$;
    COMMENT ON FUNCTION api.blocked_save(integer,integer) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.lock_pair(integer,integer) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.slow_unlocked() IS '@gregale:rpc';
    REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA api FROM PUBLIC`)
  assert.equal((await permissions(ownerURL.toString(), { role, apply: true })).ready, true)
  // Updated ACL tooling accepts both exact versions, but never repairs drift.
  await admin.query(`ALTER ROLE "${role}" RESET lock_timeout`)
  assert.equal((await permissions(ownerURL.toString(), { role })).ready, true)
  await admin.query(`ALTER ROLE "${role}" SET lock_timeout=6000`)
  await assert.rejects(permissions(ownerURL.toString(), { role, apply: true }), /Unsafe or mismatched Data API role/)
  await admin.query(`ALTER ROLE "${role}" SET lock_timeout=${limits.lockMs}`)
  assert.equal((await permissions(ownerURL.toString(), { role })).ready, true)

  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  config.snapshot = await inspect(config.connection, config.schemas)
  config.fingerprint = fingerprint(config.snapshot); config.functions = config.snapshot.functions
  const upstreamPort = await port(), readyPort = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstreamPort), PGRST_ADMIN_SERVER_PORT: String(readyPort) }, stdio: 'ignore' })
  await until(async () => { try { return (await fetch(`http://127.0.0.1:${readyPort}/ready`)).status === 200 } catch { return false } })
  const records = []
  gateway = createServer(config, async token => ({ sub: token, exp: 9999999999 }), upstreamPort, readyPort, record => records.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const base = `http://127.0.0.1:${gateway.address().port}`
  let calls = 0
  const db = createDataClient({ url: base, accessToken: 'alice', fetch: (...args) => { calls++; return fetch(...args) } }).schema('api')
  const bob = createDataClient({ url: base, accessToken: 'bob' }).schema('api')
  const locked = () => admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role]).then(result => Number(result.rows[0].count))
  const all = () => owner.query('SELECT id,value FROM api.notes ORDER BY id').then(result => result.rows)

  await owner.query('BEGIN; UPDATE api.notes SET value=100 WHERE id=1')
  const started = performance.now()
  const pending = Promise.resolve(db.rpc('blocked_save', { note_id: 1, new_value: 99 }))
  await until(async () => await locked() === 1)
  assert.equal((await bob.from('notes').update({ value: 7 }).eq('id', 3)).error, null, 'another user must use the remaining serving connection')
  assert.ok(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename=$1', [role])).rows[0].count) <= limits.pool)
  const failed = await pending
  assert.equal(failed.error?.code, '55P03'); assert.equal(classifyDataError(failed.error).category, 'lock_conflict')
  assert.equal(classifyDataError(failed.error).recovery, 'wait'); assert.equal(calls, 1)
  assert.ok(performance.now() - started >= limits.lockMs - 250)
  assert.ok(performance.now() - started < limits.queryMs, 'lock timeout must precede the HTTP/statement deadline')
  await owner.query('ROLLBACK')
  assert.equal(Number((await owner.query('SELECT count(*) FROM api.events')).rows[0].count), 0, 'earlier RPC writes must roll back')
  assert.deepEqual(await all(), [{ id: 1, value: 0 }, { id: 2, value: 0 }, { id: 3, value: 7 }])
  assert.equal((await db.rpc('blocked_save', { note_id: 1, new_value: 1 })).error, null)
  assert.equal(Number((await owner.query('SELECT count(*) FROM api.events')).rows[0].count), 1)

  await owner.query('BEGIN; UPDATE api.notes SET value=100 WHERE id=2')
  const beforeREST = calls
  const rest = await db.from('notes').update({ value: 99 }).eq('id', 2)
  assert.equal(rest.error?.code, '55P03'); assert.equal(calls, beforeREST + 1)
  await owner.query('ROLLBACK')
  assert.equal((await all())[1].value, 0)
  assert.equal((await db.from('notes').update({ value: 2 }).eq('id', 2)).error, null)

  // A controlled barrier makes two transactions retain opposite row locks.
  // Pinned PostgREST retries its deadlock victim inside the SQL transaction.
  // A nontransactional counter proves the retry; row values prove that the
  // victim's first partial update rolled back before its successful retry.
  const beforeDeadlock = calls, beforeValues = await all()
  await owner.query('SELECT pg_advisory_lock(734928149)')
  const pairs = Promise.all([
    Promise.resolve(db.rpc('lock_pair', { first_id: 1, second_id: 2 })),
    Promise.resolve(db.rpc('lock_pair', { first_id: 2, second_id: 1 })),
  ])
  await until(async () => await locked() === 2)
  await owner.query('SELECT pg_advisory_unlock(734928149)')
  const results = await pairs
  assert.equal(results.filter(result => result.error === null).length, 2)
  assert.equal(Number((await owner.query('SELECT last_value FROM api.deadlock_invocations')).rows[0].last_value), 3)
  assert.equal(calls, beforeDeadlock + 2, 'the SDK must not replay either HTTP mutation')
  assert.deepEqual(await all(), beforeValues.map(row => ({ ...row, value: row.id === 3 ? row.value : row.value + 2 })))
  assert.deepEqual((await db.from('notes').select('id').order('id')).data, [{ id: 1 }, { id: 2 }])
  assert.deepEqual((await bob.from('notes').select('id')).data, [{ id: 3 }])
  assert.equal((await db.from('notes').update({ value: 99 }).eq('id', 3).select()).data.length, 0)
  // Five seconds bounds each lock wait, not useful SQL that holds no locks.
  assert.equal((await db.rpc('slow_unlocked', {})).data, 1)
  assert.ok(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename=$1', [role])).rows[0].count) <= limits.pool)
  assert.doesNotMatch(JSON.stringify(records), /test-only|Bearer|postgres:|UPDATE api|private lock/)
})
