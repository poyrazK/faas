import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { MUTATION_JOURNAL_LIMITS } from '../../sdk/data/dist/index.js'

test('published mutation journal bounds match the central limits table', async () => {
  const limits = await readFile(new URL('../../pkg/api/limits.go', import.meta.url), 'utf8')
  const names = { maxRecords: 'MaxRecords', maxBytes: 'MaxBytes', maxEntryBytes: 'MaxEntryBytes', maxSlotBytes: 'MaxSlotBytes', maxDepth: 'MaxDepth' }
  for (const [field, name] of Object.entries(names)) {
    const value = Number(limits.match(new RegExp('DataAPIMutationJournal' + name + '\\s*=\\s*(\\d+)'))?.[1])
    assert.ok(value > 0); assert.equal(MUTATION_JOURNAL_LIMITS[field], value)
  }
})
