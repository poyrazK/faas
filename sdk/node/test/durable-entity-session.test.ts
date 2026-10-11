import test from 'node:test';
import assert from 'node:assert/strict';
import {defineDurableSessionEntity, durableSessionFrame} from '../src/durable-entity-session.js';
import {defineRecoverableDurableSessionEntity, durableSessionLifecycleFrame, decodeDurableSessionStatus, decodeDurableSessionLifecycleAcknowledgement, decodeDurableSessionLifecycleState, type DurableSessionLifecycleCommand} from '../src/durable-entity-session.js';
import {createDurableEntityRegistry} from '../src/durable-entity-runtime.js';
import {DurableEntitySimulator} from '../src/durable-entity-testing.js';
const id='11111111-1111-4111-8111-111111111111';
function registry() {return createDurableEntityRegistry([defineDurableSessionEntity({namespace:'rooms',initialState:()=>({count:0}),decodeState(value:unknown){if(value===null||typeof value!=='object'||!('count' in value)||!Number.isSafeInteger(value.count))throw new TypeError('count');return {count:value.count as number};},decodePayload:(value:unknown)=>{if(typeof value!=='number')throw new TypeError('delta');return value;},handle:({state,payload})=>({state:{count:state.count+payload},result:state.count+payload})})]);}
function input(command: Parameters<typeof durableSessionFrame>[0]) {const frame=durableSessionFrame(command);return {namespace:'rooms',key:'document:123',requestId:frame.request_id,payload:frame.payload};}
test('ordered session survives lost acknowledgement, reconnect checkpoint and explicit close',async()=>{
 const sim=new DurableEntitySimulator(registry());await sim.invoke(input({kind:'open',session_id:id}));
 const message=input({kind:'message',session_id:id,sequence:1,payload:2});
 await assert.rejects(sim.invoke(message,{fault:'after-commit'}));
 const restarted=new DurableEntitySimulator(registry());restarted.restore(sim.checkpoint());
 assert.equal((await restarted.invoke(message)).replayed,true);
 await assert.rejects(restarted.invoke(input({kind:'message',session_id:id,sequence:3,payload:1})));
 await assert.rejects(restarted.invoke({...message,requestId:'fresh-id'}));
 await assert.rejects(restarted.invoke(input({kind:'message',session_id:id,sequence:1,payload:9})));
 const closed=await restarted.invoke(input({kind:'close',session_id:id,sequence:2}));assert.equal((closed.value as {closed:boolean}).closed,true);
 await assert.rejects(restarted.invoke(input({kind:'message',session_id:id,sequence:3,payload:1})));
 assert.equal((await restarted.invoke(message)).replayed,true);
});

const operation=(n:number)=>`${n.toString(16).padStart(8,'0')}-1111-4111-8111-111111111111`;
function lifecycleRegistry() {return createDurableEntityRegistry([defineRecoverableDurableSessionEntity({namespace:'rooms',idleTicks:10,retentionTicks:5,
 initialState:()=>({count:0}),decodeState(value:unknown){const item=value as {count:number};if(!item||!Number.isSafeInteger(item.count))throw new TypeError('count');return {count:item.count};},
 decodePayload:(value:unknown)=>{if(typeof value!=='number')throw new TypeError('delta');return value;},handle:({state,payload})=>({state:{count:state.count+payload},result:state.count+payload})})]);}
