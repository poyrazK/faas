import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { OpenAPI, runBindingVerification, runProjectBindingVerification, BindingVerificationRecoveryError } from '../src/preview.js';
import type { BindingVerificationRun, BindingVerificationRunStep, ProjectBindingVerificationRun, BindingVerificationRunnerOptions, BindingVerificationProgress } from '../src/preview.js';

type Scenario={name:string;compact?:boolean;graph?:boolean;actions:string[];concurrency?:number;fault?:string;resume?:boolean;passed:boolean;admits:number};
const scenarios=JSON.parse(readFileSync(new URL('../../../binding-runner-tests/contracts.json',import.meta.url),'utf8')).cases as Scenario[];
type Run=BindingVerificationRun|ProjectBindingVerificationRun;

function diagnostics(run:Run):unknown {
 const member=(r:BindingVerificationRun)=>({run_id:r.id,graph_run_id:r.graph_run_id,app:r.app,deployment_id:r.deployment_id,scope:r.scope,status:'pending',checked_at:new Date().toISOString(),expires_at:r.expires_at,complete:false,blockers:[{code:'policy_changed',message:'Policy changed.',recovery:'review_new_run',private_command:'hidden-command'}],changes:[],recovery:[{action:'review_new_run',message:'Review a new receipt.'}],private_admissions:'hidden-secret'});
 if('members' in run)return {run_id:run.id,project:run.project,environment:run.environment,status:'pending',checked_at:new Date().toISOString(),expires_at:run.expires_at,complete:false,members:Object.fromEntries(Object.entries(run.members).map(([name,r])=>[name,member(r)])),blockers:[],changes:[],recovery:[{action:'review_new_run',message:'Review a new receipt.'}],private_admissions:'hidden-secret'};
 return member(run);
}

