import test from 'node:test'
import assert from 'node:assert/strict'
import { mkdtemp, writeFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { pathToFileURL } from 'node:url'
const runtime = process.env.DATA_API_RUNTIME_DIR
const { runHostedReplay, prepareHostedReplay } = await import(runtime ? pathToFileURL(join(runtime, 'hosted-replay.mjs')) : '../../cmd/gregale/templates/data-api/hosted-replay.mjs')
const token = subject => 'a.' + Buffer.from(JSON.stringify({ sub: subject })).toString('base64url') + '.signature'
const fp = 'f'.repeat(64), refresh = 'b'.repeat(64)
async function fixture(t) {
  const root = await mkdtemp(join(tmpdir(), 'gregale-hosted-replay-')); t.after(() => rm(root,{recursive:true,force:true}))
  const collection = join(root,'requests.json')
  const value = {version:2,scenarios:[{name:'isolation',requests:[
    {name:'Alice can read',method:'GET',path:'/rest/v1/notes',identity:'alice',expect:{status:200,json:[{id:1}]}},
    {name:'Bob cannot read Alice',method:'GET',path:'/rest/v1/notes',identity:'bob',expect:{status:200,json:[]}},
  ]}]}
  await writeFile(collection,JSON.stringify(value))
  return {options:{collection,identities:{alice:'ALICE_JWT',bob:'BOB_JWT'},scenarios:['isolation'],url:'https://hosted.example/',fingerprint:fp,require_paused:true},env:{ALICE_JWT:token('alice'),BOB_JWT:token('bob'),GREGALE_DATA_API_REFRESH_TOKEN:refresh},value,collection}
}
function transport(calls, mutate = () => {}) {
  return async (target,options) => {
    calls.push({target:String(target),options})
    assert.equal(options.redirect,'error')
    const path = new URL(target).pathname
    let response
    if (path === '/openapi.json') response = new Response('{}',{status:401})
    else if (path === '/__gregale/schema') response = Response.json({version:1,ready:true,fingerprint:fp,refresh_protocol:1,refresh_paused:true,refresh_verified:true,refresh_fingerprint:fp})
    else response = Response.json(options.headers.Authorization === `Bearer ${token('alice')}` ? [{id:1}] : [])
    return mutate(target,options,response) ?? response
  }
}
test('hosted replay pins the candidate, forwards identity/refresh tokens and emits only assertion metadata',async t => {
  const {options,env} = await fixture(t), calls=[],events=[]
  const summary = await runHostedReplay(options,env,{fetcher:transport(calls),emit:event=>events.push(event)})
  assert.equal(summary.total,6); assert.equal(summary.failed,0)
  const data = calls.filter(call=>call.target.includes('/rest/v1/'))
  assert.equal(data.length,2)
  for(const call of data) {assert.match(call.target,new RegExp(`/__gregale/contract/${fp}/rest/v1/`)); assert.equal(call.options.headers['X-Gregale-Refresh-Token'],refresh)}
  const output=JSON.stringify(events)
  for(const secret of [env.ALICE_JWT,env.BOB_JWT,refresh,'"id":1','"sub":']) assert.equal(output.includes(secret),false)
})
test('invalid selections, empty checks, duplicate subjects and contradictory fingerprints fail before any network request',async t => {
  const {options,env,value,collection} = await fixture(t)
  for(const patch of [{scenarios:['missing']},{scenarios:['isolation','isolation']},{identities:{alice:'ALICE_JWT',bob:'ALICE_JWT'}}]) await assert.rejects(prepareHostedReplay({...options,...patch},env))
  await assert.rejects(prepareHostedReplay(options,{...env,BOB_JWT:'owner-account-key'}))
  value.scenarios[0].requests[0].expectedSchemaFingerprint='c'.repeat(64)
  await writeFile(collection,JSON.stringify(value)); await assert.rejects(prepareHostedReplay(options,env),/candidate fingerprint/)
  value.scenarios[0].requests=[];await writeFile(collection,JSON.stringify(value));await assert.rejects(prepareHostedReplay(options,env),/two distinct subjects/)
})
test('unsafe origins and authentication or ownership failures prevent scenario writes',async t => {
  const {options,env,value,collection}=await fixture(t)
  value.scenarios[0].requests[0].method='POST';value.scenarios[0].requests[0].body={body:'test'}
  await writeFile(collection,JSON.stringify(value))
  for(const url of ['http://hosted.example/','https://user:password@hosted.example/','https://hosted.example/?token=secret','https://hosted.example/path']) await assert.rejects(runHostedReplay({...options,url},env))
  for(const failure of ['anonymous','invalid-identity','unowned','stale','bad-json','oversized']) {
    const calls=[]
    const fetcher=transport(calls,(target,request,response)=> {
      if(failure==='anonymous' && new URL(target).pathname==='/openapi.json') return new Response('{}',{status:200})
      if(new URL(target).pathname!=='/__gregale/schema') return response
      if(failure==='invalid-identity') return new Response('{}',{status:401})
      if(failure==='oversized') return new Response('x'.repeat(8193),{status:200})
      if(failure==='bad-json') return new Response('not json',{status:200})
      return Response.json({version:1,ready:true,fingerprint:failure==='stale'?'a'.repeat(64):fp,refresh_protocol:1,refresh_paused:true,refresh_verified:failure!=='unowned',refresh_fingerprint:fp})
    })
    await assert.rejects(runHostedReplay(options,env,{fetcher,emit:()=>{}}))
    assert.equal(calls.some(call=>call.options.method==='POST'),false)
  }
})
test('contract rejection fails even when the scenario expects HTTP 409; metadata names redact credentials',async t => {
  const {options,env,value,collection}=await fixture(t),events=[]
  value.scenarios[0].requests[0].name=refresh;value.scenarios[0].requests[0].expect={status:409}
  await writeFile(collection,JSON.stringify(value))
  await assert.rejects(runHostedReplay(options,env,{emit:event=>events.push(event),fetcher:transport([],target=>String(target).includes('/rest/v1/')?Response.json({error:{code:'schema_contract_mismatch'}},{status:409}):undefined)}))
  assert.equal(JSON.stringify(events).includes(refresh),false)
  assert.ok(events.some(event=>event.contract_mismatch===true))
})
test('captures remain scenario-scoped and transport failures and cancellation fail verification',async t => {
  const {options,env,value,collection}=await fixture(t),events=[]
  value.scenarios[0].requests[0].capture={note_id:'/0/id'}
  value.scenarios[0].requests[1].path='/rest/v1/notes?id=eq.{{note_id}}'
  await writeFile(collection,JSON.stringify(value))
  const calls=[]
  await runHostedReplay(options,env,{fetcher:transport(calls),emit:event=>events.push(event)})
  assert.ok(calls.some(call=>call.target.endsWith('id=eq.1')))
  assert.ok(events.some(event=>event.captured?.includes('note_id')))
  await assert.rejects(runHostedReplay(options,env,{fetcher:async()=>{throw new Error(env.ALICE_JWT)},emit:()=>{}}))
  const controller=new AbortController();controller.abort()
  await assert.rejects(runHostedReplay(options,env,{signal:controller.signal,fetcher:async()=>controller.signal.throwIfAborted(),emit:()=>{}}))
})
