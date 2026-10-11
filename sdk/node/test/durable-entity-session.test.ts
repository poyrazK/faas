import test from 'node:test';
import assert from 'node:assert/strict';
import {defineDurableSessionEntity, durableSessionFrame} from '../src/durable-entity-session.js';
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