function fixture(s:Scenario) {
 const created=new Date(Date.now()-(s.fault==='expired'?121_000:1000)).toISOString(),expires=new Date(Date.parse(created)+60_000).toISOString();
 const member=(app:string,n:number):BindingVerificationRun=>({id:`11111111-1111-4111-8111-${String(n).padStart(12,'0')}`,app,deployment_id:'22222222-2222-4222-8222-222222222222',scope:'production',status:'pending',created_at:created,expires_at:expires,review_digest:'a'.repeat(64),queue_publish:true,object_storage_write:false,policy:{app_id:'33333333-3333-4333-8333-333333333333',scope:'production',mode:'enforce',revision:1,max_verification_age:'5m',require_application_ack:false},inventory:{app,generated_at:created,complete:true,bindings:[]},steps:[]});
 const a=member('api',1),b=member('billing',2);if(s.compact){a.deployment_id=a.deployment_id.replaceAll('-','');b.deployment_id=b.deployment_id.replaceAll('-','');}const graphId='99999999-9999-4999-8999-999999999999';
 let root:Run=a;
 if(s.graph){a.graph_run_id=graphId;b.graph_run_id=graphId;root={id:graphId,project:'project',environment:'production',status:'pending',created_at:created,expires_at:expires,review_digest:'c'.repeat(64),queue_publish:true,object_storage_write:false,manifest:{project:'project',environment:'production',ttl_seconds:60,deployments:{api:'22222222-2222-4222-8222-222222222222',billing:'22222222-2222-4222-8222-222222222222'},expected_active_release_id:'',expected_fallback_deployments:{},expected_policy_revisions:{api:1,billing:1},service_targets:{api:{},billing:{}}},members:{api:a,billing:b}};}
 s.actions.forEach((action,i)=>{const target=s.graph&&i%2===1?b:a;const kind=action==='queue-canary'||action==='wait-queue'?'queue':'service';const step:BindingVerificationRunStep={plan:{type:kind,name:`binding-${i}`,binding:`binding-${i}`,scope:'production',action},status:'planned',owned:false};if(action==='wait'||action==='wait-queue'){step.resource_id=`44444444-4444-4444-8444-${String(i+100).padStart(12,'0')}`;step.resource_kind=kind==='queue'?'queue':'task';step.status=kind==='queue'?'published':'queued';}target.steps.push(step);});
 const original=structuredClone(root);
 let admits=0,maxActive=0,lost=false;const calls:string[]=[];
 const members=():BindingVerificationRun[]=>'members' in root?Object.values(root.members):[root];
 const fetch:typeof globalThis.fetch=async(input,init)=>{
  const path=new URL(String(input)).pathname;const method=init?.method;let result:unknown=root;
  assert.notEqual(method,'DELETE','runner implicitly cancelled work');assert.ok(!path.endsWith('/verification-runs'),'runner created a replacement receipt');
  if(path.endsWith('/diagnostics')){calls.push('diagnostics');assert.equal(method,'GET');return new Response(JSON.stringify(diagnostics(original)),{status:200,headers:{'Content-Type':'application/json'}});}
  if(path.endsWith('/admit')){
   calls.push('admit');if((s.fault==='budget_once'||s.fault==='budget_after_admit'&&admits===1)&&!lost){lost=true;return new Response(JSON.stringify({status:429,code:'binding_verification_budget_full'}),{status:429,headers:{'Content-Type':'application/problem+json'}});}if(s.fault==='capacity')return new Response(JSON.stringify({status:429,code:'binding_verification_run_limit'}),{status:429,headers:{'Content-Type':'application/problem+json'}});admits++;const parts=path.split('/'),app=parts[3],run=parts[6],index=Number(parts[8]);const target=members().find(m=>m.app===app&&m.id===run)!;const step=target.steps[index]!;
   assert.equal(step.resource_id,undefined,'runner repeated admission');step.resource_id=`55555555-5555-4555-8555-${String(admits).padStart(12,'0')}`;step.resource_kind=step.plan.type==='queue'?'queue':'task';step.owned=true;step.status=step.resource_kind==='queue'?'published':'queued';if(s.fault==='unknown_status')step.status='unknown';
   maxActive=Math.max(maxActive,members().flatMap(m=>m.steps).filter(v=>v.resource_id&&v.status!=='succeeded').length);result=step;
   if(s.fault==='admit_once'&&!lost){lost=true;throw new TypeError('admission response lost after commit');}
  }else if(path.endsWith('/complete')){
   calls.push('complete');assert.ok(members().flatMap(m=>m.steps).every(v=>!v.resource_id||v.status==='succeeded'));
   root.status='passed';for(const m of members())m.status=s.fault==='member_pending'&&m.app==='billing'?'pending':'passed';if(s.fault==='false_complete')root.status='blocked';
   if(s.fault==='complete_once'&&!lost){lost=true;throw new TypeError('completion response lost after commit');}
  }else if(path.endsWith('/resume')){calls.push('resume');}
  else if(method==='GET'){
   calls.push('get');for(const m of members())for(const step of m.steps)if(step.resource_id){step.status=s.fault==='failed'?'failed':'succeeded';if(s.fault==='resource_changed')step.resource_id='77777777-7777-4777-8777-777777777777';}
   if(s.fault==='selection_changed'&&'members' in root){const target={billing:'22222222-2222-4222-8222-222222222222'};root.manifest.service_targets.api=target;root.members.api!.inventory.service_targets=target;}
   if(s.fault==='blocked')root.status='blocked';if(s.fault==='digest_changed')root.review_digest='b'.repeat(64);
   if(s.fault==='deadline_changed'){root.expires_at=new Date(Date.parse(root.expires_at)+1000).toISOString();for(const m of members())m.expires_at=root.expires_at;}
  }else throw new Error(`unexpected request ${method} ${path}`);
  return new Response(JSON.stringify(result),{status:200,headers:{'Content-Type':'application/json'}});
 };
 return {root,fetch,calls,get admits(){return admits;},get maxActive(){return maxActive;}};
}
for(const s of scenarios)test(`binding runner contract: ${s.name}`,async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture(s),events:BindingVerificationProgress[]=[];OpenAPI.BASE='https://api.invalid';globalThis.fetch=f.fetch;
 const execute=(options:BindingVerificationRunnerOptions)=>s.graph?runProjectBindingVerification('project','production',f.root.id,options):runBindingVerification('api',f.root.id,options);
 const options={pollIntervalMs:1,concurrency:s.concurrency,onProgress:(p:BindingVerificationProgress)=>{events.push(p);}};
 try {
  const expires=f.root.expires_at;let passed=false;
  try{const result=await execute(options);passed=result.status==='passed';}
  catch(error){assert.ok(error instanceof BindingVerificationRecoveryError);assert.equal(error.runId,f.root.id);if(!s.resume){assert.equal(s.passed,false);}else{const result=await execute(options);passed=result.status==='passed';assert.equal(f.root.expires_at,expires);}}
  assert.equal(passed,s.passed);assert.equal(f.admits,s.admits);assert.ok(f.maxActive<=(s.concurrency??2));if(s.passed)assert.ok(events.some(p=>p.step===-1&&p.status==='passed'));
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
for(const callbackError of [false,true])test(`local stop preserves graph work: ${callbackError}`,async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'stop',graph:true,actions:['probe','queue-canary'],passed:false,admits:1}),controller=new AbortController();globalThis.fetch=f.fetch;OpenAPI.BASE='https://api.invalid';
 try{await assert.rejects(runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1,signal:controller.signal,onProgress:p=>{if(p.resource_id){if(callbackError)throw new Error('checkpoint failed');controller.abort();}}}),BindingVerificationRecoveryError);assert.equal(f.admits,1);assert.equal(f.root.status,'pending');}
 finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