function lifecycleInput(command:DurableSessionLifecycleCommand) {const frame=durableSessionLifecycleFrame(command);return {namespace:'rooms',key:'document:123',requestId:frame.request_id,payload:frame.payload};}
test('fresh status recovers progress while replaying a query preserves its original snapshot',async()=>{
 const sim=new DurableEntitySimulator(lifecycleRegistry());
 await sim.invoke(lifecycleInput({kind:'open',session_id:id,generation:0}));
 const query={kind:'status',session_id:id,query_id:operation(1)} as const;
 const original=await sim.invoke(lifecycleInput(query));assert.equal((original.value as {sequence:number}).sequence,0);
 const message=lifecycleInput({kind:'message',session_id:id,generation:0,sequence:1,payload:3});
 await assert.rejects(sim.invoke(message,{fault:'after-commit'}));
 const restored=new DurableEntitySimulator(lifecycleRegistry());restored.restore(sim.checkpoint());
 assert.equal((await restored.invoke(lifecycleInput(query))).replayed,true);
 const pending=durableSessionLifecycleFrame({...query,query_id:operation(2)});
 const current=await restored.invoke(lifecycleInput(pending.payload));
 const reply={protocol_version:1,request_id:pending.request_id,status:200,result:current};
 assert.equal(decodeDurableSessionStatus(reply,pending).sequence,1);
 assert.equal((await restored.invoke(message)).replayed,true);
 assert.throws(()=>decodeDurableSessionStatus({...reply,request_id:'wrong'},pending));
 assert.throws(()=>decodeDurableSessionStatus({...reply,result:{...current,value:{...(current.value as object),generation:-1}}},pending));
});
test('expiry, retention and generation fences survive lost commits and receipt removal',async()=>{
 const sim=new DurableEntitySimulator(lifecycleRegistry());
 const open=lifecycleInput({kind:'open',session_id:id,generation:0});await sim.invoke(open);
 await assert.rejects(sim.invoke(lifecycleInput({kind:'reclaim',generation:0,operation_id:operation(1)})));
 await sim.invoke(lifecycleInput({kind:'advance',generation:0,tick:9,operation_id:operation(2)}));
 await sim.invoke(lifecycleInput({kind:'message',session_id:id,generation:0,sequence:1,payload:2}));
 await sim.invoke(lifecycleInput({kind:'advance',generation:0,tick:18,operation_id:operation(3)}));
 const active=sim.inspect('rooms','document:123');assert.ok(active);
 assert.equal((active.data as {data:{sessions:{closed:boolean}[]}}).data.sessions[0]!.closed,false);
 await sim.invoke(lifecycleInput({kind:'advance',generation:0,tick:19,operation_id:operation(4)}));
 await assert.rejects(sim.invoke(lifecycleInput({kind:'message',session_id:id,generation:0,sequence:2,payload:1})));
 await assert.rejects(sim.invoke(lifecycleInput({kind:'reclaim',generation:0,operation_id:operation(5)})));
 await assert.rejects(sim.invoke(lifecycleInput({kind:'advance',generation:0,tick:18,operation_id:operation(6)})));
 await sim.invoke(lifecycleInput({kind:'advance',generation:0,tick:24,operation_id:operation(7)}));
 const reclaim=lifecycleInput({kind:'reclaim',generation:0,operation_id:operation(8)});
 await assert.rejects(sim.invoke(reclaim,{fault:'before-commit'}));
 await assert.rejects(sim.invoke(reclaim,{fault:'after-commit'}));
 assert.equal((await sim.invoke(reclaim)).replayed,true);
 // Remove receipts to prove the generation fence, independently of deduplication.
 const checkpoint=JSON.parse(sim.checkpoint());checkpoint.entities[0].receipts=[];
 const restored=new DurableEntitySimulator(lifecycleRegistry());restored.restore(JSON.stringify(checkpoint));
 await assert.rejects(restored.invoke(open));await assert.rejects(restored.invoke(reclaim));
 const fresh=durableSessionLifecycleFrame({kind:'open',session_id:id,generation:1});
 const result=await restored.invoke(lifecycleInput(fresh.payload));
 const reply={protocol_version:1,request_id:fresh.request_id,status:200,result};
 assert.equal(decodeDurableSessionLifecycleAcknowledgement(reply,fresh,v=>v).generation,1);
 assert.throws(()=>decodeDurableSessionLifecycleAcknowledgement({...reply,result:{...result,value:{...(result.value as object),generation:0}}},fresh,v=>v));
 const retained=restored.inspect('rooms','document:123');assert.ok(retained);
 assert.equal((retained.data as {data:{application:{count:number}}}).data.application.count,2);
});
test('legacy and application migrations preserve ordering and lifecycle metadata',async()=>{
 const legacy=new DurableEntitySimulator(registry());await legacy.invoke(input({kind:'open',session_id:id}));await legacy.invoke(input({kind:'message',session_id:id,sequence:1,payload:2}));
 const migrated=new DurableEntitySimulator(lifecycleRegistry());migrated.restore(legacy.checkpoint());
 const status=await migrated.invoke(lifecycleInput({kind:'status',session_id:id,query_id:operation(1)}));assert.equal((status.value as {sequence:number}).sequence,1);
 await migrated.invoke(lifecycleInput({kind:'message',session_id:id,generation:0,sequence:2,payload:3}));
 const updated=createDurableEntityRegistry([defineRecoverableDurableSessionEntity({namespace:'rooms',idleTicks:10,retentionTicks:5,schemaVersion:3,
 applicationMigrations:new Map([[2,(value:unknown)=>({total:(value as {count:number}).count})]]),initialState:()=>({total:0}),
 decodeState(value:unknown){const item=value as {total:number};if(!item||!Number.isSafeInteger(item.total))throw new TypeError('total');return {total:item.total};},
 decodePayload:(value:unknown)=>value as number,handle:({state,payload})=>({state:{total:state.total+payload},result:state.total+payload})})]);
 const restarted=new DurableEntitySimulator(updated);restarted.restore(migrated.checkpoint());
 await restarted.invoke(lifecycleInput({kind:'message',session_id:id,generation:0,sequence:3,payload:1}));
 const current=restarted.inspect('rooms','document:123');assert.ok(current);
 const state=current.data as {schema_version:number;data:{generation:number;application:{total:number};sessions:{sequence:number}[]}};
 assert.equal(state.schema_version,3);assert.equal(state.data.application.total,6);assert.equal(state.data.generation,0);assert.equal(state.data.sessions[0]!.sequence,3);
 assert.throws(()=>decodeDurableSessionLifecycleState({...state.data,tick:-1},v=>v));
 assert.throws(()=>decodeDurableSessionLifecycleState({...state.data,sessions:[{...state.data.sessions[0],active_at:1}]},v=>v));
 assert.throws(()=>durableSessionLifecycleFrame({kind:'open',session_id:id,generation:Number.MAX_SAFE_INTEGER+1}));
});

