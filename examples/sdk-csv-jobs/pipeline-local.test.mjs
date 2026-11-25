import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {createInterface} from 'node:readline';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {randomUUID,createHmac,timingSafeEqual} from 'node:crypto';
import {mkdtemp,cp,symlink,readFile,writeFile,rm,access} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {fileStorageFixture} from './file-fixture.mjs';
import {csvPipeline} from './pipeline.mjs';
import {liveFixture} from './live-fixture.mjs';
import {createPipelineReviewServer} from './pipeline-review-api.mjs';
import {csvEventPipeline} from './pipeline-events.mjs';
import {stageCsvImport} from './pipeline-producer.mjs';
import {csvPipelineTool} from './pipeline-tool.mjs';
import {paidCsvAdapter} from './paid-producer.mjs';
import {compensationAcceptance} from './compensation-acceptance.mjs';
import {pythonFixture} from './python-fixture.mjs';
import {refundAcceptance} from './refund-acceptance.mjs';
import {assignedApprovalAcceptance} from './assigned-approval-acceptance.mjs';
import {csvPipelineSchedule} from './pipeline-schedule.mjs';

const exec=promisify(execFile),here=fileURLToPath(new URL('.',import.meta.url)),root=resolve(here,'../..');
const python=process.env.GREGALE_LOCAL_PYTHON??join(root,'sdk/python/.venv/bin/python'),pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin';
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<1000;i++){try{if(await fn())return;}catch{}await pause(20);}throw new Error('Fixture did not become ready');}
async function stop(child){if(child.exitCode!==null)return;const done=new Promise(r=>child.once('exit',r));child.kill('SIGTERM');await done;}