test('abort during admission resumes the committed resource',async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'inflight',actions:['probe'],passed:true,admits:1}),controller=new AbortController();
 let announce!:()=>void;const committed=new Promise<void>(resolve=>{announce=resolve;});
 OpenAPI.BASE='https://api.invalid';globalThis.fetch=async(input,init)=>{
  const response=await f.fetch(input,init);
  if(new URL(String(input)).pathname.endsWith('/admit')) {
   announce();return new Promise<Response>((_resolve,reject)=>{init?.signal?.addEventListener('abort',()=>reject(new Error('local HTTP observation stopped')),{once:true});});
  }
  return response;
 };
 try {
  const running=runBindingVerification('api',f.root.id,{pollIntervalMs:1,signal:controller.signal});
  await committed;controller.abort();await assert.rejects(running,BindingVerificationRecoveryError);
  globalThis.fetch=f.fetch;const expiry=f.root.expires_at;
  const resumed=await runBindingVerification('api',f.root.id,{pollIntervalMs:1});
  assert.equal(resumed.status,'passed');assert.equal(f.admits,1);assert.equal(resumed.expires_at,expiry);
 }finally{controller.abort();globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
test('abort while awaiting progress preserves graph resources',async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'progress-abort',graph:true,actions:['probe','queue-canary'],passed:true,admits:2}),controller=new AbortController();
 let announce!:()=>void;const checkpoint=new Promise<void>(resolve=>{announce=resolve;});
 globalThis.fetch=f.fetch;OpenAPI.BASE='https://api.invalid';
 try {
  const running=runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1,signal:controller.signal,onProgress:p=>{if(p.resource_id){announce();return new Promise<void>(()=>{});}}});
  await checkpoint;controller.abort();await assert.rejects(running,BindingVerificationRecoveryError);assert.equal(f.admits,1);
  const expiry=f.root.expires_at;
  const resumed=await runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1});
  assert.equal(resumed.status,'passed');assert.equal(f.admits,2);assert.equal(resumed.expires_at,expiry);
 }finally{controller.abort();globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});

