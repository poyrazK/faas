import http from 'node:http';
import {readFile,mkdir,writeFile,rename,unlink} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {randomUUID} from 'node:crypto';
import {pathToFileURL} from 'node:url';
import {once} from 'node:events';
import {csvInputs} from './csv.mjs';
import {createFileLive,rejectionReport} from './file-live.mjs';

const options={timeoutMs:60000,retry:false},validId=v=>typeof v==='string'&&/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/.test(v);
async function body(request){let size=0;const chunks=[];for await(const chunk of request){size+=chunk.length;if(size>1024*1024)throw new Error('request_limit');chunks.push(chunk);}return JSON.parse(Buffer.concat(chunks).toString()||'{}');}
/** Demonstration bearer authentication; replace users with your app session
 * lookup. Every route resolves an owned record before invoking storage/jobs. */
export async function createFileImportServer({client,files,job,directory,users,sdk,browserModule,pipelineConfig,liveConfig}){
  await mkdir(directory,{recursive:true,mode:0o700});const busy=new Set();
  const save=async record=>{const tmp=join(directory,'.'+randomUUID());try{await writeFile(tmp,JSON.stringify(record),{mode:0o600,flag:'wx'});await rename(tmp,join(directory,record.id+'.json'));}finally{await unlink(tmp).catch(()=>{});}};
  const load=async id=>{try{return JSON.parse(await readFile(join(directory,id+'.json'),'utf8'));}catch(e){if(e.code==='ENOENT')return null;throw e;}};
  const recordFor=async event=>{const match=/^imports\/([a-f0-9-]{36})\/input.csv$/.exec(event.key);if(!match||!validId(match[1]))return null;const r=await load(match[1]);return r&&r.key===event.key&&r.session===event.receipt_id&&r.size_bytes===event.size_bytes&&Object.hasOwn(users,r.owner)?r:null;};
  const pipeline=pipelineConfig?sdk.createFilePipeline({name:'csv-files-v1',revision:'1',scope:pipelineConfig.scope,prefix:'imports/',suffix:'/input.csv',contract:pipelineConfig.contract,job,files,jobStore:pipelineConfig.jobStore,store:pipelineConfig.store,
    verify:sdk.createNativeFileVerifier(pipelineConfig.scope,(id,signal)=>client.invocations.getInvocation({id},{timeoutMs:0,signal,retry:false})),
    authorize:async event=>{const r=await recordFor(event);return r?{id:r.owner,source_digest:r.source_digest}:null;},
    map:async(event,bytes)=>{const r=await recordFor(event);if(!r)throw new Error('unowned_file');return csvInputs(new TextDecoder('utf-8',{fatal:true}).decode(bytes),r.id);},
  }):null;
  async function refresh(record){
    if(!pipelineConfig||!record.session)return record;
    const identity=sdk.filePipelineIdentity('csv-files-v1',pipelineConfig.scope,record.session),raw=await pipelineConfig.store.load(identity,new AbortController().signal);
    if(!raw)return record;const r=sdk.parseFilePipelineReceipt(raw);
    if(r.intent.owner_id!==record.owner||r.intent.event.key!==record.key||r.intent.identity!==identity||r.intent.pipeline!=='csv-files-v1'||r.intent.contract_sha256!==pipelineConfig.contract.sha256||['base_url','account_id','app_id','app_slug','bucket'].some(k=>r.intent.scope[k]!==pipelineConfig.scope[k]))throw new Error('receipt_scope_changed');
    record.run=r.checkpoint;record.pipeline_unresolved=!r.checkpoint;return record;
  }
  const live=liveConfig?createFileLive({client,files,job,sdk,load,refresh,users,config:liveConfig}):null;
  const server=http.createServer(async(request,response)=>{
    const send=(status,value)=>{if(response.headersSent)return;response.writeHead(status,{'Content-Type':'application/json','Cache-Control':'no-store'});response.end(JSON.stringify(value));};
    const path=new URL(request.url,'http://localhost').pathname;
    if(request.method==='POST'&&path==='/'&&pipeline){
      // These headers are gateway-owned in a deployed app. CP proof is still
      // required; direct/public caller headers alone cannot authenticate events.
      const scope=pipelineConfig.scope,id=request.headers['x-faas-invocation-id'];
      if(!validId(id)||!['async_invoke','queue','replay'].includes(request.headers['x-faas-invocation-source'])||request.headers['x-faas-app-id']!==scope.app_id||request.headers['x-faas-tenant-id']!==scope.account_id){send(403,{code:'unverified_delivery'});return;}
      try{const value=await body(request),result=await pipeline.handle({body:Buffer.from(JSON.stringify(value)),invocationId:id},{timeoutMs:60000,maxBytes:1024*1024,maxItems:1000});
        if(result.outcome==='unresolved'){send(503,{code:'submission_unresolved',identity:result.receipt.intent.identity,...(result.runId?{run_id:result.runId}:{})});return;}
        if(result.checkpoint){const record=await recordFor(result.receipt.intent.event);if(!record)throw new Error('unowned_file');record.run=result.checkpoint;record.intent=result.receipt.intent.idempotency_key;await save(record);}
        send(200,{outcome:result.outcome});
      }catch(e){send(e.code==='unverified_delivery'||e.code==='scope_mismatch'?403:503,{code:e.code??'pipeline_failed'});}return;
    }
    if(request.method==='GET'&&['/','/file-browser.mjs','/sdk-files.js'].includes(path)){
      const target=path==='/'?'file-index.html':path==='/file-browser.mjs'?'file-browser.mjs':null;
      try{const bytes=await readFile(target?new URL(target,import.meta.url):browserModule);response.writeHead(200,{'Content-Type':path==='/'?'text/html':'text/javascript','Cache-Control':'no-store'});response.end(bytes);}catch{send(500,{code:'example_not_built'});}return;
    }
    // Authored browser modules use only adjacent browser-safe SDK dependencies.
    if(request.method==='GET'&&['live-jobs','live-job-realtime','browser-realtime','realtime-resume','realtime-expiry','realtime-activity'].some(name=>path==='/sdk/'+name+'.js')){
      try{const bytes=await readFile(new URL(path.slice(5),browserModule));response.writeHead(200,{'Content-Type':'text/javascript','Cache-Control':'no-store'});response.end(bytes);}catch{send(404,{code:'not_found'});}return;
    }
    const owner=Object.entries(users).find(([,token])=>request.headers.authorization==='Bearer '+token)?.[0];
    if(!owner){send(401,{code:'unauthorized'});return;}
    const route=/^\/files(?:\/([a-f0-9-]{36})(?:\/([a-z-]+))?)?$/.exec(path);
    if(!route){send(404,{code:'not_found'});return;}
    try{
      if(request.method==='POST'&&!route[1]){
        const input=await body(request);
        if(!Number.isSafeInteger(input.size_bytes)||input.size_bytes<=0||input.size_bytes>1024*1024||!/^[a-f0-9]{64}$/.test(input.source_digest)){send(400,{code:'invalid_file'});return;}
        const id=randomUUID(),record={id,owner,key:'imports/'+id+'/input.csv',size_bytes:input.size_bytes,source_digest:input.source_digest,session:null,run:null,intent:null};
        await save(record);send(201,{id,key:record.key,scope:files.scope});return;
      }
      const id=route[1];if(!validId(id)){send(404,{code:'not_found'});return;}
      const record=await load(id);if(!record||record.owner!==owner){send(404,{code:'not_found'});return;}
      if(request.method==='GET'&&['live-ticket','snapshot'].includes(route[2])){
        if(!live){send(503,{code:'realtime_not_configured'});return;}
        const token=request.headers.authorization;
        const value=route[2]==='live-ticket'?await live.ticket(record):await live.snapshot(id);
        if(!Object.hasOwn(users,owner)||'Bearer '+users[owner]!==token){send(401,{code:'unauthorized'});return;}
        send(200,value);return;
      }
      if(busy.has(id)){send(409,{code:'file_busy'});return;}busy.add(id);
      try{
        const action=route[2],input=request.method==='POST'?await body(request):{};
        const native={slug:files.scope.app_slug,bucket:files.scope.bucket};
        const checkSession=()=>{if(!record.session||input.id!==record.session)throw new Error('invalid_session');};
        if(request.method==='GET'&&!action){send(200,{id,key:record.key,scope:files.scope});return;}
        if(request.method==='POST'&&action==='begin'){
          if(input.key!==record.key||input.size!==record.size_bytes||input.contentType!=='text/csv'||record.run){send(400,{code:'invalid_file'});return;}
          const s=record.session?await client.storage.getObjectMultipartUpload({...native,upload:record.session},options):await client.storage.createObjectMultipartUpload({...native,requestBody:{key:record.key,size_bytes:record.size_bytes,content_type:'text/csv'}},options);
          if(!validId(s.id))throw new Error('invalid_session');record.session=s.id;await save(record);send(200,s);return;
        }
        if(request.method==='POST'&&['session','parts','sign-part','complete','abort'].includes(action)){
          checkSession();const target={...native,upload:record.session};let result;
          if(action==='session')result=await client.storage.getObjectMultipartUpload(target,options);
          if(action==='parts'){
            if(input.marker!==undefined&&(!Number.isInteger(input.marker)||input.marker<1||input.marker>10000))throw new Error('invalid_marker');
            result=await client.storage.listObjectMultipartParts({...target,partNumberMarker:input.marker,limit:1000},options);
          }
          if(action==='sign-part'){
            if(!Number.isInteger(input.part)||input.part<1||input.part>10000||record.run)throw new Error('invalid_part');
            result=await client.storage.signObjectMultipartPart({...target,part:input.part,requestBody:{}},options);
          }
          if(action==='complete'){
            if(!Array.isArray(input.parts)||input.parts.length>10000||input.parts.some((p,i)=>p.part_number!==i+1||typeof p.etag!=='string'||p.etag.length>1024))throw new Error('invalid_parts');
            result=await client.storage.completeObjectMultipartUpload({...target,requestBody:{parts:input.parts}},options);
          }
          if(action==='abort'){await client.storage.abortObjectMultipartUpload(target,options);result={};}
          send(200,result);return;
        }
        if(request.method==='POST'&&action==='start'){
          if(pipeline){await refresh(record);send(202,record.run?{runId:record.run.run_id}:{status:record.pipeline_unresolved?'submission_unresolved':'awaiting_storage_event'});return;}
          if(record.run){send(200,{runId:record.run.run_id});return;}
          if(record.intent){send(409,{code:'submission_outcome_unknown'});return;}
          if(!record.session){send(409,{code:'upload_incomplete'});return;}
          const s=await client.storage.getObjectMultipartUpload({...native,upload:record.session},options);
          if(s.state!=='completed'||s.key!==record.key||s.size_bytes!==record.size_bytes){send(409,{code:'upload_incomplete'});return;}
          const bytes=await files.download(record.key,{...options,maxBytes:record.size_bytes,...(validId(s.version_id)?{versionId:s.version_id}:{})});
          if(bytes.length!==record.size_bytes||await sdk.fileSourceDigest(sdk.blobFileSource(new Blob([bytes])),new AbortController().signal)!==record.source_digest){send(409,{code:'file_changed'});return;}
          const inputs=csvInputs(new TextDecoder('utf-8',{fatal:true}).decode(bytes),id);
          record.intent='file-import:'+id;await save(record);
          const run=await job.submit(inputs,{timeoutMs:60000,maxItems:1000,idempotencyKey:record.intent,parallelism:2,retryMax:0});
          record.run=run.checkpoint;await save(record);send(202,{runId:record.run.run_id});return;
        }
        if(request.method==='GET'&&action==='progress'){
          if(pipeline){await refresh(record);if(!record.run){send(200,{status:record.pipeline_unresolved?'submission_unresolved':'awaiting_storage_event',total:0,succeeded:0,failed:0});return;}}
          if(!record.run){send(409,{code:'job_not_started'});return;}send(200,await job.resume(sdk.parseJobCheckpoint(record.run)).status({timeoutMs:60000}));return;
        }
        if(request.method==='GET'&&action==='rejections'){
          if(pipeline)await refresh(record);
          if(!record.run){send(409,{code:'job_not_started'});return;}
          const run=job.resume(sdk.parseJobCheckpoint(record.run)),progress=await run.status({timeoutMs:60000});
          if(!['succeeded','failed','cancelled','dead_letter'].includes(progress.status)){send(409,{code:'job_incomplete'});return;}
          const report=await rejectionReport({record,run,files});if(report.state!=='ready'){send(409,{code:'result_unavailable'});return;}
          response.writeHead(200,{'Content-Type':'text/csv','Content-Disposition':'attachment; filename="rejections.csv"','Cache-Control':'no-store'});
          response.end(report.bytes);return;
        }
        send(405,{code:'method_not_allowed'});
      }finally{busy.delete(id);}
    }catch(e){send(e.status===403||e.status===404?404:400,{code:e.code??'invalid_file_operation'});}
  });
  server.on('close',()=>live?.close());return server;
}

