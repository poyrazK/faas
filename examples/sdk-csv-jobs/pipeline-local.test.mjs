import test from 'node:test';
import assert from 'node:assert/strict';
import {createServer} from 'node:http';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {randomUUID} from 'node:crypto';
import {mkdtemp,cp,symlink,readFile,writeFile,rm,access} from 'node:fs/promises';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {fileStorageFixture} from './file-fixture.mjs';
import {csvPipeline} from './pipeline.mjs';
import {liveFixture} from './live-fixture.mjs';
import {createPipelineReviewServer} from './pipeline-review-api.mjs';

const exec=promisify(execFile),here=fileURLToPath(new URL('.',import.meta.url)),root=resolve(here,'../..');
const python=process.env.GREGALE_LOCAL_PYTHON??join(root,'sdk/python/.venv/bin/python'),pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin';
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<1000;i++){try{if(await fn())return;}catch{}await pause(20);}throw new Error('Fixture did not become ready');}
async function stop(child){if(child.exitCode!==null)return;const done=new Promise(r=>child.once('exit',r));child.kill('SIGTERM');await done;}

for(const reviewed of [false,true])test(`CSV ${reviewed?'reviewed':'original'} pipeline: PostgreSQL, mixed SDKs, lost journals and verified report`,{timeout:240000},async t=>{
  try{await access(python);await access(join(pgBin,'initdb'));}catch{t.skip('Set GREGALE_LOCAL_PYTHON and GREGALE_LOCAL_PG_BIN');return;}
  const home=await mkdtemp(join(tmpdir(),'gregale-csv-pipeline-')),cleanup=[];t.after(async()=>{try{for(const close of cleanup.reverse())await close();}finally{await rm(home,{recursive:true,force:true});}});
  await cp(here,join(home,'app'),{recursive:true,filter:p=>!p.includes('node_modules')});await symlink(join(root,'sdk/node/node_modules'),join(home,'app/node_modules'));
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale']);const postgres=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});cleanup.push(()=>stop(postgres));
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import(new URL('../../sdk/node/node_modules/pg/lib/index.js',import.meta.url));const pool=new pg.Pool({connectionString:database,statement_timeout:5000});cleanup.push(()=>pool.end());await until(async()=>{await pool.query('SELECT 1');return true;});
  const sdk=await import(new URL('../../sdk/node/dist/preview.js',import.meta.url)),runtime=await import(new URL('../../sdk/node/dist/job-runtime.js',import.meta.url));
  await pool.query(await readFile(new URL('schema.sql',import.meta.url),'utf8'));await pool.query(sdk.pipelineJobSchema);await pool.query(sdk.pipelineGateSchema);
  await pool.query('CREATE TABLE sdk_pipeline_events (run_id uuid, key text, body jsonb, PRIMARY KEY(run_id,key))');
  await pool.query('CREATE TABLE sdk_pipeline_runs (id uuid PRIMARY KEY, record jsonb NOT NULL, journal jsonb NOT NULL)');
  await pool.query('CREATE TABLE sdk_pipeline_barrier (driver text PRIMARY KEY)');
  const accountId=randomUUID(),jobs=new Map(),templates=new Map(),workflowKeys=new Map(),jobKeys=new Map(),work=new Set(),children=new Set();
  for(const [stage,file]of [['validate','validate-contract.json'],['import','contract.json'],['report','report-contract.json']]){const spec=JSON.parse(await readFile(new URL(file,import.meta.url),'utf8'));templates.set(spec.name,{stage,spec,id:randomUUID()});}
  const storage=fileStorageFixture(()=>env.FAAS_API,{accountId});
  const native=run=>{const total=run.tasks.length,count=status=>run.tasks.filter(t=>t.status===status).length,finished=count('succeeded')+count('failed')+count('cancelled');return {id:run.id,job_id:run.template.id,account_id:accountId,tasks:total,input_manifest_version:1,input_digest:runtime.jobHash(Buffer.from(JSON.stringify(run.tasks.map(t=>({input_id:t.input_id,input_ref:t.input_ref}))))),aggregate_status:finished<total?'running':count('failed')?'failed':count('cancelled')?'cancelled':'succeeded',tasks_succeeded:count('succeeded'),tasks_failed:count('failed'),tasks_cancelled:count('cancelled'),tasks_running:count('claimed')};};
  let workerCount=0,jobPosts=0,scope,eventPosts=0,dropWakeReplies=true;
  const env={...process.env,GREGALE_REVIEWED_PIPELINE:reviewed?'1':'0',DATABASE_URL:database,PYTHONPATH:join(root,'sdk/python'),FAAS_TOKEN:'fixture-owner',GREGALE_NODE_JOB_RUNTIME:join(root,'sdk/node/dist/job-runtime.js'),GREGALE_FILE_BUCKET:storage.bucket};
  const execute=async run=>{
    for(const task of run.tasks){task.status='claimed';task.attempt++;
      const manifest=join(home,run.id+'-'+task.task_index+'.json'),usePython=workerCount++%2===1;
      const child=spawn(usePython?python:process.execPath,[join(home,'app','pipeline-worker.'+(usePython?'py':'mjs'))],{env:{...env,GREGALE_CSV_PIPELINE_STAGE:run.template.stage,GREGALE_RUN_ID:run.id,GREGALE_TASK_INDEX:String(task.task_index),GREGALE_TASK_COUNT:String(run.tasks.length),GREGALE_TASK_ATTEMPT:String(task.attempt),GREGALE_INPUT_ID:task.input_id,GREGALE_INPUT_REF:task.input_ref,GREGALE_OUTPUT_MANIFEST_PATH:manifest},stdio:['ignore','pipe','pipe']});children.add(child);let stderr='';child.stderr.on('data',b=>stderr+=b);const code=await new Promise(r=>child.once('exit',r));children.delete(child);
      if(code===0){task.output_manifest=JSON.parse(await readFile(manifest,'utf8'));task.status='succeeded';}else{task.status='failed';task.outcome_code='handler_failed';t.diagnostic('Worker failure: '+stderr.slice(0,2000));}
    }
  };
  const control=createServer(async(req,res)=>{
    const send=(status,value)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
    try{
      const u=new URL(req.url,'http://localhost'),parts=u.pathname.split('/');
      if(u.pathname==='/v1/account'){send(200,{id:req.headers.authorization==='Bearer fixture-other'?randomUUID():accountId});return;}
      if(u.pathname.includes('/workflows/')){
        if(req.headers.authorization!=='Bearer fixture-owner'){send(403,{});return;}
        if(req.method==='POST'&&u.pathname.endsWith('/runs')){
          const chunks=[];for await(const chunk of req)chunks.push(chunk);const input=JSON.parse(Buffer.concat(chunks).toString()),key=req.headers['idempotency-key'];
          if(workflowKeys.has(key)){send(200,workflowKeys.get(key));return;}
          const record={id:randomUUID(),app_id:storage.app,workflow_name:reviewed?'csv-reviewed-pipeline':'csv-pipeline',status:'running',input};
          await pool.query('INSERT INTO sdk_pipeline_runs VALUES ($1,$2,$3)',[record.id,record,'[]']);workflowKeys.set(key,record);send(201,record);return;
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
  const client=sdk.createClient(env.FAAS_API,{token:'fixture-owner'}),files=client.storage.files({appSlug:'imports',bucket:storage.bucket});
  const csv=Buffer.from('row,email\n1,one@example.test\n2,two@example.test\n3,invalid email\n'),importId=randomUUID(),key='pipelines/'+importId+'/source.csv';
  await files.put(key,csv,{timeoutMs:10000,ifNoneMatch:'*',contentType:'text/csv'});
  const source={version:1,scope:files.scope,owner_id:'alice',key,size_bytes:csv.length,sha256:runtime.jobHash(csv)};
  const gateStore=sdk.postgresPipelineGateStore((sql,values)=>pool.query(sql,values));
  const {contract,pipeline,reviewContract}=await csvPipeline({sdk,client,scope,reviewed,gateStore,store:sdk.postgresPipelineJobStore((sql,values)=>pool.query(sql,values)),jobStore:await files.jobStore({timeoutMs:10000})});
  const typed=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-owner',scope,ownerId:'alice',contract});
  const run=await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'csv:'+importId});assert.equal((await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'csv:'+importId})).checkpoint.run_id,run.checkpoint.run_id);
  const bob=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-owner',scope,ownerId:'bob',contract});await assert.rejects(bob.resumeById(run.checkpoint.run_id,{timeoutMs:10000}),{code:'scope_mismatch'});
  const wrong=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-other',scope,ownerId:'alice',contract});await assert.rejects(wrong.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'wrong'}),{code:'scope_mismatch'});assert.equal(workflowKeys.size,1);
  let page,browser,reviewURL,approved=false;const browserErrors=[];
  if(reviewed){
    const server=createPipelineReviewServer({sdk,client,scope,files,contract,pipeline,reviewContract,gateStore,token:'fixture-owner',users:{alice:{token:'alice-review',owner_id:'alice',roles:['reviewer']},bob:{token:'bob-review',owner_id:'bob',roles:['reviewer']}},realtime:{...realtime.config(),pollDelayMs:50}});
    await new Promise(r=>server.listen(0,'127.0.0.1',r));reviewURL=`http://127.0.0.1:${server.address().port}`;cleanup.push(()=>new Promise(r=>server.close(r)));
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
    const drive=async usePython=>{const result=await exec(usePython?python:process.execPath,[join(home,'app','pipeline_driver.'+(usePython?'py':'mjs')),path],{env:{...env,...(invocation===0?{GREGALE_PIPELINE_BARRIER:'1'}:{})},timeout:30000});const reply=JSON.parse(result.stdout);if(result.stderr)reply.fixture_stderr=result.stderr;return reply;};
    let reply;
    if(invocation===0){const replies=await Promise.all([drive(false),drive(true)]);assert.ok(replies.every(r=>r.ops[0]?.status==='succeeded'),JSON.stringify({replies,barrier:(await pool.query('SELECT * FROM sdk_pipeline_barrier')).rows}));reply=replies[0];}
    else reply=await drive(usePython);
    assert.equal(reply.result.status==='failed',false,JSON.stringify(reply));assert.equal(reply.ops.some(o=>o.status==='failed'),false,JSON.stringify(reply));
    // Drop each submission/completion journal once: the next process has only
    // native Jobs and the PostgreSQL receipt, with no in-memory recovery state.
    const crash=reply.ops.find(o=>o.name.endsWith('/submit')||o.output?.state==='succeeded'||o.name.endsWith('/open')||o.name.endsWith('/resolve'));
    if(crash&&!dropped.has(crash.seq)){dropped.add(crash.seq);continue;}
    const wait=reply.ops.find(o=>o.kind==='event');
    if(wait&&reviewed&&!approved){
      assert.equal(jobPosts,1);assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows')).rows[0].n,0);
      for(const action of ['gate','snapshot','ticket'])assert.equal((await fetch(`${reviewURL}/runs/${run.checkpoint.run_id}/${action}`,{headers:{Authorization:'Bearer bob-review'}})).status,404);
      await page.goto(reviewURL+'/?run='+run.checkpoint.run_id);await page.fill('#token','alice-review');await page.click('#attach');await page.waitForFunction(()=>document.querySelector('#status').textContent==='Awaiting approval',{},{timeout:30000});
      assert.ok((await page.textContent('#data')).includes(source.sha256));assert.equal(await page.isDisabled('#report'),true);
      await page.fill('#reason','Reviewed exact source and rows');await page.click('#approve');await page.waitForFunction(()=>document.querySelector('#status').textContent==='Decision saved: approved',{},{timeout:30000});
      const gate=(await gateStore.list(run.checkpoint.run_id,'alice',new AbortController().signal))[0];assert.equal(gate.decision.state,'approved');assert.equal(gate.delivery,'pending');
      // A fresh service recovers after the native event committed but its ACK was lost.
      dropWakeReplies=false;const recovery=sdk.createPipelineGateService({scope,pipelineSha256:contract.sha256,contract:reviewContract,store:gateStore,transport:sdk.nativePipelineGateTransport({scope,token:'fixture-owner',contract,timeoutMs:10000}),authorize:async()=>{throw new Error('backend-only recovery');}});await recovery.drain(100,{timeoutMs:10000});
      assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_pipeline_events')).rows[0].n,1);assert.ok(eventPosts>=2);approved=true;
    }
    for(const op of reply.ops){assert.ok(op.seq<=row.journal.length);if(op.kind==='event'){const event=(await pool.query('SELECT body FROM sdk_pipeline_events WHERE run_id=$1',[run.checkpoint.run_id])).rows[0];assert.ok(event);row.journal[op.seq]={...op,status:'succeeded',output:event.body.payload,attempt:1};}else row.journal[op.seq]={...op,status:op.kind==='sleep'?'succeeded':op.status,attempt:1};}
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
  const restored=await typed.resumeById(run.checkpoint.run_id,{timeoutMs:10000}),output=await restored.result({timeoutMs:10000});assert.equal(output.saved,2);assert.equal(output.rejected,1);assert.equal(live.at(-1).report,'ready');assert.equal(live.at(-1).succeeded,3);
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
