import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('note change feed SQL and client bounds match the central limits table', async () => {
  const base = new URL('../../', import.meta.url)
  const [limits, sql, client] = await Promise.all(['pkg/api/limits.go', 'cmd/gregale/templates/data-api-starter/migrations/sql/0015_note_change_feed.sql', 'cmd/gregale/templates/data-api-starter/client/src/note-changes.ts'].map(path => readFile(new URL(path, base), 'utf8')))
  for (const [name, field, expression] of [
    ['MaxPageSize', 'maxPageSize', /page_size>(\d+)/],
    ['RetainedEvents', 'retainedEvents', /s\.head\+batch_size-(\d+)/],
    ['MaxCursorBytes', 'maxCursorBytes', /octet_length\(after_cursor\)>(\d+)/],
  ]) {
    const value = Number(limits.match(new RegExp(`DataAPINoteChangeFeed${name}\\s*=\\s*(\\d+)`))?.[1])
    assert.ok(value > 0); assert.equal(Number(sql.match(expression)?.[1]), value)
    assert.equal(Number(client.match(new RegExp(`${field}: (\\d+)`))?.[1]), value)
    if (name === 'RetainedEvents') {
      const occurrences = [...sql.matchAll(/(?:head|batch_size)-(\d+)/g)]
      assert.ok(occurrences.length >= 3)
      for (const occurrence of occurrences) assert.equal(Number(occurrence[1]), value)
    }
  }
})
