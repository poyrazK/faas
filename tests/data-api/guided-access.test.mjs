import test from 'node:test'
import assert from 'node:assert/strict'
import {accessScaffold,validateGuidedAccess,guidedPlan,runGuidedAccess} from '../../cmd/gregale/templates/data-api/guided-access.mjs'
const fingerprint='a'.repeat(64)
const columns=names=>names.map(name=>({name,type:name==='id'||name==='parent_id'?'number':'string'}))
const snapshot={tables:[{schema:'api',name:'parents',columns:columns(['id','tenant']),primary_key:{filterable:true,columns:['id']},relationships:[]},{schema:'api',name:'children',columns:columns(['id','tenant','parent_id']),primary_key:{filterable:true,columns:['id']},relationships:[{foreignKeyName:'child_parent',columns:['tenant','parent_id'],referencedRelation:'parents',referencedColumns:['tenant','id']}]}]}
const view={name:'Owned children',fingerprint,steps:[{schema:'api',table:'parents',columns:['id'],filter:null,sort:null,via:null},{schema:'api',table:'children',columns:['id'],filter:null,sort:null,via:{foreignKey:'child_parent',direction:'children'}}]}
const reviewed=()=>{
 const value=accessScaffold(view,['alice','bob']);value.status='reviewed'
 value.fixtures[0].keys={id:7,tenant:'synthetic-tenant'};value.fixtures[1].keys={id:8,tenant:'synthetic-tenant',parent_id:7}
 value.checks[0].steps.forEach(step=>step.rows=1);value.checks[1].steps.forEach(step=>step.rows=0)
 return value
}
test('scaffolds use named fixtures, infer no ownership expectations and require positive baselines',()=>{
 const scaffold=accessScaffold(view,['alice','bob'])
 assert.equal(scaffold.status,'needs_review');assert.equal(scaffold.fixtures[0].keys,null);assert.equal(scaffold.checks[0].steps[0].rows,null)
 assert.throws(()=>guidedPlan(scaffold,snapshot,fingerprint,['alice','bob']),/Review/)
 const value=reviewed();assert.equal(validateGuidedAccess(value,['alice','bob']),value)
 value.checks[0].steps[1].rows=0;assert.throws(()=>validateGuidedAccess(value),/positive/)
 for(const change of [value=>value.token='private-token',value=>value.checks[0].identity='unknown',value=>value.fixtures[0].keys={id:null},value=>value.checks[0].steps[0].rows=51]){
  const value=reviewed();change(value);assert.throws(()=>validateGuidedAccess(value,['alice','bob']))
 }
})
test('preflight checks every composite component, declared primary key, and schema fingerprint',()=>{
 assert.equal(guidedPlan(reviewed(),snapshot,fingerprint,['alice','bob']).length,2)
 for(const change of [value=>delete value.fixtures[1].keys.parent_id,value=>value.fixtures[1].keys.tenant='other',value=>delete value.fixtures[1].keys.id]){
  const value=reviewed();change(value);assert.throws(()=>guidedPlan(value,snapshot,fingerprint,['alice','bob']))
 }
 assert.throws(()=>guidedPlan(reviewed(),snapshot,'b'.repeat(64),['alice','bob']),/Schema changed/)
})
test('all identity steps execute independently, reports redact contents, and RLS regressions identify the failed hop',async()=>{
 const identities={alice:{token:'alice-private'},bob:{token:'bob-private'}};const calls=[]
 const context={url:'http://127.0.0.1:1234',identities,snapshot,fingerprint,fetcher:async(target,options)=>{
  calls.push({target,options})
  return new Response(JSON.stringify(options.headers.Authorization.includes('alice')?[{id:7,secret:'never-report-this'}]:[]),{status:200})
 }}
 const results=await runGuidedAccess(reviewed(),context);assert.equal(results.length,4);assert.ok(results.every(result=>result.passed));assert.equal(calls.length,4)
 for(const {target,options} of calls){assert.equal(options.method,'GET');assert.ok(target.pathname.startsWith(`/__gregale/contract/${fingerprint}/`));assert.equal(target.searchParams.get('tenant'),'eq.synthetic-tenant');assert.equal(options.headers['Accept-Profile'],'api')}
 assert.equal(calls[3].target.searchParams.get('parent_id'),'eq.7')
 assert.ok(!JSON.stringify(results).includes('never-report-this'));assert.ok(!JSON.stringify(results).includes('private'))
 const regression=await runGuidedAccess(reviewed(),{...context,fetcher:async(target,options)=>new Response(JSON.stringify(options.headers.Authorization.includes('bob')&&target.pathname.endsWith('/children')?[{id:8}]:options.headers.Authorization.includes('alice')?[{id:7}]:[]),{status:200})})
 assert.equal(regression[3].passed,false);assert.equal(regression[3].step,2);assert.equal(regression[3].identity,'bob')
 const missing=await runGuidedAccess(reviewed(),{...context,fetcher:async()=>new Response('[]',{status:200})});assert.ok(missing.some(result=>!result.passed))
 calls.length=0;const stale=await runGuidedAccess(reviewed(),{...context,fingerprint:'b'.repeat(64)});assert.equal(calls.length,0);assert.ok(stale.every(result=>result.skipped&&!result.passed))
})
test('read errors, invalid row shapes, response limits and cancellation cannot pass denial assertions',async()=>{
 const context={url:'http://127.0.0.1',identities:{alice:{token:'a'},bob:{token:'b'}},snapshot,fingerprint}
 for(const fetcher of [async()=>new Response('[]',{status:403}),async()=>new Response('[null]',{status:200}),async()=>new Response('x'.repeat(1048577)),async()=>{throw new Error('secret-value')}]){
  const results=await runGuidedAccess(reviewed(),{...context,fetcher});assert.ok(results.every(result=>!result.passed));assert.ok(!JSON.stringify(results).includes('secret-value'))
 }
 const controller=new AbortController();controller.abort();await assert.rejects(runGuidedAccess(reviewed(),{...context,signal:controller.signal,fetcher:async()=>{assert.fail('aborted request must not execute')}}))
})

import {prepareAccessChecks,runAccessChecks} from '../../cmd/gregale/templates/data-api/dev-access.mjs'
import {mkdtemp,writeFile,rm} from 'node:fs/promises'
import {join} from 'node:path'
import {tmpdir} from 'node:os'
test('CLI preparation strips storage revisions and preserves legacy/guided dispatch',async t=>{
 const root=await mkdtemp(join(tmpdir(),'gregale-guided-prepare-'));t.after(()=>rm(root,{recursive:true,force:true}))
 const path=join(root,'access.json'),identities={alice:{token:'private-a'},bob:{token:'private-b'}}
 await writeFile(path,JSON.stringify(reviewed()))
 const manifest=await prepareAccessChecks(path,{identities});assert.equal(Object.hasOwn(manifest,'revision'),false)
 const results=await runAccessChecks(manifest,{url:'http://127.0.0.1',identities,snapshot,fingerprint,fetcher:async(_,options)=>new Response(options.headers.Authorization.includes('private-a')?'[{"id":7}]':'[]',{status:200})})
 assert.ok(results.every(result=>result.passed))
 await writeFile(path,JSON.stringify({...reviewed(),status:'needs_review'}));assert.ok((await runAccessChecks(await prepareAccessChecks(path,{identities}),{url:'http://127.0.0.1',identities,snapshot,fingerprint})).every(result=>!result.passed&&result.skipped))
})
