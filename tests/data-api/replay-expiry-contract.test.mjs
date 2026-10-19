import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('published v2 SQL windows match the central limits table', async () => {
  const [limits, createSQL, mutationSQL, batchSQL, transactionSQL] = await Promise.all([
    readFile(new URL('../../pkg/api/limits.go', import.meta.url), 'utf8'),
    readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/sql/0012_note_replay_expiry.sql', import.meta.url), 'utf8'),
    readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/sql/0013_note_mutation_receipts.sql', import.meta.url), 'utf8'),
    readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/sql/0014_note_atomic_batches.sql', import.meta.url), 'utf8'),
    readFile(new URL('../../cmd/gregale/templates/data-api/transactions/v2/migration.sql', import.meta.url), 'utf8'),
  ])
  const window = Number(limits.match(/DataAPIReplayV2WindowMS\s*=\s*(\d+)/)?.[1])
  const skew = Number(limits.match(/DataAPIReplayV2FutureSkewMS\s*=\s*(\d+)/)?.[1])
  assert.ok(window > 0); assert.ok(skew > 0 && skew < window)
  for (const [sql, count] of [[createSQL, 2], [mutationSQL, 4], [batchSQL, 2], [transactionSQL, 2]]) {
    const intervals = [...sql.matchAll(/interval '(\d+) milliseconds'/g)].map(match => Number(match[1]))
    assert.equal(intervals.filter(value => value === window).length, count)
    assert.equal(intervals.filter(value => value === skew).length, count)
    assert.match(sql, new RegExp(`\\+ ${window}\\)::double precision`))
  }
})


test('atomic batch SQL and starter limits match the central table', async () => {
  const [limits, sql, client] = await Promise.all([
    readFile(new URL('../../pkg/api/limits.go', import.meta.url), 'utf8'),
    readFile(new URL('../../cmd/gregale/templates/data-api-starter/migrations/sql/0014_note_atomic_batches.sql', import.meta.url), 'utf8'),
    readFile(new URL('../../cmd/gregale/templates/data-api-starter/client/src/note-batch.ts', import.meta.url), 'utf8'),
  ])
  const count = Number(limits.match(/DataAPIAtomicBatchV2MaxOperations\s*=\s*(\d+)/)?.[1])
  const bytes = Number(limits.match(/DataAPIAtomicBatchV2MaxResultBytes\s*=\s*(\d+)/)?.[1])
  assert.ok(count > 0 && bytes > 0)
  assert.equal(Number(client.match(/NOTE_BATCH_V2_MAX_OPERATIONS = (\d+)/)?.[1]), count)
  assert.equal([...sql.matchAll(new RegExp('jsonb_array_length\\(operations\\) NOT BETWEEN 1 AND ' + count, 'g'))].length, 2)
  assert.ok(sql.includes('octet_length(snapshots::text) > ' + bytes))
})
