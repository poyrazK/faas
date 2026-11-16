// Fixed private adapter. Customer SQL is read-only; native parking consumes its
// own approval atomically. Pending repeats never request another park.
import {readFileSync} from 'node:fs';
import {pathToFileURL} from 'node:url';
import {resolve} from 'node:path';
import {createRequire} from 'node:module';
import {createHash} from 'node:crypto';
import {execFile} from 'node:child_process';
let observer;
try {
 const input=JSON.parse(process.argv[1]);
 if(!Array.isArray(input.command)||!input.command.length||input.command.some(arg=>typeof arg!=='string'||!arg||arg.includes('\0'))||!Number.isSafeInteger(input.timeoutMs)||input.timeoutMs<1||!Number.isSafeInteger(input.maxBufferBytes)||input.maxBufferBytes<1||!Number.isSafeInteger(input.workerIDsMax)||input.workerIDsMax<1||!['qualify','park','observe'].includes(input.phase)||!Array.isArray(input.workerIDs)||!input.workerIDs.length||input.workerIDs.length>input.workerIDsMax||new Set(input.workerIDs).size!==input.workerIDs.length||input.workerIDs.some(id=>typeof id!=='string'||!/^[0-9a-fA-F-]{36}$/.test(id)))throw Error('Invalid fixed adapter');
 const deadline=Date.now()+input.timeoutMs;
 const require=createRequire(pathToFileURL(resolve('package.json')));
 const pg=require('pg');
 const {resolveMcpTaskSettings}=await import(pathToFileURL(resolve('task-runtime.js')));
 const {mcpTaskHandlers}=await import(pathToFileURL(resolve('tasks.js')));
 const {restoreMcpTasks}=await import(pathToFileURL(resolve('task-release.js')));
 const settings=resolveMcpTaskSettings(JSON.parse(readFileSync('gregale-mcp.json','utf8')),{role:'observer'});
 observer=new pg.Pool({connectionString:settings.databaseURL,max:2,connectionTimeoutMillis:5000,statement_timeout:10000});
 const env={...process.env};delete env.MCP_TASK_MIGRATION_DATABASE_URL;
 const hook=(phase,body)=>new Promise((resolveHook,reject)=>{
  const timeout=deadline-Date.now();if(timeout<1){reject(Error('Expired adapter'));return;}
  const child=execFile(input.command[0],[...input.command.slice(1),'--phase',phase],{env,timeout,killSignal:'SIGKILL',maxBuffer:input.maxBufferBytes},(err,stdout)=>{if(err){reject(Error('Native hook refused'));return;}try{const out=JSON.parse(stdout);if(out[phase==='qualify'?'qualified':phase==='park'?'parked':'observed']!==true)throw Error('Unconfirmed');resolveHook();}catch{reject(Error('Unconfirmed hook'));}});
  child.stdin.on('error',()=>{});child.stdin.end(JSON.stringify(body));
 });
 const proof=async(stopped)=>{
  const client=await observer.connect();
  try{
   await client.query('BEGIN READ ONLY');
   await client.query('SELECT pg_advisory_xact_lock(hashtextextended($1,0))',[`gregale_mcp_tasks:execution:${settings.namespace}`]);
   const rows=(await client.query('SELECT worker_id::text,draining,expires_at <= clock_timestamp() AS expired FROM gregale_mcp_task_workers WHERE namespace=$1 AND worker_id=ANY($2::uuid[])',[settings.namespace,input.workerIDs])).rows;
   if(!stopped&&(rows.length!==input.workerIDs.length||input.workerIDs.some(id=>!rows.some(row=>row.worker_id===id&&row.draining===true))))throw Error('Candidate fence not observed');
   if(stopped&&rows.some(row=>row.expired!==true))throw Error('Candidate registration live');
   const observed=(await client.query('SELECT clock_timestamp() AS observed_at')).rows[0].observed_at;
   const value={worker_ids:input.workerIDs,namespace_sha256:createHash('sha256').update(settings.namespace).digest('hex'),draining:!stopped,registrations_stopped:stopped,observed_at:new Date(observed).toISOString()};
   await client.query('COMMIT');return value;
  }catch(error){await client.query('ROLLBACK').catch(()=>{});throw error;}finally{client.release();}
 };
 const report=await restoreMcpTasks({pool:observer,namespace:settings.namespace,handlers:mcpTaskHandlers,restore:async()=>{
  if(input.phase==='qualify'){await hook('qualify',await proof(false));return;}
  if(input.phase==='park')await hook('park',await proof(false));
  // A live registration or native instance keeps the operation pending. Native
  // check independently verifies the same park transition and generation.
  while(Date.now()<deadline){try{await hook('observe',await proof(true));return;}catch{if(Date.now()>=deadline)throw Error('Drain pending');await new Promise(r=>setTimeout(r,Math.min(250,Math.max(1,deadline-Date.now()))));}}
  throw Error('Drain pending');
 }});
 if(!report.ok)throw Error('Retirement pending');
 console.log('{"confirmed":true}');
}catch{process.exitCode=1;}
finally{await observer?.end().catch(()=>{});}
