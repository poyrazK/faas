import test from 'node:test'
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {readFile,mkdtemp,mkdir,writeFile,rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {pathToFileURL} from 'node:url'
import {randomUUID} from 'node:crypto'
const runtime=process.env.DATA_API_RUNTIME_DIR
const pg=runtime?createRequire(join(runtime,'package.json'))('pg'):null
const {migrate}=runtime?await import(pathToFileURL(join(runtime,'migrate.mjs'))):{}

test('DAPI027–029 catalog capture and migration transaction regressions', {skip:!runtime||!process.env.DATA_API_TEST_DATABASE_URL},async t=>{
 const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 const database='catalog_'+randomUUID().replaceAll('-','');await admin.query(`CREATE DATABASE "${database}"`)
 const url=new URL(process.env.DATA_API_TEST_DATABASE_URL);url.pathname='/'+database
 const client=new pg.Client({connectionString:url.toString()});await client.connect()
 const temporary=await mkdtemp(join(tmpdir(),'data-api-catalog-'))
 t.after(async()=>{await client.end();await admin.query(`DROP DATABASE "${database}" WITH (FORCE)`);await admin.end();await rm(temporary,{recursive:true,force:true})})
 await client.query('CREATE SCHEMA api')
 await client.query((await readFile(new URL('../../cmd/gregale/templates/data-api/change-hooks.sql',import.meta.url),'utf8')).replaceAll('{{schema}}','api').replaceAll('{{backlog_max}}','100000'))
 await client.query(`CREATE TABLE api.covered(id int,included_secret text,PRIMARY KEY(id) INCLUDE(included_secret)); SELECT gregale_hooks.enable('api.covered','keys',ARRAY['included_secret']); INSERT INTO api.covered VALUES(1,'secret');`)
 assert.deepEqual((await client.query("SELECT row_key,old_row,new_row FROM api.gregale_change_events WHERE relation_name='covered'")).rows,[{row_key:{id:1},old_row:null,new_row:null}])
 await client.query(`CREATE TABLE api.renamed(id int PRIMARY KEY,body text);SELECT gregale_hooks.enable('api.renamed','keys');ALTER TABLE api.renamed RENAME COLUMN id TO item_id`)
 await assert.rejects(client.query("INSERT INTO api.renamed VALUES(7,'body')"),error=>error.code==='55000'&&/enable the hook again/.test(error.message))
 assert.equal((await client.query("SELECT count(*)::int AS n FROM api.gregale_change_events WHERE relation_name='renamed'")).rows[0].n,0)
 await client.query("SELECT gregale_hooks.enable('api.renamed','keys');INSERT INTO api.renamed VALUES(7,'body')")
 assert.deepEqual((await client.query("SELECT row_key FROM api.gregale_change_events WHERE relation_name='renamed'")).rows[0].row_key,{item_id:7})
 await client.query('ALTER TABLE api.renamed RENAME TO new_name')
 await assert.rejects(client.query("INSERT INTO api.new_name VALUES(8,'body')"),error=>error.code==='55000')
 await client.query("SELECT gregale_hooks.enable('api.new_name','keys');INSERT INTO api.new_name VALUES(8,'body')")
 assert.deepEqual((await client.query("SELECT row_key FROM api.gregale_change_events WHERE relation_name='new_name'")).rows[0].row_key,{item_id:8})
 await client.query(`CREATE TABLE api.partitioned(id int PRIMARY KEY) PARTITION BY RANGE(id);CREATE TABLE api.leaf PARTITION OF api.partitioned FOR VALUES FROM(0) TO(10);SELECT gregale_hooks.enable('api.partitioned','keys');INSERT INTO api.partitioned VALUES(1)`)
 assert.deepEqual((await client.query("SELECT row_key FROM api.gregale_change_events WHERE relation_name='partitioned'")).rows[0].row_key,{id:1})
 const directory=join(temporary,'sql');await mkdir(directory);const file=join(directory,'0001_example.sql')
 await writeFile(file,'CREATE TABLE api.rollback_control(id integer); SELECT 1/0;')
 await assert.rejects(migrate(url.toString(),pathToFileURL(directory+'/')),error=>error.code==='22012')
 assert.equal((await client.query("SELECT to_regclass('api.rollback_control') AS value")).rows[0].value,null)
 await writeFile(file,'CREATE TABLE api.committed_without_ledger(id integer); COMMIT; SELECT 1/0;')
 await assert.rejects(migrate(url.toString(),pathToFileURL(directory+'/')),/runner owns/)
 assert.equal((await client.query("SELECT to_regclass('api.committed_without_ledger') AS value")).rows[0].value,null)
})
