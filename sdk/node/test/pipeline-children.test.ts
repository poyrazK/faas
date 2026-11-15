import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import { JobContract, jobValueHash } from '../src/job-runtime.js';
import { defineJobPipeline } from '../src/job-pipelines.js';
import { PipelineChildren, nativePipelineChildrenTransport, pipelineChildrenLiveSnapshot, type PipelineChildrenTransport } from '../src/pipeline-children.js';
import { pipelineChildrenIntent, parsePipelineChildrenReceipt, pipelineChildrenSchema, postgresPipelineChildrenStore, type PipelineChildrenReceipt, type PipelineChildrenStore } from '../src/pipeline-children-state.js';
import type { PipelineChildPolicy } from '../src/pipeline-children-types.js';
import type { PipelineJobStore } from '../src/pipeline-state.js';
import type { PipelineCheckpoint } from '../src/job-pipeline-client.js';
import { runWorkflowInvocation, WORKFLOW_CODE_PROTOCOL, type JournalEntry, type WorkflowInvocationResponse } from '../src/workflows.js';

const f = JSON.parse(readFileSync(new URL('../../../request-contracts/pipeline-children.json', import.meta.url), 'utf8'));
const root = new JobContract<{n:number},{n:number}>(f.root_contract), contract = new JobContract<{n:number},{n:number}>(f.child_contract), definition = { contract }, scope = f.envelope.scope;
const settings = { timeoutMs: 1000 }, signal = new AbortController().signal;
function memory() {
  const rows = new Map<string, PipelineChildrenReceipt>(); let expired = false, loseSave = false;
  const store: PipelineChildrenStore = {
    async open(intent) {
      const old = rows.get(intent.identity); if (old) { assert.equal(jobValueHash(old.intent), jobValueHash(intent)); return old; }
      const r = parsePipelineChildrenReceipt({ ...f.receipt, intent, state: { stop_reason: null, parent_stopped: false, children: intent.inputs.map(() => ({phase:'unstarted',checkpoint:null,cancel_requested:false})) } }); rows.set(intent.identity,r); return r;
    },
    async load(id) { const r=rows.get(id);return r?parsePipelineChildrenReceipt({...r,expired}):null; },
    async save(old,state,admission) {
      const current=rows.get(old.intent.identity)!;if(current.revision!==old.revision||admission&&expired)return null;
      const r=parsePipelineChildrenReceipt({...old,state,revision:old.revision+1,expired});rows.set(old.intent.identity,r);
      if(loseSave){loseSave=false;throw new Error('lost store acknowledgement');}return r;
    },
    async scan(s,sha,limit) { return [...rows.values()].filter(r=>jobValueHash(r.intent.scope)===jobValueHash(s)&&r.intent.parent_sha256===sha).slice(0,limit); },
  };
  return {store,rows,get expired(){return expired;},set expired(v:boolean){expired=v;},get loseSave(){return loseSave;},set loseSave(v:boolean){loseSave=v;}};
}
function native() {
  let posts=0,peak=0,held=false,failFirst=false,loseStart=false,loseCancel=false,parentStopped=false,awaiting=false;
  const runs=new Map<string,{n:number;checkpoint:PipelineCheckpoint;done:boolean;cancelled:boolean}>();
  const transport:PipelineChildrenTransport={
    async parent(i){return {runId:i.parent_run_id,status:parentStopped?'failed':'running',cancelled:parentStopped,steps:[]};},
    async start(i,n){posts++;const c:PipelineCheckpoint={version:1,run_id:randomUUID(),scope,owner_id:i.owner_id,contract_sha256:i.child_sha256,input_sha256:i.inputs[n]!.input_sha256};runs.set(c.run_id,{n,checkpoint:c,done:false,cancelled:false});peak=Math.max(peak,[...runs.values()].filter(r=>!r.done).length);if(loseStart)throw new Error('lost native ACK');return c;},
    async attach(_i,_n,id){return runs.get(id)!.checkpoint;},
    async observe(i,n,c){const r=runs.get(c.run_id)!;if(r.cancelled)return {phase:'cancelled',checkpoint:c,cancel_requested:false,native_status:'failed'};if(held)return {phase:'running',checkpoint:c,cancel_requested:false,native_status:awaiting?'awaiting_event':'running'};r.done=true;return failFirst&&n===0?{phase:'failed',checkpoint:c,cancel_requested:false,native_status:'failed'}:{phase:'succeeded',checkpoint:c,cancel_requested:false,native_status:'succeeded',output:{n:(i.inputs[n]!.value as {n:number}).n+1}};},
    async cancel(_i,_n,c){const r=runs.get(c.run_id)!;r.cancelled=r.done=true;if(loseCancel)throw new Error('lost cancel ACK');},
  };
  return {transport,runs,get posts(){return posts;},get peak(){return peak;},get held(){return held;},set held(v:boolean){held=v;},get failFirst(){return failFirst;},set failFirst(v:boolean){failFirst=v;},get loseStart(){return loseStart;},set loseStart(v:boolean){loseStart=v;},get loseCancel(){return loseCancel;},set loseCancel(v:boolean){loseCancel=v;},get parentStopped(){return parentStopped;},set parentStopped(v:boolean){parentStopped=v;},get awaiting(){return awaiting;},set awaiting(v:boolean){awaiting=v;}};
}
function setup(policy:Partial<PipelineChildPolicy>={},state=memory(),remote=native()) {
  const coordinator=()=>new PipelineChildren({scope,parentContract:root,children:[definition],store:state.store,transport:remote.transport});
  const pipeline=()=>defineJobPipeline(root,{scope,store:{} as PipelineJobStore,jobs:{},children:coordinator(),pollDelayMs:1},async(_input,wf)=>{
    const result=await wf.children('imports',definition,f.receipt.intent.inputs.map((i:any)=>({id:i.id,value:i.value})),{timeoutMs:60000,concurrency:2,...policy});
    return {n:result.children.reduce((n,c)=>n+(c.output?.n??0),0)};
  });
  const invoke=(journal:JournalEntry[]=[])=>runWorkflowInvocation(pipeline().definition,{protocol:WORKFLOW_CODE_PROTOCOL,run_id:f.envelope.run_id??f.receipt.intent.parent_run_id,workflow:root.spec.name,input:f.envelope,invocation:journal.length+1,journal});
  return {state,remote,coordinator,invoke};
}
function commit(journal:JournalEntry[],r:WorkflowInvocationResponse){for(const op of r.ops)journal[op.seq]={...op,status:op.kind==='sleep'?'succeeded':op.status!,attempt:1};}

