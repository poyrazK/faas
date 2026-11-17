// Fixed private adapter: one admitted SQL attempt, or read-only reconciliation.
// The native journal and customer Task database cannot commit atomically.
import { readFileSync } from 'node:fs';
import { pathToFileURL } from 'node:url';
import { resolve } from 'node:path';
import { createRequire } from 'node:module';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
let observer, runtime;
try {
 const input=JSON.parse(process.argv[1]);
 if (!Array.isArray(input.command)||!input.command.length||input.command.some(arg=>typeof arg!=='string'||!arg||arg.includes('\0'))||!Number.isSafeInteger(input.timeoutMs)||input.timeoutMs<1||!Number.isSafeInteger(input.maxBufferBytes)||input.maxBufferBytes<1||!Number.isSafeInteger(input.workerIDsMax)||input.workerIDsMax<1||typeof input.mutate!=='boolean'||!Array.isArray(input.workerIDs)||!input.workerIDs.length||input.workerIDs.length>input.workerIDsMax||new Set(input.workerIDs).size!==input.workerIDs.length||input.workerIDs.some(id=>typeof id!=='string'||!/^[0-9a-fA-F-]{36}$/.test(id))) throw new Error('Invalid fixed adapter');
 const deadline=Date.now()+input.timeoutMs;
 const require=createRequire(pathToFileURL(resolve('package.json')));
 const pg=require('pg');
 const {resolveMcpTaskSettings}=await import(pathToFileURL(resolve('task-runtime.js')));
 const {mcpTaskHandlers}=await import(pathToFileURL(resolve('tasks.js')));
 const {restoreMcpTasks,quarantineMcpTaskWorkers}=await import(pathToFileURL(resolve('task-release.js')));
 const config=JSON.parse(readFileSync('gregale-mcp.json','utf8'));
 const settings=resolveMcpTaskSettings(config,{role:'observer'});
 observer=new pg.Pool({connectionString:settings.databaseURL,max:2,connectionTimeoutMillis:5000,statement_timeout:10000});
 const env={...process.env};delete env.MCP_TASK_MIGRATION_DATABASE_URL;
 const hook=(phase,body)=>new Promise((resolveHook,reject)=>{
  const timeout=deadline-Date.now();if(timeout<1){reject(new Error('Expired adapter'));return;}
  const child=execFile(input.command[0],[...input.command.slice(1),'--phase',phase],{env,timeout,killSignal:'SIGKILL',maxBuffer:input.maxBufferBytes},(err,stdout)=>{if(err){reject(new Error('Native hook refused'));return;}try{const out=JSON.parse(stdout);if(out[phase==='claim'?'admitted':'observed']!==true)throw new Error('Unconfirmed hook');resolveHook();}catch{reject(new Error('Unconfirmed native hook'));}});
  child.stdin.on('error',()=>{});child.stdin.end(body?JSON.stringify(body):'');
 });
 const report=await restoreMcpTasks({pool:observer,namespace:settings.namespace,handlers:mcpTaskHandlers,restore:async()=>{
  if(input.mutate){
   await hook('claim');
   if(Date.now()>=deadline)throw new Error('Expired admission');
   const worker=resolveMcpTaskSettings(config,{role:'worker'});
   if(worker.namespace!==settings.namespace)throw new Error('Namespace mismatch');
   runtime=new pg.Pool({connectionString:worker.databaseURL,max:2,connectionTimeoutMillis:5000,statement_timeout:Math.min(10000,Math.max(1,deadline-Date.now()))});
   // A SQL error may follow a commit. Inspect fences; never repeat this write.
   try{await quarantineMcpTaskWorkers({pool:runtime,namespace:settings.namespace,workerIDs:input.workerIDs});}catch{}
  }
  const client=await observer.connect();let proof;
  try{
   await client.query('BEGIN READ ONLY');
   await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))',[`gregale_mcp_tasks:execution:${settings.namespace}`]);
   const rows=(await client.query('SELECT worker_id::text,draining FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=ANY($2::uuid[])',[settings.namespace,input.workerIDs])).rows;
   if(rows.length!==input.workerIDs.length||input.workerIDs.some(id=>!rows.some(row=>row.worker_id===id&&row.draining===true)))throw new Error('Candidate fence not observed');
   const observed=(await client.query('SELECT clock_timestamp() AS observed_at')).rows[0].observed_at;
   proof={worker_ids:input.workerIDs,namespace_sha256:createHash('sha256').update(settings.namespace).digest('hex'),draining:true,observed_at:new Date(observed).toISOString()};
   await client.query('COMMIT');
  }catch(error){await client.query('ROLLBACK').catch(()=>{});throw error;}finally{client.release();}
  await hook('observe',proof);
 }});
 if(!report.ok)throw new Error('Quarantine uncertain');
 console.log('{"quarantined":true}');
}catch{process.exitCode=1;}
finally{await runtime?.end().catch(()=>{});await observer?.end().catch(()=>{});}
