import test from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { createRequire } from 'node:module'
import { mkdtemp, readFile, writeFile, cp, rm, access, stat } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
const execute = promisify(execFile)
const runtime = process.env.DATA_API_RUNTIME_DIR, starter = process.env.DATA_API_STARTER_DIR, cli = process.env.DATA_API_DEV_CLI
const enabled = Boolean(runtime && starter && cli && process.env.DATA_API_TEST_DATABASE_URL)

test('fixture builder masks related rows, preserves the source, and feeds a bound upgrade rehearsal', { skip: !enabled, timeout: 360000 }, async t => {
 const require = createRequire(join(runtime, 'package.json')), { Client } = require('pg')
 const { migrate } = await import(pathToFileURL(join(runtime, 'migrate.mjs')))
 const { permissions } = await import(pathToFileURL(join(runtime, 'rpc-permissions.mjs')))
 const suffix = randomBytes(8).toString('hex'), database = `fixture_source_${suffix}`, apiRole = `fixture_api_${suffix}`
 const sourceURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); sourceURL.pathname = `/${database}`
 const source = sourceURL.toString()
 const admin = new Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL })
 const directory = await mkdtemp(join(tmpdir(), 'gregale-fixture-acceptance-'))
 t.after(() => rm(directory, { recursive: true, force: true }))
 await admin.connect()
 await admin.query(`CREATE DATABASE "${database}"`)
 let owner
 try {
  await admin.query(`CREATE ROLE "${apiRole}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS`)
  await migrate(source, pathToFileURL(join(starter, 'migrations/sql') + '/'))
  owner = new Client({ connectionString: source }); await owner.connect()
  await owner.query(`REVOKE ALL ON DATABASE "${database}" FROM PUBLIC; GRANT CONNECT ON DATABASE "${database}" TO "${apiRole}"; REVOKE ALL ON SCHEMA public FROM PUBLIC; ALTER ROLE "${apiRole}" SET statement_timeout=15000; ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT SELECT,INSERT,UPDATE,DELETE ON TABLES TO "${apiRole}"; ALTER DEFAULT PRIVILEGES IN SCHEMA api GRANT USAGE,SELECT ON SEQUENCES TO "${apiRole}"; GRANT USAGE ON SCHEMA api TO "${apiRole}"; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA api TO "${apiRole}"; GRANT USAGE,SELECT ON ALL SEQUENCES IN SCHEMA api TO "${apiRole}";`)
  await permissions(source, { role: apiRole, apply: true })
  const privateSubject = 'private-customer-subject', privateBody = "raw-secret-body ' \\ newline\n", safeBody = "sample 'quoted' \\ line\nend"
  await owner.query('INSERT INTO api.notes(id,subject,body,created_at) OVERRIDING SYSTEM VALUE VALUES(10,$1,$2,\'2001-02-03 04:05:06+00\'),(20,\'unselected-user\',\'unselected-secret\',now())', [privateSubject, privateBody])
  await owner.query('INSERT INTO api.comments(id,subject,note_id,body) OVERRIDING SYSTEM VALUE VALUES(100,$1,10,\'private-comment-value\')', [privateSubject])
  await owner.query('INSERT INTO api.note_details(subject,note_id,summary) VALUES($1,10,\'undeclared-sensitive-summary\')', [privateSubject])
  const before = (await owner.query('SELECT id,subject,body,created_at::text FROM api.notes ORDER BY id')).rows
  const sequence = (await owner.query('SELECT last_value::text,is_called FROM api.notes_id_seq')).rows
  const config = join(directory, 'policy.json'), output = join(directory, 'built')
  const policy = { version: 1, tables: [
   { table: 'api.notes', columns: ['id', 'subject', 'body'], where: { id: 10 }, limit: 1, mask: { subject: { kind: 'hash', group: 'subjects' }, body: { kind: 'replace', value: safeBody } } },
   { table: 'api.comments', columns: ['id', 'subject', 'note_id', 'body'], limit: 0, mask: { subject: { group: 'subjects', kind: 'hash' }, body: { kind: 'replace', value: 'sample comment' } } },
  ] }
  await writeFile(config, JSON.stringify(policy))
  const env = { ...process.env, TEST_FIXTURE_DATABASE_URL: source }
  const args = ['--json', 'data-api', 'fixture', 'build', '--config', config, '--output', output, '--database-url-env', 'TEST_FIXTURE_DATABASE_URL', '--api-role', apiRole]
  const result = await execute(cli, args, { env, timeout: 120000 })
  const report = JSON.parse(result.stdout)
  assert.equal(report.status, 'built'); assert.equal(report.rows, 2); assert.match(report.source_fingerprint, /^[a-f0-9]{64}$/)
  const fixture = await readFile(join(output, 'fixture.sql'), 'utf8')
  const assertions = await readFile(join(output, 'data-checks.json'), 'utf8')
  const contract = await readFile(join(output, 'contract.json'), 'utf8')
  const metadata = await readFile(join(output, 'fixture.json'), 'utf8')
  for (const privateValue of [privateSubject, 'raw-secret-body', 'private-comment-value', 'undeclared-sensitive-summary', 'unselected-secret', '2001-02-03', source]) assert.ok(![fixture, assertions, contract, metadata, result.stdout, result.stderr].some(content => content.includes(privateValue)), `leaked ${privateValue}`)
  assert.equal(JSON.parse(metadata).tables.find(table => table.table === 'api.comments').rows, 1)
  assert.ok(!metadata.includes(safeBody)); assert.equal((await stat(join(output, 'fixture.sql'))).mode & 0o777, 0o600)
  assert.deepEqual((await owner.query('SELECT id,subject,body,created_at::text FROM api.notes ORDER BY id')).rows, before)
  assert.deepEqual((await owner.query('SELECT last_value::text,is_called FROM api.notes_id_seq')).rows, sequence)
  const project = join(directory, 'project'); await cp(starter, project, { recursive: true }); await rm(join(project, '.gregale'), { recursive: true, force: true })
  await writeFile(join(project, 'migrations/sql/0017_fixture_upgrade.sql'), 'ALTER TABLE api.notes ADD COLUMN fixture_label text;')
  await execute(cli, ['data-api', 'dev', '--directory', project, '--once', '--port', '0'], { env, timeout: 120000 })
  const types = await readFile(join(project, 'client/src/database.types.ts'), 'utf8')
  const roundTripChecks = join(directory, 'roundtrip-checks.json')
  const checks = JSON.parse(assertions)
  checks.checks.push({ name: 'Mask replacement bytes round-trip', sql: `SELECT count(*)=1 AND bool_and(convert_to(body,'UTF8')=decode('${Buffer.from(safeBody).toString('hex')}','hex')) AS passed FROM api.notes WHERE id=10` })
  await writeFile(roundTripChecks, JSON.stringify(checks))
  const preview = ['--json', 'data-api', 'migration', 'preview', '--directory', project, '--baseline', join(output, 'contract.json'), '--from-version', '16', '--fixture', join(output, 'fixture.sql'), '--data-check', roundTripChecks]
  const upgraded = JSON.parse((await execute(cli, preview, { env, timeout: 120000 })).stdout)
  assert.equal(upgraded.status, 'passed'); assert.equal(upgraded.steps.fixture.status, 'passed'); assert.equal(upgraded.steps.data.status, 'passed')
  assert.equal(upgraded.upgrade.source_fingerprint, report.source_fingerprint)
  assert.equal(upgraded.data_checks.length, 4); for (const check of upgraded.data_checks) assert.equal(check.passed, true)
  assert.equal(await readFile(join(project, 'client/src/database.types.ts'), 'utf8'), types)
  // A generated fixture from a different source is refused before its rows load.
  const wrongFixture = join(directory, 'wrong-source.sql')
  const lines = fixture.split('\n'), header = JSON.parse(lines[0].slice('-- gregale-fixture-v1 '.length)); header.source_fingerprint = '0'.repeat(64)
  await writeFile(wrongFixture, `-- gregale-fixture-v1 ${JSON.stringify(header)}\n${lines.slice(1).join('\n')}`)
  await assert.rejects(execute(cli, preview.map(value => value === join(output, 'fixture.sql') ? wrongFixture : value), { env, timeout: 120000 }), error => {
   const failed = JSON.parse(error.stdout)
   assert.equal(failed.steps.fixture.status, 'failed'); assert.equal(failed.steps.migrations.status, 'not_run')
   assert.equal(failed.steps.cleanup.status, 'passed'); return error.code === 1
  })
  // Contract inspection must restore a preselected source role, preserving RLS.
  const scopedURL = new URL(source); scopedURL.searchParams.set('options', `-c role=${apiRole}`)
  const scopedOutput = join(directory, 'scoped-output')
  await assert.rejects(execute(cli, args.map(value => value === output ? scopedOutput : value), { env: { ...env, TEST_FIXTURE_DATABASE_URL: scopedURL.toString() }, timeout: 120000 }))
  await assert.rejects(access(scopedOutput))
  // Existing outputs remain byte-for-byte intact.
  await assert.rejects(execute(cli, args, { env, timeout: 120000 }))
  assert.equal(await readFile(join(output, 'fixture.sql'), 'utf8'), fixture)
  const failure = async tables => {
   const path = join(directory, 'rejected.json'), rejected = join(directory, `rejected-${randomBytes(4).toString('hex')}`)
   await writeFile(path, JSON.stringify({ version: 1, tables }))
   await assert.rejects(execute(cli, args.map(value => value === config ? path : value === output ? rejected : value), { env, timeout: 120000 }), error => {
    assert.ok(!error.stderr.includes(privateSubject)); assert.ok(!error.stderr.includes(privateBody)); return error.code === 1
   })
   await assert.rejects(access(rejected))
  }
  await failure([{ ...policy.tables[1], limit: 1 }]) // An active undeclared parent cannot be silently dropped.
  await failure([{ ...policy.tables[0], columns: ['id', 'subject'], mask: { subject: { kind: 'hash', group: 'subjects' } } }])
  await failure([policy.tables[0], { ...policy.tables[1], mask: { ...policy.tables[1].mask, subject: { kind: 'hash', group: 'different' } } }])
  // Structured filters cannot become executable SQL.
  await failure([{ ...policy.tables[0], where: { subject: "' OR true --" } }])
  await owner.query('CREATE UNIQUE INDEX fixture_body_unique ON api.notes(body)')
  await failure(policy.tables) // Standalone unique keys cannot use constant replacement masks.
  await owner.query('DROP INDEX api.fixture_body_unique')
  await owner.query('CREATE UNIQUE INDEX fixture_body_partial ON api.notes(body) WHERE id>0')
  await failure(policy.tables)
  await owner.query('DROP INDEX api.fixture_body_partial')
  await owner.query("INSERT INTO api.comments(id,subject,note_id,body) OVERRIDING SYSTEM VALUE SELECT id,$1,10,'fanout' FROM generate_series(2000,3000) id", [privateSubject])
  await failure(policy.tables) // Related fanout must fail instead of silently truncating.
  assert.deepEqual((await owner.query('SELECT id,subject,body,created_at::text FROM api.notes ORDER BY id')).rows, before)
  await owner.query('DELETE FROM api.comments WHERE id BETWEEN 2000 AND 3000')
  // The exported baseline must fit the comparator even when roots are tiny.
  const extraColumns = Array.from({ length: 180 }, (_, index) => `"${'long_fixture_column_'.padEnd(58, 'x')}${String(index).padStart(4, '0')}" integer`).join(',')
  for (let index = 0; index < 50; index++) await owner.query(`CREATE TABLE api.fixture_wide_${index}(id integer PRIMARY KEY,${extraColumns})`)
  const { buildFixture } = await import(pathToFileURL(join(runtime, 'fixture-builder.mjs')))
  await assert.rejects(buildFixture(source, policy, apiRole), /contract exceeds/)
 } finally {
  if (owner) await owner.end()
  await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
  await admin.query(`DROP ROLE IF EXISTS "${apiRole}"`)
  await admin.end()
 }
})
