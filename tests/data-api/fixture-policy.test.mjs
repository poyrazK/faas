import test from 'node:test'
import assert from 'node:assert/strict'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { createHash } from 'node:crypto'
const runtime = process.env.DATA_API_RUNTIME_DIR
const builder = runtime ? await import(pathToFileURL(join(runtime, 'fixture-builder.mjs'))) : undefined
const upgrade = runtime ? await import(pathToFileURL(join(runtime, 'dev-upgrade.mjs'))) : undefined

test('fixture policy requires explicit safe column scope and validates masks and bounded roots', { skip: !runtime }, () => {
 const table = { table: 'api.notes', columns: ['id', 'subject', 'body'], mask: { subject: { kind: 'hash', group: 'subjects' }, body: { kind: 'replace', value: 'sample' } }, where: { id: 1 }, limit: 1 }
 assert.deepEqual(builder.fixturePolicy(JSON.stringify({ version: 1, tables: [table] })).tables[0], table)
 for (const policy of [null, { version: 2, tables: [table] }, { version: 1, tables: [] }, { version: 1, tables: [table, table] }, { version: 1, tables: [{ ...table, columns: [null] }] }, { version: 1, tables: [{ ...table, columns: ['*'] }] }, { version: 1, tables: [{ ...table, table: 'api.notes;DROP TABLE api.notes' }] }, { version: 1, tables: [{ ...table, where: { undeclared: 'value' } }] }, { version: 1, tables: [{ ...table, mask: { body: { kind: 'automatic' } } }] }, { version: 1, tables: [{ ...table, mask: { subject: { kind: 'hash' } } }] }, { version: 1, tables: [{ ...table, mask: { body: { kind: 'replace', value: {} } } }] }, { version: 1, tables: [{ ...table, limit: 1001 }] }, { version: 1, tables: [{ ...table, sql: 'SELECT *' }] }]) assert.throws(() => builder.fixturePolicy(JSON.stringify(policy)))
 assert.throws(() => builder.fixturePolicy('private-source-value'), error => !error.message.includes('private-source-value'))
})

test('generated fixture bindings detect payload edits and preserve hand-authored fixtures', { skip: !runtime }, () => {
 const payload = "SELECT E'quote''s \\\\ value';\n"
 const header = { source_fingerprint: 'a'.repeat(64), payload_sha256: createHash('sha256').update(payload).digest('hex') }
 const sql = `-- gregale-fixture-v1 ${JSON.stringify(header)}\n${payload}`
 assert.equal(upgrade.fixtureBinding(sql), header.source_fingerprint)
 assert.throws(() => upgrade.fixtureBinding(sql + 'SELECT 2;'), /checksum/)
 assert.equal(upgrade.fixtureBinding('INSERT INTO api.notes(body) VALUES (\'sample\');'), null)
 assert.throws(() => upgrade.fixtureBinding('-- gregale-fixture-v1 sensitive_value\nSELECT 1;'), error => !error.message.includes('sensitive_value'))
 assert.equal(builder.sqlLiteral("quote's\\value\n"), "E'quote''s\\\\value\n'")
})
