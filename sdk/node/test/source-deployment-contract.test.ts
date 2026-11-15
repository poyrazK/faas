import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mkdtemp, mkdir, writeFile, readFile, rm, readdir, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createClient, SourceDeploymentError, loadSourceDeploymentState, type SourceDeploymentOptions, type SourceDeploymentProgress, type SourceDeploymentResult, type UploadDeployOptions } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

const fixture=JSON.parse(readFileSync(new URL('../../../request-contracts/source-deployments.json',import.meta.url),'utf8')) as {
  app_id:string; deployment_id:string; preparation_sha256:string; metadata_sha256:string; cases:{name:string;scenario:string;outcome:string;deploy_options?:UploadDeployOptions;metadata_sha256?:string;preparation_sha256?:string}[];
};
const code=(value:string)=>(error:unknown)=>error instanceof SourceDeploymentError && error.code===value;
const hash=(value:Buffer)=>createHash('sha256').update(value).digest('hex');
for(const [surface,factory] of [['public',createClient],['preview',createPreviewClient]] as const)for(const item of fixture.cases)test(`${surface} source workflow: ${item.name}`,{timeout:12000},async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-deploy-contract-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const directory=join(home,'project'),statePath=join(home,'deploy.json');await mkdir(directory);await writeFile(join(directory,'index.js'),'export default 1;\n');
  const scenario=item.scenario;let total=0,received=Buffer.alloc(0),commits=0,creates=0,deployments=0,receipt:Record<string,string>|undefined,stopped=false,recovered=false;
  if(scenario==='workspace'){await mkdir(join(directory,'apps','api'),{recursive:true});await writeFile(join(directory,'apps','api','main.js'),'workspace source');}
  const token='before';const calls:string[]=[],events:SourceDeploymentProgress[]=[];
  const stopScenarios=['preparing','prepared','new_upload','uploading','ready','before_commit','committing','accepted','observing','complete','changed_archive','removed_source','removed_archive_accepted','wrong_base','wrong_app','wrong_metadata','wrong_preparation'];
  const stopStage=['changed_archive','removed_source','wrong_base','wrong_app','wrong_metadata','wrong_preparation'].includes(scenario)?'prepared':scenario==='removed_archive_accepted'?'accepted':scenario;
  const client=factory('https://workflow.test/prefix',{token:'before',timeoutMs:1,fetch:async(url,init)=>{
    const headers=new Headers(init?.headers),method=init!.method!,route=new URL(String(url)).pathname.replace('/prefix','');
    assert.equal(headers.get('Authorization'),`Bearer ${token}`);assert.equal(new URL(String(url)).origin,'https://workflow.test');calls.push(`${method} ${route}`);
    const saved=await loadSourceDeploymentState(statePath);assert.equal(saved.base_url,'https://workflow.test/prefix');assert.equal(saved.preparation_sha256,item.preparation_sha256 ?? fixture.preparation_sha256);assert.equal(saved.metadata_sha256,item.metadata_sha256 ?? fixture.metadata_sha256);
    if(method==='POST' && route==='/v1/uploads'){
      creates++;const body=JSON.parse(init!.body as string);total=body.total_size;assert.equal(saved.upload!.stage,'new');assert.equal(body.sha256_hex,saved.archive!.sha256);
      return Response.json({upload_id:'upload',chunk_size:37,total_size:total,expires_at:'2026-11-20T00:00:00Z'},{status:201});
    }
    if(method==='GET' && route==='/v1/uploads/upload')return Response.json({upload_id:'upload',app_slug:'api',chunk_size:37,total_size:total,received_bytes:received.length,status:receipt?'committed':'open',deployment_id:receipt?.deployment_id,expires_at:'2026-11-20T00:00:00Z'});
    if(method==='PATCH'){
      assert.equal(Number(headers.get('Upload-Offset')),received.length);const bytes=Buffer.from(await(init!.body as Blob).arrayBuffer());
      if(scenario==='lost_chunk' && !recovered){recovered=true;received=Buffer.concat([received,bytes.subarray(0,7)]);throw new TypeError('partial reply lost');}
      received=Buffer.concat([received,bytes]);return new Response(undefined,{headers:{'Upload-Offset':String(received.length)}});
    }
    if(method==='GET' && route.includes('/deployment-submissions/'))return Response.json(receipt ?? {code:'not_found'},{status:receipt?200:404});
    if(method==='POST' && route.endsWith('/commit')){
      commits++;assert.equal(saved.upload!.stage,'committing');assert.equal(headers.get('Deployment-Attempt-ID'),saved.upload!.attempt_id);
      if(scenario==='unknown_commit')throw new TypeError('acceptance unknown');
      deployments++;receipt={attempt_id:saved.upload!.attempt_id,app_id:fixture.app_id,deployment_id:fixture.deployment_id,accepted_at:'2026-11-14T17:00:00Z'};
      if(scenario==='commit_timeout')return new Promise<Response>(()=>{});
      if(scenario==='lost_commit')throw new TypeError('commit reply lost');
      return Response.json({id:fixture.deployment_id},{status:201});
    }
    if(method==='GET' && route.endsWith('/timeline'))return Response.json({deployment_id:fixture.deployment_id,app:'api',status:'live',phase:scenario==='blocked'?'held':scenario==='failed'?'failed':scenario==='superseded'?'superseded':scenario==='poll_limit'?'building':'released',summary:'test',traffic_percent:100,events:[],created_at:'2026-11-14T17:00:00Z'});
    throw new Error(`Unexpected ${method} ${route}`);
  }});
  const options:SourceDeploymentOptions={statePath,timeoutMs:scenario==='commit_timeout'?1000:6000,wait:{minDelayMs:1,maxDelayMs:1,maxPolls:1},onProgress:async event=>{
    assert.ok(Object.isFrozen(event));events.push(event);
    if(scenario==='client_snapshot'){client.setToken('after');client.setBaseURL('https://changed.test');}
    if(!stopped && stopScenarios.includes(scenario)){
      const match=stopStage==='preparing'?event.phase==='preparing' && !event.source:
        stopStage==='prepared'?event.phase==='uploading' && !event.upload:
        stopStage==='new_upload'?event.upload?.stage==='new':
        stopStage==='uploading'?event.phase==='uploading' && event.upload?.stage==='uploading' && event.upload.received_bytes>0:
        stopStage==='ready'?event.upload?.stage==='ready':
        stopStage==='before_commit'?event.phase==='committing' && !event.upload:
        stopStage==='committing'?event.upload?.stage==='committing':
        stopStage==='accepted'?event.upload?.stage==='accepted':event.phase===stopStage;
      if(match){stopped=true;throw new Error('process interrupted after saved boundary');}
    }
  }};
  const input={directory,appSlug:'api',deployOptions:item.deploy_options};let result:SourceDeploymentResult|undefined;
  if(stopScenarios.includes(scenario) || scenario==='commit_timeout'){
    await assert.rejects(client.deployments.deploySource(input,options));assert.ok(stopped || scenario==='commit_timeout');
    const saved=await loadSourceDeploymentState(statePath),archive=join(home,`.gregale-deploy-${saved.workflow_id}.tar.gz`);
    assert.equal((await stat(statePath)).mode&0o777,0o600);assert.ok(!JSON.stringify(saved).includes('Bearer'));
    if(scenario==='changed_archive')await writeFile(archive,'changed');
    if(scenario==='removed_source')await rm(directory,{recursive:true});
    if(scenario==='removed_archive_accepted'){await rm(directory,{recursive:true});await rm(archive);}
    if(scenario==='wrong_base')client.setBaseURL('https://wrong.test');
    const before=calls.length,resume=client.deployments.resumeSourceDeployment({appSlug:scenario==='wrong_app'?'other':'api',...(scenario==='wrong_metadata'?{deployOptions:{runtime:'node'} as const}:{})},{...options,timeoutMs:6000,...(scenario==='wrong_preparation'?{preparation:{exclude:['index.js']}}:{})});
    if(['acceptance_unknown','upload_failed','state_mismatch'].includes(item.outcome)){await assert.rejects(resume,code(item.outcome));if(item.outcome==='state_mismatch' || scenario==='changed_archive')assert.equal(calls.length,before);}
    else result=await resume;
  }else if(item.outcome==='acceptance_unknown' || item.outcome==='wait_failed'){
    await assert.rejects(client.deployments.deploySource(input,options),code(item.outcome));
    if(item.outcome==='acceptance_unknown')await assert.rejects(client.deployments.resumeSourceDeployment(input,options),code('acceptance_unknown'));
  }
  else result=await client.deployments.deploySource(input,options);
  if(result){assert.equal(result.outcome,item.outcome);assert.equal(result.deploymentId,fixture.deployment_id);assert.ok(Object.isFrozen(result));}
  assert.ok(commits<=1);assert.ok(deployments<=1);assert.ok(creates<=1);
  if(deployments){const saved=await loadSourceDeploymentState(statePath);assert.equal(hash(received),saved.archive!.sha256);assert.equal(received.length,total);}
  for(let i=1;i<events.length;i++)if(events[i]!.elapsedMs<events[i-1]!.elapsedMs)assert.ok(stopped || scenario==='commit_timeout' || scenario==='unknown_commit');
});

