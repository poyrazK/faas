import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { pathToFileURL } from 'node:url'
import { join } from 'node:path'
import { verifiedGoTrueMigrations, applyGoTrueMigrations } from '../upstream-migrate.mjs'

async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'gregale-upstream-migration-'))
  t.after(() => rm(root, { recursive: true, force: true }))
  await mkdir(join(root, 'migrations')); await mkdir(join(root, 'overlay'))
  const name = '00_initial.up.sql', raw = 'CREATE TABLE {{ index .Options "Namespace" }}.example(id integer);'
  await writeFile(join(root, 'migrations', name), raw)
  await writeFile(join(root, 'migrations.sha256'), `${createHash('sha256').update(raw).digest('hex')}  ${name}\n`)
  const args = ['migrations/', 'migrations.sha256', 'overlay/'].map(path => new URL(path, pathToFileURL(root + '/')))
  return { root, name, args }
}

test('verified raw migrations use the overlay and a fixed Auth namespace', async t => {
  const f = await fixture(t)
  await writeFile(join(f.root, 'overlay', f.name), 'CREATE TABLE {{index .Options "Namespace" }}.replaced(id integer);')
  assert.deepEqual(await verifiedGoTrueMigrations(...f.args), [{ name: f.name, version: '00', sql: 'CREATE TABLE auth.replaced(id integer);' }])
  await writeFile(join(f.root, 'migrations', f.name), 'modified bytes')
  await assert.rejects(verifiedGoTrueMigrations(...f.args), /gotrue_migration_checksum_mismatch/)
})

test('unknown files, overlays, duplicate versions and template actions fail before SQL', async t => {
  for (const kind of ['extra file', 'extra overlay', 'template', 'duplicate version']) {
    const f = await fixture(t)
    if (kind === 'extra file') await writeFile(join(f.root, 'migrations', 'extra.sql'), 'SELECT 1')
    if (kind === 'extra overlay') await writeFile(join(f.root, 'overlay', '99_foreign.up.sql'), 'SELECT 1')
    if (kind === 'template') await writeFile(join(f.root, 'overlay', f.name), '{{ .Untrusted }}')
    if (kind === 'duplicate version') await writeFile(join(f.root, 'migrations.sha256'), `${'a'.repeat(64)}  00_first.up.sql\n${'b'.repeat(64)}  00_second.up.sql\n`)
    let queries = 0
    await assert.rejects(applyGoTrueMigrations({ query: async () => { queries++; return { rows: [] } } }, ...f.args))
    assert.equal(queries, 0)
  }
})

test('upstream progress uses the existing Auth schema version table and skips applied versions', async t => {
  const f = await fixture(t), log = []
  const client = { query: async (sql, args) => { log.push({ sql, args }); return { rows: sql.startsWith('SELECT version') ? [{ version: '00' }] : [] } } }
  assert.equal(await applyGoTrueMigrations(client, ...f.args), 0)
  assert.ok(log.every(value => !value.sql.includes('public.')))
  assert.ok(!log.some(value => value.sql === 'BEGIN'))
  assert.match(log.at(-1).sql, /pg_advisory_unlock/)
})

test('SQL and version recording commit together; failures roll back and release the lock', async t => {
  for (const fail of [false, true]) {
    const f = await fixture(t), log = []
    const client = { query: async (sql, args) => {
      log.push({ sql, args })
      if (fail && sql.startsWith('INSERT INTO')) throw new Error('fixture failure')
      return { rows: [] }
    } }
    if (fail) await assert.rejects(applyGoTrueMigrations(client, ...f.args), /fixture failure/)
    else assert.equal(await applyGoTrueMigrations(client, ...f.args), 1)
    assert.ok(log.some(value => value.sql === 'SET LOCAL search_path = auth, pg_catalog'))
    assert.ok(log.some(value => value.sql.startsWith('INSERT INTO') && value.args[0] === '00'))
    assert.equal(log.at(-2).sql, fail ? 'ROLLBACK' : 'COMMIT')
    assert.match(log.at(-1).sql, /pg_advisory_unlock/)
  }
})
