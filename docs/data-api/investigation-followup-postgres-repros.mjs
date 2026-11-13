// Current-defect evidence for DAPI031–034; not future acceptance assertions.
// Requires a disposable administrative PostgreSQL URL, a prepared runtime copy,
// and Go (DATA_API_GO_BIN may select it). Every created database/role is removed.
import assert from 'node:assert/strict'
import { createRequire } from 'node:module'
import { readFile, mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { randomBytes, randomUUID } from 'node:crypto'
import { execFileSync } from 'node:child_process'

const runtime = process.env.DATA_API_RUNTIME_DIR, connection = process.env.DATA_API_TEST_DATABASE_URL
if (!runtime || !connection) throw new Error('Use a prepared runtime and disposable administrative PostgreSQL URL')
const require = createRequire(join(runtime, 'package.json')), pg = require('pg')
const { checkStorageTable } = await import(pathToFileURL(join(runtime, 'storage.mjs')))
const { installRefreshBarrier } = await import(pathToFileURL(join(runtime, 'refresh.mjs')))
const { manageRefreshRotation, preflightRefreshRotation } = await import(pathToFileURL(join(runtime, 'refresh-rotation.mjs')))
const { changeEvent } = await import(pathToFileURL(join(runtime, 'changes.mjs')))
const suffix = randomBytes(16).toString('hex'), database = 'followup_' + suffix
const ownerRole = 'gregale_schema_' + suffix, scope = randomBytes(20).toString('hex')
const roles = [ownerRole, 'gregale_api_' + randomBytes(20).toString('hex'), 'gregale_api_' + randomBytes(20).toString('hex')]
const source = roles[1], candidate = roles[2], binding = randomUUID()
const migrationRole = 'followup_migration_' + suffix
roles.push(migrationRole)
const quote = value => '"' + value.replaceAll('"', '""') + '"'
const admin = new pg.Client({ connectionString: connection })
await admin.connect()
let owner
const temporary = await mkdtemp(join(tmpdir(), 'gregale-followup-validator-'))
try {
  await admin.query('CREATE DATABASE ' + quote(database))
  const url = new URL(connection); url.pathname = '/' + database
  owner = new pg.Client({ connectionString: url.toString() }); await owner.connect()
  await owner.query('CREATE ROLE ' + quote(ownerRole) + ' NOLOGIN NOINHERIT; COMMENT ON ROLE ' + quote(ownerRole) + " IS 'gregale:schema-owner:v1'")
  await owner.query('GRANT CREATE ON DATABASE ' + quote(database) + ' TO ' + quote(ownerRole) +
    '; REVOKE ALL ON DATABASE ' + quote(database) + ' FROM PUBLIC; GRANT CONNECT ON DATABASE ' + quote(database) +
    ' TO PUBLIC; ALTER SCHEMA public OWNER TO ' + quote(ownerRole) +
    '; REVOKE ALL ON SCHEMA public FROM PUBLIC; CREATE SCHEMA api AUTHORIZATION ' + quote(ownerRole))
  for (const role of [source, candidate]) {
    await owner.query('CREATE ROLE ' + quote(role) + " LOGIN NOINHERIT NOCREATEROLE NOCREATEDB NOBYPASSRLS PASSWORD 'test-only'; COMMENT ON ROLE " +
      quote(role) + " IS 'gregale:credential:v1:" + scope + ":data_api'")
  }
  await owner.query('CREATE ROLE ' + quote(migrationRole) +
    " LOGIN NOINHERIT NOCREATEROLE NOCREATEDB NOBYPASSRLS PASSWORD 'test-only'; GRANT " +
    quote(ownerRole) + ' TO ' + quote(migrationRole) + '; ALTER ROLE ' + quote(migrationRole) + ' SET role TO ' + quote(ownerRole))
  await owner.end()
  const migrationURL = new URL(url); migrationURL.username = migrationRole; migrationURL.password = 'test-only'
  owner = new pg.Client({ connectionString: migrationURL.toString() }); await owner.connect()
  await owner.query('SET ROLE ' + quote(ownerRole) + '; ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC')
  const sql = async name => (await readFile(new URL('../../cmd/gregale/templates/data-api/' + name, import.meta.url), 'utf8'))
    .replaceAll('{{schema}}', 'api').replaceAll('{{backlog_max}}', '100000')
  await owner.query(await sql('change-hooks.sql'))
  await owner.query(await sql('storage.sql'))
  await owner.query(await sql('storage-sessions.sql'))
  await owner.query(await sql('storage-cleanup.sql'))
  for (const role of [source, candidate]) {
    await owner.query('GRANT USAGE ON SCHEMA api TO ' + quote(role) +
      '; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO ' + quote(role) +
      '; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA api TO ' + quote(role))
  }

  // Run the exact managed-provider verification predicate against real catalogs.
  const provider = await readFile(new URL('../../pkg/managedpostgres/neon/data_api_credentials.go', import.meta.url), 'utf8')
  const tick = String.fromCharCode(96)
  const guard = provider.split('func verifyDataAPICredential')[1].split('err := tx.QueryRow(ctx, ' + tick)[1].split(tick)[0]
  assert.ok(guard.includes('has_database_privilege($1'))
  const restricted = new URL(url); restricted.username = source; restricted.password = 'test-only'
  assert.equal((await owner.query(guard, [source])).rows[0]['?column?'], false)
  await assert.rejects(checkStorageTable(restricted.toString(), 'api'), /register|registration/i)
  await owner.query('GRANT EXECUTE ON FUNCTION api.gregale_storage_register_key(text,text) TO ' + quote(source))
  assert.equal(await checkStorageTable(restricted.toString(), 'api'), true)
  assert.equal((await owner.query(guard, [source])).rows[0]['?column?'], true)
  console.log(JSON.stringify({ id: 'DAPI032', runtimeMaintenanceEnabled: true, managedCredentialUnsafe: true }))
  await owner.query('REVOKE EXECUTE ON FUNCTION api.gregale_storage_register_key(text,text) FROM ' + quote(source))
  // Isolate resumable storage from the separate maintenance incompatibility.
  await owner.query('DROP FUNCTION api.gregale_storage_register_key(text,text); DROP SCHEMA gregale_storage_maintenance CASCADE')
  assert.equal(await checkStorageTable(restricted.toString(), 'api'), false)

  // A valid resumable-storage login cannot rotate without copies of internal
  // gateway grants, even though ordinary annotated RPC grants are prepared.
  const internalFunctions = (await owner.query("SELECT p.oid::regprocedure::text AS signature FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='api' AND p.proname LIKE 'gregale_storage_%' AND NOT p.prosecdef")).rows
  assert.equal(internalFunctions.length, 6)
  for (const fn of internalFunctions) await owner.query('GRANT EXECUTE ON FUNCTION ' + fn.signature + ' TO ' + quote(source))
  const serving = new pg.Client({ connectionString: restricted.toString() })
  await serving.connect()
  try {
    await serving.query("SELECT set_config('request.jwt.claims',$1,false)", [JSON.stringify({ sub: 'alice', gregale_storage: 'write', gregale_storage_actor: 'a'.repeat(64) })])
    const created = await serving.query("SELECT api.gregale_storage_start($1,'uploads','audit',1,'text/plain',$2) AS session", [randomUUID(), '_gregale/objects/' + randomBytes(16).toString('hex')])
    assert.equal(created.rows[0].session.state, 'uploading')
  } finally { await serving.end() }
  const bootstrap = new pg.Client({ connectionString: url.toString() })
  await bootstrap.connect()
  try {
    await bootstrap.query('SET ROLE ' + quote(ownerRole))
    await installRefreshBarrier(bootstrap, source)
  } finally { await bootstrap.end() }
  await manageRefreshRotation(owner, 'enroll', source, binding)
  const preflight = await preflightRefreshRotation(owner, source, binding)
  assert.equal(preflight.ready, true, JSON.stringify(preflight))
  await assert.rejects(owner.query('SELECT gregale_data_api.prepare_refresh_member($1::uuid,$2,$3::name)', [binding, scope, candidate]),
    error => error.code === '55000' && error.message === 'data_api_refresh_preparation_conflict')
  for (const fn of internalFunctions) await owner.query('GRANT EXECUTE ON FUNCTION ' + fn.signature + ' TO ' + quote(candidate))
  await owner.query('SELECT gregale_data_api.prepare_refresh_member($1::uuid,$2,$3::name)', [binding, scope, candidate])
  console.log(JSON.stringify({ id: 'DAPI033', sourceSessionAccepted: true, preflightReady: true, rotationWithoutInternalGrants: '55000', temporaryControlWithGrants: 'succeeded' }))

  await owner.query("CREATE TABLE api.precision(id bigint PRIMARY KEY, amount numeric); SELECT gregale_hooks.enable('api.precision'); INSERT INTO api.precision VALUES(9007199254740993,0.123456789012345678901)")
  const raw = (await owner.query("SELECT row_to_json(e)::text AS body FROM api.gregale_change_events e WHERE relation_name='precision'")).rows[0].body
  assert.ok(raw.includes('9007199254740993'))
  assert.ok(raw.includes('0.123456789012345678901'))
  const rounded = JSON.stringify(changeEvent(JSON.parse(raw)))
  assert.ok(rounded.includes('9007199254740992'))
  assert.ok(!rounded.includes('0.123456789012345678901'))
  console.log(JSON.stringify({ id: 'DAPI031', postgresPreservesLiterals: true, relayPublicationPreservesLiterals: false }))

  await owner.query('CREATE TABLE api."orders archive"(id integer PRIMARY KEY); SELECT gregale_hooks.enable(\'api."orders archive"\'); INSERT INTO api."orders archive" VALUES(1)')
  const named = (await owner.query("SELECT row_to_json(e)::text AS body FROM api.gregale_change_events e WHERE relation_name='orders archive'")).rows[0].body
  const event = changeEvent(JSON.parse(named))
  const checker = join(temporary, 'validate.go')
  await writeFile(checker, [
    'package main',
    'import ("encoding/json"; "fmt"; "os"; "github.com/onebox-faas/faas/pkg/api")',
    'func main(){var event api.DataAPIChangeEvent; if err:=json.Unmarshal([]byte(os.Args[1]),&event);err!=nil{panic(err)}; if err:=event.Validate();err!=nil{fmt.Print(err.Error())}else{fmt.Print("accepted")}}',
  ].join('\n'))
  const rejected = execFileSync(process.env.DATA_API_GO_BIN || 'go', ['run', checker, JSON.stringify(event)], { encoding: 'utf8', timeout: 120000 }).trim()
  assert.match(rejected, /printable ASCII without spaces/)
  console.log(JSON.stringify({ id: 'DAPI034', captureSucceeded: true, eventType: event.type, platformValidation: rejected }))
} finally {
  await owner?.end()
  await admin.query('DROP DATABASE IF EXISTS ' + quote(database) + ' WITH (FORCE)')
  for (const role of roles.toReversed()) await admin.query('DROP ROLE IF EXISTS ' + quote(role))
  await admin.end()
  await rm(temporary, { recursive: true, force: true })
}
