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

const exec=promisify(execFile),here=fileURLToPath(new URL('.',import.meta.url)),root=resolve(here,'../..');
const python=process.env.GREGALE_LOCAL_PYTHON??join(root,'sdk/python/.venv/bin/python'),pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin';
const pause=ms=>new Promise(r=>setTimeout(r,ms));
async function until(fn){for(let i=0;i<1000;i++){try{if(await fn())return;}catch{}await pause(20);}throw new Error('Fixture did not become ready');}
async function stop(child){if(child.exitCode!==null)return;const done=new Promise(r=>child.once('exit',r));child.kill('SIGTERM');await done;}

test('CSV durable pipeline: real PostgreSQL, alternating Node/Python drivers/workers, lost journals and live report verification',{timeout:240000},async t=>{
  try{await access(python);await access(join(pgBin,'initdb'));}catch{t.skip('Set GREGALE_LOCAL_PYTHON and GREGALE_LOCAL_PG_BIN');return;}
  const home=await mkdtemp(join(tmpdir(),'gregale-csv-pipeline-')),cleanup=[];t.after(async()=>{try{for(const close of cleanup.reverse())await close();}finally{await rm(home,{recursive:true,force:true});}});
  await cp(here,join(home,'app'),{recursive:true,filter:p=>!p.includes('node_modules')});await symlink(join(root,'sdk/node/node_modules'),join(home,'app/node_modules'));
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale']);const postgres=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});cleanup.push(()=>stop(postgres));
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import(new URL('../../sdk/node/node_modules/pg/lib/index.js',import.meta.url));const pool=new pg.Pool({connectionString:database,statement_timeout:5000});cleanup.push(()=>pool.end());await until(async()=>{await pool.query('SELECT 1');return true;});
  const sdk=await import(new URL('../../sdk/node/dist/preview.js',import.meta.url)),runtime=await import(new URL('../../sdk/node/dist/job-runtime.js',import.meta.url));
  await pool.query(await readFile(new URL('schema.sql',import.meta.url),'utf8'));await pool.query(sdk.pipelineJobSchema);
  await pool.query('CREATE TABLE sdk_pipeline_runs (id uuid PRIMARY KEY, record jsonb NOT NULL, journal jsonb NOT NULL)');
  await pool.query('CREATE TABLE sdk_pipeline_barrier (driver text PRIMARY KEY)');
  const accountId=randomUUID(),jobs=new Map(),templates=new Map(),workflowKeys=new Map(),jobKeys=new Map(),work=new Set(),children=new Set();
  for(const [stage,file]of [['validate','validate-contract.json'],['import','contract.json'],['report','report-contract.json']]){const spec=JSON.parse(await readFile(new URL(file,import.meta.url),'utf8'));templates.set(spec.name,{stage,spec,id:randomUUID()});}
  const storage=fileStorageFixture(()=>env.FAAS_API,{accountId});
  const native=run=>{const total=run.tasks.length,count=status=>run.tasks.filter(t=>t.status===status).length,finished=count('succeeded')+count('failed')+count('cancelled');return {id:run.id,job_id:run.template.id,account_id:accountId,tasks:total,input_manifest_version:1,input_digest:runtime.jobHash(Buffer.from(JSON.stringify(run.tasks.map(t=>({input_id:t.input_id,input_ref:t.input_ref}))))),aggregate_status:finished<total?'running':count('failed')?'failed':count('cancelled')?'cancelled':'succeeded',tasks_succeeded:count('succeeded'),tasks_failed:count('failed'),tasks_cancelled:count('cancelled'),tasks_running:count('claimed')};};
  let workerCount=0,jobPosts=0,scope;
  const env={...process.env,DATABASE_URL:database,PYTHONPATH:join(root,'sdk/python'),FAAS_TOKEN:'fixture-owner',GREGALE_NODE_JOB_RUNTIME:join(root,'sdk/node/dist/job-runtime.js'),GREGALE_FILE_BUCKET:storage.bucket};
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
          const record={id:randomUUID(),app_id:storage.app,workflow_name:'csv-pipeline',status:'running',input};
          await pool.query('INSERT INTO sdk_pipeline_runs VALUES ($1,$2,$3)',[record.id,record,'[]']);workflowKeys.set(key,record);send(201,record);return;
        }
        const id=parts[4],row=(await pool.query('SELECT record,journal FROM sdk_pipeline_runs WHERE id=$1',[id])).rows[0];if(!row){send(404,{});return;}
        if(u.pathname.endsWith('/steps')){send(200,{steps:row.journal.map(s=>({step_name:s.name,status:s.status,attempt:s.attempt??1}))});return;}
        if(u.pathname.endsWith('/cancel')){row.record.cancelled_at=new Date().toISOString();row.record.status='failed';await pool.query('UPDATE sdk_pipeline_runs SET record=$2 WHERE id=$1',[id,row.record]);}
        send(200,row.record);return;
      }
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
  await new Promise(r=>control.listen(0,'127.0.0.1',r));env.FAAS_API=`http://127.0.0.1:${control.address().port}`;scope={base_url:env.FAAS_API,account_id:accountId,app_id:storage.app,app_slug:'imports'};env.GREGALE_PIPELINE_SCOPE=JSON.stringify(scope);
  cleanup.push(async()=>{for(const child of children)await stop(child);await Promise.allSettled(work);await new Promise(r=>control.close(r));});
  const client=sdk.createClient(env.FAAS_API,{token:'fixture-owner'}),files=client.storage.files({appSlug:'imports',bucket:storage.bucket});
  const csv=Buffer.from('row,email\n1,one@example.test\n2,two@example.test\n3,invalid email\n'),importId=randomUUID(),key='pipelines/'+importId+'/source.csv';
  await files.put(key,csv,{timeoutMs:10000,ifNoneMatch:'*',contentType:'text/csv'});
  const source={version:1,scope:files.scope,owner_id:'alice',key,size_bytes:csv.length,sha256:runtime.jobHash(csv)};
  const {contract,pipeline}=await csvPipeline({sdk,client,scope,store:sdk.postgresPipelineJobStore((sql,values)=>pool.query(sql,values)),jobStore:await files.jobStore({timeoutMs:10000})});
  const typed=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-owner',scope,ownerId:'alice',contract});
  const run=await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'csv:'+importId});assert.equal((await typed.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'csv:'+importId})).checkpoint.run_id,run.checkpoint.run_id);
  const bob=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-owner',scope,ownerId:'bob',contract});await assert.rejects(bob.resumeById(run.checkpoint.run_id,{timeoutMs:10000}),{code:'scope_mismatch'});
  const wrong=sdk.createJobPipelineClient({baseURL:env.FAAS_API,token:'fixture-other',scope,ownerId:'alice',contract});await assert.rejects(wrong.start({import_id:importId,source},{timeoutMs:10000,idempotencyKey:'wrong'}),{code:'scope_mismatch'});assert.equal(workflowKeys.size,1);
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
    const crash=reply.ops.find(o=>o.name.endsWith('/submit')||o.output?.state==='succeeded');
    if(crash&&!dropped.has(crash.seq)){dropped.add(crash.seq);continue;}
    for(const op of reply.ops){assert.ok(op.seq<=row.journal.length);row.journal[op.seq]={...op,status:op.kind==='sleep'?'succeeded':op.status,attempt:1};}
    if(reply.result.status==='succeeded'){row.record.status='succeeded';row.record.output=reply.result.output;finished=true;}
    await pool.query('UPDATE sdk_pipeline_runs SET record=$2,journal=$3 WHERE id=$1',[run.checkpoint.run_id,row.record,JSON.stringify(row.journal)]);
    const snapshot=await sdk.jobPipelineLiveSnapshot(run,{resourceId:importId,steps:['validate','import','report'],timeoutMs:10000,inspectJob:(step,signal)=>pipeline.inspectJob(run.checkpoint.run_id,step,'alice',{timeoutMs:10000,signal}),reportReady:async output=>{await sdk.readPipelineArtifact(files,output.report,'alice',{timeoutMs:10000,maxBytes:1024*1024});return true;}});live.push(snapshot);
    if(finished)break;
    await pause(30);
  }
  assert.equal(finished,true);assert.equal(jobs.size,3);assert.equal(jobPosts,3);assert.equal((await pool.query('SELECT count(*)::int AS n FROM gregale_pipeline_jobs')).rows[0].n,3);assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows')).rows[0].n,2);
  const restored=await typed.resumeById(run.checkpoint.run_id,{timeoutMs:10000}),output=await restored.result({timeoutMs:10000});assert.equal(output.saved,2);assert.equal(output.rejected,1);assert.equal(live.at(-1).report,'ready');assert.equal(live.at(-1).succeeded,3);
  const report=await sdk.readPipelineArtifact(files,output.report,'alice',{timeoutMs:10000,maxBytes:1024*1024});assert.ok(new TextDecoder().decode(report).includes(importId+':3,invalid_email'));
  await assert.rejects(async()=>sdk.readPipelineArtifact(files,output.report,'bob',{timeoutMs:1000}),{code:'artifact_changed'});
  storage.objects.set(key,Buffer.from('changed'));await assert.rejects(sdk.readPipelineArtifact(files,source,'alice',{timeoutMs:1000}),{code:'artifact_changed'});
  const cancelled=await typed.start({import_id:randomUUID(),source},{timeoutMs:10000,idempotencyKey:'cancel'});await cancelled.cancel({timeoutMs:10000});assert.equal((await cancelled.inspect({timeoutMs:10000})).cancelled,true);assert.equal(jobs.size,3);
});
