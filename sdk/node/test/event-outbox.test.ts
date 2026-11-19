import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {EventContract} from '../src/event-contract.js';
import {createEventPublisher,createEventOutbox,withEventOutboxTransaction,type EventOutboxTransaction,type EventOutboxPool} from '../src/event-outbox.js';
import {outboxSQL as SQL,eventOutboxSchema} from '../src/event-outbox-protocol.js';
import {prepareEventPublication,outboxHash,outboxPolicy,outboxPolicyDigest,eventOutboxRetryDelay,outboxInstant,parseOutboxRequest,parseEventOutboxStatus,outboxReceipt} from '../src/event-outbox-state.js';
const f=JSON.parse(readFileSync(new URL('../../../request-contracts/event-outbox.json',import.meta.url),'utf8'));
const options={key:f.options.key,time:f.options.time,environment:f.options.environment,schemaVersion:f.options.schema_version};
const contract=new EventContract<{label:string;n:number}>(f.contract),scope=f.scope;
const timeout={timeoutMs:1000};
function fixture() {
  const state={active:false,commits:0,rollbacks:0,releases:0,loseCommit:false,failStage:false,stageDigest:Buffer.from(f.digest,'hex'),status:{...f.status},settles:[] as unknown[][],next:true,paused:false,lease:true,policy:false};
  const pool:EventOutboxPool={async connect(){return {async query(sql:string,values:unknown[]=[]){
    if(sql.startsWith('BEGIN')){state.active=true;return {rows:[]};}
    if(sql.startsWith('SELECT set_config'))return {rows:[]};
    if(sql==='COMMIT'){state.active=false;state.commits++;if(state.loseCommit)throw Error('private commit');return {rows:[]};}
    if(sql==='ROLLBACK'){state.active=false;state.rollbacks++;return {rows:[]};}
    if(sql===SQL.stage){if(state.failStage)throw Error('private payload');assert.equal(values[5],f.body);return {rows:[]};}
    if(sql===SQL.stage_check)return {rows:[{request_digest:state.stageDigest,app_slug:scope.app_slug}]};
    if(sql===SQL.status)return {rows:[state.status]};
    if(sql===SQL.publishing_lock)return {rows:[]};
    if(sql===SQL.paused)return {rows:[{paused:state.paused}]};
    if(sql===SQL.rate)return {rows:[]};
    if(sql===SQL.next)return {rows:state.next?[{producer_key:options.key,app_slug:scope.app_slug,request_body:f.body,request_digest:state.stageDigest,expired:false,retry_window:86400,attempts:'0'}]:[]};
    if(sql===SQL.policy)return {rows:state.policy?[{version:1,revision:'1',initial_backoff_ms:'5',max_backoff_ms:'300',max_attempts:'3',initial_digest:Buffer.from(f.policy_digest,'hex')}]:[]};
    if(sql===SQL.claim){state.next=false;return {rows:[{attempts:'1'}]};}
    if(sql===SQL.lease)return {rows:state.lease?[{remaining:29}]:[]};
    if(sql===SQL.settle){assert.equal(state.active,false);state.settles.push(values);return {rows:[{...state.status,state:values[4],accepted_at:values[6],attempts:'1',last_code:values[5]}]};}
    if(sql===SQL.block)return {rows:[{...state.status,state:'blocked',last_code:values[3]}]};
    if(sql===SQL.reconcile_read)return {rows:[{...state.status,live:false,request_body:f.body,request_digest:state.stageDigest}]};
    if(sql===SQL.reconcile_settle){assert.equal(values[4],f.status.updated_at);return {rows:[{...state.status,state:'published',accepted_at:values[8],last_code:'publication_confirmed'}]};}
    throw Error('unexpected SQL');
  },release(){state.releases++;}};}};
  return {state,pool};
}
test('shared Go/Node/Python bytes, policies, jitter and exact timestamps',()=>{
  assert.equal(prepareEventPublication(contract,scope,f.input,options),f.body);
  const policy=outboxPolicy({initialBackoffMs:5,maxBackoffMs:300,maxAttempts:3});assert.equal(outboxPolicyDigest(policy),f.policy_digest);
  assert.deepEqual([1,2,3,9,2147483647].map(n=>eventOutboxRetryDelay(Buffer.from(f.token_hex,'hex'),n,policy,10)),f.jitter);
  assert.equal(outboxInstant('2026-11-16T12:00:00.123456789Z')-outboxInstant(f.status.created_at),789n);
  assert.equal(parseEventOutboxStatus({...f.status,request_body:'private',claim_token:'secret'}).updated_at,f.status.updated_at);
  assert.ok(!JSON.stringify(parseEventOutboxStatus(f.status)).includes('request_body'));
  assert.equal(outboxReceipt(f.receipt,scope,options.key).event_id,f.receipt.receipt.id);
  assert.equal(outboxHash(eventOutboxSchema),f.schema_digest);
  assert.deepEqual(SQL,JSON.parse(readFileSync(new URL('../../../request-contracts/event-outbox-sql.json',import.meta.url),'utf8')));
  assert.ok(eventOutboxSchema.includes('gregale_event_outbox_publishing_controls'));
  for(const bad of [{...JSON.parse(f.body),future:true},{...JSON.parse(f.body),expected_app_id:'other'},{...JSON.parse(f.body),environment:'invalid\n'}])assert.throws(()=>parseOutboxRequest(JSON.stringify(bad),scope.app_id),{code:'invalid_request'});
});
test('staging is transaction-bound, never publishes, and preserves the saved deadline',async()=>{
  const {pool,state}=fixture(),publisher=createEventPublisher(contract,{scope,fetch:async()=>{throw Error('unexpected HTTP');}});let saved:EventOutboxTransaction|undefined;
  const result=await withEventOutboxTransaction(pool,async tx=>{saved=tx;return publisher.stage(tx,f.input,options);},timeout);
  assert.equal(result.retry_until,f.status.retry_until);assert.equal(state.commits,1);
  await assert.rejects(publisher.stage(saved!,f.input,options),{code:'invalid_transaction'});
});
for(const mode of ['sql','conflict','invalid'] as const)test(`caught staging ${mode} still rolls the business transaction back`,async()=>{
  const {pool,state}=fixture();state.failStage=mode==='sql';if(mode==='conflict')state.stageDigest=Buffer.alloc(32);
  const publisher=createEventPublisher(contract,{scope});
  await assert.rejects(withEventOutboxTransaction(pool,async tx=>{try{await publisher.stage(tx,mode==='invalid'?{label:'x',n:0.5}:f.input,options);}catch{}return 'caught';},timeout),{code:mode==='sql'?'store_failed':mode==='conflict'?'conflict':'invalid_request'});
  assert.equal(state.commits,0);assert.equal(state.rollbacks,1);
});
test('lost commit never dispatches and retains an unknown outcome',async()=>{
  const {pool,state}=fixture();state.loseCommit=true;let posts=0;
  await assert.rejects(createEventOutbox(pool,{scope,token:'secret',fetch:async()=>{posts++;return Response.json(f.receipt);}}).recoverNext(timeout),{code:'commit_unknown'});assert.equal(posts,0);
});
for(const status of [200,403,408,429,500])test(`one-shot publication status ${status} settles only after acknowledged commit`,async()=>{
  const {pool,state}=fixture();let posts=0;const outbox=createEventOutbox(pool,{scope,token:'secret',fetch:async(url,init)=>{assert.equal(state.active,false);assert.equal(state.commits,1);assert.equal(init!.body,f.body);assert.equal(new Headers(init!.headers).get('authorization'),'Bearer secret');assert.ok(String(url).endsWith('/events:publish'));posts++;return Response.json(status===200?f.receipt:{detail:'private error'},{status});}});
  const result=await outbox.recoverNext(timeout);assert.equal(result.outcome,status===200?'published':status===403?'blocked':'unconfirmed');assert.equal(posts,1);assert.equal(state.settles.length,1);assert.ok(!JSON.stringify(result).includes('private'));assert.ok(!JSON.stringify(result).includes(f.input.label));
});
test('request deadline settles uncertainty while total operation still has time',async()=>{
  const {pool,state}=fixture();const outbox=createEventOutbox(pool,{scope,token:'secret',fetch:async()=>new Promise(()=>{})});
  assert.equal((await outbox.recoverNext({...timeout,requestTimeoutMs:10})).outcome,'unconfirmed');assert.equal(state.settles.length,1);
});
test('total deadline and cancellation fence late dispatch',async()=>{
  for(const cancel of [false,true]){const {pool,state}=fixture();let tx:EventOutboxTransaction|undefined,finish!:()=>void;const controller=new AbortController();
    const pending=withEventOutboxTransaction(pool,async t=>{tx=t;await new Promise<void>(r=>finish=r);return 1;},{timeoutMs:cancel?1000:10,signal:controller.signal});
    await new Promise(r=>setTimeout(r,5));if(cancel)controller.abort();await assert.rejects(pending,{code:cancel?'cancelled':'deadline'});finish();await new Promise(r=>setTimeout(r,1));assert.equal(state.commits,0);assert.throws(()=>tx!.query('late'),{code:'invalid_transaction'});
  }
});
test('pause, expired leases, tampering and retention mismatches avoid dispatch',async()=>{
  for(const mode of ['pause','lease','tamper','retention']){const {pool,state}=fixture();let posts=0;if(mode==='pause')state.paused=true;if(mode==='lease')state.lease=false;if(mode==='tamper')state.stageDigest=Buffer.alloc(32);
    const outbox=createEventOutbox(pool,{scope,token:'secret',receiptRetentionMs:mode==='retention'?1000:86400000,fetch:async()=>{posts++;return Response.json(f.receipt);}});
    if(mode==='retention')await assert.rejects(outbox.recoverNext(timeout),{code:'retention_mismatch'});else assert.equal((await outbox.recoverNext(timeout)).outcome,mode==='pause'?'paused':mode==='lease'?'claim_lost':'blocked');assert.equal(posts,0);
  }
});
test('bounded batch stops on empty and fences failed progress',async()=>{
  const {pool}=fixture(),outbox=createEventOutbox(pool,{scope,token:'secret',fetch:async()=>Response.json(f.receipt)});let progress=0;
  const rows=await outbox.recover({...timeout,maxEvents:3,concurrency:1,onProgress:()=>{progress++;}});assert.deepEqual(rows.map(r=>r.outcome),['published','empty']);assert.equal(progress,2);
  const second=fixture();await assert.rejects(createEventOutbox(second.pool,{scope,token:'secret',fetch:async()=>Response.json(f.receipt)}).recover({...timeout,maxEvents:3,onProgress:()=>{throw Error('private progress');}}),{code:'progress_failed'});
});
test('reconciliation uses read-only evidence and exact microsecond CAS timestamps',async()=>{
  const {pool}=fixture();let calls=0;
  const outbox=createEventOutbox(pool,{scope,token:'secret',fetch:async(url,init)=>{calls++;assert.ok(String(url).endsWith('/events/verify-publication'));assert.equal(init!.body,f.body);return Response.json({...f.receipt,event_id:f.receipt.receipt.id,status:'match',observed_at:'2026-11-16T12:01:00Z'});}});
  assert.equal((await outbox.reconcile(options.key,timeout)).outcome,'published');assert.equal(calls,1);
});