test('shared intent, ledger schema and completed journal replay across Node/Python',async()=>{
  const {version:_v,identity:_id,...base}=f.receipt.intent;assert.deepEqual(pipelineChildrenIntent(base),f.receipt.intent);
  assert.deepEqual(parsePipelineChildrenReceipt(f.final),f.final);assert.equal(pipelineChildrenSchema,readFileSync(new URL('../../../request-contracts/pipeline-children.sql',import.meta.url),'utf8'));
  const s=setup(),reply=await s.invoke(f.journal);assert.equal(reply.result.status,'succeeded');assert.deepEqual((reply.result as any).output.output,{n:9});assert.equal(s.remote.posts,0);
});
test('ordered batch survives lost journals and fresh coordinators without duplicate children',async()=>{
  const s=setup(),journal:JournalEntry[]=[],dropped=new Set<number>();let result;
  for(let n=0;n<30;n++){const r=await s.invoke(journal);if(r.result.status==='succeeded'){result=(r.result as any).output.output;break;}assert.ok(r.ops.every(o=>o.status!=='failed'),JSON.stringify(r));if(!dropped.has(r.ops[0]!.seq)){dropped.add(r.ops[0]!.seq);continue;}commit(journal,r);}
  assert.deepEqual(result,{n:9});assert.equal(s.remote.posts,3);assert.ok(s.remote.peak<=2);
  const view=await s.coordinator().inspect(f.receipt.intent.parent_run_id,'imports','alice',definition,settings);assert.deepEqual(view.children.map(c=>c.output),[{n:2},{n:3},{n:4}]);
});
test('concurrent drivers share a durable concurrency cap',async()=>{
  const s=setup();s.remote.held=true;const journal:JournalEntry[]=[];commit(journal,await s.invoke());
  const replies=await Promise.all([s.invoke(journal),s.invoke(journal)]);assert.ok(replies.every(r=>r.ops.every(o=>o.status!=='failed')),JSON.stringify(replies));assert.equal(s.remote.posts,2);assert.equal(s.remote.peak,2);
  await s.invoke(journal);assert.equal(s.remote.posts,2);
});
test('unknown accepted child is reconciled by exact run ID; a lost reservation ACK never posts',async()=>{
  const s=setup();s.remote.loseStart=true;s.remote.held=true;const j:JournalEntry[]=[];commit(j,await s.invoke());commit(j,await s.invoke(j));assert.equal(s.remote.posts,2);await s.invoke(j);assert.equal(s.remote.posts,2);
  const records=[...s.remote.runs.values()];await assert.rejects(s.coordinator().reconcile(f.receipt.intent.parent_run_id,'imports','item-1',records[1]!.checkpoint.run_id,'alice',settings),{code:'conflict'});
  await s.coordinator().reconcile(f.receipt.intent.parent_run_id,'imports','item-1',records[0]!.checkpoint.run_id,'alice',settings);
  await assert.rejects(s.coordinator().inspect(f.receipt.intent.parent_run_id,'imports','bob',definition,settings),{code:'forbidden'});
  const other=setup();commit([],await other.invoke());other.state.loseSave=true;const r=await other.invoke([{...f.journal[0]}]);assert.equal(r.ops[0]?.status,'failed');assert.equal(other.remote.posts,0);await other.invoke([{...f.journal[0]}]);assert.equal(other.remote.posts,1);
});
test('fail-fast stops new children, requests sibling cancellation, and recovery observes uncertain cancel',async()=>{
  const s=setup({failurePolicy:'fail_fast',onFailFast:'cancel_children'}),j:JournalEntry[]=[];s.remote.held=true;commit(j,await s.invoke());commit(j,await s.invoke(j));commit(j,await s.invoke(j));s.remote.held=false;s.remote.failFirst=true;s.remote.loseCancel=true;
  // Make sibling remain active while the first failure is observed.
  const observe=s.remote.transport.observe;s.remote.transport.observe=async(i,n,c,signal)=>n===1?{phase:'running',checkpoint:c,cancel_requested:false,native_status:'running'}:observe(i,n,c,signal);
  const reply=await s.invoke(j);assert.equal(reply.ops[0]?.status,'failed');assert.equal(s.remote.posts,2);
  s.remote.loseCancel=false;s.remote.transport.observe=observe;await s.coordinator().recover(10,settings);
  const view=await s.coordinator().inspect(f.receipt.intent.parent_run_id,'imports','alice',definition,settings);assert.equal(view.stop_reason,'child_failed');assert.deepEqual(view.children.map(c=>c.phase),['failed','cancelled','skipped']);
});
test('database deadline and stopped-parent policies never admit new children; leave-running is explicit',async()=>{
  for(const reason of ['deadline','parent'] as const){const s=setup({onDeadline:'cancel_children',onParentStopped:'cancel_children'}),j:JournalEntry[]=[];s.remote.held=true;commit(j,await s.invoke());commit(j,await s.invoke(j));if(reason==='deadline')s.state.expired=true;else s.remote.parentStopped=true;await s.coordinator().recover(10,settings);await s.coordinator().recover(10,settings);const v=await s.coordinator().inspect(f.receipt.intent.parent_run_id,'imports','alice',definition,settings);assert.deepEqual(v.children.map(c=>c.phase),['cancelled','cancelled','skipped']);assert.equal(s.remote.posts,2);}
  const s=setup(),j:JournalEntry[]=[];s.remote.held=true;commit(j,await s.invoke());commit(j,await s.invoke(j));s.remote.parentStopped=true;await s.coordinator().recover(10,settings);const v=await s.coordinator().inspect(f.receipt.intent.parent_run_id,'imports','alice',definition,settings);assert.equal(v.children[0]?.phase,'running');assert.equal(s.remote.posts,2);
});
test('changed input/policy conflicts, malformed checkpoints and aggregate limits fail closed',async()=>{
  const s=setup(),j:JournalEntry[]=[];commit(j,await s.invoke());const changed=setup({concurrency:1},s.state,s.remote);const r=await changed.invoke(j);assert.equal(r.result.status,'failed');assert.equal(s.remote.posts,0);
  const invalid=structuredClone(f.final);invalid.state.children[0].checkpoint.owner_id='bob';assert.throws(()=>parsePipelineChildrenReceipt(invalid));
  const {version:_v,identity:_id,...base}=f.receipt.intent;for(const patch of [{concurrency:0},{concurrency:11},{timeout_ms:0},{inputs:[]},{inputs:[base.inputs[0],base.inputs[0]]}])assert.throws(()=>pipelineChildrenIntent({...base,...patch}));
});
test('pending approval gates block child admission',async()=>{
  const s=setup();s.remote.held=true;const childCoord=s.coordinator();
  const p=defineJobPipeline(root,{scope,store:{} as PipelineJobStore,gateStore:{} as any,children:childCoord,jobs:{}},async(_input,wf)=>{const pending=wf.approval('review',contract,{n:1},{timeoutMs:60000,requiredRoles:['reviewer']});await assert.rejects(wf.child('child',definition,{n:1},{timeoutMs:60000}),{code:'blocked'});await pending;return {n:1};});
  await runWorkflowInvocation(p.definition,{protocol:WORKFLOW_CODE_PROTOCOL,run_id:f.receipt.intent.parent_run_id,workflow:root.spec.name,input:f.envelope,invocation:1,journal:[]});assert.equal(s.remote.posts,0);
});
test('native transport checks parent/child scope and typed output before reporting success',async()=>{
  let account=scope.account_id,owner='alice',lineage=f.input_envelopes[0].lineage;const childId=f.final.state.children[0].checkpoint.run_id;
  const t=nativePipelineChildrenTransport({scope,parentContract:root,children:[definition],token:'backend',fetch:async(url)=>{const p=new URL(String(url)).pathname;const child=p.includes(childId);const value=p==='/v1/account'?{id:account}:p.endsWith('/apps/imports')?{id:scope.app_id}:p.endsWith('/steps')?{steps:[]}: {id:child?childId:f.receipt.intent.parent_run_id,app_id:scope.app_id,workflow_name:child?contract.spec.name:root.spec.name,status:child?'succeeded':'running',input:child?{...f.input_envelopes[0],owner_id:owner,lineage}:f.envelope,output:{version:1,scope,owner_id:owner,contract_sha256:contract.sha256,output:{n:2}}};return new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}});}});
  assert.equal((await t.parent(f.receipt.intent,signal)).status,'running');assert.equal((await t.observe(f.receipt.intent,0,f.final.state.children[0].checkpoint,signal)).phase,'succeeded');owner='bob';await assert.rejects(t.observe(f.receipt.intent,0,f.final.state.children[0].checkpoint,signal));owner='alice';lineage={...lineage,item_id:'unrelated-item'};await assert.rejects(t.observe(f.receipt.intent,0,f.final.state.children[0].checkpoint,signal));account=randomUUID();await assert.rejects(t.parent(f.receipt.intent,signal));
});
test('combined progress retains authorized child detail and first pending approval',async()=>{
  const s=setup(),j:JournalEntry[]=[];s.remote.held=s.remote.awaiting=true;commit(j,await s.invoke());commit(j,await s.invoke(j));
  const gates={list:async()=>[{decision:null,intent:{pipeline_sha256:contract.sha256,owner_id:'alice',scope,kind:'approval',id:'a'.repeat(64),name:'review'}}]} as any;
  const c=new PipelineChildren({scope,parentContract:root,children:[definition],store:s.state.store,transport:s.remote.transport,gateStore:gates});const view=await c.inspect(f.receipt.intent.parent_run_id,'imports','alice',definition,settings);
  assert.equal(view.children[0]?.waiting?.kind,'approval');assert.equal(pipelineChildrenLiveSnapshot(view,{runId:view.parent_run_id,status:'running',cancelled:false,steps:[]},'batch').waiting?.gate_id,'a'.repeat(64));
});
test('abort fences a cancellation-ignoring native parent before any reservation or start',async()=>{
  const s=setup();const a=new AbortController();a.abort();await assert.rejects(s.coordinator().recover(10,{...settings,signal:a.signal}));assert.equal(s.remote.posts,0);
});

