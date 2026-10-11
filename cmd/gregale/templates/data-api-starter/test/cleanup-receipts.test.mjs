import test from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { options } from '../migrations/cleanup-receipts.mjs'

test('receipt cleanup defaults to preview and requires explicit bounded UTC options', () => {
  const before = '2026-01-01T00:00:00.000Z'
  assert.deepEqual(options(['--before', before]), { before, apply: false, batchSize: 100, maxBatches: 1 })
  assert.equal(options(['--before', before, '--apply', '--batch-size', '2', '--max-batches', '3']).apply, true)
  for (const args of [[], ['--before', '2026-02-30T00:00:00.000Z'], ['--before', before, '--batch-size', '0'], ['--before', before, '--max-batches', '1.5'], ['--before', before, '--unknown'], ['--before', before, '--before', before], ['--before', before, '--apply', '--apply']]) assert.throws(() => options(args))
})


test('cleanup CLI connection failures keep migration credentials out of output', () => {
  const result = spawnSync(process.execPath, [fileURLToPath(new URL('../migrations/cleanup-receipts.mjs', import.meta.url)), '--before', '2001-01-01T00:00:00.000Z'], {
    env: { ...process.env, MIGRATION_DATABASE_URL: 'postgres://owner:receipt-secret-sentinel@127.0.0.1:1/unavailable' }, encoding: 'utf8', timeout: 5000
  })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /Receipt cleanup failed/)
  assert.doesNotMatch(result.stdout + result.stderr, /receipt-secret-sentinel|postgres:\/\//)
})
