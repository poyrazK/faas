// Manual evidence for DAPI025, DAPI026, and DAPI030. Requires Node 22+
// and a POSIX mkfifo command. Assertions record defects at revision 2616a7170;
// fixes should invalidate them and add ordinary regression tests.
// Only synthetic timers, loopback HTTP, and temporary files/child processes
// are used. The FIFO child is timeboxed and terminated if its read blocks.
import assert from 'node:assert/strict'
import http from 'node:http'
const root=new URL('../../',import.meta.url)
const {createUsage}=await import(new URL('cmd/gregale/templates/data-api/usage.mjs',root))
const {createHealth}=await import(new URL('cmd/gregale/templates/data-api/health.mjs',root))
const tick=()=>new Promise(setImmediate)
{
 const actualSet=globalThis.setTimeout,actualClear=globalThis.clearTimeout
 const timers=new Set();let identityCalls=0,posts=0,failing=true,usage
 globalThis.setTimeout=(fn,delay)=>{const timer={fn,delay,unref(){return this}};timers.add(timer);return timer}
 globalThis.clearTimeout=timer=>timers.delete(timer)
 try {
  usage=createUsage({identity:'http://127.0.0.1/oidc/token',endpoint:'https://platform.example/usage'},{access:{version:1,schemas:['api']},fetchImpl:async url=>{
   if(url.includes('oidc/token')){identityCalls++;if(failing)throw Error('temporary identity failure');return Response.json({token_type:'Bearer',access_token:'x.'+Buffer.from(JSON.stringify({instance_id:'instance-test'})).toString('base64url')+'.x'})}
   posts++;return Response.json({month_to_date:{month:'2026-11',requests:0,anonymous_requests:0}})
  }})
  const initial=[...timers][0];timers.delete(initial);initial.fn();await tick()
  failing=false
  assert.equal(identityCalls,1);assert.equal(posts,0);assert.equal(timers.size,0)
  const automatic={identityCalls,posts,scheduledRetries:timers.size}
  await usage.report();assert.equal(posts,1)
  console.log(JSON.stringify({id:'DAPI025',automatic,explicitRetrySucceeds:posts===1}))
 } finally {usage?.stop();globalThis.setTimeout=actualSet;globalThis.clearTimeout=actualClear}
}
{
 let firstResponse,firstEntered,probes=0
 const entered=new Promise(resolve=>firstEntered=resolve)
 const admin=http.createServer((req,res)=>{
  if(req.url!=='/ready'){res.end();return}
  probes++;if(probes===1){firstResponse=res;firstEntered();return}
  res.writeHead(503);res.end()
 })
 await new Promise(resolve=>admin.listen(0,'127.0.0.1',resolve))
 try {
  const health=createHealth({poolObservation:async()=>({state:'unknown'})},admin.address().port,admin.address().port)
  const first=health.snapshot(()=>({}));await entered
  health.invalidate()
  const afterInvalidation=health.snapshot(()=>({}));await tick()
  firstResponse.writeHead(200);firstResponse.end()
  await first
  const stale=await afterInvalidation
  assert.equal(stale.ready,true);assert.equal(probes,1)
  health.invalidate();const fresh=await health.snapshot(()=>({}));assert.equal(fresh.ready,false)
  console.log(JSON.stringify({id:'DAPI026',afterInvalidationReady:stale.ready,readyProbesBeforeRecheck:1,freshReady:fresh.ready}))
 } finally {admin.closeAllConnections();await new Promise(resolve=>admin.close(resolve))}
}
{
 const {mkdtemp,mkdir,writeFile,rm}=await import('node:fs/promises')
 const {join}=await import('node:path')
 const {tmpdir}=await import('node:os')
 const {pathToFileURL}=await import('node:url')
 const {execFile}=await import('node:child_process')
 const {promisify}=await import('node:util')
 const execute=promisify(execFile)
 const directory=await mkdtemp(join(tmpdir(),'gregale-migration-fifo-'))
 try {
  const regular=join(directory,'regular'),fifo=join(directory,'fifo')
  await mkdir(regular);await mkdir(fifo)
  await writeFile(join(regular,'0001_example.sql'),'SELECT 1;')
  await execute('mkfifo',[join(fifo,'0001_blocked.sql')])
  const child=join(directory,'probe.mjs')
  await writeFile(child,`import {pathToFileURL} from 'node:url'
const {migrationFiles}=await import(process.argv[2])
const regular=await migrationFiles(pathToFileURL(process.argv[3]+'/'))
let state='pending'
const timer=setTimeout(()=>{console.log(JSON.stringify({state,regularFiles:regular.length}))},250)
try{await migrationFiles(pathToFileURL(process.argv[4]+'/'));state='accepted'}catch{state='rejected'}
clearTimeout(timer);console.log(JSON.stringify({state,regularFiles:regular.length}))
`)
  const module=new URL('cmd/gregale/templates/data-api-starter/migrations/migrate.mjs',root).href
  const answer=await execute(process.execPath,[child,module,regular,fifo],{timeout:1500}).then(result=>({...result,terminated:false}),error=>{
   if(!error.killed||!error.stdout)throw error
   return {stdout:error.stdout,terminated:true}
  })
  const result=JSON.parse(answer.stdout.trim())
  assert.equal(result.regularFiles,1);assert.equal(result.state,'pending')
  assert.equal(answer.terminated,true)
  console.log(JSON.stringify({id:'DAPI030',regularFileRead:true,namedPipeResult:result.state,childHadToBeTerminated:true}))
 }finally{await rm(directory,{recursive:true,force:true})}
}