for(const fault of ['budget_after_admit','capacity'])test(`wrapped client handles ${fault}`,async()=>{
 const {FaaSClient}=await import('../src/preview.js');
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'wrapped-budget',graph:true,actions:['probe','queue-canary'],fault,passed:fault!=='capacity',admits:fault==='capacity'?0:2});
 const expiry=f.root.expires_at;
 const client=new FaaSClient('https://api.invalid',{fetch:f.fetch,retry:{maxAttempts:0,backoffMs:0}});
 try {
  if(fault==='capacity') await assert.rejects(runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1}),BindingVerificationRecoveryError);
  else assert.equal((await runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1})).status,'passed');
  assert.equal(f.admits,fault==='capacity'?0:2);assert.equal(f.root.expires_at,expiry);
 } finally {client.uninstall();globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
for(const graph of [false,true])for(const mode of ['available','forbidden','wrong_run','wrong_deadline','wrong_member','skipped'])test(`automatic ${graph?'graph':'app'} diagnostics ${mode}`,async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'diagnose',graph,actions:['probe'],fault:'failed',passed:false,admits:1});let reads=0;
 globalThis.fetch=async(input,init)=>{
  if(new URL(String(input)).pathname.endsWith('/diagnostics')) {
   reads++;if(mode==='forbidden')return new Response(JSON.stringify({detail:'hidden-secret'}),{status:403});
   const raw=diagnostics(f.root) as Record<string,unknown>;
   if(mode==='wrong_run')raw.run_id='88888888-8888-4888-8888-888888888888';
   if(mode==='wrong_deadline')raw.expires_at=new Date(Date.parse(f.root.expires_at)+1000).toISOString();
   if(mode==='wrong_member') {
    if(graph)(raw.members as Record<string,Record<string,unknown>>).api!.deployment_id='88888888-8888-4888-8888-888888888888';
    else raw.deployment_id='88888888-8888-4888-8888-888888888888';
   }
   return new Response(JSON.stringify(raw),{status:200,headers:{'Content-Type':'application/json'}});
  }
  return f.fetch(input,init);
 };
 OpenAPI.BASE='https://api.invalid';
 try {
  await assert.rejects(graph?runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1,skipDiagnostics:mode==='skipped'}):runBindingVerification('api',f.root.id,{pollIntervalMs:1,skipDiagnostics:mode==='skipped'}),error=>{
   assert.ok(error instanceof BindingVerificationRecoveryError);assert.equal(error.runId,f.root.id);assert.ok(error.cause instanceof Error);
   assert.equal(error.diagnostics!==undefined,mode==='available');
   if(error.diagnostics){assert.equal(error.diagnostics.complete,false);assert.ok(!JSON.stringify(error.diagnostics).includes('hidden-'));}
   return true;
  });
  assert.equal(reads,mode==='skipped'?0:1);assert.equal(f.admits,1);
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
test('abort during diagnostics returns the original error without waiting for the response',async()=>{
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'abort-diagnostics',actions:['probe'],fault:'failed',passed:false,admits:1}),controller=new AbortController();let started!:()=>void;const reading=new Promise<void>(resolve=>{started=resolve;});
 globalThis.fetch=async(input,init)=>{
  if(new URL(String(input)).pathname.endsWith('/diagnostics')){started();return new Promise<Response>((_resolve,reject)=>init?.signal?.addEventListener('abort',()=>reject(new Error('lookup cancelled')),{once:true}));}
  return f.fetch(input,init);
 };OpenAPI.BASE='https://api.invalid';
 try {const pending=runBindingVerification('api',f.root.id,{pollIntervalMs:1,signal:controller.signal});await reading;controller.abort();await assert.rejects(pending,error=>{assert.ok(error instanceof BindingVerificationRecoveryError);assert.match(String(error.cause),/did not succeed/);assert.equal(error.diagnostics,undefined);return true;});}
 finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});

