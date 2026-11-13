import test from 'node:test'
import assert from 'node:assert/strict'
import { teamsClient } from '../dist/orders.js'
import { backgroundDataJobs,snapshotCsv } from '../dist/data-jobs.js'
const id='10000000-0000-4000-8000-000000000001'
test('a mismatched enqueue acknowledgement cannot confirm the original operation',async()=>{
  const client=teamsClient({url:'https://data.example',accessToken:'token',fetch:async()=>Response.json([{id:'20000000-0000-4000-8000-000000000001',tenant_id:'a',actor:'alice',kind:'export',state:'queued',processed:0,total:0}])},'a')
  await assert.rejects(backgroundDataJobs(client).queue('export',id),/original operation/)
})
test('snapshot CSV validates tenant scope and escapes spreadsheet formulas',()=>{
  const job={tenant_id:'=tenant',kind:'export',state:'completed',result:{rows:[{id,tenant_id:'=tenant',created_by:'=actor',total_cents:1,status:'draft',version:1}]}}
  assert.match(snapshotCsv(job),/"'=tenant","'=actor"/)
  assert.throws(()=>snapshotCsv({...job,tenant_id:'other'}),/scope/)
  assert.throws(()=>snapshotCsv({...job,state:'running'}),/unavailable/)
})

test('DAPI003 quarantined jobs retain diagnostics and resume under the current user',async()=>{
 const calls=[]
 const job={id,tenant_id:'a',actor:'alice',kind:'import',state:'blocked',processed:20,total:25,attempts:8,next_attempt_at:new Date().toISOString(),last_error_code:'request_uncertain'}
 const client=teamsClient({url:'https://data.example',accessToken:'token',fetch:async(url,init)=>{calls.push({url:String(url),body:init.body});return Response.json([{...job,...(String(url).includes('resume_team_order_job')?{state:'queued'}:{})}])}},'a')
 assert.equal((await backgroundDataJobs(client).list())[0].state,'blocked')
 assert.equal((await backgroundDataJobs(client).resume(id)).state,'queued')
 assert.deepEqual(JSON.parse(calls[1].body),{job_id:id})
})
