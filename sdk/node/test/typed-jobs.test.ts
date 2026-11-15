import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {mkdtemp,readFile,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createClient,JobContract,JobError,runLocalJobBatch,runJobTask,localJobStore,prepareJobInput,readJobInput,decodeJobOutput,parseJobCheckpoint,type JobContractSpec,type JobCheckpoint,type JobInputBinding} from '../src/index.js';
import {createClient as preview} from '../src/preview.js';
import {jobBytes,jobHash} from '../src/job-runtime.js';
import {generateJobTypes} from '../src/job-types.js';

test('optional submission scope guards reject another endpoint or account before POST',async()=>{
  const h=harness(),job=createClient(base,{token:'owner',fetch:h.fetch}).jobs.defineJob(contract);
  assert.throws(()=>job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'guard-base',expectedBaseURL:'https://other.test'}),code('invalid_receipt'));
  assert.throws(()=>job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'guard-contract',expectedContractSha256:'0'.repeat(64)}),code('invalid_receipt'));
  await assert.rejects(job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'guard-account',expectedAccountId:'99999999-9999-4999-8999-999999999999'}),code('invalid_response'));
  assert.equal(h.counts().posts,0);
  await job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'guard-match',expectedBaseURL:base,expectedAccountId:f.account_id});assert.equal(h.counts().posts,1);
});