test('workflow cancellation, client snapshots, a shared budget and journal concurrency', {timeout:10000},async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-deploy-bounds-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const directory=join(home,'project');await mkdir(directory);await writeFile(join(directory,'index.js'),'source');
  const client=createClient('https://workflow.test',{fetch:async()=>{throw new Error('No request expected');}});
  let enter!:()=>void;const entered=new Promise<void>(resolve=>{enter=resolve;});const controller=new AbortController(),reason=new Error('stop');
  const options:SourceDeploymentOptions={statePath:join(home,'state.json'),timeoutMs:6000,signal:controller.signal,onProgress:()=>{enter();return new Promise<void>(()=>{});}};
  const pending=client.deployments.deploySource({directory,appSlug:'api'},options);const rejected=assert.rejects(pending,e=>code('cancelled')(e) && (e as SourceDeploymentError).cause===reason);
  await entered;await assert.rejects(client.deployments.deploySource({directory,appSlug:'api'},options),code('busy'));controller.abort(reason);await rejected;
  await new Promise(resolve=>setImmediate(resolve));
  await assert.rejects(client.deployments.resumeSourceDeployment({appSlug:'api'},{statePath:options.statePath,timeoutMs:30,onProgress:()=>new Promise(()=>{})}),code('timeout'));
  assert.equal((await loadSourceDeploymentState(options.statePath)).upload,null);
  await assert.rejects(client.deployments.resumeSourceDeployment({appSlug:'api'},{statePath:join(home,'missing.json'),timeoutMs:1000}),code('invalid_state'));
  const elapsed:number[]=[];
  await assert.rejects(client.deployments.deploySource({directory,appSlug:'api'},{statePath:join(home,'budget.json'),timeoutMs:120,onProgress:async event=>{elapsed.push(event.elapsedMs);if(!event.source && !event.upload && !event.release)await new Promise(resolve=>setTimeout(resolve,80));}}),code('timeout'));
  assert.ok(elapsed.every((v,i)=>i===0 || v>=elapsed[i-1]!));
  assert.ok((await readdir(home)).includes('state.json'));
});

