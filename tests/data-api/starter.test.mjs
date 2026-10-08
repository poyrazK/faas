import test from 'node:test'
import assert from 'node:assert/strict'
import net from 'node:net'
import { spawn } from 'node:child_process'
import { once } from 'node:events'
import { createRequire } from 'node:module'
import { readFile, writeFile, rm } from 'node:fs/promises'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { verifyRPC, verifyRPCPolicy } from './rpc.mjs'
import { browserOrigins, verifyBrowserCORS } from './browser-cors.mjs'
import { createHash } from 'node:crypto'
import { verifyPermissions } from './rpc-permissions.mjs'
import { verifyRetention } from './retention.mjs'
import { verifyIdempotency } from './idempotency.mjs'
import { verifySchemaEvolution } from './schema-evolution.mjs'
import { verifyQueryPlans } from './query-plans.mjs'
import { command } from './staging/canary.mjs'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_POSTGREST_BIN && process.env.DATA_API_STARTER_DIR)
const runtime = process.env.DATA_API_RUNTIME_DIR ? pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/') : new URL('../../cmd/gregale/templates/data-api/', import.meta.url)
const runtimeRequire = createRequire(new URL('package.json', runtime))
const pg = runtimeRequire('pg')
const { generateKeyPair, exportJWK, createLocalJWKSet, SignJWT } = await import(pathToFileURL(runtimeRequire.resolve('jose')))
const { runtimeConfig, limits } = await import(new URL('config.mjs', runtime))
const { createServer, tokenVerifier } = await import(new URL('server.mjs', runtime))
const { inspect, generate, fingerprint } = await import(new URL('types.mjs', runtime))

async function port() {
  const server = net.createServer()
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const value = server.address().port
  await new Promise(resolve => server.close(resolve))
  return value
}

