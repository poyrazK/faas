import test from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { diagnostics, options } from '../migrations/receipt-diagnostics.mjs'

test('receipt diagnostics accepts only fixed v2 receipt kinds', async () => {
  assert.deepEqual(options([]), { receiptKind: 'all' })
  for (const receiptKind of ['all', 'create', 'mutation', 'batch']) assert.deepEqual(options(['--receipt-kind', receiptKind]), { receiptKind })
  for (const args of [['--apply'], ['--table', 'api.notes'], ['--receipt-kind', 'v1'], ['--receipt-kind'], ['--receipt-kind', 'batch', '--receipt-kind', 'create']]) assert.throws(() => options(args))
  const report = await diagnostics(undefined)
  assert.equal(report.status, 'incomplete'); assert.equal(report.receipts.length, 3)
  for (const row of report.receipts) { assert.equal(row.reason, 'migration_binding_required'); assert.equal(row.totalCount, undefined) }
  assert.throws(() => diagnostics(undefined, { receiptKind: 'constructor' }))
})

test('diagnostics CLI returns structured failures without credentials or driver errors', () => {
  for (const args of [[], ['--apply']]) {
    const result = spawnSync(process.execPath, [fileURLToPath(new URL('../migrations/receipt-diagnostics.mjs', import.meta.url)), ...args], {
      env: { ...process.env, MIGRATION_DATABASE_URL: 'postgres://owner:diagnostic-secret-sentinel@127.0.0.1:1/unavailable' }, encoding: 'utf8', timeout: 10000,
    })
    assert.equal(result.status, 1)
    assert.ok(['error', 'incomplete'].includes(JSON.parse(result.stdout).status))
    assert.doesNotMatch(result.stdout + result.stderr, /diagnostic-secret-sentinel|postgres:\/\/|ECONNREFUSED|node_modules/)
    assert.equal(result.stderr, '')
  }
})
