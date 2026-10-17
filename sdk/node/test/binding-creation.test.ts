import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { OpenAPI, createBindingVerificationFromReview, reconcileBindingVerificationCreation, createProjectBindingVerificationFromReview, reconcileProjectBindingVerificationCreation, BindingVerificationCreationError } from '../src/index.js';
import type { BindingVerificationReview, ProjectBindingVerificationReview, BindingVerificationRun, ProjectBindingVerificationRun, BindingVerificationCreationOptions } from '../src/index.js';
const template=JSON.parse(readFileSync(new URL('../../../binding-runner-tests/creation.json',import.meta.url),'utf8')) as {app_review:BindingVerificationReview; app_run:BindingVerificationRun;graph_review:ProjectBindingVerificationReview;graph_run:ProjectBindingVerificationRun};
function fixture(graph: boolean) {
 const f=structuredClone(template),created=new Date(Date.now()-1000).toISOString(),expires=new Date(Date.parse(created)+60_000).toISOString();
 const update=(r:BindingVerificationRun,v:BindingVerificationReview):void=>{r.created_at=created;r.expires_at=expires;v.reviewed_at=created;};
 update(f.app_run,f.app_review);f.graph_run.created_at=created;f.graph_run.expires_at=expires;f.graph_review.reviewed_at=created;
 for(const [name,m] of Object.entries(f.graph_run.members))update(m,f.graph_review.members[name]!);
 const run=graph?f.graph_run:f.app_run,options:BindingVerificationCreationOptions={runId:run.id,ttlSeconds:60,queuePublish:true};
 return {f,run,options,create:()=>graph?createProjectBindingVerificationFromReview('project','production',f.graph_review,options):createBindingVerificationFromReview('api',f.app_review,options),reconcile:()=>graph?reconcileProjectBindingVerificationCreation('project','production',f.graph_review,options):reconcileBindingVerificationCreation('api',f.app_review,options)};
}
for(const graph of [false,true]) {
 test(`reviewed creation lost response and read-only reconciliation: ${graph}`,async()=>{
  const f=fixture(graph),old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';const calls:string[]=[];const original=new TypeError('create response lost');
  globalThis.fetch=async(_input,init)=>{calls.push(init!.method!);if(init!.method==='POST'){const body=JSON.parse(init!.body as string);assert.equal(body.id,f.options.runId);assert.equal(body.review_digest,f.run.review_digest);assert.equal(body.queue_publish,true);if(graph)assert.deepEqual(body.review_digests,f.f.graph_review.review_digests);throw original;}return new Response(JSON.stringify(f.run),{headers:{'Content-Type':'application/json'}});};
  try {await assert.rejects(f.create(),(e:unknown)=>e instanceof BindingVerificationCreationError&&e.cause===original&&e.attempted&&e.runId===f.options.runId);const recovered=await f.reconcile();assert.equal(recovered.id,f.options.runId);assert.deepEqual(calls,['POST','GET']);}
  finally{globalThis.fetch=old;OpenAPI.BASE=base;}
 });
 for(const fault of ['uuid','consent','ready','digest','scope','member','ttl','budget','plan','deadline','graph-parent','abort'])test(`reviewed creation rejects ${graph}/${fault}`,async()=>{
  const f=fixture(graph),old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';let calls=0;let local=true;
  switch(fault){case'uuid':f.options.runId='bad';break;case'consent':f.options.queuePublish=false;break;case'ready':f.f.app_review.ready=false;f.f.graph_review.ready=false;break;case'digest':f.f.app_review.review_digest='bad';f.f.graph_review.review_digest='bad';break;case'scope':f.f.app_review.scope='other';f.f.graph_review.environment='other';break;case'member':f.f.app_run.deployment_id='changed';delete f.f.graph_run.members.billing;local=false;break;case'ttl':f.options.ttlSeconds=0;break;case'budget':f.options.maxConcurrency=9;break;case'plan':f.f.app_run.steps=[];f.f.graph_run.members.api!.steps=[];local=false;break;case'deadline':f.run.expires_at=new Date(Date.parse(f.run.expires_at)+1000).toISOString();local=false;break;case'graph-parent':if(graph)f.f.graph_run.members.api!.graph_run_id='wrong';else f.f.app_run.graph_run_id='wrong';local=false;break;case'abort':f.options.signal=AbortSignal.abort();break;}
  globalThis.fetch=async()=>{calls++;return new Response(JSON.stringify(f.run),{headers:{'Content-Type':'application/json'}});};
  try {await assert.rejects(f.create(),(e:unknown)=>e instanceof BindingVerificationCreationError&&e.runId===f.options.runId&&e.attempted===!local);assert.equal(calls,local?0:1);}
  finally{globalThis.fetch=old;OpenAPI.BASE=base;}
 });
}
for(const graph of [false,true])test(`reconciliation accepts terminal expiry and rejects changed receipt: ${graph}`,async()=>{
 const f=fixture(graph),old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';const calls:string[]=[];
 const past=new Date(Date.now()-3600_000).toISOString(),expiry=new Date(Date.parse(past)+60_000).toISOString();f.run.created_at=past;f.run.expires_at=expiry;f.run.status='cancelled';
 if('members' in f.run)for(const m of Object.values(f.run.members)){m.created_at=past;m.expires_at=expiry;m.status='cancelled';}
 globalThis.fetch=async(_input,init)=>{calls.push(init!.method!);return new Response(JSON.stringify(f.run),{headers:{'Content-Type':'application/json'}});};
 try{assert.equal((await f.reconcile()).status,'cancelled');f.run.review_digest='changed';await assert.rejects(f.reconcile(),BindingVerificationCreationError);assert.deepEqual(calls,['GET','GET']);}finally{globalThis.fetch=old;OpenAPI.BASE=base;}
});