test('concurrent accounts keep the original endpoint and token throughout the workflow',{timeout:12000},async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-deploy-accounts-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const run=async(token:string)=>{
    const directory=join(home,token),statePath=join(home,token+'.json');await mkdir(directory);await writeFile(join(directory,'index.js'),token);
    const id=fixture.deployment_id.slice(0,-1)+(token==='a'?'1':'2');let size=0,received=0,receipt:Record<string,string>|undefined,commits=0;
    const client=createClient(`https://${token}.test`,{token,fetch:async(url,init)=>{
      assert.equal(new URL(String(url)).origin,`https://${token}.test`);const headers=new Headers(init!.headers);assert.equal(headers.get('Authorization'),'Bearer '+token);
      const path=new URL(String(url)).pathname;
      if(init!.method==='POST' && path==='/v1/uploads'){size=JSON.parse(init!.body as string).total_size;return Response.json({upload_id:'upload',chunk_size:4096,total_size:size,expires_at:'2026-11-20T00:00:00Z'},{status:201});}
      if(init!.method==='GET' && path==='/v1/uploads/upload')return Response.json({upload_id:'upload',app_slug:'api',chunk_size:4096,total_size:size,received_bytes:received,status:'open',expires_at:'2026-11-20T00:00:00Z'});
      if(init!.method==='PATCH'){received+=(init!.body as Blob).size;return new Response(undefined,{headers:{'Upload-Offset':String(received)}});}
      if(init!.method==='POST'){commits++;receipt={attempt_id:headers.get('Deployment-Attempt-ID')!,app_id:fixture.app_id,deployment_id:id,accepted_at:'2026-11-14T17:00:00Z'};return Response.json({id},{status:201});}
      if(path.endsWith('/timeline'))return Response.json({deployment_id:id,app:'api',status:'live',phase:'released',summary:'done',traffic_percent:100,events:[],created_at:'2026-11-14T17:00:00Z'});
      return Response.json(receipt ?? {code:'not_found'},{status:receipt?200:404});
    }});
    const result=await client.deployments.deploySource({directory,appSlug:'api'},{statePath,timeoutMs:6000,onProgress:()=>{client.setToken('changed');client.setBaseURL('https://changed.test');}});
    assert.equal(commits,1);assert.equal(result.outcome,'succeeded');return result;
  };
  const [a,b]=await Promise.all([run('a'),run('b')]);assert.notEqual(a.workflowId,b.workflowId);assert.notEqual(a.deploymentId,b.deploymentId);
});

