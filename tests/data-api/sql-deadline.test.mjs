import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn, execFile } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { promisify } from 'node:util'
import { createDataClient, classifyDataError } from '../../sdk/data/dist/index.js'

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
async function until(check, timeout = 20000) {
  const end = performance.now() + timeout
  while (!await check()) {
    if (performance.now() >= end) throw Error('SQL deadline fixture did not settle')
    await new Promise(resolve => setTimeout(resolve, 25))
  }
}

test('SQL deadlines bound real repeated transaction retries, cancellation and refresh waits without changing RLS', { skip: !enabled, timeout: 90000 }, async t => {
  const root = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', root)), pg = require('pg')
  const { SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { runtimeConfig, limits } = await import(new URL('config.mjs', root))
  const { createServer } = await import(new URL('server.mjs', root))
  const { inspect, fingerprint } = await import(new URL('types.mjs', root))
  const { installSQLDeadline, checkSQLDeadlineSetup, checkSQLDeadlineConnection } = await import(new URL('sql-deadline.mjs', root))
  const { installRefreshBarrier, changeRefreshBarrier, refreshStatusReader } = await import(new URL('refresh.mjs', root))
  const suffix = `${process.pid}_${Date.now()}`, database = `sql_deadline_${suffix}`, role = `sql_deadline_login_${suffix}`
  const migrationOwner = `sql_deadline_owner_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL }); await admin.connect()
  let owner, direct, engine, gateway, reader
  const logs = []
  async function stop() {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)); gateway = null }
    if (engine && engine.exitCode === null && engine.signalCode === null) { engine.kill(); await once(engine, 'exit') }
    await reader?.close(); reader = null
  }
  t.after(async () => {
    await stop(); await direct?.end()
    if (owner) { await owner.query('ROLLBACK'); await owner.end() }
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.query(`DROP ROLE IF EXISTS "${migrationOwner}"`); await admin.end()
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`ALTER ROLE "${role}" SET statement_timeout=${limits.queryMs}; ALTER ROLE "${role}" SET lock_timeout=${limits.lockMs}`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api;
    CREATE TABLE api.notes(id integer PRIMARY KEY,subject text NOT NULL);
    INSERT INTO api.notes VALUES(1,'alice'),(2,'bob');
    CREATE TABLE api.effects(subject text NOT NULL);
    CREATE SEQUENCE api.attempts;
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY; ALTER TABLE api.effects ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE POLICY own_effects ON api.effects USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT,UPDATE,DELETE ON api.notes,api.effects TO "${role}";
    GRANT USAGE,SELECT ON SEQUENCE api.attempts TO "${role}";
    CREATE FUNCTION api.retry_forever(kind text) RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
    BEGIN
      PERFORM nextval('api.attempts');
      INSERT INTO api.effects VALUES(current_setting('request.jwt.claims',true)::jsonb->>'sub');
      PERFORM pg_sleep(0.05);
      RAISE EXCEPTION USING ERRCODE=kind, MESSAGE='test transaction conflict';
    END $$;
    CREATE FUNCTION api.wait_sql(delay_ms integer) RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
    BEGIN PERFORM pg_sleep(delay_ms/1000.0); RETURN 1; END $$;
    CREATE FUNCTION api.budget_probe() RETURNS jsonb LANGUAGE sql SECURITY INVOKER AS $$
      SELECT jsonb_build_object('statement_ms',extract(epoch FROM current_setting('statement_timeout')::interval)*1000,
        'lock_ms',extract(epoch FROM current_setting('lock_timeout')::interval)*1000) $$;
    COMMENT ON FUNCTION api.retry_forever(text) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.wait_sql(integer) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.budget_probe() IS '@gregale:rpc';
    REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA api FROM PUBLIC;
    GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA api TO "${role}"`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const env = { DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes', DATA_API_SQL_DEADLINE: '1' }
  await assert.rejects(checkSQLDeadlineSetup(owner, role))
  await assert.rejects(promisify(execFile)(process.execPath, [fileURLToPath(new URL('server.mjs', root))], {
    env: { ...process.env, ...env, PORT: '0' }, timeout: 10000,
  }), error => error.code === 1 && /data_api_configuration_invalid/.test(error.stderr))
  assert.equal((await owner.query("SELECT to_regprocedure('gregale_data_api.check_deadline_request()') AS hook")).rows[0].hook, null, 'startup must not install anything')
  // The ordinary schema/migration owner can install without CREATEROLE,
  // superuser privileges or membership permitting SET ROLE to the binding.
  await admin.query(`CREATE ROLE "${migrationOwner}" NOLOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS`)
  await admin.query(`GRANT CREATE ON DATABASE "${database}" TO "${migrationOwner}"`)
  await owner.query(`SET ROLE "${migrationOwner}"`)
  assert.equal((await owner.query('SELECT pg_has_role(current_user,$1,\'MEMBER\') AS member', [role])).rows[0].member, false)
  await installSQLDeadline(owner, role)
  assert.equal((await checkSQLDeadlineSetup(owner, role)).ready, true)
  await owner.query('RESET ROLE')
  // Pinned PostgREST normally lets database role settings override environment
  // settings. Opt-in must retain its hook even with an in-database override.
  await admin.query(`ALTER ROLE "${role}" IN DATABASE "${database}" SET pgrst.db_pre_request=''`)
  const definitions = () => owner.query("SELECT proname,prosrc,proconfig FROM pg_proc WHERE oid IN (to_regprocedure('gregale_data_api.enforce_deadline()'),to_regprocedure('gregale_data_api.check_deadline_request()')) ORDER BY proname").then(result => result.rows)
  const before = await definitions()
  assert.equal((await checkSQLDeadlineSetup(owner, role)).ready, true)
  assert.equal((await checkSQLDeadlineConnection(loginURL.toString())).ready, true)
  assert.deepEqual(await definitions(), before, 'checks are read only')
  await assert.rejects(checkSQLDeadlineSetup(owner, role, { coordinatedRefresh: true }), /coordinated refresh/)
  for (const [change, restore] of [
    ['ALTER FUNCTION gregale_data_api.enforce_deadline() SECURITY DEFINER','ALTER FUNCTION gregale_data_api.enforce_deadline() SECURITY INVOKER'],
    ['ALTER FUNCTION gregale_data_api.enforce_deadline() SET search_path=public','ALTER FUNCTION gregale_data_api.enforce_deadline() SET search_path=pg_catalog'],
    [`REVOKE EXECUTE ON FUNCTION gregale_data_api.enforce_deadline() FROM "${role}"`,`GRANT EXECUTE ON FUNCTION gregale_data_api.enforce_deadline() TO "${role}"`],
    [`GRANT CREATE ON SCHEMA gregale_data_api TO "${role}"`,`REVOKE CREATE ON SCHEMA gregale_data_api FROM "${role}"`],
  ]) {
    await owner.query(change); await assert.rejects(checkSQLDeadlineSetup(owner, role), /matching protected/); await owner.query(restore)
  }
  await owner.query("CREATE OR REPLACE FUNCTION gregale_data_api.enforce_deadline() RETURNS void LANGUAGE plpgsql SECURITY INVOKER SET search_path=pg_catalog AS 'BEGIN NULL; END'")
  await assert.rejects(checkSQLDeadlineSetup(owner, role), /matching protected/)
  await installSQLDeadline(owner, role)
  direct = new pg.Client({ connectionString: loginURL.toString() }); await direct.connect()
  const packet = (extra = {}) => ({ version: 1, expires_at_ms: Date.now() + 2000, coordinated_refresh: false, ...extra })
  for (const invalid of [null, {}, packet({ version: 2 }), packet({ expires_at_ms: String(Date.now()) }), packet({ expires_at_ms: Date.now() + limits.queryMs + 5000 }), packet({ expires_at_ms: -1 }), packet({ coordinated_refresh: 'false' }), packet({ extra: true })]) {
    await direct.query('BEGIN')
    await direct.query("SELECT set_config('request.jwt.claims',$1,true)", [JSON.stringify({ gregale_sql_deadline: invalid })])
    await assert.rejects(direct.query('SELECT gregale_data_api.check_deadline_request()'), error => error.code === 'PGRST' && JSON.parse(error.message).code === 'data_api_sql_deadline_invalid')
    await direct.query('ROLLBACK')
  }
  await direct.query('BEGIN; SET LOCAL statement_timeout=1000; SET LOCAL lock_timeout=40')
  await direct.query("SELECT set_config('request.jwt.claims',$1,true)", [JSON.stringify({ gregale_sql_deadline: packet() })])
  await direct.query('SELECT gregale_data_api.check_deadline_request()')
  const settings = (await direct.query("SELECT current_setting('statement_timeout') AS statement,current_setting('lock_timeout') AS lock")).rows[0]
  assert.equal(settings.statement, '1s'); assert.equal(settings.lock, '40ms')
  await direct.query('ROLLBACK'); await direct.end(); direct = null

  let config, upstream, ready, base, calls = 0
  async function start(coordinated = false) {
    config = runtimeConfig({ ...env, ...(coordinated ? { DATA_API_COORDINATED_REFRESH: '1' } : {}) })
    config.snapshot = await inspect(config.connection, config.schemas); config.functions = config.snapshot.functions; config.fingerprint = fingerprint(config.snapshot)
    if (coordinated) { reader = refreshStatusReader(config.connection); config.refreshStatus = reader.read }
    upstream = await port(); ready = await port()
    engine = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: 'ignore' })
    await until(async () => { try { return (await fetch(`http://127.0.0.1:${ready}/ready`)).status === 200 } catch { return false } })
    gateway = createServer(config, async token => ({ sub: token, exp: 9999999999, gregale_sql_deadline: packet({ expires_at_ms: Number.MAX_SAFE_INTEGER }) }), upstream, ready, row => logs.push(row))
    await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve)); base = `http://127.0.0.1:${gateway.address().port}`
  }
  const client = sub => createDataClient({ url: base, accessToken: sub, fetch: (...args) => { calls++; return fetch(...args) } }).schema('api')
  const active = () => admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND state='active'", [role]).then(result => Number(result.rows[0].count))
  const attempts = () => owner.query('SELECT last_value,is_called FROM api.attempts').then(result => result.rows[0].is_called ? Number(result.rows[0].last_value) : 0)
  await start()
  async function engineRequest(path, body, expiresAt) {
    const token = await new SignJWT({ role, sub: 'alice', exp: 9999999999, gregale_sql_deadline: packet({ expires_at_ms: expiresAt }) })
      .setProtectedHeader({ alg: 'HS256' }).sign(new TextEncoder().encode(config.secret.toString('base64url')))
    const response = await fetch(`http://127.0.0.1:${upstream}/rpc/${path}`, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
    return { status: response.status, error: await response.json() }
  }
  const expired = await engineRequest('budget_probe', {}, Date.now() - 1)
  assert.equal(expired.status, 504); assert.equal(expired.error.code, 'data_api_sql_deadline_exceeded')
  assert.equal(classifyDataError(expired.error).recovery, 'reconcile')
  const shortened = await engineRequest('wait_sql', { delay_ms: 2000 }, Date.now() + 300)
  assert.equal(shortened.error.code, '57014', 'the hook must cap the subsequent main SQL statement')
  for (const kind of ['40001','40P01']) {
    const db = client('alice'), beforeCalls = calls, beforeAttempts = await attempts(), started = performance.now(), controller = new AbortController()
    const pending = Promise.resolve(db.rpc('retry_forever', { kind }).abortSignal(controller.signal))
    await until(async () => await attempts() >= beforeAttempts + 2)
    assert.deepEqual((await client('bob').from('notes').select('id')).data, [{ id: 2 }])
    if (kind === '40P01') controller.abort()
    const result = await pending
    assert.ok(result.error)
    assert.equal(classifyDataError(result.error, { status: result.status }).recovery, 'reconcile')
    // The engine can briefly be idle between retry queries. One pg_stat_activity
    // snapshot cannot establish completion, especially after HTTP cancellation.
    let last = await attempts(), stableSince = performance.now()
    await until(async () => {
      const current = await attempts()
      if (current !== last) { last = current; stableSince = performance.now() }
      return await active() === 0 && performance.now() - stableSince >= 300
    })
    assert.ok(performance.now() - started <= limits.queryMs + 2500, 'SQL retries must stop after the original deadline even when HTTP disconnects')
    const stopped = await attempts(); assert.ok(stopped > beforeAttempts)
    await new Promise(resolve => setTimeout(resolve, 150)); assert.equal(await attempts(), stopped)
    assert.equal(calls, beforeCalls + 2, 'one mutation and one independent read, with no SDK replay')
    assert.equal(Number((await owner.query('SELECT count(*) FROM api.effects')).rows[0].count), 0)
    assert.deepEqual((await db.from('notes').select('id')).data, [{ id: 1 }])
  }
  await stop()
  await installRefreshBarrier(owner, role)
  await installSQLDeadline(owner, role, { coordinatedRefresh: true })
  await start(true)
  await owner.query('BEGIN')
  await owner.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale-data-api:' || $1,0))", [role])
  const budget = Promise.resolve(client('alice').rpc('budget_probe', {}))
  await until(async () => Number((await admin.query("SELECT count(*) FROM pg_stat_activity WHERE usename=$1 AND wait_event_type='Lock'", [role])).rows[0].count) === 1)
  await new Promise(resolve => setTimeout(resolve, 300)); await owner.query('COMMIT')
  const result = await budget
  assert.equal(result.error, null); assert.ok(result.data.statement_ms < limits.queryMs - 200, 'refresh wait must consume the same budget')
  assert.ok(result.data.lock_ms <= limits.lockMs)
  const token = 'a'.repeat(64)
  await changeRefreshBarrier(owner, 'pause', role, token)
  const paused = await client('alice').from('notes').select('id')
  assert.equal(paused.error.code, 'data_api_refresh_paused')
  await changeRefreshBarrier(owner, 'resume', role, token, config.fingerprint)
  assert.deepEqual((await client('bob').from('notes').select('id')).data, [{ id: 2 }])
  assert.ok(Number((await admin.query('SELECT count(*) FROM pg_stat_activity WHERE usename=$1', [role])).rows[0].count) <= limits.pool)
  assert.doesNotMatch(JSON.stringify(logs), /test-only|Bearer|postgres:|gregale_sql_deadline|expires_at_ms|test transaction conflict/)
})