type Input={row:number;email:string;fail_once?:boolean};type Output={row:number;saved:boolean;rejection:'none'|'invalid_email'};
const f=JSON.parse(readFileSync(new URL('../../../request-contracts/typed-jobs.json',import.meta.url),'utf8')) as {contract:JobContractSpec;inputs:{id:string;value:Input}[];account_id:string;job_id:string;run_id:string;replay_id:string;cases:{scenario:string;outcome:string|null;error:string|null}[]};
const contract=new JobContract<Input,Output>(f.contract),base='https://jobs.test/prefix';
const code=(c:string)=>(e:unknown)=>e instanceof JobError&&e.code===c;
function harness(scenario='success'){
  const refs:JobInputBinding[]=[];const blobs=new Map<string,Uint8Array>();let posts=0,reads=0,signed=0,cancels=0,replays=0;let state='succeeded';let selected:number[]=[0,1];
  const run=(id=f.run_id)=>({id,job_id:f.job_id,account_id:scenario==='wrong_account'?'99999999-9999-4999-8999-999999999999':f.account_id,input_manifest_version:1,input_digest:scenario==='wrong_digest'?'a'.repeat(64):jobHash(Buffer.from(JSON.stringify(selected.map(i=>refs[i])))),tasks:selected.length,aggregate_status:state,tasks_succeeded:state==='succeeded'?selected.length:state==='failed'?1:0,tasks_failed:state==='failed'?1:0,tasks_cancelled:state==='cancelled'?selected.length:0,tasks_running:state==='running'?selected.length:0,...(id===f.replay_id?{source_run_id:f.run_id}:{})});
  const fetch:typeof globalThis.fetch=async(url,init)=>{
    const p=new URL(String(url)).pathname.replace('/prefix','');assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer owner');assert.equal(init?.redirect,'error');
    if(p==='/v1/jobs/csv-import')return Response.json({id:f.job_id,account_id:f.account_id,name:scenario==='wrong_job'?'other-job':'csv-import'});
    if(init?.method==='POST'){
      assert.ok(new Headers(init.headers).get('Idempotency-Key'));
      if(p.endsWith('/cancel')){cancels++;state='cancelled';return Response.json({run:run(),cancelled_at:new Date().toISOString()});}
      if(p.endsWith('/replay-failed')){replays++;selected=[1];state='succeeded';return Response.json(run(f.replay_id));}
      posts++;const body=JSON.parse(init!.body as string);if(!refs.length)refs.push(...body.inputs);else assert.deepEqual(body.inputs,refs);
      state=scenario==='failure'?'failed':'succeeded';if(scenario==='lost_ack'&&posts===1)throw new Error('lost response');return Response.json(run());
    }
    reads++;
    if(p.endsWith('/tasks')){
      let tasks=selected.map((i,index)=>{
        const output={row:f.inputs[i]!.value.row,saved:true,rejection:'none'},value=scenario==='wrong_output'?{...output,saved:'secret-text'}:output;
        const raw=jobBytes({version:1,contract:scenario==='wrong_contract'?'f'.repeat(64):contract.sha256,input_id:refs[i]!.input_id,data:value}),sha=jobHash(raw),uri=(scenario.startsWith('signed')?'obj://app/bucket/':'s3://results/')+sha;blobs.set(uri,scenario==='corrupt_result'?Buffer.from('modified'):raw);
        return {run_id:p.includes(f.replay_id)?f.replay_id:f.run_id,task_index:index,input_id:scenario==='wrong_input'?'other':refs[i]!.input_id,input_ref:scenario==='wrong_binding'?'unknown':refs[i]!.input_ref,...(p.includes(f.replay_id)?{source_task_index:i}:{}),status:state==='cancelled'?'cancelled':scenario==='failure'&&!p.includes(f.replay_id)&&i===1?'failed':'succeeded',attempt:1,output_manifest:{version:1,artifacts:[{name:'result',uri:scenario==='unsafe_uri'?'https://unsafe.test/secret':uri,size_bytes:raw.length,sha256:'sha256:'+sha}]}};
      });
      if(scenario==='duplicate_task')tasks=[tasks[0]!,tasks[0]!];return Response.json({tasks,total:scenario==='wrong_total'?3:selected.length,offset:0,limit:200,next_offset:scenario==='bad_offset'?0:-1});
    }
    if(p.endsWith('/download')){signed++;const a=[...blobs.entries()][Number(p.split('/').at(-4))]??[...blobs.entries()][0]!;const bytes=a[1];return Response.json({name:'result',size_bytes:scenario==='signed_mismatch'?1:bytes.length,sha256:'sha256:'+jobHash(bytes),download:{url:'https://signed.test/blob?secret=capability',method:'GET',headers:{'X-Object-Proof':'object-only'},expires_at:new Date(Date.now()+(scenario==='signed_expired'?-1000:60000)).toISOString()}});}
    return Response.json(run(p.includes(f.replay_id)?f.replay_id:f.run_id));
  };
  const store={async put(bytes:Uint8Array,sha:string){const ref='s3://results/'+sha;blobs.set(ref,bytes);return ref;},async get(uri:string){assert.ok(!uri.startsWith('https:'));return blobs.get(uri)!;}};
  const artifactFetch:typeof globalThis.fetch=async(url,init)=>{assert.equal(new URL(String(url)).hostname,'signed.test');assert.equal(new Headers(init?.headers).get('Authorization'),null);assert.equal(new Headers(init?.headers).get('X-Object-Proof'),'object-only');assert.equal(init?.credentials,'omit');assert.equal(init?.redirect,'error');return new Response([...blobs.values()][Math.min(signed-1,blobs.size-1)]!);};
  return {fetch,store,artifactFetch,refs,counts:()=>({posts,reads,signed,cancels,replays}),setState:(s:string)=>{state=s;selected=[0,1];}};
}
for(const [surface,factory]of [['public',createClient],['preview',preview]]as const)for(const c of f.cases)test(`${surface} typed jobs: ${c.scenario}`,async()=>{
  const h=harness(c.scenario),client=factory(base,{token:'owner',fetch:h.fetch}),job=client.jobs.defineJob(contract,{store:h.store,artifactFetch:h.artifactFetch});
  const work=async()=>{
    const run=await job.submit(f.inputs,{timeoutMs:2000,idempotencyKey:'submission-1'});assert.ok(!JSON.stringify(run.checkpoint).includes('example.test'));assert.ok(Object.isFrozen(run.checkpoint.inputs));
    const resumed=job.resume(parseJobCheckpoint(JSON.parse(JSON.stringify(run.checkpoint))));
    if(c.scenario==='success'){const attached=await job.resumeById(run.checkpoint.run_id,{timeoutMs:2000});assert.deepEqual(attached.checkpoint,run.checkpoint);}
    const progress=await resumed.wait({timeoutMs:2000,maxPolls:1});assert.equal(progress.total,2);
    const outcomes=await resumed.results({timeoutMs:2000});assert.equal(outcomes[1]!.outcome,c.outcome);if(c.outcome==='succeeded')assert.deepEqual(outcomes[0]!.result,{row:1,saved:true,rejection:'none'});
  };
  if(c.error)await assert.rejects(work(),code(c.error));else await work();assert.ok(h.counts().posts<=1);if(c.scenario==='wrong_job')assert.equal(h.counts().posts,0);
});
test('replay only failed inputs, explicit remote cancellation, polling and local cancellation are separate',async()=>{
  const h=harness('failure'),job=createClient(base,{token:'owner',fetch:h.fetch}).jobs.defineJob(contract,{store:h.store});const run=await job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'run'});
  const replay=await run.retryFailed({timeoutMs:1000,idempotencyKey:'replay'});assert.deepEqual((await job.resumeById(replay.checkpoint.run_id,{timeoutMs:1000})).checkpoint,replay.checkpoint);assert.equal(replay.checkpoint.source_run_id,run.checkpoint.run_id);assert.deepEqual(replay.checkpoint.inputs.map(i=>i.input_id),['row-2']);assert.equal((await replay.results({timeoutMs:1000}))[0]!.result!.row,2);
  h.setState('queued');await assert.rejects(run.wait({timeoutMs:1000,maxPolls:2,pollDelayMs:0}),code('poll_limit'));assert.equal(h.counts().cancels,0);
  await assert.rejects(run.wait({timeoutMs:20,pollDelayMs:1000}),code('deadline'));assert.equal(h.counts().cancels,0);
  assert.equal((await run.cancel({timeoutMs:1000})).status,'cancelled');assert.equal(h.counts().cancels,1);assert.equal(h.counts().replays,1);
});
test('lost acceptance is not automatically retried; an explicit same-key resubmission returns the saved run',async()=>{
  const h=harness('lost_ack'),job=createClient(base,{token:'owner',fetch:h.fetch,retry:{maxAttempts:4,backoffMs:0}}).jobs.defineJob(contract);
  await assert.rejects(job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'same'}),code('acceptance_unknown'));assert.equal(h.counts().posts,1);
  const run=await job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'same'});assert.equal(run.checkpoint.run_id,f.run_id);assert.equal(h.counts().posts,2);
});
test('contracts validate both directions, refuse unsupported schema keywords, and generate both languages',()=>{
  for(const value of [{row:1,email:'x'},{row:1,email:'abc',extra:true},{row:NaN,email:'abc'},{row:2**53,email:'abc'}])assert.throws(()=>contract.input(value),code('invalid_input'));
  assert.throws(()=>contract.output({row:1,saved:'true',rejection:'none'}),code('invalid_output'));
  assert.throws(()=>new JobContract({...f.contract,input:{...f.contract.input,pattern:'ignored'} as JobContractSpec['input']}),code('invalid_contract'));
  assert.ok(generateJobTypes(f.contract,'typescript').includes('readonly "row": number'));assert.ok(generateJobTypes(f.contract,'python').includes('NotRequired[bool]'));
  const portable=JSON.parse(readFileSync(new URL('../../../request-contracts/typed-jobs-portable.json',import.meta.url),'utf8'));assert.equal(contract.sha256,portable.contract_sha256);assert.deepEqual(contract.input(portable.payload),portable.payload);
});
test('local batch bounds concurrency and retries; cancelled late handlers cannot publish results',async()=>{
  let active=0,peak=0;const outcomes=await runLocalJobBatch(contract,f.inputs,async(input,context)=>{active++;peak=Math.max(peak,active);await new Promise(r=>setTimeout(r,2));active--;if(input.row===2&&context.attempt===1)throw new Error('secret failure');return {row:input.row,saved:true,rejection:'none'};},{timeoutMs:1000,concurrency:1,maxAttempts:2});
  assert.equal(peak,1);assert.deepEqual(outcomes.map(o=>o.attempts),[1,2]);
  await assert.rejects(runLocalJobBatch(contract,f.inputs,()=>new Promise(()=>{}),{timeoutMs:20}),code('deadline'));
});
test('large inputs and worker manifests bind exact input, schema, bytes and output; cross-language references decode',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-typed-task-'));t.after(()=>rm(home,{recursive:true,force:true}));const store=localJobStore(home),signal=new AbortController().signal,context={inputId:'large',attempt:1,signal};
  const input={id:'large',value:{row:1,email:'x'.repeat(5000)}};const binding=await prepareJobInput(contract,input,context,store);assert.ok(binding.input_ref.length<=2048);assert.deepEqual(await readJobInput(contract,binding,context,store),input.value);
  const manifest=join(home,'manifest.json');const value=await runJobTask(contract,()=>({row:1,saved:true,rejection:'none'}),{timeoutMs:1000,store,manageSignals:false,env:{GREGALE_INPUT_ID:binding.input_id,GREGALE_INPUT_REF:binding.input_ref,GREGALE_TASK_ATTEMPT:'1',GREGALE_TASK_INDEX:'0',GREGALE_TASK_COUNT:'1',GREGALE_RUN_ID:f.run_id,GREGALE_OUTPUT_MANIFEST_PATH:manifest}});
  assert.equal(value.saved,true);const meta=JSON.parse(await readFile(manifest,'utf8')).artifacts[0];const bytes=await store.get(meta.uri,context);assert.equal(meta.sha256,'sha256:'+jobHash(bytes));assert.equal(decodeJobOutput(contract,'large',bytes).saved,true);assert.throws(()=>decodeJobOutput(contract,'other',bytes),code('invalid_output'));
  const portable=JSON.parse(readFileSync(new URL('../../../request-contracts/typed-jobs-portable.json',import.meta.url),'utf8'));assert.deepEqual(await readJobInput(contract,portable.binding,context),portable.payload);
});
test('operation snapshots and resumed handles keep accounts isolated; pre-abort and budgets prevent writes',async()=>{
  const h=harness(),seen:string[]=[];const fetch:typeof globalThis.fetch=async(url,init)=>{const token=new Headers(init?.headers).get('Authorization')!;seen.push(token);await new Promise(r=>setTimeout(r,1));return token==='Bearer owner'?h.fetch(url,init):Response.json({detail:'secret'},{status:404});};
  const a=createClient(base,{token:'owner',fetch}),b=createClient(base,{token:'foreign',fetch});const job=a.jobs.defineJob(contract),pending=job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'one'});a.setToken('changed');const run=await pending;assert.ok(!seen.includes('Bearer changed'));
  await assert.rejects(b.jobs.defineJob(contract).resume(run.checkpoint).status({timeoutMs:1000}),code('request_failed'));assert.equal(h.counts().posts,1);
  a.setToken('owner');const controller=new AbortController();controller.abort(new Error('stop'));await assert.rejects(run.wait({timeoutMs:1000,signal:controller.signal}),e=>e===controller.signal.reason);
  await assert.rejects(job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'limited',maxRequests:1}),code('limit'));assert.equal(h.counts().posts,1);
});
test('stalled reads, progress and storage share deadlines and late storage cannot submit',async()=>{
  for(const body of [false,true]){
    let cancelled=false;const client=createClient(base,{fetch:async()=>body?new Response(new ReadableStream({start(c){c.enqueue(new TextEncoder().encode('{'));},cancel(){cancelled=true;}})):new Promise(()=>{})});
    await assert.rejects(client.jobs.defineJob(contract).submit(f.inputs,{timeoutMs:20,idempotencyKey:'bounded'}),code('deadline'));if(body)assert.equal(cancelled,true);
  }
  const h=harness(),client=createClient(base,{token:'owner',fetch:h.fetch});let release!:(value:string)=>void;
  const slow=client.jobs.defineJob(contract,{store:{put:()=>new Promise(r=>{release=r;}),get:async()=>new Uint8Array()}});
  await assert.rejects(slow.submit([{id:'large',value:{row:1,email:'x'.repeat(5000)}}],{timeoutMs:20,idempotencyKey:'large'}),code('deadline'));
  release('s3://results/large');await new Promise(r=>setTimeout(r,2));assert.equal(h.counts().posts,0);
  const run=await client.jobs.defineJob(contract).submit(f.inputs,{timeoutMs:1000,idempotencyKey:'ready'});
  await assert.rejects(run.wait({timeoutMs:20,onProgress:()=>new Promise(()=>{})}),code('deadline'));
});
test('native worker timeout prevents a cancellation-ignoring handler from publishing a late manifest',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-job-late-'));t.after(()=>rm(home,{recursive:true,force:true}));const signal=new AbortController().signal;
  const input=await prepareJobInput(contract,f.inputs[0]!,{inputId:'row-1',attempt:1,signal});let release!:(value:Output)=>void;
  const manifest=join(home,'manifest');await assert.rejects(runJobTask(contract,()=>new Promise(r=>{release=r;}),{timeoutMs:20,store:localJobStore(home),manageSignals:false,env:{GREGALE_RUN_ID:f.run_id,GREGALE_TASK_INDEX:'0',GREGALE_TASK_COUNT:'1',GREGALE_TASK_ATTEMPT:'1',GREGALE_INPUT_ID:input.input_id,GREGALE_INPUT_REF:input.input_ref,GREGALE_OUTPUT_MANIFEST_PATH:manifest}}),code('deadline'));
  release({row:1,saved:true,rejection:'none'});await new Promise(r=>setTimeout(r,5));await assert.rejects(readFile(manifest),e=>(e as NodeJS.ErrnoException).code==='ENOENT');
});
test('failed signed downloads retire unused bodies without disclosing their capability',async()=>{
  const h=harness('signed');let retired=0;
  const artifactFetch:typeof fetch=async()=>new Response(new ReadableStream({cancel(){retired++;}}),{status:503});
  const job=createClient(base,{token:'owner',fetch:h.fetch}).jobs.defineJob(contract,{artifactFetch});
  const run=await job.submit(f.inputs,{timeoutMs:1000,idempotencyKey:'download-error'});
  const results=await run.results({timeoutMs:1000});
  assert.equal(retired,2);assert.ok(results.every(r=>r.outcome==='invalid_result'&&r.code==='request_failed'));assert.ok(!JSON.stringify(results).includes('secret='));
});
