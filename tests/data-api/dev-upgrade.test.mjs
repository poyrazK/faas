import test from 'node:test'
import assert from 'node:assert/strict'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
const runtime = process.env.DATA_API_RUNTIME_DIR
const upgrade = runtime ? await import(pathToFileURL(join(runtime, 'dev-upgrade.mjs'))) : undefined

test('data assertion manifests reject malformed, ambiguous and empty declarations without leaking SQL', { skip: !runtime }, () => {
  assert.deepEqual(upgrade.parseDataChecks('{"version":1,"checks":[{"name":"preserved","sql":"SELECT true AS passed"}]}'), [{ name: 'preserved', sql: 'SELECT true AS passed' }])
  for (const manifest of [null, [], { version: 2, checks: [] }, { version: 1, checks: [] }, { version: 1, checks: [{ name: 'one', sql: 'SELECT true', typo: true }] }, { version: 1, checks: [{ name: 'one', sql: '' }] }, { version: 1, checks: [{ name: 'one', sql: 'SELECT true' }, { name: 'one', sql: 'SELECT false' }] }, { version: 1, checks: Array.from({ length: 101 }, (_, i) => ({ name: `${i}`, sql: 'SELECT true' })) }]) {
    assert.throws(() => upgrade.parseDataChecks(JSON.stringify(manifest)))
  }
  assert.throws(() => upgrade.parseDataChecks('sensitive_fixture_value'), error => !error.message.includes('sensitive_fixture_value'))
})


test('upgrade preparation validates the source and loads explicit or configured older data', { skip: !runtime }, async t => {
 const directory = await mkdtemp(join(tmpdir(), 'gregale-upgrade-preparation-'))
 t.after(() => rm(directory, { recursive: true, force: true }))
 await writeFile(join(directory, '0001_initial.sql'), 'CREATE SCHEMA api;')
 await writeFile(join(directory, '0002_pending.sql'), 'CREATE TABLE api.records(id integer);')
 const project = { migrations: directory, seedSQL: 'SELECT 1;' }
 const prepared = await upgrade.prepareRehearsal({ from_version: 1 }, project)
 assert.equal(prepared.fixtureSQL, project.seedSQL)
 assert.deepEqual(prepared.pending.map(file => file.version), [2])
 assert.match(prepared.pending[0].checksum, /^[a-f0-9]{64}$/)
 assert.deepEqual((await upgrade.prepareRehearsal({ from_version: 2 }, project)).pending, [])
 // Fixtures live outside the migration directory's SQL-only inventory.
 const outside = await mkdtemp(join(tmpdir(), 'gregale-upgrade-input-'))
 t.after(() => rm(outside, { recursive: true, force: true }))
 const fixturePath = join(outside, 'older.sql')
 await writeFile(fixturePath, 'SELECT 2;')
 assert.equal((await upgrade.prepareRehearsal({ from_version: 1, fixture: fixturePath }, project)).fixtureSQL, 'SELECT 2;')
 await assert.rejects(upgrade.prepareRehearsal({ from_version: 3 }, project), /does not exist/)
 await assert.rejects(upgrade.prepareRehearsal({ from_version: 1 }, { ...project, seedSQL: null }), /requires --fixture/)
 await assert.rejects(upgrade.prepareRehearsal({ from_version: 1 }, { ...project, seedSQL: '  ' }), /requires --fixture/)
 await assert.rejects(upgrade.prepareRehearsal({ fixture: fixturePath }, project), /selected source/)
})
