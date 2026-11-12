import test from 'node:test'
import assert from 'node:assert/strict'
import { runDataJobs } from './data-jobs.mjs'
const key='gdsk_'+'A'.repeat(43),job={id:'10000000-0000-4000-8000-000000000001',actor:'alice',lease_token:'20000000-0000-4000-8000-000000000001',state:'running',processed:0,total:20}
test('an uncertain batch response leaves its lease recoverable without a failure mutation',async()=>{
  const calls=[]
  await assert.rejects(runDataJobs({url:'https://data.example',key,log:()=>{},fetchImpl:async(url,init)=>{
    calls.push({url:String(url),headers:init.headers})
    return String(url).endsWith('claim_team_order_job')?Response.json([job]):Response.json({code:'unavailable'},{status:503})
  }}))
  assert.equal(calls.length,2)
  assert.equal(calls[0].headers['x-gregale-subject'],undefined)
  assert.equal(calls[1].headers['x-gregale-subject'],'alice')
})
test('a definite rejection records a bounded error under the worker identity',async()=>{
  const calls=[]
  const result=await runDataJobs({url:'https://data.example',key,maxJobs:1,log:()=>{},fetchImpl:async(url,init)=>{
    calls.push({url:String(url),init})
    if(String(url).endsWith('claim_team_order_job'))return Response.json([job])
    if(String(url).endsWith('advance_team_order_job'))return Response.json({code:'42501',message:'private database detail'},{status:403})
    return new Response(null,{status:204})
  }})
  assert.deepEqual(result,{completed:0,failed:1})
  assert.equal(calls[2].init.headers['x-gregale-subject'],undefined)
  assert.deepEqual(JSON.parse(calls[2].init.body),{job_id:job.id,claim_token:job.lease_token,failure_code:'42501'})
})