test('shared PostgreSQL CAS and rotating recovery queries match Python',async()=>{
 const queries:string[]=[],store=postgresPipelineChildrenStore(async sql=>{queries.push(sql);return {rows:[{receipt:f.receipt}]};});
 await store.open(f.receipt.intent,signal);await store.load(f.receipt.intent.identity,signal);await store.save(f.receipt,f.receipt.state,true,signal);await store.scan(scope,root.sha256,2,signal);assert.deepEqual(queries,f.queries);
});
test('total request deadline fences a late parent read',async()=>{
 const state=memory(),remote=native();remote.transport.parent=async i=>{await new Promise(r=>setTimeout(r,20));return {runId:i.parent_run_id,status:'running',cancelled:false,steps:[]};};
 const coord=new PipelineChildren({scope,parentContract:root,children:[definition],store:state.store,transport:remote.transport,requestTimeoutMs:5});
 const p=defineJobPipeline(root,{scope,store:{} as PipelineJobStore,jobs:{},children:coord},async(_v,wf)=>{await wf.child('one',definition,{n:1},{timeoutMs:60000});return {n:1};});
 const reply=await runWorkflowInvocation(p.definition,{protocol:WORKFLOW_CODE_PROTOCOL,run_id:f.receipt.intent.parent_run_id,workflow:root.spec.name,input:f.envelope,invocation:1,journal:[]});assert.equal(reply.ops[0]?.status,'failed');await new Promise(r=>setTimeout(r,25));assert.equal(state.rows.size,0);assert.equal(remote.posts,0);
});

