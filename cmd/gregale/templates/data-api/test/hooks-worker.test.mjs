import test from 'node:test'
import assert from 'node:assert/strict'
import { pumpHooks } from '../hooks-worker.mjs'
const key='gdsk_'+'A'.repeat(43)
test('the optional hook pump drains bounded batches and stops when a lease or retry prevents progress',async()=>{
  const pending=[10,3,3],calls=[]
  const status=await pumpHooks({url:'https://data.example',key,log:()=>{},fetchImpl:async(url,init)=>{
    calls.push({url:String(url),init})
    return Response.json({version:1,pending:pending.shift(),failed:0,leased:0,lag_seconds:5})
  }})
  assert.equal(status.pending,3);assert.equal(calls.length,3)
  assert.ok(calls.every(call=>call.url==='https://data.example/__gregale/hooks/drain'&&call.init.redirect==='error'&&call.init.credentials==='omit'))
})
test('hook worker errors never echo response bodies or credentials',async()=>{
  await assert.rejects(pumpHooks({url:'https://data.example',key,log:()=>{},fetchImpl:async()=>new Response('private receiver detail',{status:503})}),error=>error.message==='Hooks pump refused (503)')
  await assert.rejects(pumpHooks({url:'http://remote.example',key,fetchImpl:async()=>{throw new Error('must not fetch')}}),/Invalid hooks worker/)
})
