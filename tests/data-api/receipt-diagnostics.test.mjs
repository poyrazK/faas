import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, mkdir, readFile, readdir, writeFile, copyFile, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createRequire } from 'node:module'
import { execFile } from 'node:child_process'
import { promisify } from 'node:util'
import { randomUUID } from 'node:crypto'

const execute = promisify(execFile)
const starter = resolve('cmd/gregale/templates/data-api-starter')
const enabled = Boolean(process.env.DATA_API_TEST_DATABASE_URL && process.env.DATA_API_RUNTIME_DIR && process.env.DATA_API_DEV_CLI)
const keyAt = ms => {
  const time = Math.floor(ms).toString(16).padStart(12, '0')
  return `${time.slice(0, 8)}-${time.slice(8)}-7${randomUUID().slice(15)}`
}

test('owner receipt diagnostics covers actual note and generated transaction contracts', { skip: !enabled, timeout: 90000 }, async t => {
  const root = await mkdtemp(join(tmpdir(), 'gregale-receipt-diagnostics-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  const require = createRequire(join(process.env.DATA_API_RUNTIME_DIR, 'package.json')), pg = require('pg')
  const suffix = `${process.pid}_${Date.now()}`, database = `receipt_diag_${suffix}`
  const ownerRole = `receipt_owner_${suffix}`, migrator = `receipt_migration_${suffix}`, apiRole = `receipt_api_${suffix}`
  const admin = new pg.Client({ connectionString: process.env.DATA_API_TEST_DATABASE_URL }); await admin.connect()
  let owner, lock, serving
  t.after(async () => {
    await lock?.end(); await serving?.end(); await owner?.end()
    await admin.query(`DROP DATABASE IF EXISTS "${database}" WITH (FORCE)`)
    for (const role of [migrator, apiRole, ownerRole]) await admin.query(`DROP ROLE IF EXISTS "${role}"`)
    await admin.end()
  })
  await admin.query(`CREATE ROLE "${ownerRole}" NOLOGIN NOBYPASSRLS;
    CREATE ROLE "${migrator}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only';
    GRANT "${ownerRole}" TO "${migrator}"; ALTER ROLE "${migrator}" SET role TO "${ownerRole}";
    CREATE ROLE "${apiRole}" LOGIN NOINHERIT NOCREATEDB NOCREATEROLE NOBYPASSRLS PASSWORD 'test-only'`)
  await admin.query(`CREATE DATABASE "${database}" OWNER "${ownerRole}"`)
  await admin.query(`REVOKE ALL ON DATABASE "${database}" FROM PUBLIC;
    GRANT CONNECT ON DATABASE "${database}" TO "${migrator}","${apiRole}"`)
  const migrationURL = new URL(process.env.DATA_API_TEST_DATABASE_URL)
  migrationURL.pathname = `/${database}`; migrationURL.username = migrator; migrationURL.password = 'test-only'
  const loginURL = new URL(migrationURL); loginURL.username = apiRole
  owner = new pg.Client({ connectionString: migrationURL.toString() }); await owner.connect()
  assert.equal((await owner.query('SELECT current_user AS name')).rows[0].name, ownerRole)
  await owner.query('REVOKE ALL ON SCHEMA public FROM PUBLIC')
  for (const file of (await readdir(join(starter, 'migrations/sql'))).filter(f => f.endsWith('.sql')).sort()) await owner.query(await readFile(join(starter, 'migrations/sql', file), 'utf8'))
  const notes = join(root, 'notes'); await mkdir(notes)
  for (const file of ['receipt-diagnostics.mjs', 'receipt-diagnostics-core.mjs', 'cleanup-receipts.mjs']) await copyFile(join(starter, 'migrations', file), join(notes, file))
  await writeFile(join(notes, 'package.json'), '{"type":"module"}')
  await symlink(join(process.env.DATA_API_RUNTIME_DIR, 'node_modules'), join(notes, 'node_modules'))
  const output = join(root, 'transaction')
  await execute(process.env.DATA_API_DEV_CLI, ['data-api', 'transaction', 'init', '--name', 'place_order', '--contract', 'v2', '--output', output])
  await symlink(join(process.env.DATA_API_RUNTIME_DIR, 'node_modules'), join(output, 'node_modules'))
  assert.equal(await readFile(join(output, 'receipt-diagnostics-core.mjs'), 'utf8'), await readFile(join(notes, 'receipt-diagnostics-core.mjs'), 'utf8'))
  assert.equal(JSON.parse(await readFile(join(output, 'package.json'), 'utf8')).scripts['receipts:diagnostics'], 'node receipt-diagnostics.mjs')
  await owner.query(await readFile(join(output, 'migration.sql'), 'utf8'))
  const { diagnostics } = await import(pathToFileURL(join(notes, 'receipt-diagnostics.mjs')))
  const { diagnostics: transactionDiagnostics, options: transactionOptions } = await import(pathToFileURL(join(output, 'receipt-diagnostics.mjs')))
  const { cleanup } = await import(pathToFileURL(join(notes, 'cleanup-receipts.mjs')))
  const { cleanup: transactionCleanup } = await import(pathToFileURL(join(output, 'cleanup-receipts.mjs')))
  assert.deepEqual(transactionOptions([]), {}); assert.throws(() => transactionOptions(['--apply']))
  const connectionString = migrationURL.toString()
  const tables = ['note_create_receipts_v2', 'note_mutation_receipts_v2', 'note_batch_receipts_v2', 'place_order_v2_receipts']
  const all = async () => [...(await diagnostics(connectionString)).receipts, ...(await transactionDiagnostics(connectionString)).receipts]

  await t.test('empty tables report genuine zeros and null backlog age', async () => {
    for (const row of await all()) {
      assert.equal(row.status, 'ok', JSON.stringify(row)); assert.equal(row.totalCount, '0')
      assert.equal(row.activeCount, '0'); assert.equal(row.expiredCount, '0')
      assert.equal(row.oldestExpiredAt, null); assert.equal(row.oldestExpiredAgeMs, null)
      assert.ok(BigInt(row.totalBytes) > 0n)
    }
  })
  const now = Number((await owner.query('SELECT floor(extract(epoch FROM clock_timestamp())*1000)::text AS ms')).rows[0].ms)
  const keys = [keyAt(now), keyAt(now - 90000000), keyAt(now - 172800000)]
  for (const table of tables) {
    for (let i = 0; i < keys.length; i++) {
      if (table === 'place_order_v2_receipts') await owner.query(`INSERT INTO api.${table}(tenant_id,created_by,request_key,request,response) VALUES($1,$2,$3,'[]',$4)`, [i % 2 ? 'alpha' : 'beta', `private-actor-${i}`, keys[i], { secret: 'private-payload-sentinel' }])
      else await owner.query(`INSERT INTO api.${table}(subject,request_key,request,response) VALUES($1,$2,$3,$4)`, [`private-actor-${i}`, keys[i], JSON.stringify(table.includes('batch') ? [] : {}), JSON.stringify(table.includes('batch') ? [{ secret: 'private-payload-sentinel' }] : { secret: 'private-payload-sentinel' })])
    }
  }
  const fingerprints = async () => {
    const result = []
    for (const table of tables) result.push((await owner.query(`SELECT md5(jsonb_agg(to_jsonb(r) ORDER BY request_key)::text) AS digest FROM api.${table} r`)).rows[0].digest)
    return result
  }
  const original = await fingerprints()

  await t.test('cross-identity counts, ages and storage are exact and disclose only aggregates', async () => {
    const report = await diagnostics(connectionString); assert.equal(report.version, 1); assert.equal(report.status, 'ok')
    assert.equal(report.receipts.length, 3)
    const rows = [...report.receipts, ...(await transactionDiagnostics(connectionString)).receipts]
    for (const row of rows) {
      assert.equal(row.status, 'ok'); assert.equal(row.totalCount, '3'); assert.equal(row.activeCount, '1'); assert.equal(row.expiredCount, '2')
      assert.equal(row.oldestExpiredAt, new Date(now - 86400000).toISOString())
      assert.equal(BigInt(row.oldestExpiredAgeMs), BigInt(Date.parse(row.observedAt) - Date.parse(row.oldestExpiredAt)))
      assert.equal(BigInt(row.totalBytes), BigInt(row.tableBytes) + BigInt(row.indexBytes))
      const expected = (await owner.query('SELECT pg_table_size($1::regclass)::text AS bytes', [row.table])).rows[0].bytes
      assert.equal(row.tableBytes, expected)
    }
    assert.doesNotMatch(JSON.stringify(rows), /private-actor|private-payload|request_key|test-only|postgres:\/\//)
    assert.deepEqual(await fingerprints(), original, 'diagnostics must leave every receipt intact')
    const cli = await execute(process.execPath, [join(output, 'receipt-diagnostics.mjs')], { env: { ...process.env, MIGRATION_DATABASE_URL: connectionString } })
    assert.equal(JSON.parse(cli.stdout).status, 'ok'); assert.equal(cli.stderr, '')
  })

  await t.test('serving login and privileged non-owner cannot receive cross-user aggregates', async () => {
    await owner.query(`GRANT USAGE ON SCHEMA api TO "${apiRole}"; GRANT SELECT ON ALL TABLES IN SCHEMA api TO "${apiRole}"`)
    serving = new pg.Client({ connectionString: loginURL.toString() }); await serving.connect()
    assert.deepEqual((await serving.query('SELECT * FROM api.note_create_receipts_v2')).rows, [])
    for (const report of [await diagnostics(loginURL.toString()), await transactionDiagnostics(loginURL.toString())]) {
      assert.equal(report.status, 'incomplete')
      for (const row of report.receipts) { assert.equal(row.reason, 'not_owner'); assert.equal(row.totalCount, undefined) }
    }
    // Even the administrative superuser is refused when it is not the exact owner.
    const adminURL = new URL(process.env.DATA_API_TEST_DATABASE_URL); adminURL.pathname = `/${database}`
    assert.equal((await diagnostics(adminURL.toString(), { receiptKind: 'create' })).receipts[0].reason, 'not_owner')
    await assert.rejects(execute(process.execPath, [join(notes, 'receipt-diagnostics.mjs')], { env: { ...process.env, MIGRATION_DATABASE_URL: loginURL.toString() } }), error => {
      assert.equal(error.code, 1); assert.equal(JSON.parse(error.stdout).status, 'incomplete'); assert.equal(error.stderr, ''); return true
    })
  })

  await t.test('missing and FORCE RLS tables remain unavailable while other contracts are inspected', async () => {
    await owner.query('ALTER TABLE api.note_batch_receipts_v2 RENAME TO hidden_batch')
    try {
      const report = await diagnostics(connectionString)
      assert.equal(report.status, 'incomplete'); assert.deepEqual(report.receipts.map(r => r.status), ['ok', 'ok', 'unavailable'])
      assert.equal(report.receipts[2].reason, 'missing_table'); assert.equal(report.receipts[2].expiredCount, undefined)
    } finally { await owner.query('ALTER TABLE api.hidden_batch RENAME TO note_batch_receipts_v2') }
    await owner.query('ALTER TABLE api.note_batch_receipts_v2 FORCE ROW LEVEL SECURITY')
    try { assert.equal((await diagnostics(connectionString, { receiptKind: 'batch' })).receipts[0].reason, 'unsupported_table') }
    finally { await owner.query('ALTER TABLE api.note_batch_receipts_v2 NO FORCE ROW LEVEL SECURITY') }
  })

  await t.test('DDL contention is bounded and does not hide successful contract reports', async () => {
    lock = new pg.Client({ connectionString }); await lock.connect()
    await lock.query('BEGIN'); await lock.query('LOCK TABLE api.note_mutation_receipts_v2 IN ACCESS EXCLUSIVE MODE')
    try {
      const report = await diagnostics(connectionString)
      assert.equal(report.status, 'incomplete'); assert.equal(report.receipts[1].reason, 'lock_timeout')
      assert.equal(report.receipts[1].activeCount, undefined); assert.equal(report.receipts[0].status, 'ok'); assert.equal(report.receipts[2].status, 'ok')
    } finally { await lock.query('ROLLBACK') }
  })

  await t.test('null expiry and incompatible column types cannot become false zero measurements', async () => {
    await owner.query('ALTER TABLE api.note_batch_receipts_v2 RENAME TO saved_batch')
    try {
      await owner.query('CREATE TABLE api.note_batch_receipts_v2(expires_at timestamptz); INSERT INTO api.note_batch_receipts_v2 VALUES(NULL)')
      assert.equal((await diagnostics(connectionString, { receiptKind: 'batch' })).receipts[0].reason, 'unsupported_table')
      await owner.query('ALTER TABLE api.note_batch_receipts_v2 ALTER COLUMN expires_at TYPE text USING expires_at::text')
      const row = (await diagnostics(connectionString, { receiptKind: 'batch' })).receipts[0]
      assert.equal(row.reason, 'unsupported_table'); assert.equal(row.totalCount, undefined)
    } finally {
      await owner.query('DROP TABLE IF EXISTS api.note_batch_receipts_v2; ALTER TABLE api.saved_batch RENAME TO note_batch_receipts_v2')
    }
  })

  await t.test('real server cancels a delayed aggregate within the configured read-only statement limit', async () => {
    const originalQuery = pg.Client.prototype.query
    let settings
    pg.Client.prototype.query = async function (text, ...args) {
      if (typeof text === 'string' && text.startsWith('WITH observation AS')) {
        settings = (await originalQuery.call(this, "SELECT current_setting('transaction_read_only') AS readonly, current_setting('statement_timeout') AS timeout, current_setting('lock_timeout') AS lock_timeout")).rows[0]
        // Slow the actual aggregate at its observation CTE, retaining its SQL
        // and the production server settings rather than faking a driver error.
        text = text.replace('SELECT statement_timestamp() AS at', 'SELECT statement_timestamp() AS at FROM pg_sleep(10)')
      }
      return originalQuery.call(this, text, ...args)
    }
    try {
      const report = await diagnostics(connectionString, { receiptKind: 'create' })
      assert.equal(report.status, 'incomplete'); assert.equal(report.receipts[0].reason, 'statement_timeout'); assert.equal(report.receipts[0].totalCount, undefined)
      assert.deepEqual(settings, { readonly: 'on', timeout: '5s', lock_timeout: '1s' })
    } finally { pg.Client.prototype.query = originalQuery }
    assert.equal((await diagnostics(connectionString)).status, 'ok')
    assert.deepEqual(await fingerprints(), original)
  })

  await t.test('owner cleanup reduces only expired backlog and retains the live replay window', async () => {
    const before = new Date(now).toISOString()
    for (const receiptKind of ['create', 'mutation', 'batch']) {
      const result = await cleanup(connectionString, { before, contract: 'v2', receiptKind, apply: true })
      assert.equal(result.deleted, '2')
    }
    assert.equal((await transactionCleanup(connectionString, { before, apply: true })).deleted, '2')
    for (const row of await all()) { assert.equal(row.totalCount, '1'); assert.equal(row.activeCount, '1'); assert.equal(row.expiredCount, '0'); assert.equal(row.oldestExpiredAgeMs, null) }
    await assert.rejects(owner.query('DELETE FROM api.note_create_receipts_v2'), error => error.message === 'idempotency_replay_window_open')
  })
})
