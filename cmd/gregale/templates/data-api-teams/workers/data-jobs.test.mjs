import test from 'node:test'
import assert from 'node:assert/strict'
import { runDataJobs } from './data-jobs.mjs'
const key='gdsk_'+'A'.repeat(43),job={id:'10000000-0000-4000-8000-000000000001',actor:'alice',lease_token:'20000000-0000-4000-8000-000000000001',state:'running',processed:0,total:20}
test('DAPI003 uncertain batches schedule recovery and allow newer jobs to advance',async()=>{
 const calls=[],newer={...job,id:'10000000-0000-4000-8000-000000000002'}
 let claims=0
 const result=await runDataJobs({url:'https://data.example',key,log:()=>{},fetchImpl:async(url,init)=>{
  calls.push({url:String(url),headers:init.headers,args:JSON.parse(init.body)})
  if(String(url).endsWith('claim_team_order_job'))return Response.json(++claims===1?[job]:claims===2?[newer]:[])
  if(String(url).endsWith('retry_team_order_job'))return Response.json([{...job,state:'queued',attempts:1}])
  if(JSON.parse(init.body).job_id===job.id)return Response.json({code:'unavailable'},{status:503})
  return Response.json([{...newer,state:'completed',processed:20}])
 }})
 assert.deepEqual(result,{completed:1,failed:0})
 assert.equal(calls[0].headers['x-gregale-subject'],undefined)
 assert.equal(calls[1].headers['x-gregale-subject'],'alice')
 assert.equal(calls[2].headers['x-gregale-subject'],undefined)
 assert.deepEqual(calls[2].args,{job_id:job.id,claim_token:job.lease_token,failure_code:'unavailable'})
 assert.ok(!calls.some(call=>call.url.endsWith('fail_team_order_job')))
})
test('DAPI003 a lost completed-batch response reconciles its completed receipt',async()=>{
 let claims=0
 const result=await runDataJobs({url:'https://data.example',key,log:()=>{},fetchImpl:async(url)=>{
  if(String(url).endsWith('claim_team_order_job'))return Response.json(++claims===1?[job]:[])
  if(String(url).endsWith('retry_team_order_job'))return Response.json([{...job,state:'completed',processed:20}])
  throw new TypeError('response lost after commit')
 }})
 assert.deepEqual(result,{completed:1,failed:0})
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