if(process.argv[1]&&import.meta.url===pathToFileURL(resolve(process.argv[1])).href){
  const sdk=await import(new URL('../../sdk/node/dist/preview.js',import.meta.url));
  const client=sdk.createClient(process.env.FAAS_API,{token:process.env.FAAS_TOKEN});
  const files=client.storage.files({appSlug:process.env.GREGALE_FILE_APP,bucket:process.env.GREGALE_FILE_BUCKET});
  const store=await files.jobStore({timeoutMs:60000});
  const contract=new sdk.JobContract(JSON.parse(await readFile(new URL('contract.json',import.meta.url),'utf8')));
  const users=JSON.parse(process.env.GREGALE_FILE_USERS??'{}');
  if(!Object.keys(users).length||new Set(Object.values(users)).size!==Object.keys(users).length||Object.values(users).some(t=>typeof t!=='string'||t.length<16))throw new Error('Configure distinct application tokens of at least 16 characters');
  const {default:pg}=await import('pg');const pool=new pg.Pool({connectionString:process.env.DATABASE_URL,statement_timeout:5000});
  await pool.query('SELECT identity FROM public.gregale_file_pipeline_receipts LIMIT 0');
  const app=await client.apps.getApp({slug:files.scope.app_slug}),account=process.env.GREGALE_FILE_ACCOUNT_ID??(await client.account.getAccount()).id;
  const scope={...files.scope,account_id:account,app_id:app.id};
  const pipelineConfig={scope,contract,jobStore:store,store:sdk.postgresFilePipelineStore((sql,values)=>pool.query(sql,values))};
  const liveConfig={endpointId:process.env.GREGALE_REALTIME_ENDPOINT_ID,url:process.env.GREGALE_REALTIME_URL};
  if(!/^[a-f0-9]{32}$/.test(liveConfig.endpointId??'')||!liveConfig.url?.startsWith('wss://'))throw new Error('Configure GREGALE_REALTIME_ENDPOINT_ID and GREGALE_REALTIME_URL for private retained subscriptions');
  const server=await createFileImportServer({client,files,job:client.jobs.defineJob(contract,{store}),directory:resolve(process.env.GREGALE_IMPORT_STATE??'.file-imports'),users,sdk,browserModule:new URL('../../sdk/node/dist/files.js',import.meta.url),pipelineConfig,liveConfig});
  server.listen(Number(process.env.PORT??8080),process.env.HOST??'127.0.0.1');await once(server,'listening');
  process.on('SIGTERM',()=>server.close(()=>{void pool.end();}));
}