for(const graph of [false,true])for(const mode of ['recover','http503','exhaust','disable','forbidden','drift','callback','abort','resume'])test(`status read retries ${graph}/${mode}`,async()=>{
 const f=fixture({name:'retry',graph,actions:['probe'],passed:true,admits:1}),old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';let reads=0;const original=new TypeError('fetch failed');const controller=new AbortController();const callbackError=new Error('callback failed');const events:BindingVerificationProgress[]=[];
 globalThis.fetch=async(input,init)=>{
  const path=new URL(String(input)).pathname;
  if(mode==='resume'&&path.endsWith('/resume'))throw original;
  if(init?.method==='GET'&&!path.endsWith('/diagnostics')){
   reads++;if(mode==='forbidden')return new Response(JSON.stringify({status:403,code:'forbidden'}),{status:403,headers:{'Content-Type':'application/problem+json'}});
   if(mode==='http503'&&reads===1)return new Response(JSON.stringify({status:503,code:'unavailable'}),{status:503,headers:{'Content-Type':'application/problem+json'}});
   if(reads===1||mode==='exhaust')throw original;
   const response=await f.fetch(input,init);if(mode==='drift'){const data=await response.json();data.review_digest='changed';return new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}});}return response;
  }return f.fetch(input,init);
 };
 const options:BindingVerificationRunnerOptions={pollIntervalMs:1,statusReadRetries:mode==='disable'?0:1,skipDiagnostics:true,signal:controller.signal,onProgress:p=>{events.push(p);if(p.status==='retrying'){if(mode==='callback')throw callbackError;if(mode==='abort')controller.abort();}}};
 try {
  const execution=graph?runProjectBindingVerification('project','production',f.root.id,options):runBindingVerification('api',f.root.id,options);
  if(mode==='recover'||mode==='http503'){assert.equal((await execution).status,'passed');assert.equal(reads,2);assert.equal(f.admits,1);assert.ok(events.some(e=>e.status==='retrying'&&e.retry_attempt===1));assert.equal(events.at(-1)!.status,'passed');}
  else {await assert.rejects(execution,(e:unknown)=>e instanceof BindingVerificationRecoveryError&&e.runId===f.root.id&&(mode==='callback'?e.cause===callbackError:['exhaust','disable','resume'].includes(mode)?e.cause===original:true));assert.equal(reads,mode==='resume'?0:['exhaust','drift'].includes(mode)?2:1);assert.ok(f.admits<=1);}
 }finally{globalThis.fetch=old;OpenAPI.BASE=base;}
});
for(const graph of [false,true])test(`status retry original expiry ${graph}`,async()=>{
 const f=fixture({name:'expiry',graph,actions:['probe'],passed:false,admits:1}),old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';let reads=0;
 const expires=new Date(Date.now()+60).toISOString();f.root.expires_at=expires;if('members' in f.root)for(const m of Object.values(f.root.members))m.expires_at=expires;
 globalThis.fetch=async(input,init)=>{if(init?.method==='GET'&&!String(input).endsWith('/diagnostics')){assert.ok(Date.now()<Date.parse(expires),'read started after original expiry');reads++;throw new TypeError('fetch failed');}return f.fetch(input,init);};
 try{await assert.rejects(graph?runProjectBindingVerification('project','production',f.root.id,{pollIntervalMs:1,skipDiagnostics:true}):runBindingVerification('api',f.root.id,{pollIntervalMs:1,skipDiagnostics:true}),BindingVerificationRecoveryError);assert.ok(reads>=1&&reads<=4);assert.equal(f.admits,1);assert.equal(f.root.expires_at,expires);}finally{globalThis.fetch=old;OpenAPI.BASE=base;}
});

for(const graph of [false,true])for(const mode of ['settles','foreign','unknown','drift','callback','cancel','deadline'])test(`pending acknowledgment ${graph}/${mode}`,async()=>{
 const f=fixture({name:'ack',graph,actions:graph?['observe','observe']:['observe'],passed:true,admits:0}),old=globalThis.fetch,base=OpenAPI.BASE;OpenAPI.BASE='https://api.invalid';let completions=0,waiting=false;const controller=new AbortController();const callbackError=new Error('observer failed');
 const members='members' in f.root?Object.values(f.root.members):[f.root];for(const m of members){m.steps[0]!.plan.type='postgres';m.steps[0]!.plan.name='database';m.steps[0]!.plan.binding='DATABASE_URL';m.steps[0]!.plan.scope='production';}
 if(mode==='deadline'){f.root.expires_at=new Date(Date.now()+120).toISOString();for(const m of members)m.expires_at=f.root.expires_at;}
 globalThis.fetch=async(input,init)=>{
  if(String(input).endsWith('/complete')){completions++;if(completions===1||mode==='deadline'){
   const data=structuredClone(f.root) as any;data.qualification={checked_at:new Date().toISOString(),conditions:[{app:mode==='foreign'?'foreign':graph?'billing':'api',code:mode==='unknown'?'unrecognized':'application_adoption_unknown',type:'postgres',name:'database',binding:'DATABASE_URL',scope:'production',private_command:'hidden'}],private_secret:'hidden'};
   return new Response(JSON.stringify(data),{status:202,headers:{'Content-Type':'application/json'}});
  }}
  const response=await f.fetch(input,init);
  if(mode==='drift'&&completions>0&&init?.method==='GET'){const data=await response.json();data.review_digest='changed';return new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}});}return response;
 };
 const options:BindingVerificationRunnerOptions={pollIntervalMs:1,skipDiagnostics:true,signal:controller.signal,onProgress:p=>{if(p.status==='waiting_for_qualification'){waiting=true;assert.equal(p.qualification!.conditions[0]!.code,'application_adoption_unknown');assert.ok(!JSON.stringify(p).includes('hidden'));if(mode==='callback')throw callbackError;if(mode==='cancel')controller.abort();}}};
 try{const execution=graph?runProjectBindingVerification('project','production',f.root.id,options):runBindingVerification('api',f.root.id,options);
  if(mode==='settles'){assert.equal((await execution).status,'passed');assert.equal(completions,2);assert.ok(waiting);}else{await assert.rejects(execution,(e:unknown)=>e instanceof BindingVerificationRecoveryError&&e.runId===f.root.id&&(mode==='callback'?e.cause===callbackError:true));if(mode!=='deadline')assert.equal(completions,1);}
  assert.equal(f.admits,0);
 }finally{globalThis.fetch=old;OpenAPI.BASE=base;}
});

