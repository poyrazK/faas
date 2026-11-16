import test from 'node:test'
import assert from 'node:assert/strict'
import {createRequire} from 'node:module'
import {pathToFileURL} from 'node:url'
import {join} from 'node:path'
import {readFile} from 'node:fs/promises'
import {randomUUID} from 'node:crypto'
const runtime=process.env.DATA_API_RUNTIME_DIR
const module=runtime?await import(pathToFileURL(join(runtime,'data-maintenance.mjs'))):null
const pg=runtime?createRequire(join(runtime,'package.json'))('pg'):null
const {maintainData,parseObjectInventory}=module??{}

test('DAPI004 inventory parsing preserves encoded keys and rejects incomplete pages',{skip:!runtime},()=>{
 const key='_gregale/objects/'+'a'.repeat(32)
 const source=`<ListBucketResult><EncodingType>url</EncodingType><IsTruncated>true</IsTruncated><NextContinuationToken>a&amp;b</NextContinuationToken><Contents><Key>${encodeURIComponent(key)}</Key><ETag>&quot;etag&quot;</ETag><LastModified>2026-01-01T00:00:00Z</LastModified></Contents></ListBucketResult>`
 assert.deepEqual(parseObjectInventory(source),{objects:[{key,etag:'"etag"',modified:'2026-01-01T00:00:00Z'}],continuation:'a&b'})
 assert.throws(()=>parseObjectInventory(source.replace('<NextContinuationToken>a&amp;b</NextContinuationToken>','')),/continuation/)
 assert.throws(()=>parseObjectInventory('<!DOCTYPE x>'+source),/Invalid object inventory/)
})

