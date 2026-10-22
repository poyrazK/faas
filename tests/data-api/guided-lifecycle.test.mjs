import test from 'node:test'
import assert from 'node:assert/strict'
import {lifecycleScaffold,validateMutations,mutationPlan,runGuidedMutations} from '../../cmd/gregale/templates/data-api/guided-mutations.mjs'
import {prepareAccessChecks,runAccessChecks} from '../../cmd/gregale/templates/data-api/dev-access.mjs'
import {mkdtemp,writeFile,rm} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
const fingerprint='a'.repeat(64)
const table=(name,columns)=>({schema:'api',name,view:false,primary_key:{filterable:true,columns:['id']},columns:columns.map(name=>({name})),relationships:[]})
const snapshot={tables:[table('notes',['id','subject','body']),table('memberships',['id','role'])]}
const read={version:3,status:'reviewed',view:{name:'Team notes',fingerprint,steps:[{schema:'api',table:'notes',columns:['id'],filter:null,sort:null,via:null}]},fixtures:[{name:'owned',schema:'api',table:'notes',keys:{id:7,subject:'alice'}}],checks:[{identity:'alice',steps:[{fixture:'owned',rows:1}]},{identity:'bob',steps:[{fixture:'owned',rows:0}]}]}
const checkpoint=(name,identity,fixture,rows)=>({name,identity,fixture,method:'GET',body:null,expect:{status:200,rows,outcome:'visible'}})
const write=(name,identity,fixture,method,body,outcome)=>({name,identity,fixture,method,body,expect:{status:204,rows:outcome==='unchanged'?0:1,outcome}})
const manifest=()=>({...structuredClone(read),version:5,mutationFixtures:[{name:'membership',schema:'api',table:'memberships',keys:{id:'alice'}},{name:'peer',schema:'api',table:'notes',keys:{id:8,subject:'bob'}}],mutations:[
 checkpoint('Member_before','alice','owned',1),
 write('Downgrade','admin','membership','PATCH',{role:'reader'},'updated'),
 checkpoint('Member_still_reads','alice','owned',1),
 write('Member_cannot_write','alice','owned','PATCH',{body:'attempted'},'unchanged'),
 checkpoint('Peer_after_downgrade','bob','peer',1),
 write('Revoke','admin','membership','DELETE',null,'deleted'),
 checkpoint('Former_member_hidden','alice','owned',0),
 write('Former_member_cannot_write','alice','owned','PATCH',{body:'attempted'},'unchanged'),
 checkpoint('Peer_after_revocation','bob','peer',1)
]})
function harness({trustStaleRole=false,ignoreRemoval=false,deleteDataOnRevocation=false}={}){
 const notes=[{id:7,subject:'alice',body:'original-private'},{id:8,subject:'bob',body:'peer-private'}],memberships=[{id:'alice',role:'editor'},{id:'bob',role:'editor'}],calls=[]
 const identities={alice:{token:'alice-issued-before-change'},bob:{token:'bob-issued-before-change'},admin:{token:'admin-issued-before-change'}}
 const context={url:'http://127.0.0.1',snapshot,fingerprint,identities,disposable:true,observe:async fixture=>({signature:JSON.stringify([notes,memberships]),rows:structuredClone((fixture.table==='notes'?notes:memberships).filter(row=>Object.entries(fixture.keys).every(([key,value])=>row[key]===value)))}),fetcher:async(target,options)=>{
  const actor=Object.entries(identities).find(([,identity])=>options.headers.Authorization===`Bearer ${identity.token}`)[0]
  calls.push({actor,token:options.headers.Authorization,target,method:options.method})
  const member=memberships.find(row=>row.id===actor),id=Number(target.searchParams.get('id')?.slice(3)),row=notes.find(row=>row.id===id)
  if(options.method==='GET'){
   const permitted=row&&row.subject===actor&&(member||ignoreRemoval)
   return new Response(JSON.stringify(permitted?[{id:row.id}]:[]),{status:200})
  }
  if(target.pathname.endsWith('/memberships')){
   assert.equal(actor,'admin')
   if(options.method==='PATCH')Object.assign(memberships.find(row=>row.id==='alice'),JSON.parse(options.body))
   else{memberships.splice(memberships.findIndex(row=>row.id==='alice'),1);if(deleteDataOnRevocation)notes.splice(notes.findIndex(row=>row.subject==='alice'),1)}
  }else if(row&&row.subject===actor&&(trustStaleRole||member?.role==='editor'))Object.assign(row,JSON.parse(options.body))
  return new Response(null,{status:204})
 }}
 return {context,calls,notes,memberships}
}
test('lifecycle scaffold leaves authorization decisions unreviewed and version 4 rejects checkpoints',()=>{
 const scaffold=lifecycleScaffold(read);assert.equal(scaffold.version,5);assert.equal(scaffold.status,'needs_review');assert.ok(scaffold.mutations.every(check=>check.identity===null));assert.ok(scaffold.mutationFixtures.every(fixture=>fixture.keys===null))
 assert.ok(scaffold.mutations.some(check=>check.method==='GET'));assert.equal(scaffold.mutationFixtures[0].table,'REVIEW_MEMBERSHIP_TABLE')
 const value=manifest();assert.equal(validateMutations(value,['alice','bob','admin']),value)
 assert.throws(()=>validateMutations({...value,version:4}));assert.throws(()=>validateMutations({...value,mutations:value.mutations.filter(check=>check.method==='GET')}))
 for(const change of [value=>value.mutations[0].identity=null,value=>value.mutations[0].expect.rows=2,value=>value.mutations[0].expect.status=401,value=>value.mutations[0].body={role:'reader'},value=>value.mutations[0].fixture='missing']){const value=manifest();change(value);assert.throws(()=>validateMutations(value,['alice','bob','admin']))}
})
test('ordered downgrade, revocation and unaffected-member checkpoints reuse tokens issued before changes',async()=>{
 const {context,calls,notes}=harness(),results=await runGuidedMutations(manifest(),context)
 assert.equal(results.length,11);assert.ok(results.every(result=>result.passed));assert.equal(notes[0].body,'original-private')
 assert.ok(results.filter(result=>result.checkpoint).every(result=>result.state_matches))
 assert.ok(calls.filter(call=>call.actor==='alice').every(call=>call.token==='Bearer alice-issued-before-change'))
 assert.ok(!JSON.stringify(results).includes('issued-before-change'));assert.ok(!JSON.stringify(results).includes('private'))
 assert.deepEqual(calls.slice(2).map(call=>call.method),['GET','PATCH','GET','PATCH','GET','DELETE','GET','PATCH','GET'])
})
test('stale role and revoked-membership regressions fail at their checkpoint and stop later steps',async()=>{
 for(const [options,failedName] of [[{trustStaleRole:true},'Member_cannot_write'],[{ignoreRemoval:true},'Former_member_hidden']]){
  const {context}=harness(options),results=await runGuidedMutations(manifest(),context)
  assert.equal(results.find(result=>!result.passed&&!result.skipped).name,failedName);assert.ok(results.at(-1).skipped)
 }
 const {context}=harness({deleteDataOnRevocation:true}),results=await runGuidedMutations(manifest(),context)
 assert.equal(results.find(result=>!result.passed&&!result.skipped).name,'Former_member_hidden')
})
test('lifecycle files dispatch through CLI preparation while inspector contexts cannot execute them',async t=>{
 const root=await mkdtemp(join(tmpdir(),'gregale-lifecycle-'));t.after(()=>rm(root,{recursive:true,force:true}))
 const path=join(root,'access.json'),{context,calls}=harness();await writeFile(path,JSON.stringify(manifest()))
 const prepared=await prepareAccessChecks(path,{identities:context.identities});assert.equal(prepared.version,5);assert.equal(Object.hasOwn(prepared,'revision'),false)
 assert.ok((await runAccessChecks(prepared,context)).every(result=>result.passed))
 calls.length=0;assert.throws(()=>mutationPlan(manifest(),{...context,disposable:false}),/disposable/)
 assert.ok((await runGuidedMutations(manifest(),{...context,disposable:false})).every(result=>result.skipped));assert.equal(calls.length,0)
})
