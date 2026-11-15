import test from 'node:test';
import assert from 'node:assert/strict';
import {spawn,execFile} from 'node:child_process';
import {promisify} from 'node:util';
import {createServer} from 'node:http';
import {randomUUID} from 'node:crypto';
import {mkdtemp,cp,symlink,readFile,writeFile,rm,access,mkdir} from 'node:fs/promises';
import {fileURLToPath} from 'node:url';
import {join,resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {csvInputs} from './csv.mjs';
import {fileStorageFixture} from './file-fixture.mjs';
import {createFileImportServer} from './file-api.mjs';
import {fileBroker} from './file-browser.mjs';

const exec=promisify(execFile),here=fileURLToPath(new URL('.',import.meta.url)),root=resolve(here,'../..');
const pgBin=process.env.GREGALE_LOCAL_PG_BIN??'/opt/homebrew/opt/postgresql@16/bin',python=process.env.GREGALE_LOCAL_PYTHON??resolve(root,'sdk/python/.venv/bin/python');
async function until(work){const end=Date.now()+20000;while(Date.now()<end){try{if(await work())return;}catch{}await new Promise(r=>setTimeout(r,50));}assert.fail('Local example did not become ready');}
async function stop(child){if(child.exitCode!==null)return;const ended=new Promise(r=>child.once('exit',r));child.kill('SIGTERM');const timer=setTimeout(()=>child.kill('SIGKILL'),5000);try{await ended;}finally{clearTimeout(timer);}}
test('CSV reader handles quotes, rejects malformed input and preserves stable row identities',()=>{
  assert.deepEqual(csvInputs('row,email\r\n1,"one@example.test"\r\n2,"invalid, email"\r\n','batch',2),[{id:'batch:1',value:{row:1,email:'one@example.test'}},{id:'batch:2',value:{row:2,email:'invalid, email',fail_once:true}}]);
  for(const csv of ['row,email\n1,"bad','row,email\n1,"bad"trailing','wrong,email\n1,abc','row,email\n01,abc'])assert.throws(()=>csvInputs(csv,'batch'));
});
// Real PostgreSQL and Node/Python worker processes, with fixture control-plane
// scheduling and task env. Deployed acceptance is tests/journeys/sdk_jobs_test.go.
test('CSV import: cross-language resume, row rejection, failed-only replay and duplicate delivery',{timeout:240000},async t=>{
  try{await access(join(pgBin,'initdb'));await access(python);}catch{t.skip('Set GREGALE_LOCAL_PG_BIN and GREGALE_LOCAL_PYTHON');return;}
  const home=await mkdtemp(join(tmpdir(),'gregale-csv-job-')),cleanup=[];t.after(async()=>{try{for(const fn of cleanup.reverse())await fn();}finally{await rm(home,{recursive:true,force:true});}});
  await cp(here,join(home,'app'),{recursive:true,filter:p=>!p.includes('node_modules')});await symlink(resolve(root,'sdk/node/node_modules'),join(home,'app/node_modules'));
  await exec(join(pgBin,'initdb'),['-D',join(home,'pg'),'-U','sdk','-A','trust','--no-locale']);const pgProcess=spawn(join(pgBin,'postgres'),['-D',join(home,'pg'),'-k',home,'-h',''],{stdio:'ignore'});cleanup.push(()=>stop(pgProcess));
  const database=`postgresql://sdk@localhost/postgres?host=${encodeURIComponent(home)}`;
  const {default:pg}=await import(new URL('../../sdk/node/node_modules/pg/lib/index.js',import.meta.url));const pool=new pg.Pool({connectionString:database});cleanup.push(()=>pool.end());await until(async()=>{await pool.query('SELECT 1');return true;});
  await pool.query(await readFile(join(here,'schema.sql'),'utf8'));
  const sdk=await import(new URL('../../sdk/node/dist/index.js',import.meta.url));const runtime=await import(new URL('../../sdk/node/dist/job-runtime.js',import.meta.url));
  const contract=new sdk.JobContract(JSON.parse(await readFile(join(here,'contract.json'),'utf8'))),jobID=randomUUID(),accountID=randomUUID(),runs=new Map(),accepted=new Map(),workers=new Set(),pending=new Set();let language='node';
  const native=(run)=>{const count=s=>run.tasks.filter(t=>t.status===s).length,done=count('succeeded')+count('failed')+count('cancelled');return {id:run.id,job_id:jobID,account_id:accountID,tasks:run.tasks.length,input_manifest_version:1,input_digest:runtime.jobHash(Buffer.from(JSON.stringify(run.tasks.map(t=>({input_id:t.input_id,input_ref:t.input_ref}))))),aggregate_status:done<run.tasks.length?(count('claimed')?'running':'queued'):count('failed')?'failed':count('cancelled')?'cancelled':'succeeded',tasks_succeeded:count('succeeded'),tasks_failed:count('failed'),tasks_cancelled:count('cancelled'),tasks_running:count('claimed'),...(run.source?{source_run_id:run.source}:{})};};
  const env={...process.env,DATABASE_URL:database,GREGALE_NODE_SDK:resolve(root,'sdk/node/dist/index.js'),GREGALE_NODE_JOB_RUNTIME:resolve(root,'sdk/node/dist/job-runtime.js'),GREGALE_LOCAL_JOB_STORE:join(home,'blobs'),PYTHONPATH:resolve(root,'sdk/python'),FAAS_TOKEN:'fixture-owner'};
  const execute=async(run,workerLanguage)=>{
    // Serialize only this local fixture; production admission/parallelism belongs to the scheduler.
    for(const task of run.tasks){if(task.status==='cancelled')continue;task.status='claimed';task.attempt++;
      const manifest=join(home,run.id+'-'+task.task_index+'.json');const workerEnv={...env,GREGALE_RUN_ID:run.id,GREGALE_TASK_INDEX:String(task.task_index),GREGALE_TASK_COUNT:String(run.tasks.length),GREGALE_TASK_ATTEMPT:String(task.attempt),GREGALE_INPUT_ID:task.input_id,GREGALE_INPUT_REF:task.input_ref,GREGALE_OUTPUT_MANIFEST_PATH:manifest};
      const child=spawn(workerLanguage==='node'?process.execPath:python,[join(home,'app',workerLanguage==='node'?'worker.mjs':'worker.py')],{env:workerEnv,stdio:'ignore'});workers.add(child);const code=await new Promise(r=>child.once('exit',r));workers.delete(child);
      if(task.status==='cancelled')continue;if(code===0){task.output_manifest=JSON.parse(await readFile(manifest,'utf8'));task.status='succeeded';}else{task.status='failed';task.outcome_code='handler_failed';}
    }
  };
  const storage=fileStorageFixture(()=>env.FAAS_API);
  const control=createServer(async(req,res)=>{const send=(status,value)=>{res.writeHead(status,{'Content-Type':'application/json'});res.end(JSON.stringify(value));};
    try{if(await storage.handle(req,res))return;if(req.headers.authorization!=='Bearer fixture-owner'){send(404,{});return;}const url=new URL(req.url,'http://localhost'),parts=url.pathname.split('/');
      if(url.pathname==='/v1/jobs/csv-import'){send(200,{id:jobID,account_id:accountID,name:'csv-import'});return;}
      const source=runs.get(parts[5]);
      if(req.method==='POST'&&(url.pathname.endsWith('/runs')||url.pathname.endsWith('/replay-failed'))){
        const key=req.headers['idempotency-key']+url.pathname;if(accepted.has(key)){send(200,accepted.get(key));return;}
        let refs;if(source){assert.ok(['failed','cancelled','dead_letter'].includes(native(source).aggregate_status));refs=source.tasks.filter(t=>t.status!=='succeeded').map(t=>({...t,source_task_index:t.task_index}));}
        else{const chunks=[];for await(const chunk of req)chunks.push(chunk);refs=JSON.parse(Buffer.concat(chunks).toString()).inputs;}
        const run={id:randomUUID(),...(source?{source:source.id}:{}),tasks:refs.map((ref,index)=>({run_id:'',task_index:index,input_id:ref.input_id,input_ref:ref.input_ref,status:'queued',attempt:0,...(source?{source_task_index:ref.source_task_index}:{})}))};for(const task of run.tasks)task.run_id=run.id;runs.set(run.id,run);const receipt=native(run);accepted.set(key,receipt);send(201,receipt);const work=execute(run,language);pending.add(work);work.then(()=>pending.delete(work),()=>pending.delete(work));return;
      }
      if(!source){send(404,{});return;}
      if(url.pathname.endsWith('/artifacts/result/download')){const artifact=source.tasks[Number(parts[7])].output_manifest.artifacts.find(a=>a.name==='result');send(200,{...artifact,download:storage.signArtifact(artifact.uri)});return;}
      if(url.pathname.endsWith('/tasks')){send(200,{tasks:source.tasks,offset:0,next_offset:-1,total:source.tasks.length,limit:200});return;}
      send(200,native(source));
    }catch(error){send(500,{code:'fixture_failed'});}
  });
  await new Promise(r=>control.listen(0,'127.0.0.1',r));cleanup.push(async()=>{for(const child of workers)await stop(child);await Promise.allSettled(pending);await new Promise(r=>control.close(r));});
  env.FAAS_API=`http://127.0.0.1:${control.address().port}`;
  const free=createServer();await new Promise(r=>free.listen(0,'127.0.0.1',r));const apiPort=free.address().port;await new Promise(r=>free.close(r));
  const api=spawn(process.execPath,[join(home,'app/api.mjs')],{env:{...env,PORT:String(apiPort),GREGALE_IMPORT_STATE:join(home,'state'),GREGALE_IMPORT_APP_TOKEN:'application-only',GREGALE_CSV_FAIL_ROW:'2'},stdio:'ignore'});cleanup.push(()=>stop(api));const appURL=`http://127.0.0.1:${apiPort}`,headers={Authorization:'Bearer application-only'};
  await until(async()=>{const response=await fetch(appURL+'/imports/missing',{headers});return response.status===404;});
  const csv='row,email\n1,one@example.test\n2,two@example.test\n3,invalid email\n';await writeFile(join(home,'rows.csv'),csv);
  const runDriver=async(binary,mode,checkpoint,extraEnv={})=>{
    const driver=binary==='node'?'import.mjs':'import_job.py';const result=await exec(binary==='node'?process.execPath:python,[join(home,'app',driver),mode,...(mode==='submit'?[join(home,'rows.csv')]:[])],{env:{...env,GREGALE_JOB_CHECKPOINT:checkpoint,...extraEnv},timeout:90000});return JSON.parse(result.stdout);
  };
  for(const workerLanguage of ['node','python']){
    language=workerLanguage;const id=workerLanguage+'-batch',checkpoint=join(home,'state',id+'.json');
    if(workerLanguage==='node'){
      const response=await fetch(appURL+'/imports/'+id,{method:'POST',headers,body:csv});assert.equal(response.status,202);await response.json();
      const repeat=await fetch(appURL+'/imports/'+id,{method:'POST',headers,body:csv});assert.equal(repeat.status,200);
    }else await runDriver('python','submit',checkpoint,{GREGALE_JOB_SUBMISSION_KEY:id,GREGALE_IMPORT_ID:id,GREGALE_CSV_FAIL_ROW:'2'});
    const follower=workerLanguage==='node'?'python':'node';assert.equal((await runDriver(follower,'wait',checkpoint)).status,'failed');
    const before=await runDriver(follower,'results',checkpoint);assert.equal(before[0].outcome,'succeeded',JSON.stringify(before));assert.equal(before[1].outcome,'failed');assert.equal(before[2].outcome,'succeeded',JSON.stringify(before));assert.equal(before[2].result.rejection,'invalid_email');
    const download=await fetch(appURL+'/imports/'+id+'/rejections.csv',{headers});assert.equal(download.status,200);assert.ok((await download.text()).includes(id+':3,invalid_email'));
    const replay=join(home,id+'-replay.json');await runDriver(follower,'replay',checkpoint,{GREGALE_JOB_SUBMISSION_KEY:id+'-retry',GREGALE_JOB_REPLAY_CHECKPOINT:replay});
    assert.equal((await runDriver(workerLanguage,'wait',replay)).status,'succeeded');assert.equal((await runDriver(workerLanguage,'results',replay))[0].result.row,2);
    const c=sdk.parseJobCheckpoint(JSON.parse(await readFile(replay,'utf8')));assert.deepEqual(c.inputs.map(i=>i.input_id),[id+':2']);
    // Explicit duplicate delivery of a successful input still inserts once.
    const data=runs.get(c.run_id).tasks[0],duplicate={id:randomUUID(),tasks:[{...data,run_id:'',task_index:0,status:'queued',attempt:0}]};duplicate.tasks[0].run_id=duplicate.id;await execute(duplicate,workerLanguage);
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows WHERE input_id LIKE $1',[id+':%'])).rows[0].n,2);
  }
  // The same real workers now use the built-in managed object JobStore.
  env.GREGALE_FILE_APP='imports';env.GREGALE_FILE_BUCKET=storage.bucket;
  const preview=await import(new URL('../../sdk/node/dist/preview.js',import.meta.url)),client=preview.createClient(env.FAAS_API,{token:'fixture-owner'});
  const files=client.storage.files({appSlug:'imports',bucket:storage.bucket}),store=await files.jobStore({timeoutMs:10000});
  const fileJob=client.jobs.defineJob(contract,{store}),users={alice:'alice-application-token',bob:'bob-application-token'},fileDirectory=join(home,'file-state');
  const makeApp=()=>createFileImportServer({client,files,job:fileJob,directory:fileDirectory,users,sdk:preview,browserModule:new URL('../../sdk/node/dist/files.js',import.meta.url)});
  let fileApp=await makeApp();await new Promise(r=>fileApp.listen(0,'127.0.0.1',r));cleanup.push(()=>new Promise(r=>fileApp.close(r)));
  let fileURL=`http://127.0.0.1:${fileApp.address().port}`;
  const appRequest=async(path,init={})=>{const r=await fetch(fileURL+path,{...init,headers:{Authorization:'Bearer '+users.alice,'Content-Type':'application/json'}});assert.ok(r.ok,await r.clone().text());return r.json();};
  for(const workerLanguage of ['node','python']){
    language=workerLanguage;const data=new Blob([csv]),source=preview.blobFileSource(data),source_digest=await preview.fileSourceDigest(source,new AbortController().signal);
    const record=await appRequest('/files',{method:'POST',body:JSON.stringify({size_bytes:data.size,source_digest})});
    const browserFiles=preview.createFileBucket(record.scope,fileBroker(record.id,appRequest));let checkpoint;const pause=new AbortController();
    await assert.rejects(browserFiles.upload(record.key,source,{timeoutMs:10000,contentType:'text/csv',signal:pause.signal,onCheckpoint:c=>{checkpoint=c;},onProgress:()=>pause.abort()}),e=>e.code==='cancelled');
    assert.equal(checkpoint.parts.length,1);
    // Restart the app, keep its durable ownership records and resume in a new client.
    await new Promise(r=>fileApp.close(r));fileApp=await makeApp();await new Promise(r=>fileApp.listen(0,'127.0.0.1',r));fileURL=`http://127.0.0.1:${fileApp.address().port}`;
    const deniedBefore=storage.counts().issued;
    for(const action of ['', '/begin','/session','/parts','/sign-part','/complete','/abort','/start','/progress','/rejections']){
      const method=['','/progress','/rejections'].includes(action)?'GET':'POST';
      const denied=await fetch(fileURL+'/files/'+record.id+action,{method,headers:{Authorization:'Bearer '+users.bob},...(method==='POST'?{body:JSON.stringify({id:checkpoint.upload_id,part:1})}:{})});assert.equal(denied.status,404);
    }assert.equal(storage.counts().issued,deniedBefore);
    if(workerLanguage==='python'){
      const checkpointPath=join(home,'file-checkpoint.json'),sourcePath=join(home,'upload.csv');await writeFile(checkpointPath,JSON.stringify(checkpoint));await writeFile(sourcePath,csv);
      await exec(python,['-c',`import asyncio,json,os
from pathlib import Path
from faas_sdk.preview import FaaSClient,FileCheckpoint,FileOptions,PathFileSource
async def main():
    checkpoint=Path(os.environ['FILE_CHECKPOINT'])
    def save(c): checkpoint.write_text(json.dumps(c.to_dict()))
    async with FaaSClient(base_url=os.environ['FAAS_API'],token=os.environ['FAAS_TOKEN']) as client:
        await client.files(app_slug='imports',bucket=os.environ['GREGALE_FILE_BUCKET']).resume(FileCheckpoint.from_dict(json.loads(checkpoint.read_text())),PathFileSource(os.environ['FILE_SOURCE']),options=FileOptions(timeout=30,on_checkpoint=save))
asyncio.run(main())`],{env:{...env,FILE_CHECKPOINT:checkpointPath,FILE_SOURCE:sourcePath},timeout:40000});
      checkpoint=JSON.parse(await readFile(checkpointPath,'utf8'));
    }
    await preview.createFileBucket(record.scope,fileBroker(record.id,appRequest)).resume(preview.parseFileCheckpoint(JSON.parse(JSON.stringify(checkpoint))),source,{timeoutMs:10000});
    const accepted=await appRequest('/files/'+record.id+'/start',{method:'POST',body:'{}'});assert.ok(accepted.runId);
    await until(async()=>{const p=await appRequest('/files/'+record.id+'/progress');assert.notEqual(p.status,'failed');return p.status==='succeeded';});
    const report=await fetch(fileURL+'/files/'+record.id+'/rejections',{headers:{Authorization:'Bearer '+users.alice}});assert.equal(report.status,200);assert.ok((await report.text()).includes(record.id+':3,invalid_email'));
    assert.ok(storage.objects.has(record.key));assert.ok(storage.objects.has('imports/'+record.id+'/rejections.csv'));
    assert.equal((await pool.query('SELECT count(*)::int AS n FROM sdk_csv_rows WHERE input_id LIKE $1',[record.id+':%'])).rows[0].n,2);
  }
  const chrome=process.env.GREGALE_LOCAL_CHROME??'/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
  try{await access(chrome);}catch{t.diagnostic('Browser UI check skipped: set GREGALE_LOCAL_CHROME');return;}
  const {chromium}=await import(new URL('../../sdk/node/node_modules/playwright-core/index.mjs',import.meta.url));
  const browser=await chromium.launch({headless:true,executablePath:chrome});
  try{
    language='node';const page=await browser.newPage(),errors=[];page.on('pageerror',e=>errors.push(e.message));
    page.on('request',request=>{const u=new URL(request.url());if(u.pathname.startsWith('/file-data/'))assert.equal(request.headers().authorization,undefined);assert.notEqual(request.headers().authorization,'Bearer fixture-owner');});
    await page.goto(fileURL);await page.fill('#token',users.alice);await page.setInputFiles('#file',{name:'rows.csv',mimeType:'text/csv',buffer:Buffer.from(csv)});
    await page.click('#start');await page.waitForFunction(()=>document.querySelector('#status').textContent.startsWith('succeeded:'),{},{timeout:60000});
    const pendingDownload=page.waitForEvent('download');await page.click('#report');const download=await pendingDownload;await download.saveAs(join(home,'browser-rejections.csv'));
    assert.ok((await readFile(join(home,'browser-rejections.csv'),'utf8')).includes('invalid_email'));assert.deepEqual(errors,[]);
    const persisted=JSON.parse(await page.evaluate(()=>localStorage.getItem('gregale-file-import')));assert.ok(persisted.checkpoint.parts.length);assert.equal(JSON.stringify(persisted).includes(users.alice),false);
    await page.reload();await page.fill('#token',users.alice);await page.setInputFiles('#file',{name:'rows.csv',mimeType:'text/csv',buffer:Buffer.from(csv)});await page.click('#resume');await page.waitForFunction(()=>document.querySelector('#status').textContent.startsWith('succeeded:'),{},{timeout:60000});
  }finally{await browser.close();}
});
