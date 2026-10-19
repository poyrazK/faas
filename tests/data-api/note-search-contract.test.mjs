import test from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'

test('ranked search bounds match the central limits table in SQL and client', async () => {
  const root = new URL('../../', import.meta.url)
  const [limits, sql, client] = await Promise.all(['pkg/api/limits.go', 'cmd/gregale/templates/data-api-starter/migrations/sql/0016_note_search.sql', 'cmd/gregale/templates/data-api-starter/client/src/note-search.ts'].map(path => readFile(new URL(path, root), 'utf8')))
  for (const [name, field, pattern] of [
    ['MaxQueryBytes', 'maxQueryBytes', /octet_length\(search_text\)>(\d+)/],
    ['MaxTerms', 'maxTerms', /cardinality\(words\)>(\d+)/],
    ['MaxPageSize', 'maxPageSize', /page_size>(\d+)/],
    ['MaxCursorBytes', 'maxCursorBytes', /octet_length\(after_cursor\)>(\d+)/],
  ]) {
    const value = Number(limits.match(new RegExp(`DataAPINoteSearch${name}\\s*=\\s*(\\d+)`))?.[1])
    assert.ok(value > 0); assert.equal(Number(sql.match(pattern)?.[1]), value)
    assert.equal(Number(client.match(new RegExp(`${field}: (\\d+)`))?.[1]), value)
  }
})
