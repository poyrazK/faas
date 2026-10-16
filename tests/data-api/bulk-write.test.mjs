import test from 'node:test'
import assert from 'node:assert/strict'
import {bulkPlan,bulkTarget,bulkOutcome,runBulkWrite,retryBulkEntries} from '../../cmd/gregale/templates/data-api/bulk-write.mjs'
const model={schema:'api',table:'notes',primaryKey:{filterable:true,columns:['subject','id']},concurrency:{kind:'integer_version',column:'version'},updateFields:[{name:'subject'},{name:'body'},{name:'version'}],updateSpec:{kind:'object',fields:[{name:'subject',optional:true,nullable:false,value:{kind:'string'}},{name:'body',optional:true,nullable:false,value:{kind:'string',maxLength:8}},{name:'version',optional:true,nullable:false,value:{kind:'number',integer:true}}]}}
const rows=[{id:1,subject:'alice',body:'old',version:1},{id:2,subject:'alice',body:'old2',version:2}]
const plan=extra=>bulkPlan({model,rows,operation:'update',changes:{body:'new'},fingerprint:'a'.repeat(64),identity:'alice',context:'context',...extra})
test('review snapshots keys and versions, validates schema changes, and pins composite-key targets',()=>{
 const p=plan(),url=bulkTarget(p,p.entries[0],'http://localhost')
 assert.equal(url.searchParams.get('subject'),'eq.alice');assert.equal(url.searchParams.get('id'),'eq.1');assert.equal(url.searchParams.get('version'),'eq.1')
 assert.equal(url.searchParams.get('select'),'"subject","id","version"');assert.equal(p.body,'{"body":"new"}');assert.deepEqual(p.entries[0].before,{body:'old'})
 for(const changes of [{},{subject:'bob'},{version:2},{unknown:true},{body:'too-long-value'}])assert.throws(()=>plan({changes}))
 assert.throws(()=>plan({rows:[rows[0],rows[0]]}));assert.throws(()=>plan({rows:[{...rows[0],version:1.5}]}));assert.throws(()=>plan({rows:[]}))
 assert.throws(()=>plan({model:{...model,concurrency:null}}));assert.throws(()=>plan({rows:Array.from({length:51},(_,id)=>({...rows[0],id}))}))
 const original={...rows[0]},snapshot=plan({rows:[original]});original.id=99;assert.equal(snapshot.entries[0].edit.keys.id,1)
})
test('only one matching key and valid returned version confirms update or delete',()=>{
 const p=plan(),e=p.entries[0]
 assert.equal(bulkOutcome(p,e,{status:200,rows:[{...rows[0],version:2}]}),'confirmed')
 assert.equal(bulkOutcome(p,e,{status:200,rows:[]}),'conflict')
 for(const response of [{status:200,rows:[{...rows[0],version:2}],rollback:true},{status:200,rows:[rows[0]]},{status:200,rows:[{...rows[0],id:2,version:3}]},{status:200,rows:[rows[0],rows[0]]},{status:204},{status:500},{status:403},{status:408,code:'42501'}])assert.equal(bulkOutcome(p,e,response),'uncertain')
 assert.equal(bulkOutcome(p,e,{status:403,code:'42501'}),'refused')
 const d=plan({operation:'delete'});assert.equal(bulkOutcome(d,d.entries[0],{status:200,rows:[rows[0]]}),'confirmed')
})
test('independent writes retain outcomes and retry only refused or unsent original snapshots',async()=>{
 const p=plan();let calls=0
 const result=await runBulkWrite(p,{send:async e=>++calls===1?{status:403,code:'42501'}:{status:200,rows:[{...e.edit.keys,version:e.edit.version+1}]}})
 assert.deepEqual(result.map(row=>row.outcome),['refused','confirmed']);assert.deepEqual(retryBulkEntries(p,result),[p.entries[0]])
 const conflict=await runBulkWrite(p,{send:async()=>({status:200,rows:[]})});assert.equal(retryBulkEntries(p,conflict).length,0)
 const unknown=await runBulkWrite(p,{send:async()=>{throw new Error('private response')}});assert.deepEqual(unknown.map(row=>row.outcome),['uncertain','not_sent']);assert.deepEqual(retryBulkEntries(p,unknown),[p.entries[1]]);assert.ok(!JSON.stringify(unknown).includes('private'))
})
test('cancellation and changed contexts stop subsequent writes without automatic retries',async()=>{
 const p=plan(),controller=new AbortController();let calls=0
 const result=await runBulkWrite(p,{signal:controller.signal,send:async e=>{calls++;controller.abort();return {status:200,rows:[{...e.edit.keys,version:e.edit.version+1}]}}})
 assert.equal(calls,1);assert.deepEqual(result.map(row=>row.outcome),['confirmed','not_sent'])
 const changed=await runBulkWrite(p,{current:()=>false,send:async()=>assert.fail('unexpected write')});assert.ok(changed.every(row=>row.outcome==='not_sent'))
 calls=0;const mismatch=await runBulkWrite(p,{send:async()=>{calls++;return {status:409,code:'schema_contract_mismatch'}}});assert.equal(calls,1);assert.deepEqual(mismatch.map(row=>row.outcome),['refused','not_sent'])
})
const {matchingQuery,collectMatchingRows}=await import('../../cmd/gregale/templates/data-api/bulk-write.mjs')
const table={schema:'api',name:'notes',columns:['subject','id','version','body'].map(name=>({name,type:name==='id'||name==='version'?'number':'string'})),primary_key:{columns:['subject','id']}}
const matching=extra=>matchingQuery({table,model,operation:'update',changes:{body:'new'},fingerprint:'a'.repeat(64),filter:{column:'body',operator:'eq',value:'old'},scope:{schema:'api',table:'notes',predicates:[['subject','eq.alice']]},rowLimit:1000,...extra})
test('matching queries project only keys, versions and changed fields and retain ordered view scope',()=>{
 const query=matching();assert.deepEqual(query.columns,['subject','id','version','body']);assert.deepEqual(query.order,[['subject','asc'],['id','asc']]);assert.deepEqual(query.predicates,[['subject','eq.alice']]);assert.equal(query.filter.value,'old')
 assert.deepEqual(matching({operation:'delete',changes:{}}).columns,['subject','id','version'])
 for(const rowLimit of [0,1001,1.5])assert.throws(()=>matching({rowLimit}))
 assert.throws(()=>matching({changes:{id:99}}));assert.throws(()=>matching({model:{...model,concurrency:null}}))
})
test('matching collection goes beyond visible pages and refuses incomplete row-limited previews',async()=>{
 const source=Array.from({length:105},(_,id)=>({...rows[0],id})),calls=[]
 const fetched=await collectMatchingRows(matching({rowLimit:105}),{fetchPage:async(offset,limit)=>{calls.push([offset,limit]);return source.slice(offset,offset+Math.min(limit,25))}})
 assert.equal(fetched.length,105);assert.ok(calls.length>4)
 const reviewed=plan({rows:fetched,source:'matching'});assert.equal(reviewed.entries.length,105)
 const extra={...rows[0],id:106};await assert.rejects(collectMatchingRows(matching({rowLimit:105}),{fetchPage:async(offset,limit)=>[...source,extra].slice(offset,offset+limit)}),/More rows match/)
 assert.throws(()=>plan({rows:fetched}));assert.throws(()=>plan({rows:Array.from({length:1001},(_,id)=>({...rows[0],id})),source:'matching'}))
})
test('matching collection fails closed on context changes, cancelled reads and malformed or duplicate rows',async()=>{
 let current=true;await assert.rejects(collectMatchingRows(matching(),{current:()=>current,fetchPage:async()=>{current=false;return [rows[0]]}}),/View changed/)
 const controller=new AbortController();await assert.rejects(collectMatchingRows(matching(),{signal:controller.signal,fetchPage:async()=>{controller.abort();return [rows[0]]}}))
 await assert.rejects(collectMatchingRows(matching(),{fetchPage:async()=>[]}),/No rows match/)
 await assert.rejects(collectMatchingRows(matching(),{fetchPage:async()=>[{id:1}]}),/Unexpected/)
 assert.throws(()=>plan({rows:[rows[0],rows[0]],source:'matching'}),/each key once/)
 await assert.rejects(collectMatchingRows(matching({rowLimit:1}),{fetchPage:async()=>[{...rows[0],body:'x'.repeat(5242880)}]}),/5 MiB/)
})
const {bulkReport}=await import('../../cmd/gregale/templates/data-api/bulk-write.mjs')
const reportMetadata={runId:'11111111-1111-4111-8111-111111111111',startedAt:'2026-10-13T12:00:00.000Z',finishedAt:'2026-10-13T12:00:01.000Z'}
test('reports enumerate every outcome and reconciliation index without serializing private plan or response data',()=>{
 const p=plan({source:'matching',rows:Array.from({length:5},(_,id)=>({...rows[0],id,subject:'private-subject',body:'private-before'})),changes:{body:'private'}})
 const outcomes=['confirmed','refused','conflict','uncertain','not_sent']
 const results=outcomes.map((outcome,index)=>({index:index+1,outcome,status:index===4?undefined:200,body:'private-response',token:'private-token',keys:{secret:'private-key'}}))
 const report=bulkReport({...p,context:'private-context',token:'private-token'},results,{...reportMetadata,termination:'stopped'})
 assert.equal(report.total,5);assert.deepEqual(report.summary,Object.fromEntries(outcomes.map(outcome=>[outcome,1])))
 assert.deepEqual(report.reconciliation,{conflicts:[3],uncertain:[4]});assert.deepEqual(report.retryable,[2,5]);assert.equal(report.identity,'alice');assert.equal(report.fingerprint,'a'.repeat(64))
 assert.equal(report.started_at,reportMetadata.startedAt);assert.equal(report.termination,'stopped');assert.ok(!JSON.stringify(report).includes('private'))
 assert.deepEqual(Object.keys(report.rows[0]),['index','outcome','status'])
 results[0].outcome='uncertain';assert.equal(report.rows[0].outcome,'confirmed')
})
test('retry reports retain an immediate parent run and original row indexes without adding row values',()=>{
 const p={...plan(),retryOf:{runId:'22222222-2222-4222-8222-222222222222',indexes:[7,9]}}
 const report=bulkReport(p,[{index:1,outcome:'confirmed',status:200},{index:2,outcome:'not_sent'}],{...reportMetadata,termination:'cancelled'})
 assert.equal(report.retry_of,p.retryOf.runId);assert.deepEqual(report.rows.map(row=>row.source_index),[7,9]);assert.deepEqual(report.retryable,[2])
 assert.deepEqual(report.reconciliation,{conflicts:[],uncertain:[]})
 for(const retryOf of [{runId:'bad',indexes:[7,9]},{runId:p.retryOf.runId,indexes:[7,7]},{runId:p.retryOf.runId,indexes:[7]},{runId:p.retryOf.runId,indexes:[0,9]}])assert.throws(()=>bulkReport({...p,retryOf},report.rows,reportMetadata))
})
test('reports reject incomplete or malformed outcomes and invalid run metadata',()=>{
 const p=plan(),results=[{index:1,outcome:'confirmed'},{index:2,outcome:'conflict'}]
 for(const candidate of [[results[0]],[results[1],results[1]],[{index:1,outcome:'unknown'},results[1]]])assert.throws(()=>bulkReport(p,candidate,reportMetadata))
 for(const metadata of [{runId:'bad'},{startedAt:'invalid'},{finishedAt:'2026-10-12T00:00:00Z'},{termination:'private-message'}])assert.throws(()=>bulkReport(p,results,{...reportMetadata,...metadata}))
 const report=bulkReport(p,[{index:1,outcome:'uncertain',status:999},{index:2,outcome:'not_sent'}],reportMetadata);assert.equal(Object.hasOwn(report.rows[0],'status'),false)
})
const {preflightTarget,preflightModel,runBulkPreflight}=await import('../../cmd/gregale/templates/data-api/bulk-write.mjs')
test('preflight checks current keys without a version predicate and validates pinned metadata',()=>{
 const p=plan(),url=preflightTarget(p,p.entries[0],'http://localhost')
 assert.equal(url.searchParams.get('version'),null);assert.equal(url.searchParams.get('subject'),'eq.alice');assert.equal(url.searchParams.get('limit'),'2');assert.ok(url.pathname.includes(p.fingerprint))
 assert.equal(preflightModel(p,model,p.fingerprint),true)
 assert.equal(preflightModel(p,model,'b'.repeat(64)),false)
 assert.equal(preflightModel(p,{...model,updateFields:[]},p.fingerprint),false)
 assert.equal(preflightModel(p,{...model,concurrency:{kind:'integer_version',column:'other'}},p.fingerprint),false)
})
test('preflight reports current, changed and unavailable rows without writes or row contents',async()=>{
 const p=plan();let calls=0
 const good=await runBulkPreflight(p,{read:async e=>{calls++;return {status:200,rows:[{...e.edit.keys,version:e.edit.version}]}}})
 assert.equal(good.ready,true);assert.equal(good.summary.ready,2);assert.equal(calls,2)
 const changed=await runBulkPreflight(p,{read:async e=>({status:200,rows:e.index===1?[{...e.edit.keys,version:e.edit.version+1,body:'private-data'}]:[]})})
 assert.equal(changed.ready,false);assert.equal(changed.summary.changed,1);assert.equal(changed.summary.unavailable,1);assert.ok(!JSON.stringify(changed).includes('private-data'));assert.ok(!JSON.stringify(changed).includes('alice'))
})
test('preflight fails closed on malformed responses, refused reads, cancellation and context changes',async()=>{
 const p=plan()
 for(const response of [{status:409},{status:200,rows:[{id:1}]},{status:200,rows:rows},{status:200,rows:[{...rows[0],subject:'wrong'}]},null]){
  let calls=0;const result=await runBulkPreflight(p,{read:async()=>{calls++;return response}});assert.equal(result.ready,false);assert.equal(calls,1);assert.equal(result.summary.unchecked,1)
 }
 const controller=new AbortController();const cancelled=await runBulkPreflight(p,{signal:controller.signal,read:async()=>{controller.abort();return {status:200,rows:[rows[0]]}}});assert.equal(cancelled.ready,false);assert.equal(cancelled.summary.unchecked,2)
 const context=await runBulkPreflight(p,{current:()=>false,read:async()=>assert.fail('unexpected read')});assert.equal(context.ready,false)
})
const {reconciliationEntries,reconciliationTarget,reconcileBulk}=await import('../../cmd/gregale/templates/data-api/bulk-write.mjs')
test('reconciliation reads only conflicting and uncertain keys without silently retrying them',async()=>{
 const p=plan(),outcomes=[{index:1,outcome:'conflict'},{index:2,outcome:'uncertain'}],reads=[]
 const url=reconciliationTarget(p,p.entries[0],'http://localhost');assert.equal(url.searchParams.get('version'),null);assert.equal(url.searchParams.get('select'),'"subject","id","version","body"')
 const result=await reconcileBulk(p,outcomes,{read:async e=>{reads.push(e.index);return {status:200,rows:[{...e.edit.keys,version:e.edit.version+1,body:e.index===1?'different':'new'}]}}})
 assert.deepEqual(reads,[1,2]);assert.deepEqual(result.rows.map(row=>row.state),['changed','potentially_applied']);assert.equal(result.rows[1].outcome,'uncertain');assert.equal(outcomes[1].outcome,'uncertain')
 assert.equal(retryBulkEntries(p,outcomes).length,0);assert.equal(reconciliationEntries(p,[{index:1,outcome:'confirmed'},{index:2,outcome:'refused'}]).length,0)
})
test('delete absence and unchanged current rows do not resolve an uncertain operation',async()=>{
 const p=plan({operation:'delete'}),outcomes=[{index:1,outcome:'uncertain'},{index:2,outcome:'uncertain'}]
 const result=await reconcileBulk(p,outcomes,{read:async e=>({status:200,rows:e.index===1?[]:[rows[1]]})})
 assert.deepEqual(result.rows.map(row=>row.state),['unavailable','current']);assert.ok(result.rows.every(row=>row.outcome==='uncertain'));assert.equal(Object.hasOwn(result.rows[0],'record'),false)
})
test('reconciliation compares structured values without depending on object property order',async()=>{
 const p=plan({changes:{body:'new'}})
 const structured={...p,changes:{details:{a:1,b:[2,null]}}},outcomes=[{index:1,outcome:'uncertain'},{index:2,outcome:'conflict'}]
 const result=await reconcileBulk(structured,outcomes,{read:async e=>({status:200,rows:[{...e.edit.keys,version:e.edit.version+1,details:{b:[2,null],a:1}}]})})
 assert.ok(result.rows.every(row=>row.state==='potentially_applied'))
})
test('reconciliation stops on unsafe reads, context changes, cancellation and response bounds',async()=>{
 const p=plan(),outcomes=[{index:1,outcome:'uncertain'},{index:2,outcome:'conflict'}]
 for(const response of [{status:403},{status:200,rows:[{...rows[0],subject:'wrong'}]},{status:200,rows:[{...rows[0],version:1.5}]},null]){
  let calls=0;const result=await reconcileBulk(p,outcomes,{read:async()=>{calls++;return response}});assert.equal(calls,1);assert.equal(result.rows[1].state,'unchecked');assert.equal(Object.hasOwn(result.rows[0],'record'),false)
 }
 const controller=new AbortController(),cancelled=await reconcileBulk(p,outcomes,{signal:controller.signal,read:async()=>{controller.abort();return {status:200,rows:[rows[0]]}}});assert.equal(cancelled.complete,false);assert.ok(cancelled.rows.every(row=>row.state==='unchecked'))
 const changed=await reconcileBulk(p,outcomes,{current:()=>false,read:async()=>assert.fail('unexpected read')});assert.equal(changed.complete,false)
 const huge=await reconcileBulk(p,outcomes,{read:async()=>({status:200,rows:[{...rows[0],body:'x'.repeat(5242880)}]})});assert.equal(huge.rows[0].state,'read_failed');assert.equal(Object.hasOwn(huge.rows[0],'record'),false)
})
test('new reconciled-operation reports link original indexes without changing old outcomes or leaking data',()=>{
 const p={...plan(),followupOf:{runId:reportMetadata.runId,indexes:[5,7]}},outcomes=[{index:1,outcome:'confirmed'},{index:2,outcome:'conflict'}]
 const report=bulkReport(p,outcomes,{...reportMetadata,runId:'33333333-3333-4333-8333-333333333333'})
 assert.equal(report.reconciliation_of,reportMetadata.runId);assert.equal(Object.hasOwn(report,'retry_of'),false);assert.deepEqual(report.rows.map(row=>row.source_index),[5,7]);assert.ok(!JSON.stringify(report).includes('old2'))
 assert.throws(()=>bulkReport({...p,retryOf:p.followupOf},outcomes,reportMetadata))
})