const liveInboxMode=process.env.GREGALE_CSV_LIVE_INBOX==='1';
const clarificationMode=process.env.GREGALE_CSV_CLARIFICATION==='1';
const pipelineTasksMode=clarificationMode||process.env.GREGALE_CSV_PIPELINE_TASKS==='1';
const toolkitMode=pipelineTasksMode||liveInboxMode||process.env.GREGALE_CSV_APP_TOOLKIT==='1';
const refundMode=toolkitMode||process.env.GREGALE_CSV_REFUND==='1';
const compensationMode=refundMode||process.env.GREGALE_CSV_COMPENSATION==='1';
const toolMode=process.env.GREGALE_CSV_TOOL_PIPELINE==='1';
const assignedApprovalMode=process.env.GREGALE_CSV_ASSIGNED_APPROVAL==='1';
const paid=refundMode||process.env.GREGALE_CSV_STRIPE_PIPELINE==='1';
const eventTriggered=paid||process.env.GREGALE_CSV_EVENT_PIPELINE==='1',scheduled=process.env.GREGALE_CSV_SCHEDULED_PIPELINE==='1';
for(const reviewed of assignedApprovalMode?[true]:compensationMode?[paid]:eventTriggered||scheduled||toolMode?[true]:[false,true])test(`CSV ${refundMode?'paid refund ':compensationMode?'compensation ':toolMode?'agent-tool ':assignedApprovalMode?'assigned approval ':paid?'Stripe-paid ':eventTriggered?'event-triggered ':scheduled?'scheduled ':''}${reviewed?'reviewed':'original'} pipeline: PostgreSQL, mixed SDKs, lost journals and verified report`,{timeout:eventTriggered||scheduled||toolMode||compensationMode||assignedApprovalMode?600000:240000},async t=>{
  try{await access(python);await access(join(pgBin,'initdb'));}catch{t.skip('Set GREGALE_LOCAL_PYTHON and GREGALE_LOCAL_PG_BIN');return;}
  const home=await mkdtemp(join(tmpdir(),'gregale-csv-pipeline-')),cleanup=[];t.after(async()=>{try{for(const close of cleanup.reverse())await close();}finally{await rm(home,{recursive:true,force:true});}});
  await cp(here,join(home,'app'),{recursive:true,filter:p=>!p.includes('node_modules')});await symlink(join(root,'sdk/node/node_modules'),join(home,'app/node_modules'));
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale','-E','UTF8']);const postgres=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});cleanup.push(()=>stop(postgres));
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import(new URL('../../sdk/node/node_modules/pg/lib/index.js',import.meta.url));const pool=new pg.Pool({connectionString:database,statement_timeout:5000});cleanup.push(()=>pool.end());await until(async()=>{await pool.query('SELECT 1');return true;});
  const sdk=await import(new URL('../../sdk/node/dist/preview.js',import.meta.url)),runtime=await import(new URL('../../sdk/node/dist/job-runtime.js',import.meta.url));
  await pool.query(await readFile(new URL('schema.sql',import.meta.url),'utf8'));await pool.query(sdk.pipelineJobSchema);await pool.query(sdk.pipelineGateSchema);
  await pool.query('CREATE TABLE sdk_pipeline_events (run_id uuid, key text, body jsonb, PRIMARY KEY(run_id,key))');
  await pool.query('CREATE TABLE sdk_pipeline_runs (id uuid PRIMARY KEY, record jsonb NOT NULL, journal jsonb NOT NULL)');
  await pool.query('CREATE TABLE sdk_pipeline_barrier (driver text PRIMARY KEY)');
  if(eventTriggered){await pool.query(sdk.eventOutboxSchema);await pool.query(sdk.eventPipelineSchema);await pool.query('CREATE TABLE sdk_csv_event_inputs (import_id uuid PRIMARY KEY,owner_id text NOT NULL,input jsonb NOT NULL)');}
  if(toolMode||assignedApprovalMode){await pool.query(sdk.pipelineToolSchema);await pool.query('CREATE TABLE sdk_csv_tool_imports (request_id uuid PRIMARY KEY,owner_id text NOT NULL,enabled boolean NOT NULL DEFAULT true,input jsonb NOT NULL)');}
  const accountId=randomUUID(),jobs=new Map(),templates=new Map(),workflowKeys=new Map(),jobKeys=new Map(),work=new Set(),children=new Set();
  for(const [stage,file]of [['validate','validate-contract.json'],['import','contract.json'],['report','report-contract.json']]){const spec=JSON.parse(await readFile(new URL(file,import.meta.url),'utf8'));templates.set(spec.name,{stage,spec,id:randomUUID()});}
  const storage=fileStorageFixture(()=>env.FAAS_API,{accountId});
  const native=run=>{const total=run.tasks.length,count=status=>run.tasks.filter(t=>t.status===status).length,finished=count('succeeded')+count('failed')+count('cancelled');return {id:run.id,job_id:run.template.id,account_id:accountId,tasks:total,input_manifest_version:1,input_digest:runtime.jobHash(Buffer.from(JSON.stringify(run.tasks.map(t=>({input_id:t.input_id,input_ref:t.input_ref}))))),aggregate_status:finished<total?'running':count('failed')?'failed':count('cancelled')?'cancelled':'succeeded',tasks_succeeded:count('succeeded'),tasks_failed:count('failed'),tasks_cancelled:count('cancelled'),tasks_running:count('claimed')};};
  const stripeEndpoint=randomUUID(),stripeKey=randomUUID(),stripeSecrets=[randomUUID(),randomUUID()],stripeInvocations=new Map();
  if(paid){await pool.query(sdk.stripeWebhookSchema);await pool.query(await readFile(new URL('paid-schema.sql',import.meta.url),'utf8'));}
  const publications=new Map();let publishPosts=0,scheduleDefinition;const scheduledDeployment=randomUUID();
  if(scheduled)await pool.query('CREATE TABLE sdk_csv_schedule_occurrences (id uuid PRIMARY KEY, scheduled_for timestamptz UNIQUE, record jsonb NOT NULL)');
  let workerCount=0,jobPosts=0,scope,eventPosts=0,dropWakeReplies=true,droppedCompensationAck=false;
  const env={...process.env,GREGALE_QUERY_CURSOR_KEY:randomUUID()+randomUUID(),GREGALE_REVIEWED_PIPELINE:reviewed?'1':'0',GREGALE_CSV_REPORT_FAIL:compensationMode?'1':'0',DATABASE_URL:database,PYTHONPATH:join(root,'sdk/python'),FAAS_TOKEN:'fixture-owner',GREGALE_NODE_JOB_RUNTIME:join(root,'sdk/node/dist/job-runtime.js'),GREGALE_FILE_BUCKET:storage.bucket};
  const execute=async run=>{
    for(const task of run.tasks){
      for(;;){task.status='claimed';task.attempt++;
        const manifest=join(home,run.id+'-'+task.task_index+'.json'),usePython=!assignedApprovalMode&&workerCount++%2===1&&!run.template.stage.startsWith('clarification_report'),entry=run.template.stage==='compensate'?'compensation-worker':'pipeline-worker';
        const child=spawn(usePython?python:process.execPath,[join(home,'app',entry+'.'+(usePython?'py':'mjs'))],{env:{...env,GREGALE_CSV_PIPELINE_STAGE:run.template.stage,GREGALE_RUN_ID:run.id,GREGALE_TASK_INDEX:String(task.task_index),GREGALE_TASK_COUNT:String(run.tasks.length),GREGALE_TASK_ATTEMPT:String(task.attempt),GREGALE_INPUT_ID:task.input_id,GREGALE_INPUT_REF:task.input_ref,GREGALE_OUTPUT_MANIFEST_PATH:manifest},stdio:['ignore','pipe','pipe']});children.add(child);let stderr='';child.stderr.on('data',b=>stderr+=b);const code=await new Promise(r=>child.once('exit',r));children.delete(child);
        if(code===0){
          if(compensationMode&&run.template.stage==='compensate'&&!droppedCompensationAck){droppedCompensationAck=true;continue;}
          task.output_manifest=JSON.parse(await readFile(manifest,'utf8'));task.status='succeeded';
        }else{task.status='failed';task.outcome_code='handler_failed';t.diagnostic('Worker failure: '+stderr.slice(0,2000));}
        break;
      }
    }
  };
  const control=createServer(async(req,res)=>{
    const send=(status,value)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
    try{
      const u=new URL(req.url,'http://localhost'),parts=u.pathname.split('/');
      if(u.pathname==='/v1/account'){send(200,{id:req.headers.authorization==='Bearer fixture-other'?randomUUID():accountId});return;}
      if(paid&&u.pathname==='/v1/hooks/fixture-stripe'){
        // Native ingress contract fixture: exact bytes, overlap keys, deterministic receipt.
        const chunks=[];for await(const chunk of req)chunks.push(chunk);const body=Buffer.concat(chunks),signature=String(req.headers['stripe-signature']??''),timestamp=signature.match(/(?:^|,)t=(\d+)(?:,|$)/)?.[1],supplied=signature.match(/(?:^|,)v1=([0-9a-f]{64})(?:,|$)/)?.[1];
        if(!timestamp||!supplied||Math.abs(Date.now()/1000-Number(timestamp))>300||!stripeSecrets.some(secret=>timingSafeEqual(Buffer.from(supplied,'hex'),createHmac('sha256',secret).update(timestamp+'.').update(body).digest()))){send(400,{code:'signature'});return;}
        const event=JSON.parse(body),hash=runtime.jobHash(body),old=stripeInvocations.get(event.id);
        if(old&&old.hash!==hash){send(409,{code:'receipt_conflict'});return;}
        const receipt_id=sdk.stripeReceiptId(stripeEndpoint,event.id);
        if(!old)stripeInvocations.set(event.id,{hash,delivery:{method:'POST',path:'/webhooks/stripe',body,headers:{authorization:'Bearer '+stripeKey,'x-faas-invocation-source':'inbound_webhook','x-faas-invocation-id':receipt_id,'x-gregale-webhook-endpoint-id':stripeEndpoint,'x-gregale-webhook-event-id':event.id,'x-gregale-webhook-event-type':event.type,'x-gregale-webhook-provider':'stripe','x-gregale-webhook-body-sha256':hash}}});
        if(!old){res.destroy();return;}send(200,{receipt_id,duplicate:true,status:'accepted'});return;
      }
      if(paid&&u.pathname.endsWith('/inbound-webhooks/'+stripeEndpoint)){
        if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}
        send(200,{id:stripeEndpoint,account_id:accountId,app_id:storage.app,provider:'stripe',delivery_path:'/webhooks/stripe',enabled:true});return;
      }
      if(eventTriggered&&(u.pathname.endsWith('/events:publish')||u.pathname.endsWith('/events/verify-publication'))){
        if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}
        const chunks=[];for await(const chunk of req)chunks.push(chunk);const input=JSON.parse(Buffer.concat(chunks).toString());
        if(input.expected_app_id!==storage.app){send(409,{});return;}
        const eventId='key.'+runtime.jobHash(Buffer.from(input.key)),source='app.'+storage.app,old=publications.get(input.key);
        if(u.pathname.endsWith('/verify-publication')){send(200,{app_id:storage.app,source,event_id:eventId,observed_at:new Date().toISOString(),status:old?'match':'unavailable',...(old?{receipt:old.receipt}: {})});return;}
        publishPosts++;const receipt=old??{app_id:storage.app,source,duplicate:false,receipt:{account_id:accountId,id:eventId,accepted_at:new Date().toISOString()},input};
        if(old&&runtime.jobValueHash(old.input)!==runtime.jobValueHash(input)){send(409,{});return;}
        publications.set(input.key,receipt);
        if(!old){res.destroy();return;}send(200,{...receipt,duplicate:true});return;
      }
      if(scheduled&&u.pathname==='/v1/deployments/'+scheduledDeployment){if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}send(200,{id:scheduledDeployment,app_id:storage.app,scope:'default',workflows:[{name:contract.spec.name,trigger:scheduleDefinition.trigger}]});return;}
      if(u.pathname.includes('/workflows/')){
        if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}
        if(scheduled&&u.pathname.includes('/schedules')){
          const trigger=scheduleDefinition.trigger,meta={workflow_name:contract.spec.name,deployment_id:scheduledDeployment,schedule:trigger.schedule,timezone:trigger.timezone,overlap:trigger.overlap,enabled:trigger.enabled};
          if(u.pathname.endsWith('/schedules')){send(200,{runtime_enabled:true,schedules:[{...meta,catch_up:'skip'}]});return;}
          if(u.pathname.endsWith('/preview')){send(200,{...meta,observed_at:'2026-11-16T00:00:00Z',evaluation_at:'2026-11-16T00:00:00Z',dst_behavior:{mode:'fixed_wall_time',spring_gap:'shift_to_first_valid_minute',fall_fold:'run_once_at_first_occurrence'},upcoming:[{scheduled_for:'2026-11-16T02:00:00Z',local_time:'2026-11-16T02:00:00Z'}],catch_up:{policy:'skip',outcome:'no_due_occurrence',eligible_occurrences:0,coalesced_occurrences:0,missed_occurrences_not_recovered:false,missed_outside_window:false}});return;}
          if(u.pathname.endsWith('/occurrences')){send(200,{occurrences:(await pool.query('SELECT record FROM sdk_csv_schedule_occurrences ORDER BY scheduled_for DESC,id DESC')).rows.map(r=>r.record)});return;}
        }
        if(req.method==='GET'&&parts[5]==='run-starts'){
          const match=workflowKeys.get(req.headers['idempotency-key']);
          if(!match||match.app_id!==storage.app||match.workflow_name!==parts[6]){send(404,{code:'workflow_run_not_found'});return;}
          send(200,{run_id:match.id});return;
        }
        if(req.method==='POST'&&u.pathname.endsWith('/runs')){
          const chunks=[];for await(const chunk of req)chunks.push(chunk);const input=JSON.parse(Buffer.concat(chunks).toString()),key=req.headers['idempotency-key'];
          if(workflowKeys.has(key)){send(200,workflowKeys.get(key));return;}
          const record={id:randomUUID(),app_id:storage.app,workflow_name:parts[5],status:'running',input};
          await pool.query('INSERT INTO sdk_pipeline_runs VALUES ($1,$2,$3)',[record.id,record,'[]']);workflowKeys.set(key,record);
          if(eventTriggered&&key?.startsWith('event-pipeline:')||(toolMode||compensationMode)&&!['clarify-csv-columns','clarify-csv-columns-v2'].includes(parts[5])&&key?.startsWith('pipeline-tool:')){res.destroy();return;}
          send(201,record);return;
        }
        const id=parts[4],row=(await pool.query('SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1',[id])).rows[0];if(!row){send(404,{});return;}
        if(u.pathname.endsWith('/events')&&req.method==='POST'){
          const chunks=[];for await(const chunk of req)chunks.push(chunk);const body=JSON.parse(Buffer.concat(chunks).toString()),key=req.headers['idempotency-key'];eventPosts++;
          await pool.query('INSERT INTO sdk_pipeline_events VALUES ($1,$2,$3) ON CONFLICT DO NOTHING',[id,key,body]);
          const old=(await pool.query('SELECT body FROM sdk_pipeline_events WHERE run_id=$1 AND key=$2',[id,key])).rows[0].body;assert.equal(runtime.jobValueHash(old),runtime.jobValueHash(body));
          if(dropWakeReplies){res.destroy();return;}send(200,{status:'received',event_name:body.event_name});return;
        }
        if(u.pathname.endsWith('/steps')){send(200,{steps:row.journal.map(s=>({step_name:s.name,status:s.status,attempt:s.attempt??1}))});return;}
        if(u.pathname.endsWith('/cancel')){row.record.cancelled_at=new Date().toISOString();row.record.status='failed';await pool.query('UPDATE sdk_pipeline_runs SET record=$2 WHERE id=$1',[id,row.record]);}
        send(200,row.record);return;
      }
      if(await realtime.handle(req,res))return;
      if(await storage.handle(req,res))return;
      if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}
      const template=templates.get(parts[3]);if(!template){send(404,{});return;}
      if(parts.length===4){send(200,{id:template.id,account_id:accountId,name:template.spec.name});return;}
      if(req.method==='POST'&&u.pathname.endsWith('/runs')){
        jobPosts++;
        const key=req.headers['idempotency-key'];if(jobKeys.has(key)){send(200,native(jobKeys.get(key)));return;}
        const chunks=[];for await(const chunk of req)chunks.push(chunk);const body=JSON.parse(Buffer.concat(chunks).toString());
        const run={id:randomUUID(),template,tasks:body.inputs.map((ref,index)=>({...ref,task_index:index,status:'queued',attempt:0}))};for(const task of run.tasks)task.run_id=run.id;jobs.set(run.id,run);jobKeys.set(key,run);send(201,native(run));const running=execute(run);work.add(running);running.finally(()=>work.delete(running));return;
      }
      const run=jobs.get(parts[5]);if(!run){send(404,{});return;}
      if(u.pathname.endsWith('/artifacts/result/download')){const artifact=run.tasks[Number(parts[7])].output_manifest.artifacts.find(a=>a.name==='result');send(200,{...artifact,download:storage.signArtifact(artifact.uri)});return;}
      if(u.pathname.endsWith('/tasks')){send(200,{tasks:run.tasks,offset:0,next_offset:-1,total:run.tasks.length,limit:200});return;}
      send(200,native(run));
    }catch(error){t.diagnostic(String(error));send(500,{code:'fixture_failed'});}
  });
  const realtime=liveFixture(control);cleanup.push(()=>realtime.close());
  await new Promise(r=>control.listen(0,'127.0.0.1',r));env.FAAS_API=`http://127.0.0.1:${control.address().port}`;scope={base_url:env.FAAS_API,account_id:accountId,app_id:storage.app,app_slug:'imports'};env.GREGALE_PIPELINE_SCOPE=JSON.stringify(scope);
  cleanup.push(async()=>{for(const child of children)await stop(child);await Promise.allSettled(work);await new Promise(r=>control.close(r));});
  const compensationPython=compensationMode?await pythonFixture({python,home,env,children,cleanup}):null;
  const client=sdk.createClient(env.FAAS_API,{token:'fixture-owner'}),files=client.storage.files({appSlug:'imports',bucket:storage.bucket});
  const csv=Buffer.from('row,email\n1,one@example.test\n2,two@example.test\n3,invalid email\n'),importId=randomUUID(),key='pipelines/'+importId+'/source.csv';
  if(assignedApprovalMode)storage.enableVersions(key);
  const uploaded=assignedApprovalMode
    ? await files.upload(key,sdk.blobFileSource(new Blob([csv])),{timeoutMs:10000,contentType:'text/csv'})
    : await files.put(key,csv,{timeoutMs:10000,ifNoneMatch:'*',contentType:'text/csv'});
  const source={version:1,scope:files.scope,owner_id:'alice',key,size_bytes:csv.length,sha256:runtime.jobHash(csv),...(assignedApprovalMode&&{version_id:uploaded.version_id})};
  const gateStore=sdk.postgresPipelineGateStore((sql,values)=>pool.query(sql,values));
  const {contract,pipeline,reviewContract}=await csvPipeline({sdk,client,scope,reviewed,gateStore,...(assignedApprovalMode&&{gateTimeoutMs:600000}),store:sdk.postgresPipelineJobStore((sql,values)=>pool.query(sql,values)),jobStore:await files.jobStore({timeoutMs:10000})});
  if(assignedApprovalMode){
    await assignedApprovalAcceptance({sdk,runtime,scope,files,contract,pipeline,gateStore,reviewContract,source,importId,pool,home,env,python,cleanup});
    return;
  }
  const typed=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-owner',scope,ownerId:'alice',contract});
  let toolMCP,makeTool,pythonTool,toolInvoke,toolRequestId;
  let run,outboxKey='csv-import:'+importId;
  if(toolMode){
    toolRequestId=randomUUID();const input={import_id:importId,source};
    await pool.query('INSERT INTO sdk_csv_tool_imports(request_id,owner_id,input) VALUES ($1,$2,$3)',[toolRequestId,'alice',input]);
    makeTool=async reviewBaseURL=>sdk.createPipelineToolMCP(await csvPipelineTool({sdk,scope,token:'fixture-owner',reviewBaseURL,valueHash:runtime.jobValueHash,
      store:sdk.postgresPipelineToolStore((sql,values)=>pool.query(sql,values)),gateStore,
      loadImport:async id=>(await pool.query('SELECT owner_id,enabled,input FROM sdk_csv_tool_imports WHERE request_id=$1',[id])).rows[0],
    }));
    toolMCP=await makeTool('http://127.0.0.1/');
    // A tiny authenticated JSON-RPC fixture exercises registration/call output.
    // Production registers the adapter with an existing MCP transport/lifecycle host.
    const toolServer=createServer(async(req,res)=>{
      const actor=req.headers.authorization==='Bearer agent-alice'?'alice':req.headers.authorization==='Bearer agent-bob'?'bob':null;
      if(!actor){res.writeHead(401);res.end();return;}
      const chunks=[];for await(const chunk of req)chunks.push(chunk);const packet=JSON.parse(Buffer.concat(chunks).toString());
      if(packet.method==='notifications/initialized'){res.writeHead(202);res.end();return;}
      let reply;
      try{
        const result=packet.method==='initialize'?{protocolVersion:'2025-11-25',capabilities:{tools:{}},serverInfo:{name:'csv-pipeline-fixture',version:'1'}}:packet.method==='tools/list'?{tools:toolMCP.tools}:packet.method==='tools/call'?await toolMCP.call(packet.params.name,packet.params.arguments,actor,{timeoutMs:10000}):(()=>{throw new sdk.PipelineToolMCPError(-32601);})();
        reply={jsonrpc:'2.0',id:packet.id,result};
      }catch(error){reply={jsonrpc:'2.0',id:packet.id,error:{code:[-32601,-32602].includes(error.code)?error.code:-32603,message:'Pipeline tool request rejected'}};}
      res.writeHead(200,{'content-type':'application/json'});res.end(JSON.stringify(reply));
    });
    await new Promise(r=>toolServer.listen(0,'127.0.0.1',r));cleanup.push(()=>new Promise(r=>toolServer.close(r)));
    const rpc=async(method,params={},access='alice')=>{const response=await fetch(`http://127.0.0.1:${toolServer.address().port}`,{method:'POST',headers:{authorization:'Bearer agent-'+access,'content-type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:randomUUID(),method,params})});return (await response.json()).result;};
    toolInvoke=(operation,args={request_id:toolRequestId},access='alice')=>rpc('tools/call',{name:'csv_import_'+operation,arguments:args},access);
    assert.equal((await rpc('initialize',{protocolVersion:'2025-11-25',capabilities:{},clientInfo:{name:'agent-fixture',version:'1'}})).protocolVersion,'2025-11-25');
    const descriptor=await rpc('tools/list');assert.equal(descriptor.tools.length,5);assert.ok(descriptor.tools.every(t=>t.outputSchema));
    const worker=spawn(python,[join(home,'app/event_driver.py'),'--stdio'],{env,stdio:['pipe','pipe','pipe']});children.add(worker);
    let ready,errors='',sequence=0;const pending=new Map(),lines=createInterface({input:worker.stdout});worker.stderr.on('data',b=>errors+=b);
    const started=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('Python tool fixture startup timed out')),120000);ready=()=>{clearTimeout(timer);resolve();};worker.once('exit',code=>{clearTimeout(timer);reject(Error('Python tool fixture stopped '+code+' '+errors));});});
    lines.on('line',line=>{const packet=JSON.parse(line);if(packet.ready){ready();return;}pending.get(packet.request_id)?.(packet.result);pending.delete(packet.request_id);});worker.once('exit',()=>children.delete(worker));cleanup.push(()=>lines.close());await started;
    pythonTool=(operation,args={request_id:toolRequestId},access='alice',review_base_url='http://127.0.0.1/')=>new Promise((resolve,reject)=>{const request_id=String(sequence++),timer=setTimeout(()=>{pending.delete(request_id);reject(Error('Python tool request timed out '+errors));},30000);pending.set(request_id,value=>{clearTimeout(timer);resolve(value);});worker.stdin.write(JSON.stringify({request_id,action:operation==='list'?'tool-list':operation==='drive'?'tool-drive':'tool-call',name:'csv_import_'+operation,arguments:args,access,review_base_url})+'\n');});
    assert.deepEqual(await pythonTool('list'),descriptor);
    assert.equal((await toolInvoke('start',{request_id:toolRequestId,input},'bob')).isError,true);
    const starts=await Promise.all([toolInvoke('start',{request_id:toolRequestId,input}),pythonTool('start',{request_id:toolRequestId,input})]);
    assert.ok(starts.every(r=>r.structuredContent?.state==='start_unknown'),JSON.stringify(starts));assert.equal(starts.filter(r=>r.structuredContent.replayed===false).length,1);assert.equal(workflowKeys.size,1);
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_pipeline_tool_starts')).rows[0].n,1);
    const retained=(await pool.query('SELECT id FROM sdk_pipeline_runs')).rows[0].id;
    const adopted=await pythonTool('reconcile',{request_id:toolRequestId,run_id:retained});assert.equal(adopted.structuredContent.state,'started');
    const repeated=await toolInvoke('start',{request_id:toolRequestId,input});assert.equal(repeated.structuredContent.receipt.run_id,retained);assert.equal(repeated.structuredContent.replayed,true);assert.equal(workflowKeys.size,1);
    await pool.query('UPDATE sdk_csv_tool_imports SET enabled=false');assert.equal((await pythonTool('status')).isError,true);await pool.query('UPDATE sdk_csv_tool_imports SET enabled=true');
    run=await typed.resumeById(retained,{timeoutMs:10000});
  }else if(eventTriggered){
    // The authenticated producer names an immutable business record, not an owner/file supplied by the event.
    if(paid){
      env.GREGALE_CSV_STRIPE_KEY=stripeKey;
      await pool.query('INSERT INTO sdk_csv_payments (payment_id,import_id,owner_id,customer_id,amount_cents,currency,input) VALUES ($1,$2,$3,$4,$5,$6,$7)',['pi_csv',importId,'alice','cus_alice',1000,'usd',{import_id:importId,source}]);
      const adapter=await paidCsvAdapter({sdk,pool,scope,endpointId:stripeEndpoint,verify:async d=>{
        const supplied=Buffer.from(String(d.headers.authorization??'')),expected=Buffer.from('Bearer '+stripeKey);
        if(supplied.length!==expected.length||!timingSafeEqual(supplied,expected))throw Error('unauthorized');
        return sdk.stripeDeliveryContextFromHeaders(d.headers,d.body,accountId,scope.app_id);
      }});
      const event={id:'evt_csv_paid',object:'event',api_version:'2024-06-20',livemode:false,created:1794844800,type:'payment_intent.succeeded',data:{object:{id:'pi_csv',object:'payment_intent',customer:'cus_alice',amount_received:1000,currency:'usd',status:'succeeded',metadata:{owner_id:'bob'}}}};
      const ingress=async(value,secret=stripeSecrets[0])=>{const body=JSON.stringify(value),timestamp=Math.floor(Date.now()/1000),v1=createHmac('sha256',secret).update(timestamp+'.'+body).digest('hex');return fetch(env.FAAS_API+'/v1/hooks/fixture-stripe',{method:'POST',headers:{'stripe-signature':`t=${timestamp},v1=${v1}`},body});};
      assert.equal((await ingress(event,'wrong-secret')).status,400);
      await assert.rejects(ingress(event)); // Native receipt survived a dropped acknowledgement.
      assert.equal((await (await ingress(event,stripeSecrets[1])).json()).duplicate,true);
      assert.equal((await ingress({...event,created:event.created+1})).status,409);
      const delivery=stripeInvocations.get(event.id).delivery;
      await assert.rejects(adapter.handle({...delivery,headers:{...delivery.headers,authorization:'Bearer forged'}},'alice',{timeoutMs:10000}),{code:'unauthorized'});
      await assert.rejects(adapter.handle(delivery,'bob',{timeoutMs:10000}),{code:'unauthorized'});
      const worker=spawn(python,[join(home,'app/event_driver.py'),'--stdio'],{env,stdio:['pipe','pipe','pipe']});children.add(worker);
      let ready,errors='',seq=0;const pending=new Map(),lines=createInterface({input:worker.stdout});worker.stderr.on('data',b=>errors+=b);
      const started=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('Python Stripe fixture startup timed out')),120000);ready=()=>{clearTimeout(timer);resolve();};worker.once('exit',code=>{clearTimeout(timer);reject(Error('Python Stripe fixture stopped '+code+' '+errors));});});
      lines.on('line',line=>{const packet=JSON.parse(line);if(packet.ready){ready();return;}pending.get(packet.request_id)?.(packet.result);pending.delete(packet.request_id);});cleanup.push(()=>lines.close());worker.once('exit',()=>children.delete(worker));await started;
      const pythonStripe=(action,d=delivery,access='alice')=>new Promise((resolve,reject)=>{const request_id=String(seq++),timer=setTimeout(()=>{pending.delete(request_id);reject(Error('Python Stripe request timed out: '+errors));},30000);pending.set(request_id,value=>{clearTimeout(timer);resolve(value);});worker.stdin.write(JSON.stringify({request_id,action,access,endpoint_id:stripeEndpoint,delivery:{...d,body:d.body.toString()}})+'\n');});
      const outcomes=await Promise.all([adapter.handle(delivery,'alice',{timeoutMs:10000}),pythonStripe('stripe-handle')]);
      assert.ok(outcomes.every(o=>o.outcome==='staged'));assert.equal(outcomes.filter(o=>o.replayed).length,1);
      outboxKey=outcomes[0].receipt.outbox_key;
      assert.deepEqual(await pythonStripe('stripe-inspect'),await adapter.inspect(delivery,'alice',{timeoutMs:10000}));
      assert.equal((await pythonStripe('stripe-inspect',delivery,'bob')).code,'unauthorized');
      const other={...event,id:'evt_csv_also_paid'};await assert.rejects(ingress(other));const otherDelivery=stripeInvocations.get(other.id).delivery;
      assert.equal((await pythonStripe('stripe-handle',otherDelivery)).reason,'already_applied');
      const older={...event,id:'evt_csv_old_failure',type:'payment_intent.payment_failed',created:event.created-60};await assert.rejects(ingress(older));
      assert.equal((await adapter.handle(stripeInvocations.get(older.id).delivery,'alice',{timeoutMs:10000})).reason,'already_applied');
      await pool.query('UPDATE sdk_csv_payments SET enabled=false');assert.equal((await pythonStripe('stripe-handle')).code,'unauthorized');await pool.query('UPDATE sdk_csv_payments SET enabled=true');
      assert.equal((await pool.query('SELECT applications FROM sdk_csv_payments')).rows[0].applications,1);
      assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_stripe_webhook_receipts')).rows[0].n,3);
      await stop(worker);
    }else await stageCsvImport({sdk,pool,scope,importId,ownerId:'alice',input:{import_id:importId,source}});
    const publisher=sdk.createEventPublisher(new sdk.EventContract(JSON.parse(await readFile(new URL('event-contract.json',import.meta.url),'utf8'))),{scope});
    const rolledBack=randomUUID();await assert.rejects(sdk.withEventOutboxTransaction(pool,async tx=>{
      await tx.query('INSERT INTO sdk_csv_event_inputs VALUES ($1,$2,$3)',[rolledBack,'alice',{import_id:rolledBack,source}]);
      await publisher.stage(tx,{import_id:rolledBack},{key:'csv-import:'+rolledBack});throw Error('rollback');
    },{timeoutMs:10000}));
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_event_inputs')).rows[0].n,1);
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_event_outbox')).rows[0].n,1);
    const outbox=sdk.createEventOutbox(pool,{scope,token:'fixture-owner'});
    assert.equal((await outbox.recoverNext({timeoutMs:10000,backoffMs:1})).outcome,'unconfirmed');
    const published=publications.get(outboxKey);assert.ok(published);assert.equal(publishPosts,1);

    const subscriptionId=randomUUID(),eventSource=published.source,ingressKey=randomUUID();env.GREGALE_CSV_EVENT_KEY=ingressKey;
    const body=JSON.stringify({specversion:'1.0',source:eventSource,id:published.receipt.id,type:published.input.type,data:published.input.data});
    const headers={authorization:'Bearer '+ingressKey,'x-gregale-event-subscription-id':subscriptionId,'x-gregale-event-acceptance-id':'9007199254740993','x-gregale-event-source':eventSource,'x-gregale-event-id':published.receipt.id};
    const delivery={headers,body:Buffer.from(body)};
    const events=await csvEventPipeline({sdk,scope,subscriptionId,source:eventSource,contract,token:'fixture-owner',
      store:sdk.postgresEventPipelineStore((sql,values)=>pool.query(sql,values)),
      verify:async d=>{if(d.headers.authorization!=='Bearer '+ingressKey)throw new Error('unauthorized');return sdk.eventConsumerRequestFromHeaders(d.headers,d.body,accountId,scope.app_id);},
      loadImport:async id=>(await pool.query('SELECT owner_id,input FROM sdk_csv_event_inputs WHERE import_id=$1',[id])).rows[0],
      authorizeOwner:async(owner,actor)=>owner===actor&&['alice','bob'].includes(actor),
    });
    const eventWorker=spawn(python,[join(home,'app/event_driver.py'),'--stdio'],{env,stdio:['pipe','pipe','pipe']});children.add(eventWorker);
    let eventErrors='',eventInvocation=0,ready;const pending=new Map();
    eventWorker.stderr.on('data',bytes=>eventErrors+=bytes);
    const lines=createInterface({input:eventWorker.stdout});
    const started=new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error('Python event fixture startup timed out')),120000);ready=()=>{clearTimeout(timer);resolve();};eventWorker.once('exit',code=>{clearTimeout(timer);reject(new Error('Python event fixture stopped: '+code+' '+eventErrors));});});
    lines.on('line',line=>{const packet=JSON.parse(line);if(packet.ready){ready();return;}const callback=pending.get(packet.request_id);if(callback){pending.delete(packet.request_id);callback(packet.result);}});
    eventWorker.once('exit',()=>children.delete(eventWorker));cleanup.push(()=>lines.close());await started;
    const pythonEvent=(action,access='alice',run_id)=>new Promise((resolve,reject)=>{
      const request_id=String(eventInvocation++),timer=setTimeout(()=>{pending.delete(request_id);reject(new Error('Python event request timed out'));},30000);
      pending.set(request_id,result=>{clearTimeout(timer);resolve(result);});
      eventWorker.stdin.write(JSON.stringify({request_id,action,access,run_id,key:outboxKey,subscription_id:subscriptionId,source:eventSource,delivery:{headers,body}})+'\n');
    });
    assert.equal((await pythonEvent('outbox-reconcile')).outcome,'published');
    assert.equal((await outbox.status(outboxKey,{timeoutMs:10000})).state,'published');
    assert.equal((await pool.query('SELECT request_body FROM gregale_event_outbox')).rows[0].request_body,null);assert.equal(publishPosts,1);
    await assert.rejects(events.handle(delivery,'bob',{timeoutMs:10000}),{code:'unauthorized'});
    const starts=await Promise.all([events.handle(delivery,'alice',{timeoutMs:10000}),pythonEvent('handle')]);
    assert.ok(starts.every(s=>s.state==='start_unknown'));assert.equal(workflowKeys.size,1);
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_event_pipeline_starts')).rows[0].n,1);
    const retained=(await pool.query('SELECT id FROM sdk_pipeline_runs')).rows[0].id;
    assert.equal((await pythonEvent('inspect','bob')).code,'unauthorized');
    const adopted=await pythonEvent('reconcile','alice',retained);
    assert.equal(adopted.state,'started');
    assert.deepEqual((await events.inspect(delivery,'alice',{timeoutMs:10000})).receipt,adopted.receipt);
    assert.equal((await events.handle(delivery,'alice',{timeoutMs:10000})).receipt.checkpoint.run_id,retained);
    assert.equal((await pythonEvent('handle')).receipt.checkpoint.run_id,retained);assert.equal(workflowKeys.size,1);
    run=await typed.resumeById(retained,{timeoutMs:10000});
  }else if(scheduled){
    scheduleDefinition=csvPipelineSchedule({sdk,scope,contract,input:{import_id:importId,source},ownerId:'alice'});
    // This fixture stands in for native atomic admission, never an SDK timer/start loop.
    const admit=async()=>{const c=await pool.connect();try{await c.query('BEGIN');const id=randomUUID(),runId=randomUUID(),nominal='2026-11-16T02:00:00Z';
      const occurrence={id,app_id:scope.app_id,deployment_id:scheduledDeployment,workflow_name:contract.spec.name,scheduled_for:nominal,evaluated_at:'2026-11-16T02:00:01Z',status:'started',run_id:runId};
      const inserted=await c.query('INSERT INTO sdk_csv_schedule_occurrences VALUES ($1,$2,$3) ON CONFLICT(scheduled_for) DO NOTHING RETURNING id',[id,nominal,occurrence]);
      if(inserted.rows.length){const record={id:runId,app_id:scope.app_id,deployment_id:scheduledDeployment,workflow_name:contract.spec.name,scheduled_for:nominal,status:'running',input:scheduleDefinition.trigger.input};await c.query('INSERT INTO sdk_pipeline_runs VALUES ($1,$2,$3)',[runId,record,'[]']);}
      await c.query('COMMIT');
    }catch(e){await c.query('ROLLBACK');throw e;}finally{c.release();}};
    await Promise.all([admit(),admit()]);assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_pipeline_runs')).rows[0].n,1);
    const schedules=sdk.createPipelineScheduleClient(scheduleDefinition,{deploymentId:scheduledDeployment,token:'fixture-owner',authorize:async actor=>actor==='alice'});
    await assert.rejects(schedules.history('bob',{timeoutMs:10000}),{code:'unauthorized'});
    const preview=await schedules.preview('alice',{timeoutMs:10000}),history=await schedules.history('alice',{timeoutMs:10000});assert.equal(preview.upcoming[0].scheduled_for,'2026-11-16T02:00:00Z');assert.equal(history.occurrences.length,1);
    run=await schedules.attach(history.occurrences[0].id,'alice',{timeoutMs:10000});
    const request=join(home,'schedule.json');await writeFile(request,JSON.stringify({scope,input:{import_id:importId,source},deployment_id:scheduledDeployment,occurrence_id:history.occurrences[0].id}));
    const pythonReply=JSON.parse((await exec(python,[join(home,'app/schedule_driver.py'),request],{env,timeout:120000})).stdout);assert.equal(pythonReply.identity,scheduleDefinition.identity);assert.deepEqual(pythonReply.checkpoint,run.checkpoint);assert.deepEqual(pythonReply.preview,preview);
  }else{
    run=await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'csv:'+importId});assert.equal((await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'csv:'+importId})).checkpoint.run_id,run.checkpoint.run_id);
  }
  const bob=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-owner',scope,ownerId:'bob',contract});await assert.rejects(bob.resumeById(run.checkpoint.run_id,{timeoutMs:10000}),{code:'scope_mismatch'});
  const wrong=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-other',scope,ownerId:'alice',contract});await assert.rejects(wrong.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'wrong'}),{code:'scope_mismatch'});assert.equal(workflowKeys.size,scheduled?0:1);
  let page,browser,reviewURL,approved=false;const browserErrors=[];
  if(reviewed){
    const server=createPipelineReviewServer({sdk,client,scope,files,contract,pipeline,reviewContract,gateStore,token:'fixture-owner',users:{alice:{token:'alice-review',owner_id:'alice',roles:['reviewer']},bob:{token:'bob-review',owner_id:'bob',roles:['reviewer']}},realtime:{...realtime.config(),pollDelayMs:50}});
    await new Promise(r=>server.listen(0,'127.0.0.1',r));reviewURL=`http://127.0.0.1:${server.address().port}`;if(toolMode)toolMCP=await makeTool(reviewURL+'/');cleanup.push(()=>new Promise(r=>server.close(r)));
    const chrome=process.env.GREGALE_LOCAL_CHROME??'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';await access(chrome);
    const {chromium}=await import(new URL('../../sdk/node/node_modules/playwright-core/index.mjs',import.meta.url));browser=await chromium.launch({headless:true,executablePath:chrome});cleanup.push(()=>browser.close());page=await browser.newPage();page.on('pageerror',e=>browserErrors.push(e.message));
    page.on('request',r=>assert.notEqual(r.headers().authorization,'Bearer fixture-owner'));
  }
  const dropped=new Set(),live=[];let finished=false;
  for(let invocation=0;invocation<180;invocation++){
    const row=(await pool.query('SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1',[run.checkpoint.run_id])).rows[0];
    const request={protocol:'gregale.workflow.code/v1',run_id:run.checkpoint.run_id,workflow:contract.spec.name,input:row.record.input,invocation:invocation+1,journal:row.journal};
    const path=join(home,'request.json');await writeFile(path,JSON.stringify(request));
    const usePython=invocation%2===1;
    const drive=async usePython=>{if(compensationMode&&usePython)return compensationPython({action:'compensation-drive',path,barrier:invocation===0});if(toolMode&&usePython)return pythonTool('drive',{path,barrier:invocation===0});const result=await exec(usePython?python:process.execPath,[join(home,'app','pipeline_driver.'+(usePython?'py':'mjs')),path],{env:{...env,...(invocation===0?{GREGALE_PIPELINE_BARRIER:'1'}:{})},timeout:30000});const reply=JSON.parse(result.stdout);if(result.stderr)reply.fixture_stderr=result.stderr;return reply;};
    let reply;
    if(invocation===0){const replies=await Promise.all([drive(false),drive(true)]);assert.ok(replies.every(r=>r.ops[0]?.status==='succeeded'),JSON.stringify({replies,barrier:(await pool.query('SELECT * FROM sdk_pipeline_barrier')).rows}));reply=replies[0];}
    else reply=await drive(usePython);
    if(compensationMode&&reply.result.status==='failed'){
      for(const op of reply.ops)row.journal[op.seq]={...op,attempt:3};row.record.status='failed';row.record.last_error=reply.result.error;
      await pool.query('UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1',[run.checkpoint.run_id,row.record,JSON.stringify(row.journal)]);
      assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows')).rows[0].n,2);
      const driveNode=async requestPath=>JSON.parse((await exec(process.execPath,[join(home,'app/pipeline_driver.mjs'),requestPath],{env,timeout:30000})).stdout);
      await compensationAcceptance({sdk,runtime,pool,client,files,storage,scope,sourceRunId:run.checkpoint.run_id,importId,home,env,cleanup,pythonCall:compensationPython,templates,driveNode,pause,reviewed,withQueries:refundMode,withToolkit:toolkitMode,withPipelineTasks:pipelineTasksMode,withClarification:clarificationMode,withLiveInbox:liveInboxMode?{realtime,python}:undefined,afterCleanup:refundMode?async review=>refundAcceptance({sdk,pool,scope,home,env,cleanup,pythonCall:compensationPython,driveNode,pause,...review}):undefined});
      assert.equal(droppedCompensationAck,true);return;
    }
    if(!compensationMode){assert.equal(reply.result.status==='failed',false,JSON.stringify(reply));assert.equal(reply.ops.some(o=>o.status==='failed'),false,JSON.stringify(reply));}
    // Drop each submission/completion journal once: the next process has only
    // native Jobs and the PostgreSQL receipt, with no in-memory recovery state.
    const crash=reply.ops.find(o=>o.name.endsWith('/submit')||o.output?.state==='succeeded'||o.name.endsWith('/open')||o.name.endsWith('/resolve'));
    if(crash&&!dropped.has(crash.seq)){dropped.add(crash.seq);continue;}
    const wait=reply.ops.find(o=>o.kind==='event');
    if(wait&&reviewed&&!approved){
      assert.equal(jobPosts,1);assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows')).rows[0].n,0);
      for(const action of ['gate','snapshot','ticket'])assert.equal((await fetch(`${reviewURL}/runs/${run.checkpoint.run_id}/${action}`,{headers:{Authorization:'Bearer bob-review'}})).status,404);
      if(toolMode){
        const waiting=await toolInvoke('status'),pythonWaiting=await pythonTool('status',undefined,'alice',reviewURL+'/');
        assert.deepEqual(waiting,pythonWaiting);assert.equal(waiting.structuredContent.state,'awaiting_approval');
        assert.equal((await toolInvoke('result')).structuredContent.state,'awaiting_approval');
        assert.equal(new URL(waiting.structuredContent.approval.review_url).origin,reviewURL);assert.equal((await toolInvoke('status',undefined,'bob')).isError,true);
        assert.equal((await pool.query('SELECT decision FROM gregale_pipeline_gates')).rows[0].decision,null);
      }
      await page.goto(reviewURL+'/?run='+run.checkpoint.run_id);await page.fill('#token','alice-review');await page.click('#attach');await page.waitForFunction(()=>document.querySelector('#status').textContent==='Awaiting approval',{},{timeout:30000});
      assert.ok((await page.textContent('#data')).includes(source.sha256));assert.equal(await page.isDisabled('#report'),true);
      await page.fill('#reason','Reviewed exact source and rows');await page.click('#approve');await page.waitForFunction(()=>document.querySelector('#status').textContent==='Decision saved: approved',{},{timeout:30000});
      const gate=(await gateStore.list(run.checkpoint.run_id,'alice',new AbortController().signal))[0];assert.equal(gate.decision.state,'approved');assert.equal(gate.delivery,'pending');
      // A fresh service recovers after the native event committed but its ACK was lost.
      dropWakeReplies=false;const recovery=sdk.createPipelineGateService({scope,pipelineSha256:contract.sha256,contract:reviewContract,store:gateStore,transport:sdk.nativePipelineGateTransport({scope,token:'fixture-owner',contract,timeoutMs:10000}),authorize:async()=>{throw new Error('backend-only recovery');}});await recovery.drain(100,{timeoutMs:10000});
      assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_pipeline_events')).rows[0].n,1);assert.ok(eventPosts>=2);approved=true;
    }
    for(const op of reply.ops){assert.ok(op.seq<=row.journal.length);if(op.kind==='event'){const event=(await pool.query('SELECT body FROM sdk_pipeline_events WHERE run_id=$1',[run.checkpoint.run_id])).rows[0];assert.ok(event);row.journal[op.seq]={...op,status:'succeeded',output:event.body.payload,attempt:1};}else row.journal[op.seq]={...op,status:op.kind==='sleep'?'succeeded':op.status,attempt:compensationMode&&op.status==='failed'?3:1};}
    if(reply.result.status==='succeeded'){row.record.status='succeeded';row.record.output=reply.result.output;finished=true;}
    await pool.query('UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1',[run.checkpoint.run_id,row.record,JSON.stringify(row.journal)]);
    const snapshot=await sdk.jobPipelineLiveSnapshot(run,{resourceId:importId,steps:['validate','import','report'],timeoutMs:10000,inspectJob:(step,signal)=>pipeline.inspectJob(run.checkpoint.run_id,step,'alice',{timeoutMs:10000,signal}),reportReady:async output=>{await sdk.readPipelineArtifact(files,output.report,'alice',{timeoutMs:10000,maxBytes:1024*1024});return true;}});live.push(snapshot);
    if(finished)break;
    await pause(30);
  }
  assert.equal(finished,true);assert.equal(jobs.size,3);assert.equal(jobPosts,3);assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_pipeline_jobs')).rows[0].n,3);assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows')).rows[0].n,2);
  if(reviewed){
    assert.equal(approved,true);await page.waitForFunction(()=>!document.querySelector('#report').disabled,{},{timeout:30000});
    const downloadEvent=page.waitForEvent('download');await page.click('#report');const download=await downloadEvent;await download.saveAs(join(home,'reviewed.csv'));assert.ok((await readFile(join(home,'reviewed.csv'),'utf8')).includes('invalid_email'));
    await page.fill('#token','bob-review');assert.equal(await page.isVisible('#review'),false);assert.equal(await page.textContent('#data'),'');assert.equal(await page.isDisabled('#report'),true);assert.deepEqual(browserErrors,[]);
  }
  const restored=await typed.resumeById(run.checkpoint.run_id,{timeoutMs:10000}),output=await restored.result({timeoutMs:10000});assert.equal(output.saved,2);assert.equal(output.rejected,1);if(toolMode){const result=await toolInvoke('result'),pythonResult=await pythonTool('result',undefined,'alice',reviewURL+'/');assert.deepEqual(result,pythonResult);assert.deepEqual(result.structuredContent.output,output);assert.equal(result.structuredContent.state,'succeeded');assert.equal(JSON.stringify(result).includes('fixture-owner'),false);}assert.equal(live.at(-1).report,'ready');assert.equal(live.at(-1).succeeded,3);
  const report=await sdk.readPipelineArtifact(files,output.report,'alice',{timeoutMs:10000,maxBytes:1024*1024});assert.ok(new TextDecoder().decode(report).includes(importId+':3,invalid_email'));
  await assert.rejects(async()=>sdk.readPipelineArtifact(files,output.report,'bob',{timeoutMs:1000}),{code:'artifact_changed'});
  if(reviewed){
    const raceRun=await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'decision-race'}),input=F=>runtime.jobValueHash({version:1,contract_sha256:contract.sha256,scope,owner_id:'alice',input:F});
    const raceIntent=sdk.pipelineGateIntent({kind:'approval',scope,workflow_run_id:raceRun.checkpoint.run_id,owner_id:'alice',pipeline_sha256:contract.sha256,input_sha256:input({import_id:importId,source}),name:'review',contract_sha256:reviewContract.sha256,review:{source,rows:[{id:'one',value:{row:1,email:'one@example.test'}}]},required_roles:['reviewer'],timeout_ms:60000});
    await gateStore.open(raceIntent,new AbortController().signal);
    const raceStore=sdk.postgresPipelineGateStore(async(sql,values)=>{
      if(sql.startsWith('UPDATE public.gregale_pipeline_gates SET decision=CASE WHEN expires_at')){
        await pool.query("INSERT INTO sdk_pipeline_barrier VALUES ('gate-node') ON CONFLICT DO NOTHING");
        await until(async()=>((await pool.query("SELECT count(*)::int AS n FROM sdk_pipeline_barrier WHERE driver IN ('gate-node','gate-python')")).rows[0].n===2));
      }return pool.query(sql,values);
    });
    const raceService=sdk.createPipelineGateService({scope,pipelineSha256:contract.sha256,contract:reviewContract,store:raceStore,transport:sdk.nativePipelineGateTransport({scope,token:'fixture-owner',contract,timeoutMs:10000}),authorize:async()=>({actor_id:'alice',owner_id:'alice',roles:['reviewer']})});
    const raceFile=join(home,'race.json');await writeFile(raceFile,JSON.stringify({scope,root_contract:contract.spec,contract:reviewContract.spec,intent:raceIntent}));
    const [node,py]=await Promise.all([raceService.decide(raceIntent.id,{binding_sha256:raceIntent.binding_sha256,state:'approved',payload:{reason:'simultaneous choice'}},'alice',{timeoutMs:40000}).then(v=>({ok:true,state:v.decision.state}),e=>({ok:false,code:e.code})),exec(python,[join(home,'app/gate_decision_driver.py'),raceFile],{env,timeout:45000}).then(r=>JSON.parse(r.stdout))]);
    assert.equal([node,py].filter(r=>r.ok).length,1,JSON.stringify({node,py}));assert.equal([node,py].find(r=>!r.ok).code,'conflict');
    const expiryRun=await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'expiry-check'}),{version:_v,id:_id,review_sha256:_r,binding_sha256:_b,...base}=raceIntent;
    const expiring=sdk.pipelineGateIntent({...base,workflow_run_id:expiryRun.checkpoint.run_id});await gateStore.open(expiring,new AbortController().signal);await pool.query("UPDATE gregale_pipeline_gates SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1",[expiring.id]);
    await assert.rejects(raceService.decide(expiring.id,{binding_sha256:expiring.binding_sha256,state:'approved',payload:{reason:'late'}},'alice',{timeoutMs:10000}),{code:'conflict'});assert.equal((await gateStore.resolve(expiring.id,new AbortController().signal)).decision.state,'expired');
    const cancelRun=await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'gate-cancel'}),cancelIntent=sdk.pipelineGateIntent({...base,workflow_run_id:cancelRun.checkpoint.run_id});await gateStore.open(cancelIntent,new AbortController().signal);await cancelRun.cancel({timeoutMs:10000});await raceService.drain(100,{timeoutMs:10000});assert.equal((await gateStore.resolve(cancelIntent.id,new AbortController().signal)).decision.state,'cancelled');assert.equal(jobPosts,3);
    const pageIds=[];for(let n=0;n<3;n++){const i=sdk.pipelineGateIntent({...base,pipeline_sha256:'f'.repeat(64),name:'review-page-'+n});await gateStore.open(i,new AbortController().signal);pageIds.push(i.id);}
    const page1=await gateStore.pending(2,scope,'f'.repeat(64),reviewContract.sha256,new AbortController().signal),page2=await gateStore.pending(2,scope,'f'.repeat(64),reviewContract.sha256,new AbortController().signal);assert.equal(new Set([...page1,...page2].map(r=>r.intent.id)).size,3,'recovery rotates beyond an active batch');await pool.query('DELETE FROM gregale_pipeline_gates WHERE id=ANY($1::text[])',[pageIds]);
  }
  storage.objects.set(key,Buffer.from('changed'));await assert.rejects(sdk.readPipelineArtifact(files,source,'alice',{timeoutMs:1000}),{code:'artifact_changed'});
  const cancelled=await typed.start({import_id:randomUUID(),source},{timeoutMs:10000,idempotencyKey:'cancel'});await cancelled.cancel({timeoutMs:10000});assert.equal((await cancelled.inspect({timeoutMs:10000})).cancelled,true);assert.equal(jobs.size,3);
});
