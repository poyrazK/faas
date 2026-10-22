import test from 'node:test'
import assert from 'node:assert/strict'
import {parseImportCsv,previewCsvImport,csvBatches,runCsvImport,retryableImportRows,importOutcome,csvLimits} from '../../cmd/gregale/templates/data-api/csv-import.mjs'
const field=(name,value,extra={})=>({name,value,optional:false,nullable:false,...extra})
const model={fields:['subject','body','priority','active','status','details','labels'].map(name=>({name})),spec:{kind:'object',fields:[field('subject',{kind:'string'}),field('body',{kind:'string',maxLength:3}),field('priority',{kind:'number',integer:true,min:0,max:5},{optional:true}),field('active',{kind:'boolean'},{optional:true}),field('status',{kind:'enum',labels:['open','closed']},{optional:true}),field('details',{kind:'Json'},{optional:true,nullable:true}),field('labels',{kind:'array',value:{kind:'string'}},{optional:true})]}}
const map=headers=>headers.map(header=>({field:model.fields.some(field=>field.name===header)?header:'',empty:'value'}))
const entries=count=>Array.from({length:count},(_,index)=>Object.freeze({record:index+2,json:JSON.stringify({subject:'alice',body:String(index)})}))
test('CSV parsing preserves quoted commas, multiline fields, escaped quotes and Unicode headers',()=>{
 const data=parseImportCsv('\uFEFF subject ,body\r\nalice,"a,b"\r\nalice,"x\n""y"""\r\n')
 assert.deepEqual(data,{headers:['subject','body'],records:[['alice','a,b'],['alice','x\n"y"']]})
 for(const source of ['', 'body\n','a,a\nx,y','a,\nx,y','a\n"unterminated','a\nx"y','a\n"x"junk', 'a\n'+ 'x'.repeat(csvLimits.bytes)])assert.throws(()=>parseImportCsv(source))
 assert.throws(()=>parseImportCsv('a\n'+Array.from({length:1001},()=> 'x').join('\n')))
})
test('schema mapping handles typed cells, defaults, NULL, ignored readonly fields and Unicode length',()=>{
 const data=parseImportCsv('subject,body,priority,active,status,details,labels,id\nalice,😀😀😀,2,TRUE,open,"{""ok"":true}","[""x""]",999')
 const preview=previewCsvImport(data,map(data.headers),model);assert.equal(preview.ok,true)
 assert.deepEqual(JSON.parse(preview.entries[0].json),{subject:'alice',body:'😀😀😀',priority:2,active:true,status:'open',details:{ok:true},labels:['x']});assert.equal(preview.entries[0].record,2)
 const empty=parseImportCsv('subject,body,priority,details\nalice,,,');const mapping=map(empty.headers);mapping[2].empty='omit';mapping[3].empty='null'
 assert.deepEqual(JSON.parse(previewCsvImport(empty,mapping,model).entries[0].json),{subject:'alice',body:'',details:null})
 const bad=map(data.headers);bad[7].field='id';assert.throws(()=>previewCsvImport(data,bad,model))
 const duplicate=map(data.headers);duplicate[0].field='body';assert.throws(()=>previewCsvImport(data,duplicate,model))
})
test('any invalid record suppresses all payloads; errors retain record numbers and never input values',()=>{
 const data=parseImportCsv('subject,body,priority,active,status,details\nalice,private-secret,2,true,open,{}\nalice,ok,1.5,true,open,{}\nalice,ok,2,yes,open,{}\nalice,ok,2,true,unknown-private,{}\nalice,ok,2,true,open,bad-private\nalice,ok,2,true,open,{}')
 const result=previewCsvImport(data,map(data.headers),model);assert.equal(result.ok,false);assert.equal(result.invalid,5);assert.equal(result.valid,1);assert.equal(Object.hasOwn(result,'entries'),false)
 assert.deepEqual(result.errors.map(error=>error.record),[2,3,4,5,6]);assert.ok(!JSON.stringify(result).includes('private'))
 const short=parseImportCsv('subject,body\nalice');assert.equal(previewCsvImport(short,map(short.headers),model).errors[0].code,'column_count')
 const unbounded={fields:[{name:'subject'}],spec:{kind:'object',fields:[field('subject',{kind:'string'})]}}
 const huge=parseImportCsv('subject\n'+ 'x'.repeat(65536));assert.equal(previewCsvImport(huge,[{field:'subject',empty:'value'}],unbounded).errors[0].code,'row_too_large')
})
test('bounded batches classify confirmed, transaction-refused and uncertain outcomes conservatively',()=>{
 assert.deepEqual(csvBatches(entries(26)).map(batch=>batch.length),[25,1])
 const large=Array.from({length:3},(_,index)=>({record:index+2,json:JSON.stringify({body:'x'.repeat(40000)})}));assert.deepEqual(csvBatches(large).map(batch=>batch.length),[1,1,1])
 assert.equal(importOutcome({status:201,count:1},1),'confirmed');assert.equal(importOutcome({status:403,code:'42501'}),'refused');assert.equal(importOutcome({status:400,code:'23505'}),'refused')
 for(const response of [{status:201},{status:201,count:0},{status:400},{status:502},{status:408},{status:201,rollback:true,count:1},{status:202},null])assert.equal(importOutcome(response,1),'uncertain')
})
test('partial success reports every record and eligible retries exclude confirmed and uncertain rows',async()=>{
 const source=entries(26);let calls=0
 const result=await runCsvImport(source,{sendBatch:async body=>{assert.ok(new TextEncoder().encode(body).length<=65536);return ++calls===1?{status:403,code:'42501'}:{status:201,count:JSON.parse(body).length}}})
 assert.equal(calls,2);assert.equal(result.summary.refused,25);assert.equal(result.summary.confirmed,1)
 const retry=retryableImportRows(source,result.rows);assert.equal(retry.length,25);assert.ok(!retry.some(row=>row.record===27))
 const second=await runCsvImport(retry,{sendBatch:async body=>({status:201,count:JSON.parse(body).length})});assert.equal(second.summary.confirmed,25)
 const uncertain=await runCsvImport(source,{sendBatch:async()=>{throw new Error('private-network-error')}})
 assert.equal(uncertain.summary.uncertain,25);assert.equal(uncertain.summary.not_sent,1);assert.deepEqual(retryableImportRows(source,uncertain.rows).map(row=>row.record),[27]);assert.ok(!JSON.stringify(uncertain).includes('private'))
})
test('context changes, stop requests and schema mismatches prevent subsequent writes without retries',async()=>{
 const source=entries(26);let calls=0,current=true
 const stale=await runCsvImport(source,{current:()=>current,sendBatch:async()=>{calls++;current=false;return {status:201,count:25}}});assert.equal(calls,1);assert.equal(stale.summary.not_sent,1)
 const controller=new AbortController();calls=0
 const aborted=await runCsvImport(source,{signal:controller.signal,sendBatch:async()=>{calls++;controller.abort();throw new Error('aborted')}});assert.equal(calls,1);assert.equal(aborted.summary.uncertain,25);assert.equal(aborted.summary.not_sent,1)
 calls=0;const mismatch=await runCsvImport(source,{sendBatch:async()=>{calls++;return {status:409,code:'schema_contract_mismatch'}}});assert.equal(calls,1);assert.equal(mismatch.summary.refused,25);assert.equal(mismatch.summary.not_sent,1)
 const blocked=await runCsvImport(source,{current:()=>false,sendBatch:async()=>{assert.fail('stale approval sent a write')}});assert.equal(blocked.summary.not_sent,26)
})
test('trigger-suppressed insert counts cannot falsely confirm a batch or offer it for retry',async()=>{
 let calls=0
 const result=await runCsvImport(entries(26),{sendBatch:async()=>{calls++;return {status:201,count:24}}})
 assert.equal(calls,1);assert.equal(result.summary.uncertain,25);assert.equal(result.summary.not_sent,1);assert.equal(result.rows[0].acknowledged_batch_rows,24)
 assert.deepEqual(retryableImportRows(entries(26),result.rows).map(row=>row.record),[27])
})