for(const graph of [false,true])for(const mode of ['committed','gateway','absent','read_forbidden','read_failed','retry','drift','ttl','consent','budget','expired','cancelled','failed_resource','member','passed_incomplete','duplicate','callback','cancel','deadline','forbidden','mutation'])test(`automatic creation ${graph}/${mode}`,async()=>{
 const f=fixture(graph),oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,controller=new AbortController(),events:unknown[]=[];let writes=0,reads=0;const original=new TypeError('fetch failed hidden-create-secret');
 for(const [r,v] of [[f.f.app_run,f.f.app_review],...Object.entries(f.f.graph_run.members).map(([n,m])=>[m,f.f.graph_review.members[n]!])] as [BindingVerificationRun,BindingVerificationReview][]){for(const [i,step] of r.steps.entries()){if(step.plan.action==='task')step.plan.action='probe';v.plan[i]=structuredClone(step.plan);}}
 f.options.statusReadRetries=mode==='retry'?1:0;f.options.signal=controller.signal;
 f.options.onProgress=p=>{events.push(p);if(p.status==='reconciling'){if(mode==='callback')throw new Error('checkpoint failed');if(mode==='cancel')controller.abort();if(mode==='deadline')setTimeout(()=>controller.abort(),20);if(mode==='mutation'){f.f.app_review.plan[0]!.name='changed';f.f.graph_review.members.api!.plan[0]!.name='changed';}}};
 const members='members'in f.run?Object.values(f.run.members):[f.run];
 if(mode==='drift')f.run.review_digest='changed';
 if(mode==='ttl')f.run.expires_at=new Date(Date.parse(f.run.expires_at)+1000).toISOString();
 if(mode==='consent')f.run.queue_publish=false;
 if(mode==='budget')f.run.max_concurrency=8;
 if(mode==='cancelled')f.run.status='cancelled';
 if(mode==='expired'){const at=new Date(Date.now()-120000).toISOString(),until=new Date(Date.parse(at)+60000).toISOString();f.run.created_at=at;f.run.expires_at=until;for(const m of members){m.created_at=at;m.expires_at=until;}}
 if(mode==='failed_resource'){const step=members[0]!.steps[0]!;step.owned=true;step.resource_id='55555555-5555-4555-8555-000000000001';step.resource_kind='task';step.status='failed';}
 if(mode==='passed_incomplete'){f.run.status='passed';for(const m of members)m.status='passed';}
 if(mode==='member'){if('members'in f.run)delete f.run.members.billing;else f.run.graph_run_id='unexpected';}
 if(mode==='duplicate'){if('members'in f.run)f.run.members.billing!.id=f.run.members.api!.id;else f.run.id='55555555-5555-4555-8555-000000000001';}
 OpenAPI.BASE='https://api.invalid';
 globalThis.fetch=async(_input,init)=>{
  if(init?.method==='POST'){writes++;const body=JSON.parse(init.body as string);assert.equal(body.id,f.options.runId);if(mode==='forbidden')return new Response(JSON.stringify({status:403}),{status:403});if(mode==='gateway')return new Response(JSON.stringify({status:503}),{status:503});throw original;}
  assert.equal(init?.method,'GET');reads++;
  if(mode==='deadline')return new Promise<Response>(()=>{});
  if(mode==='absent'||mode==='read_forbidden'||mode==='read_failed'||mode==='retry'&&reads===1)return new Response('{}',{status:mode==='absent'?404:mode==='read_forbidden'?403:503});
  return new Response(JSON.stringify({...f.run,private_output:'hidden-create-secret'}),{headers:{'Content-Type':'application/json'}});
 };
 try{
  if(['committed','gateway','retry','mutation'].includes(mode)){assert.equal((await f.create()).id,f.options.runId);}
  else await assert.rejects(f.create(),error=>{assert.ok(error instanceof BindingVerificationCreationError);assert.equal(error.runId,f.options.runId);assert.equal(error.attempted,true);if(mode!=='forbidden'){assert.ok(error.cause instanceof AggregateError);assert.equal(error.cause.cause,original);}return true;});
  assert.equal(writes,1);if(['callback','cancel','forbidden'].includes(mode))assert.equal(reads,0);if(mode==='read_failed')assert.equal(reads,1);if(mode==='retry')assert.equal(reads,2);
  assert.equal(events.length>0,mode!=='forbidden');if(events.length)assert.equal((events[0] as {operation:string}).operation,'creation');assert.ok(!JSON.stringify(events).includes('hidden-create-secret'));
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});

for(const graph of [false,true])test(`creation validates recovery retry budget ${graph}`,async()=>{
 const f=fixture(graph),old=globalThis.fetch;let calls=0;globalThis.fetch=async()=>{calls++;throw new Error('unexpected request');};
 try{for(const retries of [-1,9]){f.options.statusReadRetries=retries;await assert.rejects(f.create(),e=>e instanceof BindingVerificationCreationError&&!e.attempted);}assert.equal(calls,0);}finally{globalThis.fetch=old;}
});
