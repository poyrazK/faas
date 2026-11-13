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

test('DAPI029 transaction control is rejected before opening a database connection', async t => {
 const {validateMigrationSQL}=await import('../migrations/migrate.mjs')
 for(const command of ['COMMIT','END WORK','ROLLBACK TO foo','ABORT','BEGIN','START TRANSACTION','SAVEPOINT foo','RELEASE foo','PREPARE TRANSACTION \'x\'','SET TRANSACTION READ ONLY','SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY']) {
  assert.throws(()=>validateMigrationSQL(`CREATE TABLE x(id int); /* outer /* inner */ comment */ ${command}; SELECT 1;`),/runner owns/)
 }
 for(const sql of ["SELECT 'COMMIT;';",'SELECT "COMMIT" FROM x;',"DO $body$ BEGIN RAISE NOTICE '$$ COMMIT;'; END $body$;", "CREATE FUNCTION f() RETURNS integer LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 ELSE 2 END; END;", "SELECT E'\\\';COMMIT;';"]) assert.doesNotThrow(()=>validateMigrationSQL(sql))
 const directory=await mkdtemp(join(tmpdir(),'migration-transaction-'));t.after(()=>rm(directory,{recursive:true,force:true}))
 await writeFile(join(directory,'0001_bad.sql'),'CREATE TABLE x(id int); COMMIT;')
 await assert.rejects(migrate('postgres://unreachable@127.0.0.1:1/db',pathToFileURL(directory+'/')),/runner owns/)
})

test('DAPI030 special migration files are rejected promptly and reads honor cancellation', async t => {
 const {symlink}=await import('node:fs/promises')
 const directory=await mkdtemp(join(tmpdir(),'migration-special-'));t.after(()=>rm(directory,{recursive:true,force:true}))
 const path=join(directory,'0001_bad.sql'),url=pathToFileURL(directory+'/')
 assert.equal(spawnSync('mkfifo',[path]).status,0)
 await assert.rejects(migrationFiles(url),/regular files/)
 await rm(path);await mkdirForTest(path)
 await assert.rejects(migrationFiles(url),/regular files/)
 await rm(path,{recursive:true})
 const outside=await mkdtemp(join(tmpdir(),'migration-link-target-'));t.after(()=>rm(outside,{recursive:true,force:true}))
 const target=join(outside,'source.sql');await writeFile(target,'SELECT 1;');await symlink(target,path)
 await assert.rejects(migrationFiles(url),error=>error.code==='ELOOP')
 const abort=new AbortController();abort.abort(new Error('cancelled'))
 await assert.rejects(migrationFiles(url,{signal:abort.signal}),/cancelled/)
 async function mkdirForTest(path){const {mkdir}=await import('node:fs/promises');await mkdir(path)}
})
