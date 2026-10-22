import test from 'node:test'
import assert from 'node:assert/strict'
import {mutationScaffold,validateMutations,mutationPlan,runGuidedMutations} from '../../cmd/gregale/templates/data-api/guided-mutations.mjs'
const fingerprint='a'.repeat(64)
const snapshot={tables:[{schema:'api',name:'notes',view:false,primary_key:{filterable:true,columns:['id']},columns:[{name:'id',insertable:false},{name:'tenant'},{name:'body'}],relationships:[]}]}
const read={version:3,status:'reviewed',view:{name:'Notes',fingerprint,steps:[{schema:'api',table:'notes',columns:['id'],filter:null,sort:null,via:null}]},fixtures:[{name:'owned',schema:'api',table:'notes',keys:{id:7,tenant:'alice'}}],checks:[{identity:'alice',steps:[{fixture:'owned',rows:1}]},{identity:'bob',steps:[{fixture:'owned',rows:0}]}]}
const operation=(name,identity,method,fixture,body,status,rows,outcome)=>({name,identity,method,fixture,body,expect:{status,rows,outcome}})
const manifest=()=>({...structuredClone(read),version:4,mutationFixtures:[{name:'created',schema:'api',table:'notes',keys:{tenant:'alice',body:'created-marker'}}],mutations:[
 operation('Denied_create','bob','POST','created',{tenant:'alice',body:'created-marker'},403,0,'unchanged'),
 operation('Owner_create','alice','POST','created',{tenant:'alice',body:'created-marker'},201,1,'created'),
 operation('Denied_update','bob','PATCH','owned',{body:'attempted'},204,0,'unchanged'),
 operation('Denied_transfer','alice','PATCH','owned',{tenant:'bob'},403,0,'unchanged'),
 operation('Owner_update','alice','PATCH','owned',{body:'updated'},204,1,'updated'),
 operation('Denied_delete','bob','DELETE','owned',null,204,0,'unchanged'),
 operation('Owner_delete','alice','DELETE','owned',null,204,1,'deleted')
]})
function harness({regression=false,errorAfterWrite=false,transferAllowed=false}={}){
 const state=[{id:7,tenant:'alice',body:'original',hidden:'private-state'}],calls=[]
 const context={url:'http://127.0.0.1',identities:{alice:{token:'secret-a'},bob:{token:'secret-b'}},snapshot,fingerprint,disposable:true,observe:async fixture=>({signature:JSON.stringify(state),rows:structuredClone(state.filter(row=>Object.entries(fixture.keys).every(([key,value])=>row[key]===value)))}),fetcher:async(target,options)=>{
  calls.push({target,options});const alice=options.headers.Authorization.endsWith('secret-a')
  if(options.method==='GET')return new Response(JSON.stringify(alice?[{id:7}]:[]),{status:200})
  assert.equal(options.headers.Prefer,'return=minimal')
  const body=options.body?JSON.parse(options.body):null
  if(options.method==='POST'){
   if(!alice)return new Response('{"code":"42501","message":"private-error"}',{status:403})
   state.push({id:8,...body});return new Response(null,{status:201})
  }
  if(!alice){if(regression&&options.method==='PATCH')state[0].body='unexpected-mutation';return new Response(null,{status:204})}
  if(options.method==='PATCH'){
   if(body.tenant==='bob'){if(transferAllowed){Object.assign(state[0],body);return new Response(null,{status:204})}return new Response('{"code":"42501"}',{status:403})}
   Object.assign(state[0],body);if(errorAfterWrite)throw new Error('private-timeout');return new Response(null,{status:204})
  }
  state.splice(0,1);return new Response(null,{status:204})
 }}
 return {state,calls,context}
}
test('mutation scaffold is unreviewed; writes require a disposable observer and scoped keys',()=>{
 const scaffold=mutationScaffold(read);assert.equal(scaffold.status,'needs_review');assert.ok(scaffold.mutations.every(check=>check.body===null&&check.expect.outcome===null));assert.equal(scaffold.mutationFixtures[0].keys,null)
 const {context}=harness();const value=manifest();assert.equal(validateMutations(value),value);assert.throws(()=>mutationPlan(value,{...context,disposable:false}),/disposable/);assert.throws(()=>mutationPlan(value,{...context,observe:undefined}),/observer/)
 for(const change of [value=>value.mutations[0].body={body:'wrong'},value=>value.mutations[2].fixture='missing',value=>value.mutations[4].body={missing:1},value=>value.mutations[0].expect.status=400]){const invalid=manifest();change(invalid);assert.throws(()=>mutationPlan(invalid,context))}
})
test('owner CRUD and denied cross-owner writes and transfers verify private before/after state',async()=>{
 const {state,calls,context}=harness(),results=await runGuidedMutations(manifest(),context)
 assert.equal(results.length,9);assert.ok(results.every(result=>result.passed));assert.equal(state.length,1);assert.equal(state[0].body,'created-marker')
 assert.ok(results.filter(result=>result.operation&&result.name.startsWith('Denied')).every(result=>result.unchanged))
 assert.ok(!JSON.stringify(results).includes('private'));assert.ok(!JSON.stringify(results).includes('secret'));assert.ok(!JSON.stringify(results).includes('created-marker'))
 const writes=calls.filter(call=>call.options.method!=='GET');assert.equal(writes.length,7);assert.ok(writes.every(call=>call.target.pathname.includes(`/contract/${fingerprint}/`)))
 assert.equal(writes[2].target.searchParams.get('id'),'eq.7')
})
test('hidden-state regressions and uncertain outcomes stop later writes; no vacuous or no-op passes',async()=>{
 const {context,calls}=harness({regression:true}),results=await runGuidedMutations(manifest(),context)
 const failure=results.find(result=>!result.passed&&!result.skipped);assert.equal(failure.name,'Denied_update');assert.equal(failure.unchanged,false);assert.ok(results.at(-1).skipped)
 assert.equal(calls.filter(call=>call.options.method!=='GET').length,3)
 const {context:uncertain}=harness({errorAfterWrite:true});const outcomes=await runGuidedMutations(manifest(),uncertain);assert.equal(outcomes.find(result=>result.name==='Owner_update').passed,false);assert.ok(outcomes.at(-1).skipped)
 const {context:missing}=harness();missing.observe=async()=>({signature:'empty',rows:[]});assert.ok((await runGuidedMutations(manifest(),missing)).some(result=>!result.passed))
 const {context:noOp}=harness();const same=manifest();same.mutations=[operation('No_op','alice','PATCH','owned',{body:'original'},204,1,'updated')];assert.equal((await runGuidedMutations(same,noOp)).at(-1).passed,false)
})
test('inspector contexts and unreviewed or stale manifests never send writes',async()=>{
 for(const change of [context=>context.disposable=false,context=>context.fingerprint='b'.repeat(64)]){
  const {context,calls}=harness();change(context);const results=await runGuidedMutations(manifest(),context);assert.equal(calls.length,0);assert.ok(results.every(result=>!result.passed&&result.skipped))
 }
 const {context,calls}=harness();const value=mutationScaffold(read);const results=await runGuidedMutations(value,context);assert.equal(calls.length,0);assert.ok(results.every(result=>!result.passed))
})
test('minimal responses expose an ownership-transfer regression even when the writer loses read access',async()=>{
 const {context,state}=harness({transferAllowed:true}),results=await runGuidedMutations(manifest(),context)
 const transfer=results.find(result=>result.name==='Denied_transfer');assert.equal(transfer.passed,false);assert.equal(transfer.unchanged,false);assert.equal(state[0].tenant,'bob');assert.ok(results.at(-1).skipped)
})
test('a failed positive read baseline prevents all mutation requests',async()=>{
 const {context,calls}=harness();context.fetcher=async(target,options)=>{calls.push({target,options});return new Response('[]',{status:200})}
 const results=await runGuidedMutations(manifest(),context);assert.ok(results.some(result=>!result.passed));assert.ok(results.filter(result=>result.operation).every(result=>result.skipped));assert.ok(calls.every(call=>call.options.method==='GET'))
})
