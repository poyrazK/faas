import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { mkdtemp, mkdir, writeFile, readFile, rm, stat } from 'node:fs/promises';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { createHash } from 'node:crypto';
import { gunzipSync } from 'node:zlib';
import { createClient, projectVerificationTarget, parseReviewedProjectPlan, loadProjectApplicationState, ProjectWorkflowError, type PlanResponse, type ProjectApplyOptions } from '../src/index.js';
import { createClient as createPreviewClient } from '../src/preview.js';

const fixture=JSON.parse(readFileSync(new URL('../../../request-contracts/project-workflows.json',import.meta.url),'utf8')) as {preview:PlanResponse;cases:{name:string;scenario:string;outcome:string}[]};
const ids=['11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222'];
const code=(expected:string)=>(error:unknown)=>error instanceof ProjectWorkflowError&&error.code===expected;
test('review fingerprints preserve numeric JSON across Node and Python',()=>{
  const portable=JSON.parse(readFileSync(new URL('../../../request-contracts/project-workflows.json',import.meta.url),'utf8')).portable_plan;
  assert.equal(parseReviewedProjectPlan(portable).fingerprint,portable.fingerprint);
  assert.deepEqual(parseReviewedProjectPlan(JSON.parse(JSON.stringify(portable))).source,portable.source);
});
for(const [surface,factory]of [['public',createClient],['preview',createPreviewClient]]as const)for(const item of fixture.cases)test(`${surface} reviewed project: ${item.name}`,{timeout:15000},async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-project-contract-'));t.after(()=>rm(home,{recursive:true,force:true}));
  const root=join(home,'repo');await mkdir(root);for(const name of ['api','worker']){await mkdir(join(root,name));await writeFile(join(root,name,'Dockerfile'),'FROM scratch\n');}
  await writeFile(join(root,'compose.yaml'),'services:\n  api: {build: ./api}\n  worker: {build: ./worker}\n  db: {image: postgres:17}\n');
  const scenario=item.scenario,statePath=join(home,'apply.json');let scans=0,posts=0,reads=0,paused=false,total=0;
  let receipt:Record<string,unknown>|undefined,archive=Buffer.alloc(0);const preview=structuredClone(fixture.preview);
  if(scenario==='blocked_plan')preview.can_apply=false;
  if(scenario==='environment')preview.environment='staging';
  if(scenario==='unchanged')preview.unaffected=[{slug:'shop-old',id:ids[0]!,action:'noop'}];
  const phases:string[]=[];
  const client=factory('https://projects.test/prefix',{token:'owner',retry:{maxAttempts:4,backoffMs:1},fetch:async(url,init)=>{
    const path=new URL(String(url)).pathname.replace('/prefix',''),headers=new Headers(init?.headers);assert.equal(headers.get('Authorization'),'Bearer owner');assert.equal(init?.redirect,'error');
    if(path==='/v1/projects/scan'){
      scans++;assert.equal(init!.method,'POST');const body=init!.body as FormData;assert.equal(body.get('project_slug'),'shop');if(scenario==='environment'){assert.equal(body.get('environment'),'staging');assert.deepEqual(JSON.parse(body.get('app_slugs') as string),{api:'shop-api',worker:'shop-worker'});}
      archive=Buffer.from(await(body.get('source') as File).arrayBuffer());assert.ok(gunzipSync(archive).includes(Buffer.from('postgres:17')));return Response.json(preview);
    }
    if(path==='/v1/projects/apply-plan'){
      posts++;const saved=await loadProjectApplicationState(statePath);assert.equal(saved.stage,'applying');assert.equal(saved.attempt_id,headers.get('Deployment-Attempt-ID'));assert.equal(headers.get('Idempotency-Key'),saved.attempt_id);
      const body=JSON.parse(init!.body as string);assert.equal(body.plan_token,preview.plan_token);assert.ok(!('source'in body));
      if(scenario==='secrets'){assert.deepEqual(body.initial_secrets,{api:{DATABASE_URL:'private-value'}});assert.equal(body.approval_token,'private-approval');}
      if(['expired','stale','permission'].includes(scenario))return Response.json({code:scenario==='stale'?'plan_token_stale':scenario==='expired'?'preview_expired':'forbidden',title:'refused',status:scenario==='expired'?410:scenario==='permission'?403:409},{status:scenario==='expired'?410:scenario==='permission'?403:409,headers:{'Content-Type':'application/problem+json'}});
      if(scenario==='unknown')throw new Error('reply missing');
      const apps=[{slug:'shop-api',id:ids[0]!},{slug:'shop-worker',id:ids[1]!}],builds:Record<string,string>[] = apps.map(a=>({slug:a.slug,app_id:a.id,deployment_id:a.id,build_id:a.id}));
      if(scenario==='partial'){delete builds[1]!.deployment_id;delete builds[1]!.build_id;builds[1]!.error='enqueue failed';}
      if(['interrupted','missing_row'].includes(scenario)){delete builds[1]!.deployment_id;delete builds[1]!.build_id;}
      if(scenario==='image')delete builds[1]!.build_id;
      if(scenario==='wrong_app')builds[1]!.app_id='another';
      if(scenario==='accepted_error')builds[1]!.error='source checkpoint failed';
      receipt={...(scenario==='environment'?{environment:'staging'}:{}),attempt_id:saved.attempt_id,project_slug:scenario==='wrong_project'?'another':'shop',selection_complete:true,status:scenario==='interrupted'?'interrupted':'complete',apply:{project_slug:'',scan_source:'',can_apply:false,plan_token:'',workloads:null,managed:null,crons:null,submission_attempt_id:saved.attempt_id,apps:scenario==='empty_receipt'?[]:apps,builds:scenario==='empty_receipt'?[]:builds,unchanged:null}};
      if(scenario==='wrong_receipt')receipt.attempt_id='f'.repeat(32);
      total++;
      if(scenario==='lost_reply')throw new Error('accepted reply lost');
      return Response.json({project_slug:'shop',scan_source:'compose',can_apply:true,plan_token:preview.plan_token,submission_attempt_id:saved.attempt_id,apps,builds});
    }
    if(path.startsWith('/v1/project-submissions/')){
      reads++;if(!receipt)return Response.json({code:'not_found'},{status:404});
      if(scenario==='poll_limit'||scenario==='processing'&&reads===1)return Response.json({...receipt,status:'processing',selection_complete:false,apply:{project_slug:'',scan_source:'',can_apply:false,plan_token:''}});
      return Response.json(receipt);
    }
    if(path.endsWith('/timeline')){const id=path.split('/')[3]!,worker=id===ids[1],phase=worker&&scenario==='failed_release'?'failed':worker&&scenario==='blocked_release'?'held':'released';return Response.json({deployment_id:id,app:worker?'shop-worker':'shop-api',status:'live',phase,summary:'test',traffic_percent:100,events:[],created_at:'2026-11-14T19:00:00Z'});}
    throw new Error('Unexpected '+path);
  }});
  const plan=await client.projects.previewLocal({directory:root,projectSlug:'shop',...(scenario==='environment'?{environment:'staging',appSlugs:{api:'shop-api',worker:'shop-worker'}}:{})},{timeoutMs:5000});assert.equal(scans,1);assert.equal(posts,0);assert.ok(Object.isFrozen(plan.preview));
  assert.equal(plan.source.sha256,createHash('sha256').update(archive).digest('hex'));assert.equal(plan.source.size,archive.length);
  const restored=parseReviewedProjectPlan(JSON.parse(JSON.stringify(plan)));assert.deepEqual(restored,plan);
  const options:ProjectApplyOptions={statePath,timeoutMs:7000,maxReceiptPolls:scenario==='poll_limit'?1:3,wait:{minDelayMs:1,maxDelayMs:1,maxPolls:2},...(scenario==='secrets'?{initialSecrets:{api:{DATABASE_URL:'private-value'}},approvalToken:'private-approval'}:{}),onProgress:event=>{
    phases.push(event.phase);assert.ok(Object.isFrozen(event));
    if(!paused&&(scenario==='restart'&&event.phase==='recovering'||scenario==='before_post'&&event.phase==='applying')){paused=true;throw new Error('process stopped');}
  }};
  let result;
  if(scenario==='restart'||scenario==='before_post'){
    await assert.rejects(client.projects.applyPlan(restored,options),code('progress_failed'));assert.ok(paused);await rm(root,{recursive:true});
    if(scenario==='restart')result=await client.projects.resumePlanApplication(restored,options);
    else await assert.rejects(client.projects.resumePlanApplication(restored,options),code('acceptance_unknown'));
  }else if(['succeeded','partial','failed','blocked'].includes(item.outcome)&&scenario!=='blocked_plan')result=await client.projects.applyPlan(restored,options);
  else{await assert.rejects(client.projects.applyPlan(restored,options),code(item.outcome));if(scenario==='unknown'){const before=posts;await assert.rejects(client.projects.resumePlanApplication(restored,options),code('acceptance_unknown'));assert.equal(posts,before);}}
  if(result){const target=await projectVerificationTarget(result);assert.equal(target.project_slug,'shop');assert.deepEqual(target.workloads.map(w=>w.deployment_id),result.workloads.map(w=>w.deploymentId));assert.equal(result.outcome,item.outcome);assert.ok(Object.isFrozen(result.workloads));assert.ok(result.workloads.length>=2);if(scenario==='unchanged')assert.equal(result.workloads.find(w=>w.slug==='shop-old')!.outcome,'unchanged');}
  assert.ok(posts<=1);assert.ok(total<=1);if(scenario==='blocked_plan'||scenario==='before_post')assert.equal(posts,0);
  const saved=await loadProjectApplicationState(statePath);assert.equal((await stat(statePath)).mode&0o777,0o600);assert.ok(!JSON.stringify(saved).includes('private-'));assert.ok(!JSON.stringify(plan).includes('private-'));
});

