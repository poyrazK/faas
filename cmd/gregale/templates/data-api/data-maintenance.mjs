import pg from 'pg'
import http from 'node:http'
import https from 'node:https'
import { randomUUID } from 'node:crypto'
import { pathToFileURL } from 'node:url'
import { storageBindings, schemaNames, limits } from './config.mjs'
import { objectURL, signHeaders } from './storage.mjs'

// Mirrors DataAPIMaintenance* in pkg/api/limits.go. All cleanup is opt-in;
// dry-run inventories and checks references without changing SQL or objects.
export const maintenanceLimits = Object.freeze({ graceSeconds: 300, retentionDays: 30, batch: 100, inventory: 10000, responseBytes: 4 << 20, runMs: 110000 })
const keyPattern = /^_gregale\/objects\/[0-9a-f]{32}$/
const quote = name => '"' + name.replaceAll('"','""') + '"'
const xml = value => value.replace(/&(?:amp|lt|gt|quot|apos|#\d+|#x[\da-f]+);/gi, entity => {
  const known = {'&amp;':'&','&lt;':'<','&gt;':'>','&quot;':'"','&apos;':"'"}
  return known[entity] ?? String.fromCodePoint(parseInt(entity.slice(entity[2]==='x'?3:2,-1),entity[2]==='x'?16:10))
})
export function parseObjectInventory(source) {
  if (!/<ListBucketResult(?:\s|>)/.test(source) || /<!DOCTYPE|<!ENTITY/.test(source)) throw new Error('Invalid object inventory')
  const tag = (body,name) => { const found=body.match(new RegExp(`<${name}>([\\s\\S]*?)</${name}>`)); return found ? xml(found[1]) : null }
  if (tag(source,'EncodingType') !== 'url') throw new Error('Object inventory must use URL encoding')
  const objects=[]
  for (const match of source.matchAll(/<Contents>([\s\S]*?)<\/Contents>/g)) {
    const key=decodeURIComponent(tag(match[1],'Key')??''),etag=tag(match[1],'ETag'),modified=tag(match[1],'LastModified')
    if (!etag || /[\r\n]/.test(etag) || !modified || !Number.isFinite(Date.parse(modified))) throw new Error('Invalid inventory object')
    if (keyPattern.test(key)) objects.push({key,etag,modified})
  }
  const continuation=tag(source,'NextContinuationToken')
  if (tag(source,'IsTruncated')==='true' && !continuation) throw new Error('Missing inventory continuation')
  return {objects,continuation}
}
async function s3(binding,method,url,headers,signal) {
  return await new Promise((resolve,reject)=>{
    const signed=signHeaders({method,url,region:binding.region,accessKeyId:binding.accessKeyId,secretAccessKey:binding.secretAccessKey,headers})
    const request=(url.protocol==='https:'?https:http).request(url,{method,headers:signed,signal},response=>{
      let bytes=0;const chunks=[]
      response.on('data',chunk=>{bytes+=chunk.length;if(bytes>maintenanceLimits.responseBytes)response.destroy(new Error('Inventory response exceeds bounds'));else chunks.push(chunk)})
      response.on('error',reject);response.on('end',()=>resolve({status:response.statusCode,headers:response.headers,body:Buffer.concat(chunks).toString('utf8')}))
    });request.on('error',reject);request.end()
  })
}
export function objectMaintenance(binding) {
  return {
    async inventory(continuation,signal) {
      const url=objectURL(binding,'');url.searchParams.set('list-type','2');url.searchParams.set('encoding-type','url');url.searchParams.set('prefix','_gregale/objects/');url.searchParams.set('max-keys','1000')
      if(continuation)url.searchParams.set('continuation-token',continuation)
      const response=await s3(binding,'GET',url,{},signal)
      if(response.status!==200)throw new Error('Object inventory unavailable')
      return parseObjectInventory(response.body)
    },
    async head(key,signal) { const result=await s3(binding,'HEAD',objectURL(binding,key),{},signal);return {status:result.status,etag:result.headers.etag,modified:result.headers['last-modified']} },
    async remove(object,signal) { return (await s3(binding,'DELETE',objectURL(binding,object.key),{'if-match':object.etag},signal)).status },
  }
}
async function transaction(client,work) {
  await client.query('BEGIN ISOLATION LEVEL READ COMMITTED')
  try {
    await client.query("SET LOCAL row_security=off; SET LOCAL lock_timeout='1s'; SET LOCAL statement_timeout='5s'")
    const result=await work();await client.query('COMMIT');return result
  }catch(error){await client.query('ROLLBACK').catch(()=>{});throw error}
}
async function references(client,schema,bucket,key,graceSeconds) {
  const table=quote(schema)
  if((await client.query(`SELECT EXISTS(SELECT 1 FROM ${table}.storage_objects WHERE bucket=$1 AND object_key=$2) AS found`,[bucket,key])).rows[0].found)return true
  const sessions=(await client.query('SELECT to_regclass($1) IS NOT NULL AS present',[`${table}.gregale_storage_uploads`])).rows[0].present
  return sessions && (await client.query(`SELECT EXISTS(SELECT 1 FROM ${table}.gregale_storage_uploads s WHERE bucket=$1 AND state IN ('uploading','finishing') AND expires_at>clock_timestamp()-make_interval(secs=>$3) AND (object_key=$2 OR EXISTS(SELECT 1 FROM jsonb_each(s.parts) p WHERE p.value->>'key'=$2))) AS found`,[bucket,key,graceSeconds])).rows[0].found
}
async function installation(client,schema) {
  const tables=(await client.query(`SELECT c.relname,EXISTS(SELECT 1 FROM pg_trigger t JOIN pg_proc p ON p.oid=t.tgfoid JOIN pg_namespace pn ON pn.oid=p.pronamespace WHERE t.tgrelid=c.oid AND t.tgname='gregale_cleanup_reference_guard' AND t.tgenabled IN ('O','A') AND pn.nspname='gregale_storage_maintenance' AND p.proname='guard_reference') AS guarded FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname IN ('storage_objects','gregale_storage_uploads')`,[schema])).rows
  if(!tables.some(t=>t.relname==='storage_objects') || tables.some(t=>!t.guarded))throw new Error('Install storage SQL --maintenance after optional upload sessions')
  if(!(await client.query("SELECT to_regclass('gregale_storage_maintenance.owned_keys') IS NOT NULL AS installed")).rows[0].installed)throw new Error('Install storage ownership registration before collection')
}
export async function maintainData({connectionString,schemas=['api'],buckets=new Map(),dryRun=true,graceSeconds=maintenanceLimits.graceSeconds,retentionDays=maintenanceLimits.retentionDays,batch=maintenanceLimits.batch,signal=AbortSignal.timeout(maintenanceLimits.runMs),log=()=>{}}) {
  if(!connectionString || !Array.isArray(schemas)||!schemas.length||schemas.some(s=>typeof s!=='string'||!/^[a-z][a-z0-9_]{0,62}$/.test(s))||!Number.isInteger(graceSeconds)||graceSeconds<maintenanceLimits.graceSeconds||graceSeconds>86400||!Number.isInteger(retentionDays)||retentionDays<30||retentionDays>3650||!Number.isInteger(batch)||batch<1||batch>maintenanceLimits.batch)throw new Error('Invalid owner maintenance configuration')
  const client=new pg.Client({connectionString,connectionTimeoutMillis:5000,query_timeout:10000,application_name:'gregale-data-maintenance'})
  const report={dry_run:dryRun,candidates:0,referenced:0,deleted:0,retrying:0,foreign:0,sessions:0,jobs:0,exports:0,receipts:0,inventory_truncated:false}
  const abort=()=>{void client.end().catch(()=>{})}
  try {
    signal.throwIfAborted()
    await client.connect();await client.query('SET row_security=off');signal.throwIfAborted();signal.addEventListener('abort',abort,{once:true})
    const cutoff=(await client.query('SELECT clock_timestamp()-make_interval(secs=>$1) AS cutoff',[graceSeconds])).rows[0].cutoff.getTime()
    const locked=(await client.query("SELECT pg_try_advisory_lock(hashtextextended('gregale:data-maintenance',0)) AS locked")).rows[0].locked
    if(!locked)throw new Error('Owner maintenance already running')
    for(const schema of schemas) {
      signal.throwIfAborted();const table=quote(schema)
      const jobs=(await client.query('SELECT to_regclass($1) IS NOT NULL AS present',[`${table}.team_order_jobs`])).rows[0].present
      if(jobs) {
        const installed=(await client.query("SELECT to_regclass('gregale_job_maintenance.retired_ids') IS NOT NULL AS present")).rows[0].present
        if(!installed)throw new Error('Apply the Teams job-history retention migration before owner maintenance')
        const expired=(await client.query(`SELECT id,kind FROM ${table}.team_order_jobs WHERE state IN ('completed','failed','cancelled') AND completed_at<clock_timestamp()-make_interval(days=>$1) ORDER BY completed_at,id LIMIT $2`,[retentionDays,batch])).rows
        for(const job of expired) {
          signal.throwIfAborted()
          if(!dryRun) {
            const retired=await transaction(client,async()=>{
              await client.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:job-id:'||$1,0))",[job.id])
              const current=(await client.query(`SELECT id FROM ${table}.team_order_jobs WHERE id=$1 AND state IN ('completed','failed','cancelled') AND completed_at<clock_timestamp()-make_interval(days=>$2) FOR UPDATE`,[job.id,retentionDays])).rows
              if(!current.length)return false
              await client.query('INSERT INTO gregale_job_maintenance.retired_ids(id) VALUES($1) ON CONFLICT DO NOTHING',[job.id])
              await client.query(`DELETE FROM ${table}.team_order_jobs WHERE id=$1`,[job.id]);return true
            })
            if(!retired)continue
          }
          report.jobs++;if(job.kind==='export')report.exports++
        }
      }
      if(!buckets.size || schema!==schemas[0])continue
      await installation(client,schema)
      const sessions=(await client.query('SELECT to_regclass($1) IS NOT NULL AS present',[`${table}.gregale_storage_uploads`])).rows[0].present
      if(sessions) {
        const expired=(await client.query(`SELECT id FROM ${table}.gregale_storage_uploads WHERE expires_at<clock_timestamp()-make_interval(days=>$1) ORDER BY expires_at,id LIMIT $2`,[retentionDays,batch])).rows
        for(const session of expired) {
          signal.throwIfAborted()
          if(dryRun){report.sessions++;continue}
          const retired=await transaction(client,async()=>{
            await client.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:upload-id:'||$1||':'||$2,0))",[schema,session.id])
            const current=(await client.query(`SELECT id FROM ${table}.gregale_storage_uploads WHERE id=$1 AND expires_at<clock_timestamp()-make_interval(days=>$2) FOR UPDATE`,[session.id,retentionDays])).rows
            if(!current.length)return false
            await client.query('INSERT INTO gregale_storage_maintenance.retired_upload_ids(relation_schema,id) VALUES($1,$2) ON CONFLICT DO NOTHING',[schema,session.id])
            await client.query(`DELETE FROM ${table}.gregale_storage_uploads WHERE id=$1`,[session.id]);return true
          })
          if(retired)report.sessions++
        }
      }
      for(const [bucket,adapter] of buckets) {
        let token=dryRun?null:(await client.query('SELECT continuation FROM gregale_storage_maintenance.scans WHERE relation_schema=$1 AND bucket=$2',[schema,bucket])).rows[0]?.continuation
        let pages=0
        do {
          signal.throwIfAborted();const page=await adapter.inventory(token,signal)
          if(!Array.isArray(page.objects)||page.objects.length>1000)throw new Error('Invalid bounded inventory')
          for(const object of page.objects) {
            signal.throwIfAborted()
            if(!keyPattern.test(object.key)||!object.etag||!Number.isFinite(Date.parse(object.modified)))throw new Error('Invalid inventory candidate')
            if(Date.parse(object.modified)>cutoff)continue
            const owned=(await client.query('SELECT EXISTS(SELECT 1 FROM gregale_storage_maintenance.owned_keys WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3) AS owned',[schema,bucket,object.key])).rows[0].owned
            if(!owned){report.foreign++;continue}
            if(dryRun) {
              if(await references(client,schema,bucket,object.key,graceSeconds))report.referenced++
              else {report.candidates++;log({event:'data_api_cleanup_candidate',schema,bucket,key:object.key})}
            } else await client.query(`INSERT INTO gregale_storage_maintenance.objects(relation_schema,bucket,object_key,etag,modified_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(relation_schema,bucket,object_key) DO UPDATE SET etag=excluded.etag,modified_at=excluded.modified_at,state='waiting',attempts=0,next_attempt_at=clock_timestamp(),lease=NULL,leased_until=NULL,deleted_at=NULL WHERE (objects.etag,objects.modified_at) IS DISTINCT FROM (excluded.etag,excluded.modified_at) OR objects.state='referenced'`,[schema,bucket,object.key,object.etag,object.modified])
          }
          pages++;token=page.continuation
          if(!dryRun)await client.query('INSERT INTO gregale_storage_maintenance.scans(relation_schema,bucket,continuation) VALUES($1,$2,$3) ON CONFLICT(relation_schema,bucket) DO UPDATE SET continuation=excluded.continuation',[schema,bucket,token])
          if(token && pages*1000>=maintenanceLimits.inventory){report.inventory_truncated=true;break}
        }while(token)
      }
      if(dryRun)continue
      report.receipts+=(await client.query(`WITH expired AS (SELECT relation_schema,bucket,object_key FROM gregale_storage_maintenance.objects WHERE relation_schema=$1 AND (state='deleted' AND deleted_at<clock_timestamp()-make_interval(days=>$2) OR state='referenced' AND modified_at<clock_timestamp()-make_interval(days=>$2)) LIMIT $3), removed AS (DELETE FROM gregale_storage_maintenance.objects o USING expired e WHERE (o.relation_schema,o.bucket,o.object_key)=(e.relation_schema,e.bucket,e.object_key) RETURNING o.relation_schema,o.bucket,o.object_key,o.state), ownership AS (DELETE FROM gregale_storage_maintenance.owned_keys k USING removed r WHERE r.state='deleted' AND (k.relation_schema,k.bucket,k.object_key)=(r.relation_schema,r.bucket,r.object_key) RETURNING k.object_key) SELECT count(*)::int AS n FROM removed`,[schema,retentionDays,batch])).rows[0].n
      const due=(await client.query(`SELECT * FROM gregale_storage_maintenance.objects WHERE relation_schema=$1 AND EXISTS(SELECT 1 FROM gregale_storage_maintenance.owned_keys k WHERE k.relation_schema=objects.relation_schema AND k.bucket=objects.bucket AND k.object_key=objects.object_key) AND modified_at<=clock_timestamp()-make_interval(secs=>$3) AND state IN ('waiting','deleting') AND next_attempt_at<=clock_timestamp() AND (leased_until IS NULL OR leased_until<clock_timestamp()) ORDER BY next_attempt_at,object_key LIMIT $2`,[schema,batch,graceSeconds])).rows
      for(const object of due) {
        signal.throwIfAborted();const adapter=buckets.get(object.bucket);if(!adapter)continue
        let missing=false
        if(!object.etag) {
          let head
          try{head=await adapter.head(object.object_key,AbortSignal.any([signal,AbortSignal.timeout(limits.queryMs)]))}catch{signal.throwIfAborted()}
          if(head?.status===200&&head.etag&&Number.isFinite(Date.parse(head.modified))) {
            await client.query("UPDATE gregale_storage_maintenance.objects SET etag=$4,modified_at=$5,next_attempt_at=greatest(clock_timestamp(),$5::timestamptz+make_interval(secs=>$6)) WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3",[schema,object.bucket,object.object_key,head.etag,new Date(head.modified).toISOString(),graceSeconds]);continue
          }
          if(head?.status===404)missing=true
          else {await client.query("UPDATE gregale_storage_maintenance.objects SET attempts=attempts+1,last_error='head_unavailable',next_attempt_at=clock_timestamp()+interval '1 minute' WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3",[schema,object.bucket,object.object_key]);report.retrying++;continue}
        }
        const lease=randomUUID()
        const claimed=await transaction(client,async()=>{
          await client.query("SELECT pg_advisory_xact_lock(hashtextextended('gregale:storage-key:'||$1||':'||$2||':'||$3,0))",[schema,object.bucket,object.object_key])
          const protectedKey=await references(client,schema,object.bucket,object.object_key,graceSeconds)
          if(protectedKey){await client.query("UPDATE gregale_storage_maintenance.objects SET state='referenced',lease=NULL,leased_until=NULL WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3",[schema,object.bucket,object.object_key]);report.referenced++;return false}
          return (await client.query("UPDATE gregale_storage_maintenance.objects SET state='deleting',lease=$4,leased_until=clock_timestamp()+interval '60 seconds',attempts=attempts+1 WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3 AND (leased_until IS NULL OR leased_until<clock_timestamp())",[schema,object.bucket,object.object_key,lease])).rowCount===1
        })
        if(!claimed)continue
        report.candidates++
        let status=missing?404:0;try{if(!missing)status=await adapter.remove({key:object.object_key,etag:object.etag},AbortSignal.any([signal,AbortSignal.timeout(limits.queryMs)]))}catch{signal.throwIfAborted()}
        if(status===404||status>=200&&status<300) {
          await client.query("UPDATE gregale_storage_maintenance.objects SET state='deleted',deleted_at=clock_timestamp(),lease=NULL,leased_until=NULL,last_error=NULL WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3 AND lease=$4",[schema,object.bucket,object.object_key,lease]);report.deleted++
        }else {
          await client.query("UPDATE gregale_storage_maintenance.objects SET lease=NULL,leased_until=NULL,last_error=$5,next_attempt_at=clock_timestamp()+make_interval(secs=>least(3600,5*power(2,least(attempts,9))::integer)) WHERE relation_schema=$1 AND bucket=$2 AND object_key=$3 AND lease=$4",[schema,object.bucket,object.object_key,lease,status===412?'object_changed':'delete_uncertain']);report.retrying++
        }
      }
    }
    return report
  }finally{signal.removeEventListener('abort',abort);await client.end()}
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href) {
  const execute=process.argv.includes('--execute')
  if(process.argv.slice(2).some(arg=>!['--execute','--dry-run'].includes(arg))||execute&&process.argv.includes('--dry-run'))throw new Error('Use --dry-run or --execute')
  const buckets=new Map([...storageBindings(process.env)].map(([name,binding])=>[name,objectMaintenance(binding)]))
  maintainData({connectionString:process.env.MIGRATION_DATABASE_URL,schemas:schemaNames(process.env.DATA_API_SCHEMAS),buckets,dryRun:!execute,log:record=>console.log(JSON.stringify(record))}).then(report=>console.log(JSON.stringify(report))).catch(()=>{console.error('Owner maintenance failed; check owner access, installation, inventory and retained cleanup receipts.');process.exitCode=1})
}
