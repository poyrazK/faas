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
import {csvBatchPipeline} from './pipeline-batch.mjs';
import {liveFixture} from './live-fixture.mjs';
import {createPipelineReviewServer} from './pipeline-review-api.mjs';

const exec=promisify(execFile),here=fileURLToPath(new URL('.',import.meta.url)),root=resolve(here,'../..');
const python=process.env.GREGALE_LOCAL_PYTHON??join(root,'sdk/python/.venv/bin/python'),pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin';
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<1000;i++){try{if(await fn())return;}catch{}await pause(20);}throw new Error('Fixture did not become ready');}
async function stop(child){if(child.exitCode!==null)return;const done=new Promise(r=>child.once('exit',r));child.kill('SIGTERM');await done;}

test('CSV child composition: PostgreSQL CAS, mixed SDKs, exact recovery, approvals and combined reports',{timeout:300000},async t=>{
  const reviewed=true;
  try{await access(python);await access(join(pgBin,'initdb'));}catch{t.skip('Set GREGALE_LOCAL_PYTHON and GREGALE_LOCAL_PG_BIN');return;}
  const home=await mkdtemp(join(tmpdir(),'gregale-csv-pipeline-')),cleanup=[];t.after(async()=>{try{for(const close of cleanup.reverse())await close();}finally{await rm(home,{recursive:true,force:true});}});
  await cp(here,join(home,'app'),{recursive:true,filter:p=>!p.includes('node_modules')});await symlink(join(root,'sdk/node/node_modules'),join(home,'app/node_modules'));
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale']);const postgres=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});cleanup.push(()=>stop(postgres));
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import(new URL('../../sdk/node/node_modules/pg/lib/index.js',import.meta.url));const pool=new pg.Pool({connectionString:database,statement_timeout:5000});cleanup.push(()=>pool.end());await until(async()=>{await pool.query('SELECT 1');return true;});
  const sdk=await import(new URL('../../sdk/node/dist/preview.js',import.meta.url)),runtime=await import(new URL('../../sdk/node/dist/job-runtime.js',import.meta.url));
  await pool.query(await readFile(new URL('schema.sql',import.meta.url),'utf8'));await pool.query(sdk.pipelineJobSchema);await pool.query(sdk.pipelineGateSchema);await pool.query(sdk.pipelineChildrenSchema);
  await pool.query('CREATE TABLE sdk_pipeline_events (run_id uuid, key text, body jsonb, PRIMARY KEY(run_id,key))');
  await pool.query('CREATE TABLE sdk_pipeline_runs (id uuid PRIMARY KEY, record jsonb NOT NULL, journal jsonb NOT NULL)');
  await pool.query('CREATE TABLE sdk_pipeline_barrier (driver text PRIMARY KEY)');
  const accountId=randomUUID(),jobs=new Map(),templates=new Map(),workflowKeys=new Map(),jobKeys=new Map(),work=new Set(),children=new Set();
  for(const [stage,file]of [['validate','validate-contract.json'],['import','contract.json'],['report','report-contract.json']]){const spec=JSON.parse(await readFile(new URL(file,import.meta.url),'utf8'));templates.set(spec.name,{stage,spec,id:randomUUID()});}
  const storage=fileStorageFixture(()=>env.FAAS_API,{accountId});
  const native=run=>{const total=run.tasks.length,count=status=>run.tasks.filter(t=>t.status===status).length,finished=count('succeeded')+count('failed')+count('cancelled');return {id:run.id,job_id:run.template.id,account_id:accountId,tasks:total,input_manifest_version:1,input_digest:runtime.jobHash(Buffer.from(JSON.stringify(run.tasks.map(t=>({input_id:t.input_id,input_ref:t.input_ref}))))),aggregate_status:finished<total?'running':count('failed')?'failed':count('cancelled')?'cancelled':'succeeded',tasks_succeeded:count('succeeded'),tasks_failed:count('failed'),tasks_cancelled:count('cancelled'),tasks_running:count('claimed')};};
  let workerCount=0,jobPosts=0,scope,eventPosts=0,dropWakeReplies=false,lostChildStart=false,workflowPosts=0,peakChildren=0;
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
          const record={id:randomUUID(),app_id:storage.app,workflow_name:decodeURIComponent(parts[5]),status:'running',input};
          await pool.query('INSERT INTO sdk_pipeline_runs VALUES ($1,$2,$3)',[record.id,record,'[]']);workflowKeys.set(key,record);workflowPosts++;
          peakChildren=Math.max(peakChildren,[...workflowKeys.values()].filter(v=>v.workflow_name==='csv-reviewed-pipeline'&&v.status!=='succeeded').length);
          if(record.workflow_name==='csv-reviewed-pipeline'&&!lostChildStart){lostChildStart=true;res.destroy();return;}
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
  const client=sdk.createClient(env.FAAS_API,{token:'fixture-owner'}),files=client.storage.files({appSlug:'imports',bucket:storage.bucket});
  const gateStore=sdk.postgresPipelineGateStore((sql,values)=>pool.query(sql,values)),childrenStore=sdk.postgresPipelineChildrenStore((sql,values)=>pool.query(sql,values));
  const {contract,child,coordinator,pipeline}=await csvBatchPipeline({sdk,scope,token:'fixture-owner',store:sdk.postgresPipelineJobStore((sql,values)=>pool.query(sql,values)),childrenStore,gateStore,requestTimeoutMs:30000});
  const childPipeline=await csvPipeline({sdk,client,scope,store:sdk.postgresPipelineJobStore((sql,values)=>pool.query(sql,values)),jobStore:await files.jobStore({timeoutMs:10000}),gateStore,reviewed:true});
  const users={alice:{token:'alice-review',owner_id:'alice',roles:['reviewer']},bob:{token:'bob-review',owner_id:'bob',roles:['reviewer']}};
  const server=createPipelineReviewServer({sdk,client,scope,files,contract:childPipeline.contract,pipeline:childPipeline.pipeline,reviewContract:childPipeline.reviewContract,gateStore,users,realtime:{...realtime.config(),pollDelayMs:100},token:'fixture-owner',batch:{contract,child,coordinator,pipeline}});
  await new Promise(r=>server.listen(0,'127.0.0.1',r));const appURL=`http://127.0.0.1:${server.address().port}`;cleanup.push(()=>new Promise(r=>server.close(r)));
  const {chromium}=await import(new URL('../../sdk/node/node_modules/playwright-core/index.mjs',import.meta.url));const browser=await chromium.launch({headless:true,executablePath:process.env.GREGALE_LOCAL_CHROME??'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'});cleanup.push(()=>browser.close());const page=await browser.newPage(),browserErrors=[];page.on('pageerror',e=>browserErrors.push(e.message));page.on('request',r=>assert.notEqual(r.headers().authorization,'Bearer fixture-owner'));let attached=false;
  const imports=[];
  for(let n=0;n<2;n++){
    const importId=randomUUID(),key='pipelines/'+importId+'/source.csv',csv=Buffer.from('row,email\n1,one@example.test\n2,invalid email\n');
    await files.put(key,csv,{timeoutMs:10000,ifNoneMatch:'*',contentType:'text/csv'});
    imports.push({id:'file-'+n,value:{import_id:importId,source:{version:1,scope:files.scope,owner_id:'alice',key,size_bytes:csv.length,sha256:runtime.jobHash(csv)}}});
  }
  const typed=sdk.createJobPipelineClient({baseURL:scope.base_url,scope,token:'fixture-owner',ownerId:'alice',contract});
  const parent=await typed.start({imports},{timeoutMs:10000,idempotencyKey:'batch-start'});
  const bob=sdk.createJobPipelineClient({baseURL:scope.base_url,scope,token:'fixture-owner',ownerId:'bob',contract});
  await assert.rejects(bob.resumeById(parent.checkpoint.run_id,{timeoutMs:10000}),{code:'scope_mismatch'});
  const gates=sdk.createPipelineGateService({scope,pipelineSha256:child.contract.sha256,contract:childPipeline.reviewContract,store:gateStore,transport:sdk.nativePipelineGateTransport({scope,token:'fixture-owner',contract:child.contract,timeoutMs:10000}),authorize:async(_r,_a,user)=>({actor_id:user,owner_id:user,roles:['reviewer']})});
  let invocation=0,duelled=false,reconciled=false,sawWaiting=false,completed=false;const dropped=new Set();
  async function drive(record,journal,pythonDriver,barrier=false){
    const path=join(home,record.id+'-'+pythonDriver+'.json');await writeFile(path,JSON.stringify({protocol:'gregale.workflow.code/v1',run_id:record.id,workflow:record.workflow_name,input:record.input,invocation:++invocation,journal}));
    const r=await exec(pythonDriver?python:process.execPath,[join(home,'app','pipeline_driver.'+(pythonDriver?'py':'mjs')),path],{env:{...env,...(barrier?{GREGALE_CHILDREN_BARRIER:'1'}:{})},timeout:45000});
    const reply=JSON.parse(r.stdout);assert.notEqual(reply.result.status,'failed',JSON.stringify(reply));assert.ok(reply.ops.every(op=>op.status!=='failed'),JSON.stringify(reply));return reply;
  }
  for(let turn=0;turn<100;turn++){
    const records=(await pool.query('SELECT record,journal FROM sdk_pipeline_runs ORDER BY id')).rows;
    for(const row of records){
      const record=row.record;if(record.status==='succeeded')continue;
      let reply;
      if(record.id===parent.checkpoint.run_id&&row.journal.length===1&&!duelled){
        const responses=await Promise.all([drive(record,row.journal,false,true),drive(record,row.journal,true,true)]);reply=responses[0];duelled=true;
      }else reply=await drive(record,row.journal,invocation%2===1);
      const crash=reply.ops.find(op=>op.kind==='step'&&(op.name.endsWith('/open')||op.name.endsWith('/round')||op.name.endsWith('/submit')||op.name.endsWith('/resolve')||op.output?.state==='succeeded'));
      if(crash&&!dropped.has(record.id+':'+crash.seq)){dropped.add(record.id+':'+crash.seq);continue;}
      for(const op of reply.ops){
        if(op.kind==='event'){
          row.journal[op.seq]={...op,status:'waiting',attempt:1};record.status='awaiting_event';await pool.query('UPDATE sdk_pipeline_runs SET record=$2 WHERE id=$1',[record.id,record]);
          const pending=(await gateStore.list(record.id,'alice',new AbortController().signal))[0];assert.ok(pending);
          const view=await coordinator.inspect(parent.checkpoint.run_id,'imports','alice',child,{timeoutMs:30000});
          if(view.children.some(c=>c.waiting)){sawWaiting=true;assert.equal(sdk.pipelineChildrenLiveSnapshot(view,await parent.inspect({timeoutMs:10000}),'batch').waiting.kind,'approval');
            if(!attached){
              for(const action of ['view','snapshot','ticket'])assert.equal((await fetch(`${appURL}/batches/${parent.checkpoint.run_id}/${action}`,{headers:{Authorization:'Bearer bob-review'}})).status,404);
              await page.goto(appURL+'/batch-review.html?run='+parent.checkpoint.run_id);await page.fill('#token','alice-review');await page.click('button');await page.waitForFunction(()=>document.querySelector('#status').textContent==='Awaiting child approval',{},{timeout:30000});assert.ok((await page.textContent('#children')).includes('file-0'));attached=true;
            }
          }
          await assert.rejects(gates.decide(pending.intent.id,{binding_sha256:pending.intent.binding_sha256,state:'approved',payload:{reason:'reviewed'}},'bob',{timeoutMs:10000}),{code:'forbidden'});
          await gates.decide(pending.intent.id,{binding_sha256:pending.intent.binding_sha256,state:'approved',payload:{reason:'reviewed'}},'alice',{timeoutMs:10000});
          const event=(await pool.query('SELECT body FROM sdk_pipeline_events WHERE run_id=$1',[record.id])).rows[0];assert.ok(event);
          row.journal[op.seq]={...op,status:'succeeded',attempt:1,output:event.body.payload};record.status='running';
        }else row.journal[op.seq]={...op,status:op.kind==='sleep'?'succeeded':op.status,attempt:1};
      }
      if(reply.result.status==='succeeded'){record.status='succeeded';record.output=reply.result.output;workflowKeys.set([...workflowKeys.entries()].find(([,v])=>v.id===record.id)[0],record);}
      await pool.query('UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1',[record.id,record,JSON.stringify(row.journal)]);
    }
    if(duelled&&!reconciled){
      const receipt=await childrenStore.load(sdk.pipelineChildrenIdentity(scope,parent.checkpoint.run_id,'imports'),new AbortController().signal);
      for(let n=0;n<receipt.state.children.length;n++)if(receipt.state.children[n].phase==='submission_unresolved'){
        const raw=(await pool.query("SELECT record FROM sdk_pipeline_runs WHERE record->>'workflow_name'='csv-reviewed-pipeline'")).rows.find(r=>r.record.input.lineage.item_id===receipt.intent.inputs[n].id).record;
        await assert.rejects(coordinator.reconcile(parent.checkpoint.run_id,'imports',receipt.intent.inputs[n].id,raw.id,'bob',{timeoutMs:10000}),{code:'forbidden'});
        await coordinator.reconcile(parent.checkpoint.run_id,'imports',receipt.intent.inputs[n].id,raw.id,'alice',{timeoutMs:10000});reconciled=true;
      }
    }
    const status=await parent.inspect({timeoutMs:10000});if(status.status==='succeeded'){completed=true;break;}
  }
  assert.equal(completed,true);assert.equal(duelled,true);assert.equal(reconciled,true);assert.equal(sawWaiting,true);
  assert.equal(workflowPosts,3);assert.equal(peakChildren,2);assert.equal(jobPosts,6);assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows')).rows[0].n,2);
  const output=await parent.result({timeoutMs:10000});assert.equal(output.saved,2);assert.equal(output.rejected,2);assert.deepEqual(output.children.map(c=>c.id),['file-0','file-1']);
  for(const c of output.children){assert.equal(c.phase,'succeeded');assert.equal(c.status,'completed');assert.ok(new TextDecoder().decode(await sdk.readPipelineArtifact(files,c.report,'alice',{timeoutMs:10000})).includes('invalid_email'));}
  await assert.rejects(coordinator.inspect(parent.checkpoint.run_id,'imports','bob',child,{timeoutMs:10000}),{code:'forbidden'});
  assert.equal(attached,true);await page.waitForFunction(()=>document.querySelector('#totals').textContent==='2 rows saved; 2 rows rejected',{},{timeout:30000});assert.equal(await page.locator('#children a').count(),2);await page.fill('#token','bob-review');assert.equal(await page.textContent('#children'),'');assert.equal(await page.textContent('#totals'),'');assert.deepEqual(browserErrors,[]);
  const receipt=(await pool.query('SELECT state FROM gregale_pipeline_children')).rows[0];assert.equal(receipt.state.children.filter(c=>c.phase==='succeeded').length,2);
});
