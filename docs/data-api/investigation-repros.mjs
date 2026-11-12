// Manual evidence for investigation-findings.md at revision 2616a7170.
// Assertions record existing defects; this is not an acceptance gate. A fix
// should invalidate its assertion and add the corresponding regression test.
// Requires Node 24+ and the SDK/runtime development dependencies. Optional:
// GREGALE_DATA_API_REVIEW_DEPS=/path/to/another/checkout reuses installed deps.
// This script uses fake transports/clocks, touches no database, and cleans its
// own temporary compilation directory. DAPI014 covers normalization/decoding,
// not a PostgreSQL integration; DAPI004/005 are documentation/review findings.
import assert from 'node:assert/strict'
import http from 'node:http'
import {getEventListeners} from 'node:events'
import {mkdir,mkdtemp,readFile,writeFile,rm} from 'node:fs/promises'
import {createRequire,registerHooks} from 'node:module'
import {fileURLToPath,pathToFileURL} from 'node:url'
import {join,resolve} from 'node:path'
import {tmpdir} from 'node:os'
const root=fileURLToPath(new URL('../../',import.meta.url))
const dependencies=process.env.GREGALE_DATA_API_REVIEW_DEPS ? resolve(process.env.GREGALE_DATA_API_REVIEW_DEPS) : root
const sdkRequire=createRequire(join(dependencies,'sdk/data/package.json'))
const runtimeRequire=createRequire(join(dependencies,'cmd/gregale/templates/data-api/package.json'))
registerHooks({resolve(specifier,context,nextResolve){
  try{return nextResolve(specifier,context)}catch(error){
    if(!['pg','jose'].includes(specifier))throw error
    return nextResolve(pathToFileURL(runtimeRequire.resolve(specifier)).href,context)
  }
}})
const ts=sdkRequire('typescript')
const out=await mkdtemp(join(tmpdir(),'gregale-data-api-findings-'))
try {
  await mkdir(join(out,'live'))
  await writeFile(join(out,'package.json'),'{"type":"module"}')
  for(const file of ['storage-uploads','numeric','live/client','live/protocol']) {
    const source=await readFile(join(root,'sdk/data/src',file+'.ts'),'utf8')
    await writeFile(join(out,file+'.js'),ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
  }
{
const {storageUploads} = await import(`${out}/storage-uploads.js`)
const ids = []; let callbacks = 0
const files = storageUploads('uploads', async (_target,init) => {
  ids.push(JSON.parse(init.body).id)
  throw new TypeError('Simulated lost reply after the server creates a session')
},()=>{})
for (let i=0;i<2;i++) await assert.rejects(files.uploadResumable('example.txt','same bytes',{onSession:()=>callbacks++}))
assert.equal(new Set(ids).size,2)
assert.equal(callbacks,0)
console.log(JSON.stringify({id:'DAPI002',case:'lost_session_start_response',createdSessions:ids.length,sessionCallbacks:callbacks,differentIds:true}))

const {LiveConnection} = await import(`${out}/live/client.js`)
class Socket {
  readyState=1; listeners=new Map()
  addEventListener(type,fn){if(!this.listeners.has(type))this.listeners.set(type,new Set());this.listeners.get(type).add(fn)}
  removeEventListener(type,fn){this.listeners.get(type)?.delete(fn)}
  send(){}
  close(){this.readyState=3}
  deliver(frame){for(const fn of this.listeners.get('message')??[])fn({data:JSON.stringify(frame)})}
}
const socket=new Socket()
const conn=new LiveConnection({connect:()=>socket})
await new Promise(setImmediate)
const query=conn.query({database:'db',read:'secrets?select=value',token:()=> 'token'})
await new Promise(setImmediate)
socket.deliver({type:'live.rows',subscription:query.id,database:'db',version:1,snapshot:'1:2:',expires:Math.ceil(Date.now()/1000)+60,columns:[{name:'value',type:'text'}],rows:[{id:'one',values:['private row']}],complete:true})
socket.deliver({type:'live.error',subscription:query.id,code:'forbidden',message:'Access revoked',closed:true})
assert.equal(query.getSnapshot().status,'closed')
assert.equal(query.getSnapshot().rows.length,1)
console.log(JSON.stringify({id:'DAPI001',case:'authorization_close_retains_rows',status:query.getSnapshot().status,remainingRows:query.getSnapshot().rows.length}))
conn.close()

const {runDataJobs}=await import(`${root}/cmd/gregale/templates/data-api-teams/workers/data-jobs.mjs`)
let claims=0,advances=0
const job={id:'10000000-0000-4000-8000-000000000001',actor:'alice',lease_token:'20000000-0000-4000-8000-000000000001',state:'running',processed:0,total:20}
for(let i=0;i<3;i++)await assert.rejects(runDataJobs({url:'https://data.example',key:'gdsk_'+'A'.repeat(43),log:()=>{},fetchImpl:async(url)=>{
  if(String(url).endsWith('claim_team_order_job')){claims++;return Response.json([job])}
  advances++;return Response.json({code:'XX000'},{status:500})
}}))
assert.equal(claims,3)
assert.equal(advances,3)
console.log(JSON.stringify({id:'DAPI003',case:'repeated_oldest_job_failure_after_lease_expiry',invocations:3,claims,advances,subsequentClaimsPerInvocation:0}))

const {parseLosslessJSON}=await import(`${out}/numeric.js`)
const decoded=parseLosslessJSON('{"numeric":9007199254740993,"text":"9007199254740993"}')
assert.equal(decoded.numeric,decoded.text)
console.log(JSON.stringify({id:'DAPI006',case:'lossless_round_trip_loses_number_tag',encodedAgain:JSON.stringify(decoded)}))
}
const {LiveConnection,parseDataAPIValue}=await import(`${out}/live/client.js`)
const {parseImportCsv,previewCsvImport}=await import(`${root}/cmd/gregale/templates/data-api/csv-import.mjs`)
const {createAccessSafety,rowSecurityDenial}=await import(`${root}/cmd/gregale/templates/data-api/access.mjs`)
const {createServer}=await import(`${root}/cmd/gregale/templates/data-api/server.mjs`)
const {runtimeConfig}=await import(`${root}/cmd/gregale/templates/data-api/config.mjs`)
const {enrichOpenAPI,normalize}=await import(`${root}/cmd/gregale/templates/data-api/types.mjs`)
const {openAPI31}=await import(`${root}/cmd/gregale/templates/data-api/openapi31.mjs`)
const emit=(id,result)=>console.log(JSON.stringify({id,...result}))
const settle=()=>new Promise(setImmediate)
class Socket {
  readyState=1;sent=[];listeners=new Map()
  addEventListener(type,fn){if(!this.listeners.has(type))this.listeners.set(type,new Set());this.listeners.get(type).add(fn)}
  removeEventListener(type,fn){this.listeners.get(type)?.delete(fn)}
  send(value){this.sent.push(JSON.parse(value))}
  close(){this.readyState=3;for(const fn of [...(this.listeners.get('close')??[])])fn({})}
  deliver(frame){for(const fn of [...(this.listeners.get('message')??[])])fn({data:JSON.stringify(frame)})}
}
class Clock {
  t=1800000000000;timers=new Set()
  now=()=>this.t
  setTimeout=(fn,ms)=>{const h={at:this.t+ms,fn};this.timers.add(h);return h}
  clearTimeout=h=>this.timers.delete(h)
  advance(ms){this.t+=ms;for(;;){const h=[...this.timers].filter(h=>h.at<=this.t).sort((a,b)=>a.at-b.at)[0];if(!h)return;this.timers.delete(h);h.fn()}}
}
const rows=(q,expires)=>({type:'live.rows',subscription:q.id,database:'db',version:1,snapshot:'1:2:',expires,columns:[{name:'value',type:'text'}],rows:[{id:'one',values:['private']}],complete:true})

{
  const spec={kind:'object',fields:[{name:'amount',optional:false,nullable:false,value:{kind:'number'}}]}
  const model={fields:[{name:'amount'}],spec}
  const preview=previewCsvImport(parseImportCsv('amount\n0.10000000000000001\n'),[{field:'amount',empty:'value'}],model)
  const precise=previewCsvImport(parseImportCsv('amount\n0.10000000000000001\n'),[{field:'amount',empty:'value'}],model,'json')
  assert.equal(preview.ok,true);assert.equal(preview.entries[0].json,'{"amount":0.1}')
  assert.equal(precise.ok,false);assert.equal(precise.errors[0].code,'precision_loss')
  emit('DAPI007',{plainAcceptedAndRounded:preview.entries[0].json,jsonMode:precise.errors[0].code})
}
{
  let now=0
  const config={rowSecurity:new Map([['api.notes',true]]),schemas:['api']}
  const safety=createAccessSafety(config,{monotonic:()=>now,read:async()=>{throw Error('simulated catalog-only connection failure')}})
  safety.restored()
  const ready=await safety.ready(100)
  now=3600000;await safety.refresh()
  const denial=rowSecurityDenial({url:'/rest/v1/notes',method:'GET',headers:{}},config,null)
  assert.equal(ready,true);assert.equal(denial,null)
  emit('DAPI008',{readyAfterFailedRestoreCheck:ready,requestDenial:denial,retainedSafeFlag:config.rowSecurity.get('api.notes')})
}
{
  const clock=new Clock(),socket=new Socket(),conn=new LiveConnection({connect:()=>socket,clock})
  await settle();const q=conn.query({sealed:'seal'});await settle();socket.deliver(rows(q,clock.now()/1000+60))
  const already=new AbortController();already.abort()
  let state='pending'
  const pending=q.awaitTxId(99,{signal:already.signal}).then(()=>state='resolved',()=>state='rejected')
  await settle();assert.equal(state,'pending')
  const controller=new AbortController()
  const seen=q.awaitTxId(5,{signal:controller.signal})
  socket.deliver({type:'live.progress',database:'db',snapshot:'1:10:'})
  await seen
  const listeners=getEventListeners(controller.signal,'abort').length
  assert.equal(listeners,1)
  emit('DAPI009',{preAbortedWait:state,listenersAfterResolution:listeners})
  conn.close();await pending
}
{
  const clock=new Clock(),sockets=[]
  const conn=new LiveConnection({clock,reconnectDelayMs:()=>1,connect:()=>{const socket=new Socket();sockets.push(socket);return socket}})
  await settle();let finishOld,calls=0
  const q=conn.query({database:'db',read:'notes?select=id',token:()=>++calls===1?new Promise(resolve=>finishOld=resolve):Promise.resolve('fresh-token')})
  await settle();sockets[0].close();clock.advance(1);await settle()
  finishOld('old-token');await settle()
  const tokens=sockets[1].sent.filter(f=>f.type==='live.subscribe').map(f=>f.token)
  assert.deepEqual(tokens,['fresh-token','old-token'])
  emit('DAPI010',{tokensSentOnReplacementSocket:tokens,sameSubscriptionId:sockets[1].sent.every(f=>f.subscription===q.id)})
  conn.close()
}
{
  const clock=new Clock(),socket=new Socket(),conn=new LiveConnection({connect:()=>socket,clock})
  let calls=0,recovered=false
  await settle();const q=conn.query({database:'db',read:'notes?select=id',token:async()=>{calls++;if(calls>1&&!recovered)throw Error('temporary auth failure');return 'token'}})
  await settle();socket.deliver(rows(q,clock.now()/1000+60))
  clock.advance(45000);await settle()
  clock.advance(15000);socket.deliver({type:'live.error',subscription:q.id,code:'seal_expired',message:'expired',closed:false});await settle()
  recovered=true;clock.advance(120000);await settle()
  assert.equal(calls,3);assert.equal(q.getSnapshot().status,'stale');assert.equal(clock.timers.size,0)
  emit('DAPI011',{credentialCalls:calls,statusAfterProviderRecovery:q.getSnapshot().status,retryTimers:clock.timers.size})
  conn.close()
}
{
  const snapshot={schemas:['api','other'],enums:{},tables:[{schema:'other',name:'notes',view:false,relationships:[],columns:[{name:'id',type:'number',nullable:false,optional:false,insertable:true,updatable:true}]}],functions:[{schema:'api',name:'list_other',args:[],returns:'Database["other"]["Tables"]["notes"][\'Row\'][]'}]}
  const swagger={paths:{'/rpc/list_other':{post:{responses:{200:{description:'OK'}}}}}}
  const doc=openAPI31(enrichOpenAPI(swagger,snapshot,'api'))
  const ref=doc.paths['/rpc/list_other'].post.responses[200].content['application/json'].schema.items.$ref
  assert.equal(ref,'#/components/schemas/other.notes.Row');assert.equal(doc.components.schemas['other.notes.Row'],undefined)
  emit('DAPI012',{danglingReference:ref,targetDefined:false})
}
{
  const doc={definitions:{Row:{type:'object',properties:{$ref:{type:'string'},normal:{type:'string'}}}},paths:{}}
  assert.throws(()=>openAPI31(doc),TypeError)
  const output=openAPI31({definitions:{Row:{type:'object',properties:{'x-nullable':{type:'string'},normal:{type:'string'}}}}})
  assert.equal(output.components.schemas.Row.properties['x-nullable'],undefined)
  emit('DAPI013',{dollarRefColumn:'throws TypeError',xNullableColumn:'silently omitted'})
}
{
  const types=[{id:'23',schema:'pg_catalog',name:'int4',kind:'b',element:'0'},{id:'90001',schema:'api',name:'cents',kind:'d',base:'23',element:'0'},{id:'90002',schema:'api',name:'_cents',kind:'b',element:'90001'}]
  const column={id:'90003',schema:'api',relation:'items',name:'amounts',kind:'r',type:'90002',required:true,defaulted:false,identity:'',insertable:true,updatable:true,generated:false}
  const normalized=normalize([column],types,[],['api'])
  const declared=normalized.tables[0].columns[0].type
  const parsed=parseDataAPIValue('{1,2}','_cents')
  assert.equal(declared,'(number | null)[]');assert.deepEqual(parsed,['1','2'])
  emit('DAPI014',{generatedType:declared,liveDecoded:parsed,level:'normalizer and codec; wire type follows compile.go resultColumns query'})
}
{
  let dispatched=0,releaseSafety,enteredReady
  const safetyResult=new Promise(resolve=>releaseSafety=resolve)
  const readyEntered=new Promise(resolve=>enteredReady=resolve)
  const config=runtimeConfig({DATABASE_URL:'postgres://api:password@127.0.0.1/app',DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'app'})
  config.rowSecurity=new Map([['api.notes',true]])
  const safety=createAccessSafety(config,{read:()=>safetyResult,monotonic:()=>0})
  config.accessSafety=safety
  config.lifecycle={ready:ms=>{enteredReady();return safety.ready(ms)}}
  safety.restored()
  const upstream=http.createServer((_req,res)=>{dispatched++;res.setHeader('Content-Type','application/json');res.end('[{"secret":"unprotected row"}]')})
  await new Promise(resolve=>upstream.listen(0,'127.0.0.1',resolve))
  const gateway=createServer(config,async()=>({sub:'alice',exp:Date.now()/1000+60}),upstream.address().port,upstream.address().port,()=>{})
  await new Promise(resolve=>gateway.listen(0,'127.0.0.1',resolve))
  try {
    const pending=fetch(`http://127.0.0.1:${gateway.address().port}/rest/v1/notes`,{headers:{Authorization:'Bearer test'}})
    await readyEntered
    releaseSafety({rowSecurity:new Map([['api.notes',false]]),unsafePublicRead:new Set(),unsafePublicRpc:new Set()})
    const response=await pending
    await response.arrayBuffer()
    assert.equal(response.status,200);assert.equal(dispatched,1)
    const denial=rowSecurityDenial({url:'/rest/v1/notes',method:'GET',headers:{}},config,null)
    assert.equal(denial.code,'rls_required')
    emit('DAPI015',{firstRequestStatus:response.status,dispatchedAfterSuccessfulUnsafeRefresh:dispatched,currentGuard:denial.code})
  } finally { for(const server of [gateway,upstream]){server.closeAllConnections();await new Promise(resolve=>server.close(resolve))} }
}

} finally { await rm(out,{recursive:true,force:true}) }