for(const graph of [false,true])for(const phase of ['admission','completion'] as const)for(const mode of ['committed','gateway','absent','drift','deadline','unowned','malformed','read_failed','retry','forbidden','callback','cancel','expires','member_pending'])test(`uncertain ${phase} ${mode} ${graph?'graph':'app'}`,async()=>{
 if(mode==='member_pending'&&phase==='admission')return;
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE,f=fixture({name:'reconcile',graph,actions:['probe','queue-canary'],passed:true,admits:2}),controller=new AbortController();
 if(mode==='expires'){f.root.expires_at=new Date(Date.now()+80).toISOString();if('members'in f.root)for(const member of Object.values(f.root.members))member.expires_at=f.root.expires_at;}
 const original=new TypeError('fetch failed'),events:BindingVerificationProgress[]=[];let lost=false,writes=0,reads=0,targetPath='';
 globalThis.fetch=async(input,init)=>{
  const path=new URL(String(input)).pathname;
  if(path===targetPath)writes++;
  if(!lost&&path.endsWith(phase==='admission'?'/admit':'/complete')){
   lost=true;targetPath=path;writes++;
   if(mode==='forbidden')return new Response(JSON.stringify({status:403,code:'forbidden'}),{status:403});
   if(mode!=='absent')await f.fetch(input,init);
   if(mode==='gateway')return new Response(JSON.stringify({status:503}),{status:503});
   throw original;
  }
  if(lost&&init?.method==='GET'){
   reads++;
   if(mode==='expires')await new Promise(resolve=>setTimeout(resolve,200));
   if(mode==='read_failed'||mode==='retry'&&reads===1)return new Response(JSON.stringify({status:503}),{status:503});
   const response=await f.fetch(input,init),raw=await response.json() as Run;
   const members='members'in raw?Object.values(raw.members):[raw];
   if(mode==='drift')raw.review_digest='d'.repeat(64);
   if(mode==='deadline')raw.expires_at=new Date(Date.parse(raw.expires_at)+1000).toISOString();
   if(mode==='unowned')for(const member of members)for(const step of member.steps)step.owned=false;
   if(mode==='malformed')for(const member of members)for(const step of member.steps)step.resource_id='bad';
   if(mode==='member_pending')members.at(-1)!.status='pending';
   return new Response(JSON.stringify(raw),{status:200,headers:{'Content-Type':'application/json'}});
  }
  return f.fetch(input,init);
 };
 OpenAPI.BASE='https://api.invalid';
 try{
  const options={skipDiagnostics:true,statusReadRetries:mode==='retry'?1:0,pollIntervalMs:1,signal:controller.signal,onProgress:(event:BindingVerificationProgress)=>{events.push(event);if(event.status==='reconciling'){if(mode==='callback')throw new Error('checkpoint failed');if(mode==='cancel')controller.abort();}}};
  const run=()=>graph?runProjectBindingVerification('project','production',f.root.id,options):runBindingVerification('api',f.root.id,options);
  if(['committed','gateway','retry'].includes(mode)){assert.equal((await run()).status,'passed');assert.equal(f.admits,2);}
  else await assert.rejects(run(),error=>{assert.ok(error instanceof BindingVerificationRecoveryError);assert.equal(error.runId,f.root.id);if(!['forbidden','cancel'].includes(mode)){assert.ok(error.cause instanceof AggregateError);assert.equal(error.cause.cause,original);}return true;});
  assert.equal(writes,1,'uncertain write was replayed');
  const progress=events.filter(e=>e.status==='reconciling');assert.equal(progress.length,mode==='forbidden'?0:1);if(progress.length)assert.equal(progress[0]!.operation,phase);
  if(['callback','cancel','forbidden'].includes(mode))assert.equal(reads,0);
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