test('project workflows bind intent, reject concurrent journals and share cancellation/deadline budgets',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-project-bounds-'));t.after(()=>rm(home,{recursive:true,force:true}));const root=join(home,'repo');await mkdir(root);await writeFile(join(root,'package.json'),'{}');
  let requests=0;const client=createClient('https://projects.test',{fetch:async()=>{requests++;return Response.json(fixture.preview);}}),plan=await client.projects.previewLocal({directory:root,projectSlug:'shop'},{timeoutMs:5000}),statePath=join(home,'state.json');
  assert.throws(()=>parseReviewedProjectPlan({...plan,project_slug:'other'}));
  const foreign=createClient('https://other.test',{fetch:async()=>{assert.fail('Wrong endpoint');}});await assert.rejects(foreign.projects.applyPlan(plan,{statePath,timeoutMs:1000}),code('state_mismatch'));
  let enter!:()=>void;const entered=new Promise<void>(r=>{enter=r;}),controller=new AbortController();
  const pending=client.projects.applyPlan(plan,{statePath,timeoutMs:1000,signal:controller.signal,onProgress:async()=>{enter();await new Promise(()=>{});}});await entered;
  await assert.rejects(client.projects.applyPlan(plan,{statePath,timeoutMs:1000}),code('busy'));controller.abort();await assert.rejects(pending,code('cancelled'));assert.equal(requests,1);
  await assert.rejects(client.projects.previewLocal({directory:root,projectSlug:'shop'},{timeoutMs:15,onProgress:()=>new Promise(()=>{})}),code('timeout'));assert.equal(requests,1);
  await assert.rejects(client.projects.applyPlan(plan,{statePath:join(home,'deadline.json'),timeoutMs:15,onProgress:()=>new Promise(()=>{})}),code('timeout'));assert.equal(requests,1);
});