test('a hard process stop at committing resumes with receipt reads only',{timeout:15000},async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-deploy-crash-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const directory=join(home,'project'),statePath=join(home,'deploy.json');await mkdir(directory);await writeFile(join(directory,'main.js'),'source');
  const program=`
    const {createClient}=await import(process.argv[3]);let total=0,received=0;
    const client=createClient('https://workflow.test',{fetch:async(url,init)=>{
      const path=new URL(String(url)).pathname;
      if(init.method==='POST' && path==='/v1/uploads'){total=JSON.parse(init.body).total_size;return Response.json({upload_id:'u',chunk_size:4096,total_size:total,expires_at:'2026-11-20T00:00:00Z'},{status:201});}
      if(init.method==='GET' && path==='/v1/uploads/u')return Response.json({upload_id:'u',app_slug:'api',chunk_size:4096,total_size:total,received_bytes:received,status:'open',expires_at:'2026-11-20T00:00:00Z'});
      if(init.method==='PATCH'){received+=init.body.size;return new Response(undefined,{headers:{'Upload-Offset':String(received)}});}
      return Response.json({code:'not_found'},{status:404});
    }});
    await client.deployments.deploySource({directory:process.argv[1],appSlug:'api'},{statePath:process.argv[2],timeoutMs:6000,onProgress:event=>{if(event.upload?.stage==='committing')process.exit(0);}});
    throw new Error('did not stop at committing');`;
  await promisify(execFile)(process.execPath,['--input-type=module','-e',program,directory,statePath,new URL('../src/index.js',import.meta.url).href]);
  assert.equal((await loadSourceDeploymentState(statePath)).upload!.stage,'committing');
  let reads=0;const client=createClient('https://workflow.test',{fetch:async(url,init)=>{assert.equal(init!.method,'GET');assert.ok(String(url).includes('/deployment-submissions/'));reads++;return Response.json({code:'not_found'},{status:404});}});
  await assert.rejects(client.deployments.resumeSourceDeployment({appSlug:'api'},{statePath,timeoutMs:3000}),code('acceptance_unknown'));assert.equal(reads,1);
});

test('invalid policies and corrupt journals fail before remote work',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-deploy-invalid-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const directory=join(home,'project');await mkdir(directory);await writeFile(join(directory,'main.js'),'source');
  const client=createClient('https://workflow.test',{fetch:async()=>{throw new Error('unexpected HTTP');}}),statePath=join(home,'state.json');
  await assert.rejects(client.deployments.deploySource({directory,appSlug:'api'},{statePath,timeoutMs:1000,wait:{maxPolls:0}}),TypeError);
  await assert.rejects(client.deployments.deploySource({directory,appSlug:'api'},{statePath:join(directory,'state.json'),timeoutMs:1000}),code('state_mismatch'));
  await writeFile(statePath,'{"version":99}');await assert.rejects(client.deployments.resumeSourceDeployment({appSlug:'api'},{statePath,timeoutMs:1000}),code('invalid_state'));
});
