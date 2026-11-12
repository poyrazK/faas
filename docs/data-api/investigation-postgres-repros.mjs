// Manual PostgreSQL evidence for DAPI027-DAPI029 and further verification of
// DAPI014. Requires Node 24+, PostgreSQL 16+, installed runtime pg and SDK
// typescript dependencies. GREGALE_DATA_API_REVIEW_DEPS may select another
// checkout's installed development dependencies; sources always come from here.
// DATA_API_INVESTIGATION_PG_URL must point at a disposable loopback server's
// postgres database. This script creates/drops only its own uniquely named DB.
// Assertions record current defects, not acceptance criteria for fixes.
import assert from 'node:assert/strict'
import {readFile,mkdir,mkdtemp,writeFile,rm} from 'node:fs/promises'
import {fileURLToPath,pathToFileURL} from 'node:url'
import {join} from 'node:path'
import {tmpdir} from 'node:os'
import {createRequire,registerHooks} from 'node:module'
const root=fileURLToPath(new URL('../../',import.meta.url))
const dependencies=process.env.GREGALE_DATA_API_REVIEW_DEPS ?? root
const require=createRequire(join(dependencies,'sdk/data/package.json'))
const runtimeRequire=createRequire(join(dependencies,'cmd/gregale/templates/data-api/package.json'))
registerHooks({resolve(specifier,context,nextResolve){
 try{return nextResolve(specifier,context)}catch(error){
  if(specifier!=='pg')throw error
  return nextResolve(pathToFileURL(runtimeRequire.resolve(specifier)).href,context)
 }
}})
const {default:pg}=await import('pg')
const {migrate}=await import(pathToFileURL(join(root,'cmd/gregale/templates/data-api-starter/migrations/migrate.mjs')))
if(!process.env.DATA_API_INVESTIGATION_PG_URL)throw new Error('Set DATA_API_INVESTIGATION_PG_URL to a disposable loopback PostgreSQL server')
const source=new URL(process.env.DATA_API_INVESTIGATION_PG_URL)
assert.ok(['postgres:','postgresql:'].includes(source.protocol)&&['127.0.0.1','localhost','[::1]'].includes(source.hostname)&&['','/','/postgres'].includes(source.pathname),'Use the postgres database on a disposable loopback PostgreSQL server')
const db='dapi_review_'+process.pid+'_'+Date.now()
const admin=new pg.Client({connectionString:source.href,connectionTimeoutMillis:2000})
const temporary=await mkdtemp(join(tmpdir(),'gregale-data-api-pg-'))
let c,created=false
try {
 await admin.connect();await admin.query(`CREATE DATABASE ${db}`);created=true
 const url=new URL(source);url.pathname='/'+db
 c=new pg.Client({connectionString:url.href});await c.connect()
 await c.query('CREATE SCHEMA api')
 const hooks=(await readFile(join(root,'cmd/gregale/templates/data-api/change-hooks.sql'),'utf8')).replaceAll('{{schema}}','api').replaceAll('{{backlog_max}}','100000')
 await c.query(hooks)
 await c.query(`CREATE TABLE api.covered (id integer, included_secret text, PRIMARY KEY(id) INCLUDE(included_secret));
 SELECT gregale_hooks.enable('api.covered',payload=>'keys');
 INSERT INTO api.covered VALUES(1,'not-a-primary-key');`)
 const key=(await c.query("SELECT row_key FROM api.gregale_change_events WHERE relation_name='covered'")).rows[0].row_key
 assert.deepEqual(key,{id:1,included_secret:'not-a-primary-key'})
 await assert.rejects(c.query("SELECT gregale_hooks.enable('api.covered',payload=>'keys',exclude=>ARRAY['included_secret'])"),error=>error.code==='22023')
 console.log(JSON.stringify({id:'DAPI027',rowKey:key,nonKeyCannotBeExcluded:true}))
 await c.query(`CREATE TABLE api.renamed (id integer PRIMARY KEY, body text);
 SELECT gregale_hooks.enable('api.renamed',payload=>'keys');
 ALTER TABLE api.renamed RENAME COLUMN id TO item_id;
 INSERT INTO api.renamed VALUES(7,'example');`)
 const stale=(await c.query("SELECT row_key FROM api.gregale_change_events WHERE relation_name='renamed'")).rows[0].row_key
 assert.deepEqual(stale,{id:null})
 console.log(JSON.stringify({id:'DAPI028',actualPrimaryKey:{item_id:7},emittedKey:stale}))
 const files=join(temporary,'migrations');await mkdir(files)
 const file=join(files,'0001_example.sql')
 await writeFile(file,'CREATE TABLE api.rollback_control(id integer); SELECT 1/0;')
 await assert.rejects(migrate(url.href,pathToFileURL(files+'/')),error=>error.code==='22012')
 assert.equal((await c.query("SELECT to_regclass('api.rollback_control') AS value")).rows[0].value,null)
 await writeFile(file,'CREATE TABLE api.committed_without_ledger(id integer); COMMIT; SELECT 1/0;')
 await assert.rejects(migrate(url.href,pathToFileURL(files+'/')),error=>error.code==='22012')
 const kept=(await c.query("SELECT to_regclass('api.committed_without_ledger') IS NOT NULL AS kept,(SELECT count(*) FROM gregale_migrations.applied)::int AS applied")).rows[0]
 assert.equal(kept.kept,true);assert.equal(kept.applied,0)
 console.log(JSON.stringify({id:'DAPI029',migrationFailed:true,tableRemains:kept.kept,ledgerRows:kept.applied,ordinaryFailureRolledBack:true}))
 await c.query('CREATE DOMAIN api.cents AS integer; CREATE TABLE api.domain_arrays(amounts api.cents[]); INSERT INTO api.domain_arrays VALUES(ARRAY[1,2]::api.cents[])')
 const actual=await c.query('SELECT amounts FROM api.domain_arrays')
 const oid=actual.fields[0].dataTypeID
 const wire=(await c.query("SELECT coalesce(b.typname,t.typname)::text AS name FROM pg_type t LEFT JOIN pg_type b ON t.typtype='d' AND b.oid=t.typbasetype WHERE t.oid=$1",[oid])).rows[0].name
 const rest=(await c.query('SELECT to_jsonb(amounts) AS json FROM api.domain_arrays')).rows[0].json
 const out=join(temporary,'sdk');await mkdir(join(out,'live'),{recursive:true});await writeFile(join(out,'package.json'),'{"type":"module"}')
 const ts=require('typescript')
 for(const f of ['numeric','live/client','live/protocol'])await writeFile(join(out,f+'.js'),ts.transpileModule(await readFile(join(root,'sdk/data/src',f+'.ts'),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
 const {parseDataAPIValue}=await import(pathToFileURL(join(out,'live/client.js')))
 const live=parseDataAPIValue(actual.rows[0].amounts,wire)
 assert.deepEqual(rest,[1,2]);assert.deepEqual(live,['1','2'])
 console.log(JSON.stringify({id:'DAPI014',postgresWireType:wire,live,rest,realPostgresVerified:true}))
} finally {
 await c?.end().catch(()=>{})
 try {if(created)await admin.query(`DROP DATABASE ${db} WITH (FORCE)`)}
 finally {await admin.end().catch(()=>{});await rm(temporary,{recursive:true,force:true})}
}