test('concurrent accounts keep reviewed plans and recovered receipts isolated',async t=>{
  const home=await mkdtemp(join(tmpdir(),'gregale-project-accounts-'));t.after(()=>rm(home,{recursive:true,force:true}));const root=join(home,'repo');await mkdir(root);await writeFile(join(root,'package.json'),'{}');
  const receipts=new Map<string,object>();let mutations=0,entered=0,release!:()=>void;const barrier=new Promise<void>(r=>{release=r;});
  const transport:typeof fetch=async(url,init)=>{
    const token=new Headers(init?.headers).get('Authorization')!.slice(7),path=new URL(String(url)).pathname;
    if(path==='/v1/projects/scan')return Response.json({...fixture.preview,plan_token:'plan-'+token});
    if(path==='/v1/projects/apply-plan'){
      const body=JSON.parse(init!.body as string);if(body.plan_token!=='plan-'+token)return Response.json({code:'preview_expired'},{status:410});
      const attempt=new Headers(init?.headers).get('Deployment-Attempt-ID')!;mutations++;
      const apply={project_slug:'shop',scan_source:'compose',can_apply:true,plan_token:body.plan_token,submission_attempt_id:attempt,apps:ids.map((id,i)=>({id,slug:i?'shop-worker':'shop-api'})),builds:ids.map((id,i)=>({app_id:id,slug:i?'shop-worker':'shop-api',deployment_id:id,build_id:id}))};
      receipts.set(token+attempt,{attempt_id:attempt,project_slug:'shop',selection_complete:true,status:'complete',apply});return Response.json(apply);
    }
    if(path.startsWith('/v1/project-submissions/')){const value=receipts.get(token+path.split('/').at(-1));return Response.json(value??{code:'not_found'},{status:value?200:404});}
    if(path.endsWith('/timeline'))return Response.json({deployment_id:path.split('/')[3],app:'shop',status:'live',phase:'released',summary:'test',traffic_percent:100,events:[],created_at:'2026-11-14T19:00:00Z'});
    throw new Error(path);
  };
  const a=createClient('https://projects.test',{token:'a',fetch:transport}),b=createClient('https://projects.test',{token:'b',fetch:transport});
  const [pa,pb]=await Promise.all([a.projects.previewLocal({directory:root,projectSlug:'shop'},{timeoutMs:5000}),b.projects.previewLocal({directory:root,projectSlug:'shop'},{timeoutMs:5000})]);assert.notEqual(pa.fingerprint,pb.fingerprint);
  const results=await Promise.all([a,b].map((client,i)=>client.projects.applyPlan(i?pb:pa,{statePath:join(home,i+'.json'),timeoutMs:5000,onProgress:async event=>{if(event.phase==='applying'){client.setToken('changed');if(++entered===2)release();await barrier;}}})));
  assert.equal(mutations,2);assert.ok(results.every(r=>r.outcome==='succeeded'));b.setToken('b');
  await assert.rejects(b.projects.resumePlanApplication(pa,{statePath:join(home,'0.json'),timeoutMs:5000}),code('acceptance_unknown'));assert.equal(mutations,2);
  await assert.rejects(b.projects.applyPlan(pa,{statePath:join(home,'foreign.json'),timeoutMs:5000}),code('preview_expired'));assert.equal(mutations,2);
});
