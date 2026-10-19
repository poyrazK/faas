import assert from 'node:assert/strict'
import { cp, mkdtemp, rm, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
import test from 'node:test'
import { migrationFiles } from '../cmd/gregale/templates/data-api-starter/migrations/migrate.mjs'
import { rolloutMigrationVersions } from './data-api-bundle-rollout.mjs'

test('packaged rollout migrations extend the actual starter without collisions or rewrites', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-rollout-migrations-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  await cp(new URL('../cmd/gregale/templates/data-api-starter/migrations/sql/', import.meta.url), directory, { recursive: true })
  const url = pathToFileURL(`${directory}/`)
  const original = await migrationFiles(url)
  const versions = await rolloutMigrationVersions(directory)
  const statements = ['ALTER TABLE api.notes ADD COLUMN release_label text;\n', 'ALTER TABLE api.notes ADD COLUMN later_label text;\n']
  for (const [index, version] of versions.entries()) await writeFile(join(directory, `${version}_release_fixture.sql`), statements[index])
  const extended = await migrationFiles(url)
  assert.equal(extended.length, original.length + 2)
  assert.deepEqual(extended.slice(0, -2), original)
  assert.deepEqual(extended.slice(-2).map(file => file.sql), statements)
})

test('custom rollout versions follow the highest migration even when versions have gaps', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'gregale-custom-rollout-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  await writeFile(join(directory, '0001_orders.sql'), 'CREATE SCHEMA api;\n')
  await writeFile(join(directory, '0042_upgrade.sql'), 'SELECT 1;\n')
  assert.deepEqual(await rolloutMigrationVersions(directory), ['0043', '0044'])
})
