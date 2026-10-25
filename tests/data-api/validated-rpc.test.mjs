import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { mkdtemp, mkdir, writeFile, cp, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { command } from './staging/canary.mjs'
import { classifyDataError, RpcValidationError, validateRpcResponse, RpcResponseValidationError } from '../../sdk/data/dist/index.js'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_RUNTIME_DIR)
async function probePort() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port
  await new Promise(resolve => server.close(resolve))
  return value
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

function completed(outcome) {
  assert.equal(outcome.outcome, 'completed', outcome.error?.message)
  return outcome.result
}

function failure(outcome, code, category) {
  const result = completed(outcome)
  assert.equal(result.data, null)
  assert.equal(result.error?.code, code)
  const classified = classifyDataError(result.error, { status: result.status })
  assert.equal(classified.category, category)
  assert.equal(classified.original, result.error)
  return result
}

test('validated RPC acceptance against generated contracts, JWT RLS and real PostgREST', { skip: !enabled, timeout: 60000 }, async t => {
  const runtime = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', runtime)), pg = require('pg')
  const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(require.resolve('jose')))
  const { inspect, generate, fingerprint } = await import(new URL('types.mjs', runtime))
  const { runtimeConfig } = await import(new URL('config.mjs', runtime))
  const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
  const suffix = `${process.pid}_${Date.now()}`, database = `validated_rpc_${suffix}`, role = `validated_rpc_login_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  const directory = await mkdtemp(join(tmpdir(), 'gregale-validated-rpc-'))
  let owner, child, gateway
  t.after(async () => {
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
    await rm(directory, { recursive: true, force: true })
  })
  await admin.query(`CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON SCHEMA public FROM PUBLIC;
    CREATE SCHEMA api;
    CREATE SEQUENCE api.rpc_attempts;
    CREATE TABLE api.parents(id integer PRIMARY KEY);
    INSERT INTO api.parents VALUES (1);
    CREATE TABLE api.notes(id integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
      subject text NOT NULL, slug text NOT NULL, body text NOT NULL,
      parent_id integer REFERENCES api.parents(id), UNIQUE(subject,slug));
    ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
    CREATE POLICY own_notes ON api.notes
      USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')
      WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub');
    CREATE TABLE api.secrets(secret integer);
    INSERT INTO api.secrets VALUES (42);
    CREATE FUNCTION api.create_note(note_subject text, note_slug text, note_body text, parent_id integer DEFAULT NULL)
      RETURNS SETOF api.notes LANGUAGE plpgsql SECURITY INVOKER AS $$
      BEGIN
        PERFORM nextval('api.rpc_attempts');
        RETURN QUERY INSERT INTO api.notes(subject,slug,body,parent_id)
          VALUES(note_subject,note_slug,note_body,parent_id) RETURNING *;
      END $$;
    CREATE FUNCTION api.list_notes() RETURNS SETOF api.notes LANGUAGE sql STABLE SECURITY INVOKER AS 'SELECT * FROM api.notes ORDER BY id';
    CREATE FUNCTION api.count_notes() RETURNS integer LANGUAGE sql STABLE SECURITY INVOKER AS 'SELECT count(*)::integer FROM api.notes';
    CREATE FUNCTION api.read_config() RETURNS jsonb LANGUAGE sql STABLE SECURITY INVOKER AS $$ SELECT '{"mode":"notes"}'::jsonb $$;
    CREATE FUNCTION api.read_secret() RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
      BEGIN PERFORM nextval('api.rpc_attempts'); RETURN (SELECT secret FROM api.secrets LIMIT 1); END $$;
    CREATE FUNCTION api.busy() RETURNS integer LANGUAGE plpgsql SECURITY INVOKER AS $$
      BEGIN PERFORM nextval('api.rpc_attempts'); RAISE SQLSTATE 'PT503' USING MESSAGE = 'test_service_unavailable'; END $$;
    CREATE FUNCTION api.unapproved() RETURNS integer LANGUAGE sql AS 'SELECT 1';
    REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA api FROM PUBLIC;
    GRANT USAGE ON SCHEMA api TO "${role}";
    GRANT SELECT,INSERT ON api.notes TO "${role}";
    GRANT SELECT ON api.parents TO "${role}";
    GRANT USAGE ON ALL SEQUENCES IN SCHEMA api TO "${role}";
    GRANT EXECUTE ON FUNCTION api.create_note(text,text,text,integer),api.list_notes(),api.count_notes(),api.read_config(),api.read_secret(),api.busy() TO "${role}";
    COMMENT ON FUNCTION api.create_note(text,text,text,integer) IS '@gregale:rpc';
    COMMENT ON FUNCTION api.list_notes() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.count_notes() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.read_config() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.read_secret() IS '@gregale:rpc';
    COMMENT ON FUNCTION api.busy() IS '@gregale:rpc'`)
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  const snapshot = await inspect(loginURL.toString(), ['api'])
  assert.deepEqual(snapshot.functions.map(fn => fn.name), ['busy', 'count_notes', 'create_note', 'list_notes', 'read_config', 'read_secret'])
  await mkdir(join(directory, 'node_modules/@gregale'), { recursive: true })
  await symlink(new URL('../../sdk/data', import.meta.url).pathname, join(directory, 'node_modules/@gregale/data'))
  await writeFile(join(directory, 'package.json'), '{"type":"module"}')
  await writeFile(join(directory, 'database.types.ts'), generate(snapshot))
  await cp(new URL('./validated-rpc-client.ts', import.meta.url), join(directory, 'client.ts'))
  await command(new URL('../../sdk/data/node_modules/.bin/tsc', import.meta.url).pathname,
    ['--strict', '--target', 'ES2022', '--module', 'NodeNext', '--outDir', 'dist', 'client.ts'], { cwd: directory, env: process.env })
  const typed = await import(pathToFileURL(join(directory, 'dist/client.js')))
  const config = runtimeConfig({ DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'validated-rpc' })
  config.snapshot = snapshot; config.functions = snapshot.functions; config.fingerprint = fingerprint(snapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''; child.stdout.on('data', value => { logs += value }); child.stderr.on('data', value => { logs += value })
  const { privateKey, publicKey } = await generateKeyPair('ES256'), jwk = await exportJWK(publicKey)
  const sign = sub => new SignJWT({ sub, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(config.auth.audience).setExpirationTime('5m').sign(privateKey)
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, () => {})
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${gateway.address().port}`
  for (let i = 0; i < 100 && (await fetch(url + '/healthz')).status !== 200 && child.exitCode === null; i++) await new Promise(resolve => setTimeout(resolve, 50))
  assert.equal((await fetch(url + '/healthz')).status, 200, logs)
  let requests = 0, tokens = 0
  const aliceToken = await sign('alice')
  const alice = typed.client({ url, accessToken: () => { tokens++; return aliceToken }, fetch: (input, init) => { requests++; return fetch(input, init) } })
  const bob = typed.client({ url, accessToken: await sign('bob') })
  const attempts = async () => {
    const { rows: [row] } = await owner.query('SELECT last_value::integer, is_called FROM api.rpc_attempts')
    return row.is_called ? row.last_value : 0
  }
  const note = (subject, slug, extra = {}) => ({ note_subject: subject, note_slug: slug, note_body: ' hello ', ...extra })
  const persisted = async slug => (await owner.query('SELECT count(*)::integer AS count FROM api.notes WHERE slug=$1', [slug])).rows[0].count

  await t.test('invalid inputs never reach token lookup, HTTP transport or PostgreSQL', async () => {
    for (const input of [null, {}, note('alice', ''), note('alice', 'invalid', { parent_id: '1' })]) {
      const outcome = await typed.create(alice, input)
      assert.equal(outcome.outcome, 'validation_failed')
      assert.ok(outcome.error instanceof RpcValidationError)
      assert.equal(outcome.error.code, 'RPC_ARGUMENT_VALIDATION_FAILED')
      assert.equal('cause' in outcome.error, false)
    }
    assert.deepEqual([tokens, requests, await attempts()], [0, 0, 0])
    assert.equal((await owner.query('SELECT count(*)::integer AS count FROM api.notes')).rows[0].count, 0)
  })

  await t.test('typed invoker RPCs preserve projections, defaults, scalar results and JWT subject RLS', async () => {
    const saved = completed(await typed.create(alice, note('alice', 'first')))
    assert.equal(saved.error, null, JSON.stringify(saved.error))
    assert.equal(saved.data.body, 'hello')
    assert.equal(saved.data.parent_id, null)
    assert.equal(saved.data.subject, 'alice')
    const hidden = completed(await typed.list(bob))
    assert.equal(hidden.error, null); assert.deepEqual(hidden.data, [])
    const own = completed(await typed.list(alice))
    assert.equal(own.error, null); assert.deepEqual(own.data, [{ id: saved.data.id, subject: 'alice', slug: 'first' }])
    assert.equal(completed(await typed.count(alice)).data, 1)
    assert.equal(completed(await typed.count(bob)).data, 0)
    const bobSaved = completed(await typed.create(bob, note('bob', 'first', { parent_id: 1 })))
    assert.equal(bobSaved.error, null)
    assert.equal(bobSaved.data.subject, 'bob'); assert.equal(bobSaved.data.parent_id, 1)
    assert.notEqual(bobSaved.data.id, saved.data.id)
    assert.equal(completed(await typed.count(alice)).data, 1)
    assert.equal(completed(await typed.count(bob)).data, 1)
  })

  await t.test('RLS and SQL privilege violations remain classified permission errors', async () => {
    let before = await attempts()
    failure(await typed.create(alice, note('bob', 'forbidden-subject')), '42501', 'permission_denied')
    assert.equal(await attempts(), before + 1)
    assert.equal(await persisted('forbidden-subject'), 0)
    before = await attempts()
    failure(await typed.forbidden(alice), '42501', 'permission_denied')
    assert.equal(await attempts(), before + 1)
  })

  await t.test('constraint failures preserve SQLSTATE and failed mutations execute only once', async () => {
    let before = await attempts(), sent = requests
    failure(await typed.create(alice, note('alice', 'first')), '23505', 'unique_violation')
    assert.equal(requests, sent + 1); assert.equal(await attempts(), before + 1)
    assert.equal(await persisted('first'), 2)
    before = await attempts(); sent = requests
    failure(await typed.create(alice, note('alice', 'bad-parent', { parent_id: 999 })), '23503', 'foreign_key_violation')
    assert.equal(requests, sent + 1); assert.equal(await attempts(), before + 1)
    assert.equal(await persisted('bad-parent'), 0)
  })

  await t.test('authentication and guarded contract errors are rejected before function execution', async () => {
    const before = await attempts()
    const invalid = typed.client({ url, accessToken: 'invalid-token' })
    const auth = completed(await typed.create(invalid, note('alice', 'bad-token')))
    assert.equal(auth.status, 401)
    assert.equal(classifyDataError(auth.error, { status: auth.status }).category, 'authentication')
    const pinned = typed.client({ url, accessToken: aliceToken, expectedSchemaFingerprint: '0'.repeat(64) })
    failure(await typed.create(pinned, note('alice', 'stale-contract')), 'schema_contract_mismatch', 'contract_mismatch')
    assert.equal(await attempts(), before)
    assert.equal(await persisted('bad-token'), 0); assert.equal(await persisted('stale-contract'), 0)
  })

  await t.test('retryable HTTP 503 executes the function once with explicit retry(false)', async () => {
    const before = await attempts(), sent = requests
    const unavailable = failure(await typed.unavailable(alice), 'PT503', 'unavailable')
    assert.equal(unavailable.status, 503)
    assert.equal(requests, sent + 1); assert.equal(await attempts(), before + 1)
  })

  await t.test('a lost response after commit is not replayed and is reconciled by reading', async () => {
    let droppedRequests = 0
    const lostClient = typed.client({ url, accessToken: aliceToken, fetch: async (input, init) => {
      droppedRequests++
      const response = await fetch(input, init)
      assert.equal(response.status, 200)
      await response.arrayBuffer()
      throw new TypeError('test response lost after commit')
    } })
    const before = await attempts()
    const lost = completed(await typed.create(lostClient, note('alice', 'lost-response')))
    assert.equal(lost.status, 0)
    const classified = classifyDataError(lost.error, { status: lost.status })
    assert.equal(classified.category, 'no_response'); assert.equal(classified.recovery, 'reconcile')
    assert.equal(droppedRequests, 1); assert.equal(await attempts(), before + 1)
    assert.equal(await persisted('lost-response'), 1)
    const reconciled = completed(await typed.list(alice))
    assert.equal(reconciled.error, null)
    assert.equal(reconciled.data.filter(row => row.slug === 'lost-response').length, 1)
    assert.equal(await attempts(), before + 1, 'read-only reconciliation must not execute the mutation')
  })

  await t.test('whole-response parsers validate typed scalar, JSON and RLS-filtered row results', async () => {
    const count = await typed.checkedCount(alice)
    assert.equal(count.outcome, 'validated'); assert.equal(typeof count.result.data, 'number')
    assert.equal(count.result.error, null); assert.equal(count.result.status, 200)
    const config = await typed.checkedConfig(alice)
    assert.equal(config.outcome, 'validated'); assert.deepEqual(config.result.data, { mode: 'notes' })
    const rows = completed(await typed.list(bob))
    const checked = validateRpcResponse(rows, { parse(value) {
      assert.ok(Array.isArray(value))
      for (const row of value) { assert.equal(typeof row.id, 'number'); assert.equal(row.subject, 'bob'); assert.equal(typeof row.slug, 'string') }
      return value.map(row => ({ id: row.id, slug: row.slug }))
    } })
    assert.equal(checked.outcome, 'validated')
    assert.equal(checked.result.data.length, 1)
    assert.deepEqual(Object.keys(checked.result.data[0]).sort(), ['id', 'slug'])
    const denied = completed(await typed.forbidden(alice))
    const unchanged = validateRpcResponse(denied, { parse() { throw new Error('must not parse server errors') } })
    assert.equal(unchanged.outcome, 'request_failed'); assert.equal(unchanged.result, denied)
    assert.equal(unchanged.result.error.code, '42501')
  })

  await t.test('response-validation failure after commit keeps HTTP status and never retries the mutation', async () => {
    const before = await attempts(), sent = requests
    const raw = completed(await typed.create(alice, note('alice', 'rejected-response')))
    assert.equal(raw.error, null)
    const checked = validateRpcResponse(raw, { parse(value) { throw new Error('private rejected note ' + value.body) } })
    assert.equal(checked.outcome, 'validation_failed')
    assert.ok(checked.error instanceof RpcResponseValidationError)
    assert.equal(checked.error.code, 'RPC_RESPONSE_VALIDATION_FAILED'); assert.equal(checked.status, 200)
    assert.equal('result' in checked, false); assert.equal('cause' in checked.error, false)
    assert.equal(requests, sent + 1); assert.equal(await attempts(), before + 1)
    assert.equal(await persisted('rejected-response'), 1, 'validation failure does not undo a committed write')
    const reconciled = completed(await typed.list(alice))
    assert.equal(reconciled.data.filter(row => row.slug === 'rejected-response').length, 1)
    assert.equal(await attempts(), before + 1)
  })
})