test('a deadline between reservation ACK and POST retires only the never-sent child',async()=>{
 const s=setup(),j:JournalEntry[]=[];commit(j,await s.invoke());const save=s.state.store.save;
 s.state.store.save=async(old,state,admission,signal)=>{const r=await save(old,state,admission,signal);if(admission&&r)s.state.expired=true;return r;};
 const reply=await s.invoke(j);assert.equal(reply.ops[0]?.status,'succeeded');assert.equal(s.remote.posts,0);
 const view=await s.coordinator().inspect(f.result.parent_run_id,'imports','alice',definition,settings);assert.equal(view.stop_reason,'deadline');assert.ok(view.children.every(c=>c.phase==='skipped'));
});

test('single-child invocation returns a typed outcome; intermediate batch records omit outputs',async()=>{
 const s=setup(),j:JournalEntry[]=[];
 const p=defineJobPipeline(root,{scope,store:{} as PipelineJobStore,jobs:{},children:s.coordinator(),pollDelayMs:1},async(_v,wf)=>{const child=await wf.child('onechild',definition,{n:1},{timeoutMs:60000});return {n:child.output?.n??0};});
 let output;
 for(let n=0;n<8;n++){
  const r=await runWorkflowInvocation(p.definition,{protocol:WORKFLOW_CODE_PROTOCOL,run_id:f.result.parent_run_id,workflow:root.spec.name,input:f.envelope,invocation:n+1,journal:j});
  if(r.result.status==='succeeded'){output=(r.result as any).output.output;break;}commit(j,r);
 }
 assert.deepEqual(output,{n:2});assert.equal(s.remote.posts,1);
 const held=setup(),journal:JournalEntry[]=[];held.remote.held=true;commit(journal,await held.invoke());const r=await held.invoke(journal);assert.equal(Object.hasOwn(r.ops[0]!.output as object,'children'),false);assert.equal((await held.coordinator().inspect(f.result.parent_run_id,'imports','alice',definition,settings)).children.length,3);
});