test('starter migrations, generated contract, packed client and two-user RLS', { skip: !enabled, timeout: 60000 }, async t => {
  const root = process.env.DATA_API_STARTER_DIR
  const { migrate } = await import(pathToFileURL(join(root, 'migrations/migrate.mjs')))
  const suffix = `${process.pid}_${Date.now()}`
  const database = `starter_${suffix}`
  const scope = createHash('sha256').update(database).digest('hex').slice(0, 40)
  const schemaOwner = `starter_owner_${suffix}`
  const migrator = `starter_migration_${suffix}`
  const role = `starter_api_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
  await admin.connect()
  let owner, child, gateway, origins
  t.after(async () => {
    await origins?.close()
    if (gateway) { gateway.closeAllConnections(); await new Promise(resolve => gateway.close(resolve)) }
    if (child && child.exitCode === null) { child.kill(); await once(child, 'exit') }
    await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    for (const name of [migrator, role, schemaOwner]) await admin.query(`DROP ROLE IF EXISTS "${name}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${schemaOwner}" NOLOGIN NOBYPASSRLS;
    CREATE ROLE "${migrator}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    GRANT "${schemaOwner}" TO "${migrator}";
    ALTER ROLE "${migrator}" SET role TO "${schemaOwner}";
    CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const ownerURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); ownerURL.pathname = `/${database}`
  owner = new pg.Client({ connectionString: ownerURL.toString() }); await owner.connect()
  await owner.query(`REVOKE ALL ON DATABASE "${database}" FROM PUBLIC;
    GRANT CONNECT ON DATABASE "${database}" TO "${migrator}", "${role}";
    GRANT CONNECT, CREATE ON DATABASE "${database}" TO "${schemaOwner}";
    REVOKE ALL ON SCHEMA public FROM PUBLIC;
    SET ROLE "${schemaOwner}";
    CREATE SCHEMA api;
    GRANT USAGE ON SCHEMA api TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO "${role}";
    ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE, SELECT ON SEQUENCES TO "${role}";`)
  await admin.query(`COMMENT ON ROLE "${migrator}" IS 'gregale:credential:v1:${scope}:migration';
    COMMENT ON ROLE "${role}" IS 'gregale:credential:v1:${scope}:data_api';
    ALTER ROLE "${role}" SET statement_timeout=${limits.queryMs}`)
  const migrationURL = new URL(ownerURL); migrationURL.username = migrator; migrationURL.password = 'test-only'
  const loginURL = new URL(ownerURL); loginURL.username = role; loginURL.password = 'test-only'
  // Exercise exactly the stable-owner migration session and restricted API role.
  await migrate(migrationURL.toString())
  await migrate(migrationURL.toString())
  assert.equal((await owner.query('SELECT count(*)::integer AS count FROM gregale_migrations.applied')).rows[0].count, 10)
  const firstSQL = join(root, 'migrations/sql/0001_notes.sql')
  const originalSQL = await readFile(firstSQL, 'utf8')
  await writeFile(firstSQL, originalSQL + '\n-- changed after deployment\n')
  try { await assert.rejects(migrate(migrationURL.toString()), /changed or removed/) }
  finally { await writeFile(firstSQL, originalSQL) }
  const failedSQL = join(root, 'migrations/sql/0011_failure.sql')
  await writeFile(failedSQL, 'ALTER TABLE api.notes ADD COLUMN rolled_back text; SELECT 1/0;')
  try { await assert.rejects(migrate(migrationURL.toString())) }
  finally { await rm(failedSQL) }
  assert.equal((await owner.query('SELECT count(*)::integer AS count FROM gregale_migrations.applied')).rows[0].count, 10)
  assert.equal((await owner.query("SELECT count(*)::integer AS count FROM information_schema.columns WHERE table_schema='api' AND column_name='rolled_back'")).rows[0].count, 0)
  await verifyPermissions({ root, owner, admin, role, migrationURL: migrationURL.toString(), loginURL: loginURL.toString(), scope, database })
  await verifyRPCPolicy({ owner, inspect, loginURL: loginURL.toString(), role })
  const snapshot = await inspect(loginURL.toString(), ['api'])
  assert.deepEqual(snapshot.tables.find(table => table.name === 'comments').relationships[0].referencedColumns, ['subject', 'id'])
  const types = generate(snapshot)
  assert.match(types, /"foreignKeyName":"comments_note_fkey","columns":\["subject","note_id"\],"isOneToOne":false/)
  assert.match(types, /"foreignKeyName":"note_details_note_fkey","columns":\["subject","note_id"\],"isOneToOne":true/)
  assert.match(types, /"foreignKeyName":"note_tags_note_fkey","columns":\["subject","note_id"\],"isOneToOne":false/)
  assert.match(types, /"foreignKeyName":"note_tags_tag_fkey","columns":\["subject","tag_id"\],"isOneToOne":false/)
  assert.match(types, /"foreignKeyName":"note_favorite_tags_note_fkey","columns":\["subject","note_id"\],"isOneToOne":false/)
  assert.match(types, /"foreignKeyName":"note_favorite_tags_tag_fkey","columns":\["subject","tag_id"\],"isOneToOne":false/)
  assert.doesNotMatch(types, /gregale_migrations|test-only|starter_api/)
  const fixture = new URL('../../cmd/gregale/templates/data-api-starter/client/src/database.types.ts', import.meta.url)
  if (process.env.DATA_API_STARTER_UPDATE_FIXTURE === '1') {
    await writeFile(fixture, types)
    await writeFile(join(root, 'client/src/database.types.ts'), types)
  } else {
    assert.equal(types, await readFile(fixture, 'utf8'), 'starter types must match its migrations')
  }
  await verifyQueryPlans({ owner, loginURL: loginURL.toString(), pg })
  assert.equal(generate(await inspect(loginURL.toString(), ['api'])), types, 'indexes must not change the generated contract')
  // Actual constraint changes must update cardinality and the fingerprint.
  await owner.query('ALTER TABLE api.note_details DROP CONSTRAINT note_details_pkey')
  const many = generate(await inspect(loginURL.toString(), ['api']))
  assert.notEqual(many, types)
  assert.match(many, /"foreignKeyName":"note_details_note_fkey","columns":\["subject","note_id"\],"isOneToOne":false/)
  await owner.query('CREATE UNIQUE INDEX details_unique_index ON api.note_details (subject, note_id)')
  assert.equal(generate(await inspect(loginURL.toString(), ['api'])), many, 'standalone unique indexes must match PostgREST cardinality')
  await owner.query('DROP INDEX api.details_unique_index; ALTER TABLE api.note_details ADD CONSTRAINT details_unique UNIQUE (subject, note_id)')
  assert.equal(generate(await inspect(loginURL.toString(), ['api'])), types)
  await owner.query('ALTER TABLE api.note_details DROP CONSTRAINT details_unique')
  await owner.query('ALTER TABLE api.note_details ADD PRIMARY KEY (subject, note_id)')
  assert.equal(generate(await inspect(loginURL.toString(), ['api'])), types)
  // Both schemas are readable, but postgrest-js would resolve the other schema's
  // same-named target against api.notes and infer the wrong nested body type.
  await owner.query(`CREATE SCHEMA other; CREATE TABLE other.notes (id integer PRIMARY KEY, body integer);
    CREATE TABLE api.cross_refs (id integer PRIMARY KEY, note_id integer REFERENCES other.notes(id));
    GRANT USAGE ON SCHEMA other TO "${role}"; GRANT SELECT ON other.notes TO "${role}";`)
  const cross = await inspect(loginURL.toString(), ['api', 'other'])
  assert.deepEqual(cross.tables.find(table => table.schema === 'api' && table.name === 'cross_refs').relationships, [])
  await owner.query('DROP TABLE api.cross_refs; DROP SCHEMA other CASCADE')
  await command('npm', ['run', 'typecheck'], { cwd: join(root, 'client'), env: process.env })
  await command('npm', ['run', 'build'], { cwd: join(root, 'client'), env: process.env })
  const restricted = new pg.Client({ connectionString: loginURL.toString() }); await restricted.connect()
  try {
    await assert.rejects(restricted.query('SELECT * FROM gregale_migrations.applied'), error => error.code === '42501')
    await assert.rejects(restricted.query('ALTER TABLE api.notes ADD COLUMN forbidden integer'), error => error.code === '42501')
  } finally { await restricted.end() }
  await owner.query(`CREATE VIEW api.slow_notes WITH (security_invoker=true) AS
    SELECT id, body, pg_sleep(5)::text AS delay FROM api.notes WHERE body='slow-read-fixture';
    GRANT SELECT ON api.slow_notes TO "${role}"`)
  if (process.env.DATA_API_CHROMIUM_BIN) {
    await command('npm', ['run', 'build:browser'], { cwd: join(root, 'client'), env: process.env })
    origins = await browserOrigins(root)
  }
  const config = runtimeConfig({ DATA_API_ALLOWED_ORIGINS: origins?.allowed ?? '', DATABASE_URL: loginURL.toString(), DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  const servingSnapshot = await inspect(loginURL.toString(), ['api'])
  config.functions = servingSnapshot.functions
  config.fingerprint = fingerprint(servingSnapshot)
  const upstream = await port(), ready = await port()
  child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
  let logs = ''
  child.stdout.on('data', value => { logs += value }); child.stderr.on('data', value => { logs += value })
  const { privateKey, publicKey } = await generateKeyPair('ES256')
  const jwk = await exportJWK(publicKey)
  const gatewayLogs = []
  gateway = createServer(config, tokenVerifier(config.auth, createLocalJWKSet({ keys: [jwk] })), upstream, ready, record => gatewayLogs.push(record))
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  const url = `http://127.0.0.1:${gateway.address().port}`
  let available = false
  for (let i = 0; i < 100; i++) {
    if ((await fetch(url + '/healthz')).status === 200) { available = true; break }
    await new Promise(resolve => setTimeout(resolve, 50))
  }
  assert.equal(available, true, logs)
  const refresh = async () => {
    child.kill()
    await once(child, 'exit')
    child = spawn(process.env.DATA_API_POSTGREST_BIN, [], { env: { ...process.env, ...config.postgrestEnv, PGRST_SERVER_PORT: String(upstream), PGRST_ADMIN_SERVER_PORT: String(ready) }, stdio: ['ignore', 'pipe', 'pipe'] })
    child.stdout.on('data', value => { logs += value }); child.stderr.on('data', value => { logs += value })
    let healthy = false
    for (let i = 0; i < 100; i++) {
      if ((await fetch(url + '/healthz')).status === 200) { healthy = true; break }
      await new Promise(resolve => setTimeout(resolve, 50))
    }
    assert.equal(healthy, true, 'schema_refresh_not_ready')
  }
  const sign = (subject, audience = 'notes') => new SignJWT({ sub: subject, role: 'postgres' }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience(audience).setExpirationTime('5m').sign(privateKey)
  const userA = { subject: 'identity|alice', token: await sign('identity|alice') }
  const userB = { subject: 'identity|bob', token: await sign('identity|bob') }
  const contractHeaders = { Authorization: `Bearer ${userA.token}` }
  assert.equal((await fetch(url + '/__gregale/schema')).status, 401)
  const serving = await (await fetch(url + '/__gregale/schema', { headers: contractHeaders })).json()
  assert.equal(serving.fingerprint, fingerprint(servingSnapshot))
  // Grants change the exported contract, but cannot update the captured runtime.
  await owner.query(`REVOKE EXECUTE ON FUNCTION api.create_note_with_tags(text, text[]) FROM "${role}"`)
  try {
    assert.notEqual(fingerprint(await inspect(loginURL.toString(), ['api'])), serving.fingerprint)
    assert.equal((await (await fetch(url + '/__gregale/schema', { headers: contractHeaders })).json()).fingerprint, serving.fingerprint)
  } finally { await owner.query(`GRANT EXECUTE ON FUNCTION api.create_note_with_tags(text, text[]) TO "${role}"`) }
  const { verifyAuthorization } = await import(pathToFileURL(join(root, 'client/test/authorization.mjs')))
  const { notesClient, noteCursor } = await import(pathToFileURL(join(root, 'client/dist/notes.js')))
  const diagnostics = []
  const client = notesClient({ url, subject: userA.subject, accessToken: userA.token, onResponse: info => diagnostics.push(info) })
  const { readCursorPageWithSession } = await import(pathToFileURL(join(root, 'client/dist/session.js')))
  await verifyRPC({ owner, client, url, userA, userB })
  // The RPC's explicit isolation must win over a stronger role default.
  await admin.query(`ALTER ROLE "${role}" SET default_transaction_isolation = 'repeatable read'`)
  await refresh()
  try { await verifyIdempotency({ owner, client, url, userA, userB, makeClient: notesClient }) }
  finally {
    await admin.query(`ALTER ROLE "${role}" RESET default_transaction_isolation`)
    await refresh()
  }
  await verifyRetention({ owner, client, root, migrationURL: migrationURL.toString(), loginURL: loginURL.toString(), pg, userA })
  const expired = await new SignJWT({ sub: userA.subject }).setProtectedHeader({ alg: 'ES256' }).setIssuer(config.auth.issuer).setAudience('notes').setExpirationTime('0s').sign(privateKey)
  if (origins) {
    const fixtures = await owner.query('INSERT INTO api.notes(subject, body) VALUES ($1, $3), ($2, $3) RETURNING id, subject', [userA.subject, userB.subject, 'browser-cors-fixture'])
    try {
      await verifyBrowserCORS({ origins, url, token: userA.token, expired, subject: userA.subject, gateway, expectedID: fixtures.rows.find(row => row.subject === userA.subject).id })
    } finally { await owner.query('DELETE FROM api.notes WHERE id = ANY($1::integer[])', [fixtures.rows.map(row => row.id)]) }
  }
  let sessionToken = expired
  const sessionClient = notesClient({ url, subject: userA.subject, accessToken: () => sessionToken })
  const rejected = await sessionClient.cursorPage().retry(false)
  assert.equal(rejected.status, 401)
  assert.equal(rejected.error.code, 'token_invalid')
  assert.equal(JSON.stringify(rejected.error).includes(expired), false, 'expired_token_leaked')
  const renewed = await readCursorPageWithSession({ client: sessionClient, signal: new AbortController().signal, renewSession: async () => { sessionToken = userA.token } })
  assert.equal(renewed.error, null)
  const invalid = await client.db.from('notes').insert({ subject: userA.subject }).retry(false)
  assert.equal(invalid.error.code, '23502')
  for (const secret of [userA.token, userB.token, 'test-only']) assert.equal(JSON.stringify(invalid.error).includes(secret), false, 'database_error_leaked_credentials')
  await assert.rejects(Promise.resolve(client.db.from('notes').insert({ subject: userB.subject, body: 'forbidden' }).retry(false).throwOnError()), error => error.code === '42501')
  await owner.query("INSERT INTO api.notes (subject, body) VALUES ($1,'slow-read-fixture')", [userA.subject])
  const controller = new AbortController()
  const pending = Promise.resolve(client.db.from('slow_notes').select('*').abortSignal(controller.signal).retry(false))
  let started = false
  try {
    for (let i = 0; i < 100; i++) {
      const active = await admin.query("SELECT pid FROM pg_stat_activity WHERE datname=$1 AND usename=$2 AND state='active' AND query LIKE '%slow_notes%'", [database, role])
      if (active.rowCount) { started = true; break }
      await new Promise(resolve => setTimeout(resolve, 20))
    }
    assert.equal(started, true, 'slow_query_not_started')
    controller.abort()
    const canceled = await pending
    assert.equal(canceled.status, 0)
    assert.match(canceled.error.message, /AbortError/)
  } finally {
    controller.abort()
    await pending
    // Client abort is not a database rollback guarantee; clean this local fixture.
    await admin.query("SELECT pg_cancel_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND usename=$2 AND state='active' AND query LIKE '%slow_notes%'", [database, role])
    await owner.query("DELETE FROM api.notes WHERE body='slow-read-fixture'")
  }
  // Large fixtures stay local; protected preview checks only create a few rows.
  await owner.query("INSERT INTO api.notes (subject, body) SELECT $1, 'page-limit-fixture' FROM generate_series(1, $2)", [userA.subject, limits.rows + 1])
  try {
    const capped = await client.page({ size: limits.rows + 1 }).eq('body', 'page-limit-fixture')
    assert.equal(capped.error, null)
    assert.equal(capped.data.length, limits.rows, 'server_row_cap_not_enforced')
    assert.equal(capped.count, limits.rows + 1, 'count_must_include_unreturned_visible_rows')
    const tail = await client.page({ offset: limits.rows, size: 1 }).eq('body', 'page-limit-fixture')
    assert.equal(tail.error, null)
    assert.equal(tail.data.length, 1)
    const cursorCapped = await client.cursorPage({ size: limits.rows + 1 }).eq('body', 'page-limit-fixture')
    assert.equal(cursorCapped.error, null)
    assert.equal(cursorCapped.data.length, limits.rows)
    const cursorTail = await client.cursorPage({ after: noteCursor(cursorCapped.data.at(-1)) }).eq('body', 'page-limit-fixture')
    assert.equal(cursorTail.error, null)
    assert.deepEqual(cursorTail.data.map(row => row.id), tail.data.map(row => row.id))
    assert.ok(!capped.data.some(row => row.id === tail.data[0].id), 'row_cap_page_overlap')
    const ranged = await fetch(url + '/rest/v1/notes?select=id&body=eq.page-limit-fixture&order=id.desc', { headers: { Authorization: `Bearer ${userA.token}`, 'Accept-Profile': 'api', Range: '2-4', 'Range-Unit': 'items', Prefer: 'count=exact' } })
    assert.equal(ranged.status, 206)
    assert.equal(ranged.headers.get('Content-Range'), `2-4/${limits.rows + 1}`)
    assert.deepEqual((await ranged.json()).map(row => row.id), capped.data.slice(2, 5).map(row => row.id))
    const hidden = await notesClient({ url, subject: userB.subject, accessToken: userB.token }).page().eq('body', 'page-limit-fixture')
    assert.equal(hidden.error, null)
    assert.equal(hidden.count, 0)
    assert.deepEqual(hidden.data, [])
  } finally {
    await owner.query("DELETE FROM api.notes WHERE subject=$1 AND body='page-limit-fixture'", [userA.subject])
  }
  await verifyAuthorization({ url, userA, userB })
  for (const table of ['notes', 'comments', 'note_details', 'tags', 'note_tags', 'note_favorite_tags']) assert.equal((await owner.query(`SELECT count(*)::integer AS count FROM api.${table}`)).rows[0].count, 0)
  await assert.rejects(verifyAuthorization({ url, userA, userB: userA }), /subjects_must_differ/)
  const publicRequest = token => fetch(url + '/rest/v1/notes', { headers: token ? { Authorization: `Bearer ${token}` } : {} })
  assert.equal((await publicRequest()).status, 401)
  assert.equal((await publicRequest(await sign(userA.subject, 'wrong'))).status, 401)
  // Prove the shipped authorization check catches a policy regression.
  await owner.query('ALTER TABLE api.notes DISABLE ROW LEVEL SECURITY')
  await assert.rejects(verifyAuthorization({ url, userA, userB }), /cross_subject_read_allowed|count_leaked_other_user_rows/)
  await owner.query('ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY')
  assert.equal((await owner.query('SELECT count(*)::integer AS count FROM api.notes')).rows[0].count, 0)
  for (const table of ['tags', 'note_tags', 'note_favorite_tags']) {
    await owner.query(`ALTER TABLE api.${table} DISABLE ROW LEVEL SECURITY`)
    try {
      await assert.rejects(verifyAuthorization({ url, userA, userB }), /cross_subject_tag_read_allowed/)
    } finally {
      await owner.query(`ALTER TABLE api.${table} ENABLE ROW LEVEL SECURITY`)
    }
  }
  for (const table of ['notes', 'tags', 'note_tags', 'note_favorite_tags']) assert.equal((await owner.query(`SELECT count(*)::integer AS count FROM api.${table}`)).rows[0].count, 0)
  await new Promise(setImmediate)
  assert.ok(diagnostics.length > 0)
  for (const info of diagnostics) {
    assert.ok(Object.isFrozen(info))
    assert.ok(info.requestId)
    const record = gatewayLogs.find(record => record.request_id === info.requestId)
    assert.ok(record, 'SDK ID must correlate with an actual gateway record')
    assert.equal(info.status, record.status)
    assert.deepEqual(Object.keys(info).sort(), ['durationMs', 'requestId', 'status'])
  }
  // Schema drift changes the contract; regenerating before client compilation
  // is required, while the committed fixture remains untouched.
  await owner.query('ALTER TABLE api.notes ADD COLUMN description text')
  assert.notEqual(generate(await inspect(loginURL.toString(), ['api'])), types)
  await verifySchemaEvolution({ owner, client, refresh, inspect, generate, loginURL: loginURL.toString(), root, command })
})
