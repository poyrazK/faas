import test from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { migrate, migrationFiles } from '../migrations/migrate.mjs'

test('missing migration binding fails without using or printing runtime credentials', async () => {
  await assert.rejects(migrate(''), /MIGRATION_DATABASE_URL is required/)
  const credential = 'postgres://runtime:secret-test-password@localhost/forbidden'
  const result = spawnSync(process.execPath, [fileURLToPath(new URL('../migrations/migrate.mjs', import.meta.url))], {
    encoding: 'utf8', env: { ...process.env, DATABASE_URL: credential, MIGRATION_DATABASE_URL: '' },
  })
  assert.equal(result.status, 1)
  assert.match(result.stderr, /Migration failed/)
  assert.ok(!result.stderr.includes(credential) && !result.stderr.includes('secret-test-password'))
})

test('migration files require positive unique versions and SQL names', async t => {
  const directory = await mkdtemp(join(tmpdir(), 'data-api-migration-files-'))
  t.after(() => rm(directory, { recursive: true, force: true }))
  const url = pathToFileURL(directory + '/')
  await writeFile(join(directory, '0001_first.sql'), 'SELECT 1;')
  await writeFile(join(directory, '0001_duplicate.sql'), 'SELECT 2;')
  await assert.rejects(migrationFiles(url), /positive and unique/)
  await rm(join(directory, '0001_duplicate.sql'))
  await writeFile(join(directory, 'notes.sql'), 'SELECT 2;')
  await assert.rejects(migrationFiles(url), /numbered SQL/)
})
