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
