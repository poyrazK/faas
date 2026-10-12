import test from 'node:test'
import assert from 'node:assert/strict'
import { bulkMutations, createDataClient } from '../dist/index.js'
const sleep = ms => new Promise(resolve => setTimeout(resolve,ms))
function client(fetch) { return createDataClient({url:'https://data.example',accessToken:'jwt',fetch}).schema('api') }
test('bounded windows preserve input order with partial failures and progress', async () => {
  let active=0,maximum=0
  const started=[],completed=[],progress=[]
  const db=client(async (_url,init) => {
    const {body}=JSON.parse(init.body); started.push(body); maximum=Math.max(maximum,++active)
    await sleep(body===0?20:5); active--;completed.push(body)
    if(body===1) return new Response('{"code":"23514"}',{status:400})
    if(body===2) return new Response('[]')
    return new Response(JSON.stringify([{id:body}]))
  })
  const result=await bulkMutations([0,1,2,3,4],body=>db.from('notes').update({body}).eq('id',body).select('id'),{batchSize:3,concurrency:2,onProgress:p=>{assert.ok(Object.isFrozen(p));progress.push(p)}})
  assert.equal(maximum,2)
  assert.ok(completed.slice(0,3).every(id=>id<3),'next window waits for all active requests')
  assert.deepEqual(result.map(r=>r.index),[0,1,2,3,4])
  assert.deepEqual(result.map(r=>r.outcome),['applied','error','conflict','applied','applied'])
  assert.equal(result[1].error.code,'23514');assert.equal(progress.at(-1).completed,5)
  assert.deepEqual(Object.keys(progress.at(-1)).sort(),['applied','completed','conflicts','errors','invalid','skipped','total','uncertain'])
})
test('cancellation aborts active work, skips queued work and never retries', async () => {
  const controller=new AbortController();let calls=0
  const db=client(async (_url,init)=>{calls++;return new Promise((_resolve,reject)=>{init.signal.addEventListener('abort',()=>reject(init.signal.reason),{once:true});setTimeout(()=>controller.abort(),5)})})
  const result=await bulkMutations([1,2,3,4],id=>db.from('notes').delete().eq('id',id).select('id'),{concurrency:2,batchSize:3,signal:controller.signal})
  assert.equal(calls,2);assert.deepEqual(result.map(r=>r.outcome),['uncertain','uncertain','skipped','skipped'])
})
test('deadlines, thrown transport errors and gateway failures remain uncertain', async () => {
  let calls=0
  const db=client(async (_url,init)=>{calls++;return new Promise((_resolve,reject)=>init.signal.addEventListener('abort',()=>reject(init.signal.reason),{once:true}))})
  const timed=await bulkMutations([1],()=>db.from('notes').insert({body:'x'}).select(),{timeoutMs:10})
  assert.equal(timed[0].outcome,'uncertain');assert.equal(timed[0].error.code,'request_timeout');assert.equal(calls,1)
  const failed=client(async()=>new Response('{"code":"unavailable"}',{status:503}))
  const gateway=await bulkMutations([1],()=>failed.from('notes').insert({body:'x'}).select())
  assert.equal(gateway[0].outcome,'uncertain')
  const thrown=client(async()=>{throw Error('lost')})
  assert.equal((await bulkMutations([1],()=>thrown.from('notes').insert({body:'x'}).select().throwOnError()))[0].outcome,'uncertain')
})
test('invalid options and pre-cancellation do not create queries; observers cannot interrupt', async () => {
  let built=0
  const db=client(async()=>new Response('[{"id":1}]'))
  const build=()=>{built++;return db.from('notes').insert({body:'x'}).select('id')}
  for (const options of [{batchSize:0},{concurrency:1.5},{timeoutMs:0}]) await assert.rejects(bulkMutations([1],build,options))
  assert.equal((await bulkMutations([1],build,{signal:AbortSignal.abort()}))[0].outcome,'skipped');assert.equal(built,0)
  assert.equal((await bulkMutations([1,2],build,{onProgress:async()=>{throw Error('observer')}})).length,2)
  assert.deepEqual(await bulkMutations([],build),[])
  assert.equal((await bulkMutations([1],()=>{throw Error('invalid input')}))[0].outcome,'invalid')
})
