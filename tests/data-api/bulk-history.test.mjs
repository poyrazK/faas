import test from 'node:test'
import assert from 'node:assert/strict'
import {mkdtemp,readFile,rm,symlink,stat} from 'node:fs/promises'
import {join} from 'node:path'
import {tmpdir} from 'node:os'
import {emptyHistory,validateBulkHistory,validateBulkReport,retainBulkHistory,historyLimits} from '../../cmd/gregale/templates/data-api/bulk-history.mjs'
import {requestStore} from '../../cmd/gregale/templates/data-api/dev-requests.mjs'
const make=(n,rows=1)=>({version:1,run_id:`00000000-0000-4000-8000-${String(n).padStart(12,'0')}`,operation:'update',source:rows>50?'matching':'selected',schema:'api',table:'notes',identity:'alice',fingerprint:'a'.repeat(64),started_at:'2026-10-13T12:00:00.000Z',finished_at:`2026-10-13T12:00:${String(n%60).padStart(2,'0')}.000Z`,termination:'completed',total:rows,summary:{confirmed:rows,refused:0,conflict:0,uncertain:0,not_sent:0},rows:Array.from({length:rows},(_,i)=>({index:i+1,outcome:'confirmed',status:200})),reconciliation:{conflicts:[],uncertain:[]},retryable:[]})
test('history strictly validates redacted report schemas and rejects payloads, keys and credentials',()=>{
 const report=make(1);assert.equal(validateBulkReport(report),report)
 for(const bad of [{...report,token:'secret'},{...report,keys:{id:1}},{...report,rows:[{...report.rows[0],body:'private'}]},{...report,summary:{...report.summary,confirmed:0}},{...report,retryable:[1]},{...report,finished_at:'invalid'},{...report,retry_of:''},{...report,reconciliation_of:null},{...report,total:0}])assert.throws(()=>validateBulkReport(bad))
 assert.throws(()=>validateBulkHistory({...emptyHistory(),reports:[report,report]}));assert.throws(()=>validateBulkHistory({...emptyHistory(),retention:{runs:21,days:30}}));assert.throws(()=>validateBulkHistory({...emptyHistory(),data:'private'}))
})
test('bounded retention prunes old runs and age-expired reports and deduplicates appends',()=>{
 const now=Date.parse('2026-10-14T00:00:00Z'),value={...emptyHistory(),retention:{runs:2,days:1},reports:[make(1),make(2)]}
 const retained=retainBulkHistory(value,make(3),now);assert.deepEqual(retained.reports.map(r=>r.run_id),[make(3).run_id,make(2).run_id])
 assert.equal(retainBulkHistory(retained,make(3),now).reports.length,2)
 assert.equal(retainBulkHistory(retained,null,now+86400000).reports.length,0)
 const large=n=>({...make(n,1000),retry_of:make(999).run_id,rows:make(n,1000).rows.map(row=>({...row,source_index:row.index}))})
 const big={...emptyHistory(),reports:Array.from({length:20},(_,i)=>large(i+1))};assert.throws(()=>validateBulkHistory(big))
 let current=emptyHistory();for(let i=1;i<=20;i++)current=retainBulkHistory(current,large(i),now)
 assert.ok(current.reports.length<20);assert.ok(new TextEncoder().encode(JSON.stringify(current,null,2)+'\n').length<=historyLimits.bytes)
})
test('project history preserves large complete reports, private file modes and revision conflicts',async()=>{
 const directory=await mkdtemp(join(tmpdir(),'gregale-history-'));try{
  const path=join(directory,'data-api.bulk-history.json'),store=requestStore(path,{validate:validateBulkHistory,empty:emptyHistory(),maxBytes:historyLimits.bytes}),initial=await store.read()
  const value={...emptyHistory(),reports:[make(1,1000)]},saved=await store.save(value,initial.revision)
  assert.equal(saved.reports[0].rows.length,1000);assert.ok((await readFile(path)).length>65536);assert.equal((await stat(path)).mode&0o777,0o600)
  await assert.rejects(store.save(emptyHistory(),initial.revision),error=>error.status===409)
  const reloaded=await requestStore(path,{validate:validateBulkHistory,empty:emptyHistory(),maxBytes:historyLimits.bytes}).read();assert.equal(reloaded.reports[0].run_id,make(1).run_id)
  const link=join(directory,'linked.json');await symlink(path,link);await assert.rejects(requestStore(link,{validate:validateBulkHistory,empty:emptyHistory(),maxBytes:historyLimits.bytes}).read())
  const cleared=await store.save(emptyHistory(),saved.revision);assert.equal(cleared.reports.length,0)
 }finally{await rm(directory,{recursive:true,force:true})}
})