test('invalid migrations fail before publication and cannot mutate retained metadata',async()=>{
 const base={namespace:'rooms',idleTicks:10,retentionTicks:5,initialState:()=>({count:0}),decodeState:(value:unknown)=>value as {count:number},decodePayload:(value:unknown)=>value,handle:({state}:{state:{count:number}})=>({state,result:null})};
 assert.throws(()=>defineRecoverableDurableSessionEntity({...base,schemaVersion:3}));
 const sim=new DurableEntitySimulator(lifecycleRegistry());await sim.invoke(lifecycleInput({kind:'open',session_id:id,generation:0}));
 const bad=createDurableEntityRegistry([defineRecoverableDurableSessionEntity({...base,schemaVersion:3,applicationMigrations:new Map([[2,async(value:unknown)=>value]])})]);
 const restored=new DurableEntitySimulator(bad);restored.restore(sim.checkpoint());
 const before=restored.checkpoint();
 await assert.rejects(restored.invoke(lifecycleInput({kind:'status',session_id:id,query_id:operation(1)})));
 assert.equal(restored.checkpoint(),before);
});
test('retained session identity has a bounded budget and frames capture exact pending payload',async()=>{
 const sim=new DurableEntitySimulator(registry());
 for(let n=0;n<32;n++){await sim.invoke(input({kind:'open',session_id:`${n.toString(16).padStart(8,'0')}-1111-4111-8111-111111111111`}));}
 await assert.rejects(sim.invoke(input({kind:'open',session_id:'ffffffff-1111-4111-8111-111111111111'})));
 assert.throws(()=>durableSessionFrame({kind:'message',session_id:id,sequence:0,payload:1}));
 assert.throws(()=>durableSessionFrame({kind:'message',session_id:id,sequence:1,payload:'x'.repeat(65536)}));
 const payload={delta:1};const frame=durableSessionFrame({kind:'message',session_id:id,sequence:1,payload});payload.delta=9;assert.deepEqual((frame.payload as {payload:unknown}).payload,{delta:1});
});

test('invalid acknowledgement cannot advance the session cursor', async()=>{
 const {decodeDurableSessionAcknowledgement}=await import('../src/durable-entity-session.js');
 const pending=durableSessionFrame({kind:'message',session_id:id,sequence:1,payload:2});
 const response={protocol_version:1,request_id:pending.request_id,status:200,result:{version:2,replayed:true,value:{session_id:id,sequence:1,closed:false,value:2}}};
 assert.equal(decodeDurableSessionAcknowledgement(response,pending,v=>v).replayed,true);
 assert.throws(()=>decodeDurableSessionAcknowledgement({...response,request_id:'different'},pending,v=>v));
 assert.throws(()=>decodeDurableSessionAcknowledgement({...response,result:{...response.result,version:Number.MAX_SAFE_INTEGER+1}},pending,v=>v));
 assert.throws(()=>decodeDurableSessionAcknowledgement({...response,status:503},pending,v=>v));
});

import {connectDurableEntityBrowserSession, type DurableEntityBrowserGrant} from '../src/durable-entity-browser.js';
test('browser connector keeps scoped credentials out of URLs and rejects unsafe targets',()=>{
 const original=globalThis.WebSocket;
 const calls:Array<{url:string;protocols:unknown}>=[];
 class FakeSocket {constructor(url:string|URL,protocols?:string|string[]){calls.push({url:String(url),protocols});}}
 globalThis.WebSocket=FakeSocket as unknown as typeof WebSocket;
 try {
  const grant:DurableEntityBrowserGrant={credential:`${id}.${'a'.repeat(43)}`,connect_path:'/v1/apps/rooms/entities/session-connect',expires_at:new Date(Date.now()+60000).toISOString(),protocol:'gregale.entity.v1'};
  connectDurableEntityBrowserSession('https://api.example.com',grant);
  assert.equal(calls[0]?.url,'wss://api.example.com/v1/apps/rooms/entities/session-connect');
  assert.deepEqual(calls[0]?.protocols,['gregale.entity.v1',`gregale.grant.${grant.credential}`]);
  connectDurableEntityBrowserSession('https://api.example.com',{...grant,credential:`${'a'.repeat(64)}.${grant.credential}`});
  assert.deepEqual(calls[1]?.protocols,['gregale.entity.v1',`gregale.grant.${'a'.repeat(64)}.${grant.credential}`]);
  for(const origin of ['http://api.example.com','https://owner:secret@api.example.com','https://api.example.com/?token=secret'])assert.throws(()=>connectDurableEntityBrowserSession(origin,grant));
  assert.throws(()=>connectDurableEntityBrowserSession('https://api.example.com',{...grant,connect_path:'//attacker.example/connect'}));
  assert.throws(()=>connectDurableEntityBrowserSession('https://api.example.com',{...grant,expires_at:'invalid'}));
  assert.throws(()=>connectDurableEntityBrowserSession('https://api.example.com',{...grant,expires_at:new Date(0).toISOString()}));
 } finally {globalThis.WebSocket=original;}
});
