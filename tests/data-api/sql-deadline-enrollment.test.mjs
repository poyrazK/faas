import test from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes, randomUUID } from 'node:crypto'
import { createRequire } from 'node:module'
import { pathToFileURL, fileURLToPath } from 'node:url'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'

const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_RUNTIME_DIR)

test('owner explicitly enrolls a binding for deadline grants, checks without writes and withdraws approval', { skip: !enabled, timeout: 30000 }, async t => {
  const root = pathToFileURL(process.env.DATA_API_RUNTIME_DIR + '/')
  const require = createRequire(new URL('package.json', root)), pg = require('pg')
  const { installSQLDeadline, manageSQLDeadlineEnrollment } = await import(new URL('sql-deadline.mjs', root))
  const suffix = randomBytes(16).toString('hex'), database = `deadline_enroll_${suffix}`
  const ownerRole = `gregale_schema_${suffix}`, role = `gregale_api_${randomBytes(20).toString('hex')}`
  const migrationRole = `deadline_migration_${suffix}`
  const scope = randomBytes(20).toString('hex'), binding = randomUUID()
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL }); await admin.connect()
  let owner, login
  t.after(async () => {
    if (owner) { await owner.query('ROLLBACK'); await owner.end() }
    await login?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    await admin.query(`DROP ROLE IF EXISTS "${role}"; DROP ROLE IF EXISTS "${migrationRole}"; DROP ROLE IF EXISTS "${ownerRole}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${ownerRole}" NOLOGIN NOINHERIT;
    COMMENT ON ROLE "${ownerRole}" IS 'gregale:schema-owner:v1';
    CREATE ROLE "${role}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    COMMENT ON ROLE "${role}" IS 'gregale:credential:v1:${scope}:data_api';
    CREATE ROLE "${migrationRole}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    GRANT "${ownerRole}" TO "${migrationRole}";
    ALTER ROLE "${migrationRole}" SET role TO "${ownerRole}"`)
  await admin.query(`CREATE DATABASE "${database}"`)
  const url = new URL(process.env.DATA_API_TEST_DATABASE_URL); url.pathname = `/${database}`
  owner = new pg.Client({ connectionString: url.toString() }); await owner.connect()
  await owner.query(`GRANT CREATE ON DATABASE "${database}" TO "${ownerRole}";
    ALTER SCHEMA public OWNER TO "${ownerRole}"; REVOKE ALL ON SCHEMA public FROM PUBLIC;
    CREATE SCHEMA api AUTHORIZATION "${ownerRole}"; SET ROLE "${ownerRole}"`)
  await installSQLDeadline(owner, role)
  const manage = (action, id = binding, options) => manageSQLDeadlineEnrollment(owner, action, role, id, options)
  await assert.rejects(manage('enroll', 'invalid'), /binding ID/)
  await assert.rejects(manage('enroll', binding, { coordinatedRefresh: true }), /deadline-only/)
  await assert.rejects(manage('check-enrollment'), /table/)
  for (let i = 0; i < 2; i++) assert.equal((await manage('enroll')).enrolled, true)
  const policies = () => owner.query('SELECT * FROM gregale_data_api.deadline_binding_policies').then(result => result.rows)
  const before = await policies()
  assert.equal(before.length, 1); assert.equal(before[0].binding_id, binding); assert.equal(before[0].scope, scope)
  assert.equal((await manage('check-enrollment')).enrolled, true)
  const migrationURL = new URL(url); migrationURL.username = migrationRole; migrationURL.password = 'test-only'
  const commandEnv = { ...process.env, MIGRATION_DATABASE_URL: migrationURL.toString() }; delete commandEnv.DATA_API_COORDINATED_REFRESH
  const cli = await promisify(execFile)(process.execPath, [fileURLToPath(new URL('sql-deadline.mjs', root)), 'check-enrollment', role, binding], { env: commandEnv, timeout: 15000 })
  assert.deepEqual(JSON.parse(cli.stdout), { action: 'check-enrollment', version: 1, enrolled: true, coordinated_refresh: false })
  assert.deepEqual(await policies(), before, 'checking must leave owner approvals unchanged')
  const loginURL = new URL(url); loginURL.username = role; loginURL.password = 'test-only'
  login = new pg.Client({ connectionString: loginURL.toString() }); await login.connect()
  await assert.rejects(login.query('SELECT * FROM gregale_data_api.deadline_binding_policies'), error => error.code === '42501')
  await assert.rejects(manageSQLDeadlineEnrollment(login, 'enroll', role, randomUUID()), /managed schema owner/)
  await owner.query('UPDATE gregale_data_api.deadline_binding_policies SET version=2')
  await assert.rejects(manage('enroll'), /incompatible/)
  assert.equal((await policies())[0].version, 2, 'enrollment cannot overwrite an unknown contract')
  await owner.query('UPDATE gregale_data_api.deadline_binding_policies SET version=1')
  await owner.query(`GRANT SELECT(version) ON gregale_data_api.deadline_binding_policies TO PUBLIC`)
  await assert.rejects(manage('check-enrollment'), /drifted/)
  await owner.query(`REVOKE SELECT(version) ON gregale_data_api.deadline_binding_policies FROM PUBLIC`)
  await owner.query('ALTER TABLE gregale_data_api.deadline_binding_policies ENABLE ROW LEVEL SECURITY')
  await assert.rejects(manage('check-enrollment'), /drifted/)
  await owner.query('ALTER TABLE gregale_data_api.deadline_binding_policies DISABLE ROW LEVEL SECURITY')
  await owner.query(`CREATE TABLE gregale_data_api.refresh_gate(role_name text)`)
  await assert.rejects(manage('enroll'), /without coordinated refresh gates/)
  assert.equal((await manage('unenroll', binding, { coordinatedRefresh: true })).enrolled, false)
  assert.deepEqual(await policies(), [])
  await owner.query('DROP TABLE gregale_data_api.refresh_gate')
  await assert.rejects(manage('check-enrollment'), /absent or incompatible/)
  assert.equal((await manage('enroll')).enrolled, true)
  assert.equal((await manage('unenroll')).enrolled, false)
})
