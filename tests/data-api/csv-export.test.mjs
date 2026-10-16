import test from 'node:test'
import assert from 'node:assert/strict'
import {parseExactJSON,exactJSON,decodeJsonCell,exportRecord,exportHeader,csvExportPlan,exportTarget,runCsvExport} from '../../cmd/gregale/templates/data-api/csv-export.mjs'
import {parseImportCsv,previewCsvImport} from '../../cmd/gregale/templates/data-api/csv-import.mjs'
const table={schema:'api',name:'notes',columns:['id','subject','body'].map(name=>({name,type:'string'})),primary_key:{columns:['subject','id']}}
const plan=extra=>csvExportPlan({table,columns:['body'],fingerprint:'a'.repeat(64),mode:'json',...extra})
test('exact JSON preserves large integers and nested decimal lexemes without rounding',()=>{
 const source='{"big":9223372036854775807,"nested":[0.1234567890123456789,null,"null",""]}'
 assert.equal(exactJSON(parseExactJSON(source)),source)
 assert.throws(()=>decodeJsonCell('9223372036854775807'),/precision_loss/)
 assert.throws(()=>decodeJsonCell('0.1234567890123456789'),/precision_loss/)
 assert.deepEqual(decodeJsonCell('[1.00,1e2,null,true,"null",""]'),[1,100,null,true,'null',''])
 for(const bad of ['01','[1,]','{"x":1,}','NaN','"bad\n"','1 true'])assert.throws(()=>parseExactJSON(bad))
 assert.equal(Object.hasOwn(parseExactJSON('{"__proto__":null}'),'__proto__'),true)
})
test('spreadsheet protection and JSON cell encoding retain distinct NULL and string values',()=>{
 const row=parseExactJSON('{"subject":"alice","body":"=1+1","details":null,"n":-12}')
 assert.equal(exportRecord(row,['body','n'],'spreadsheet'),'"\'=1+1","-12"\r\n')
 assert.ok(exportHeader(['=formula'],'spreadsheet').includes("'=formula"))
 const fields=['subject','body','details'],source=exportHeader(fields,'json')+exportRecord(row,fields,'json')
 const data=parseImportCsv(source),model={fields:fields.map(name=>({name})),spec:{kind:'object',fields:fields.map(name=>({name,optional:false,nullable:name==='details',value:{kind:'string'}}))}}
 const result=previewCsvImport(data,fields.map(field=>({field,empty:'value'})),model,'json')
 assert.equal(result.ok,true);assert.deepEqual(JSON.parse(result.entries[0].json),{subject:'alice',body:'=1+1',details:null})
 assert.deepEqual(parseImportCsv(exportHeader(['body'],'json')+exportRecord({body:'a,"b"\n😀'},['body'],'json')).records,[[JSON.stringify('a,"b"\n😀')]])
})
test('plans retain selected columns, intersect filters with scope, and append primary-key sort ties',()=>{
 const p=plan({sort:'body',direction:'desc',filter:{column:'subject',operator:'eq',value:'bob'},scope:{schema:'api',table:'notes',predicates:[['subject','eq.alice']]}})
 const url=exportTarget(p,'http://localhost',100,100)
 assert.equal(url.searchParams.get('select'),'"body"');assert.deepEqual(url.searchParams.getAll('subject'),['eq.bob','eq.alice'])
 assert.equal(url.searchParams.get('order'),'"body".desc,"subject".asc,"id".asc')
 assert.throws(()=>plan({columns:['unknown']}));assert.throws(()=>plan({rowLimit:10001}));assert.throws(()=>plan({scope:{schema:'wrong',table:'notes',predicates:[['subject','eq.alice']]}}))
 assert.deepEqual(plan({table:{...table,primary_key:undefined}}).order,[['id','asc']])
})
test('exports cross pages and server caps, detect extra rows, and distinguish an exact row limit',async()=>{
 const rows=Array.from({length:205},(_,index)=>({body:String(index)})),calls=[]
 const result=await runCsvExport(plan({rowLimit:105}),{fetchPage:async(offset,limit)=>{calls.push([offset,limit]);return rows.slice(offset,offset+Math.min(limit,30))}})
 assert.equal(result.rows,105);assert.equal(result.limited,true);assert.equal(parseImportCsv(result.csv).records.length,105);assert.ok(calls.length>3)
 const exact=await runCsvExport(plan({rowLimit:205}),{fetchPage:async(offset,limit)=>rows.slice(offset,offset+limit)})
 assert.equal(exact.rows,205);assert.equal(exact.limited,false)
 const empty=await runCsvExport(plan(),{fetchPage:async()=>[]});assert.equal(empty.rows,0)
})
test('context changes, cancellation, malformed pages and byte limits never return partial artifacts',async()=>{
 let current=true;await assert.rejects(runCsvExport(plan(),{current:()=>current,fetchPage:async()=>{current=false;return [{body:'private'}]}}),/context changed/)
 const controller=new AbortController();await assert.rejects(runCsvExport(plan(),{signal:controller.signal,fetchPage:async()=>{controller.abort();return [{body:'private'}]}}))
 await assert.rejects(runCsvExport(plan(),{fetchPage:async()=>[{}]}),/omitted/)
 await assert.rejects(runCsvExport(plan(),{fetchPage:async()=>({body:'private'})}),/Unexpected/)
 await assert.rejects(runCsvExport(plan(),{fetchPage:async()=>Array.from({length:100},()=>({body:'x'.repeat(110000)}))}),/10 MiB/)
})
test('JSON-cell import distinguishes literal null and empty strings and refuses precision loss',()=>{
 const model={fields:[{name:'body'}],spec:{kind:'object',fields:[{name:'body',optional:false,nullable:true,value:{kind:'Json'}}]}}
 const mapping=[{field:'body',empty:'value'}]
 for(const value of [null,'null','',true,{nested:[1,null]}]){
  const data=parseImportCsv(exportHeader(['body'],'json')+exportRecord(parseExactJSON(JSON.stringify({body:value})),['body'],'json'))
  const result=previewCsvImport(data,mapping,model,'json');assert.equal(result.ok,true);assert.deepEqual(JSON.parse(result.entries[0].json).body,value)
 }
 const data=parseImportCsv(exportHeader(['body'],'json')+exportRecord(parseExactJSON('{"body":9223372036854775807}'),['body'],'json'))
 const result=previewCsvImport(data,mapping,model,'json');assert.equal(result.ok,false);assert.equal(result.errors[0].code,'precision_loss');assert.equal(Object.hasOwn(result,'entries'),false)
})