test('DAPI004 owner cleanup protects references, fences races and retries ambiguous deletes', {skip:!runtime||!process.env.DATA_API_TEST_DATABASE_URL},async t=>{
 const admin=new pg.Client({connectionString:process.env.DATA_API_TEST_DATABASE_URL});await admin.connect()
 const suffix=randomUUID().replaceAll('-',''),database='cleanup_'+suffix,role='cleanup_reader_'+suffix
 await admin.query(`CREATE DATABASE "${database}"`)
 const url=new URL(process.env.DATA_API_TEST_DATABASE_URL);url.pathname='/'+database
 const owner=new pg.Client({connectionString:url.toString()});await owner.connect()
 t.after(async()=>{await owner.end();await admin.query(`DROP DATABASE "${database}" WITH (FORCE)`);await admin.query(`DROP ROLE IF EXISTS "${role}"`);await admin.end()})
 await owner.query('CREATE SCHEMA api')
 for(const file of ['storage.sql','storage-sessions.sql','storage-cleanup.sql'])await owner.query((await readFile(new URL('../../cmd/gregale/templates/data-api/'+file,import.meta.url),'utf8')).replaceAll('{{schema}}','api'))
 await owner.query("SELECT set_config('request.jwt.claims',$1,false)",[JSON.stringify({gregale_storage:'write',gregale_storage_actor:'a'.repeat(64),sub:'owner'})])
 const key=letter=>'_gregale/objects/'+letter.repeat(32)
 await owner.query('INSERT INTO api.storage_objects(bucket,name,size,content_type,object_key) VALUES(\'uploads\',\'keep\',1,\'text/plain\',$1)',[key('a')])
 await owner.query(`INSERT INTO api.gregale_storage_uploads(id,actor,bucket,name,size,content_type,object_key,parts) VALUES($1,'actor','uploads','active',1,'text/plain',$2,$3)`,[randomUUID(),key('b'),JSON.stringify({'1':{key:key('c'),size:1}})])
 await owner.query(`INSERT INTO api.gregale_storage_uploads(id,actor,bucket,name,size,content_type,object_key,expires_at) VALUES($1,'actor','uploads','expired',1,'text/plain',$2,clock_timestamp()-interval '40 days')`,[randomUUID(),key('d')])
 await owner.query('CREATE TABLE api.team_order_jobs(id uuid PRIMARY KEY,state text,kind text,completed_at timestamptz,result jsonb)')
 await owner.query(await readFile(new URL('../../cmd/gregale/templates/data-api-teams/migrations/sql/0015_job_history_retention.sql',import.meta.url),'utf8'))
 await owner.query(`INSERT INTO api.team_order_jobs VALUES($1,'completed','export',clock_timestamp()-interval '40 days','{"rows":[{"id":1}],"snapshot_at":"old"}')`,[randomUUID()])
 const old=new Date(Date.now()-600000).toISOString(),objects=['a','b','c','d','e','9'].map(letter=>({key:key(letter),etag:'"etag"',modified:old}))
 objects.push({key:key('f'),etag:'"fresh"',modified:new Date().toISOString()})
 objects.push({key:key('8'),etag:'"foreign"',modified:old})
 for(const letter of ['a','b','c','d','e','f','9'])await owner.query("INSERT INTO gregale_storage_maintenance.owned_keys(relation_schema,bucket,object_key) VALUES('api','uploads',$1)",[key(letter)])
 const removed=[];let lost=true
 const adapter={inventory:async()=>({objects,continuation:null}),remove:async object=>{
  removed.push(object.key)
  if(object.key===key('9'))await assert.rejects(owner.query('INSERT INTO api.storage_objects(bucket,name,size,content_type,object_key) VALUES(\'uploads\',\'race\',1,\'text/plain\',$1)',[object.key]),error=>error.code==='PT409')
  if(object.key===key('e')&&lost){lost=false;throw new Error('delete committed but reply lost')}
  return 204
 }}
 const options={connectionString:url.toString(),buckets:new Map([['uploads',adapter]])}
 const dry=await maintainData(options)
 assert.equal(dry.candidates,3);assert.equal(dry.referenced,3);assert.equal(dry.sessions,1);assert.equal(dry.exports,1);assert.equal(dry.foreign,1);assert.equal(removed.length,0)
 assert.equal((await owner.query('SELECT count(*)::int AS n FROM gregale_storage_maintenance.objects')).rows[0].n,0)
 const first=await maintainData({...options,dryRun:false})
 assert.equal(first.deleted,2);assert.equal(first.retrying,1);assert.equal(first.sessions,1);assert.equal(first.exports,1)
 assert.deepEqual(new Set(removed),new Set(['d','e','9'].map(key)))
 const expiredUpload=(await owner.query('SELECT id FROM gregale_storage_maintenance.retired_upload_ids')).rows[0].id
 await assert.rejects(owner.query("INSERT INTO api.gregale_storage_uploads(id,actor,bucket,name,size,content_type,object_key) VALUES($1,'actor','uploads','old-id',1,'text/plain',$2)",[expiredUpload,key('0')]),error=>error.code==='PT410')
 assert.equal((await owner.query('SELECT count(*)::int AS n FROM api.team_order_jobs')).rows[0].n,0)
 assert.equal((await owner.query('SELECT count(*)::int AS n FROM gregale_job_maintenance.retired_ids')).rows[0].n,1,'compact retired identity prevents replay after history expires')
 const retired=(await owner.query('SELECT id FROM gregale_job_maintenance.retired_ids')).rows[0].id
 await assert.rejects(owner.query("INSERT INTO api.team_order_jobs(id,state,kind,completed_at,result) VALUES($1,'queued','export',null,null)",[retired]),error=>error.code==='PT410')
 await owner.query("UPDATE gregale_storage_maintenance.objects SET next_attempt_at=clock_timestamp()-interval '1 second' WHERE state='deleting'")
 const retried=await maintainData({...options,dryRun:false})
 assert.equal(retried.deleted,1);assert.equal(retried.retrying,0)
 assert.equal((await owner.query("SELECT state FROM gregale_storage_maintenance.objects WHERE object_key=$1",[key('e')])).rows[0].state,'deleted')
 await assert.rejects(owner.query('INSERT INTO api.storage_objects(bucket,name,size,content_type,object_key) VALUES(\'uploads\',\'reuse\',1,\'text/plain\',$1)',[key('e')]),error=>error.code==='PT409')
 // A registered key whose PUT never happened also leaves a bounded receipt.
 await owner.query("SELECT api.gregale_storage_register_key('uploads',$1)",[key('0')])
 await owner.query("UPDATE gregale_storage_maintenance.objects SET modified_at=clock_timestamp()-interval '10 minutes' WHERE object_key=$1",[key('0')])
 adapter.head=async()=>({status:404})
 assert.equal((await maintainData({...options,dryRun:false})).deleted,1)
 assert.ok(!removed.includes(key('0')))
 // A create that began before retirement committed must also see the fence.
 const raceId=randomUUID(),inserter=new pg.Client({connectionString:url.toString()});await inserter.connect()
 await owner.query("INSERT INTO api.team_order_jobs VALUES($1,'completed','export',clock_timestamp()-interval '40 days','{\"rows\":[]}')",[raceId])
 await owner.query('BEGIN');await owner.query('SELECT id FROM api.team_order_jobs WHERE id=$1 FOR UPDATE',[raceId])
 const retiring=maintainData({connectionString:url.toString(),buckets:new Map(),dryRun:false})
 try {
  let fenced=false
  for(let attempt=0;attempt<100;attempt++) {
   const acquired=(await inserter.query("SELECT pg_try_advisory_lock(hashtextextended('gregale:job-id:'||$1,0)) AS acquired",[raceId])).rows[0].acquired
   if(!acquired){fenced=true;break}
   await inserter.query("SELECT pg_advisory_unlock(hashtextextended('gregale:job-id:'||$1,0))",[raceId])
   await new Promise(resolve=>setTimeout(resolve,10))
  }
  assert.equal(fenced,true,'retirement must lock the identity before waiting for its row')
  const creating=inserter.query("INSERT INTO api.team_order_jobs VALUES($1,'queued','export',null,null)",[raceId]).then(()=>null,error=>error)
  await owner.query('COMMIT')
  assert.equal((await creating)?.code,'PT410')
  assert.equal((await retiring).jobs,1)
 }finally{await owner.query('ROLLBACK');await retiring.catch(()=>{});await inserter.end()}
 // Even a mistakenly over-granted maintenance login cannot equate RLS-hidden
 // rows with unreferenced objects: row_security=off makes that query fail.
 await admin.query(`CREATE ROLE "${role}" LOGIN PASSWORD 'test-only'`)
 await owner.query(`GRANT USAGE ON SCHEMA api,gregale_storage_maintenance TO "${role}"; GRANT SELECT ON ALL TABLES IN SCHEMA api,gregale_storage_maintenance TO "${role}"`)
 await owner.query('DROP TABLE api.team_order_jobs')
 const restricted=new URL(url);restricted.username=role;restricted.password='test-only'
 await assert.rejects(maintainData({...options,connectionString:restricted.toString()}),/row-level security/)
})
