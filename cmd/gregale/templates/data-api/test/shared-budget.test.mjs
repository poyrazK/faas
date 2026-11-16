import test from 'node:test'
import assert from 'node:assert/strict'
import {sharedBudgetConfig,createSharedBudget} from '../shared-budget.mjs'
import http from 'node:http'
import {createServer} from '../server.mjs'
import {runtimeConfig} from '../config.mjs'
const env={DATA_API_USAGE_URL:'https://api.example',FAAS_WORKLOAD_IDENTITY_ENDPOINT:'http://127.0.0.1:8001/oidc/token',DATA_API_SHARED_BUDGET:JSON.stringify({version:1,requests:7,window_seconds:60,claim:'tenant_id'})}
test('shared budgets require bounded owner policy and trusted reporting destinations',()=>{
 assert.equal(sharedBudgetConfig({}),null)
 for(const change of [{DATA_API_USAGE_URL:'http://localhost'},{DATA_API_SHARED_BUDGET:'{}'},{DATA_API_SHARED_BUDGET:JSON.stringify({version:1,requests:0,window_seconds:60})},{FAAS_WORKLOAD_IDENTITY_ENDPOINT:'http://private.example/oidc/token'}])assert.throws(()=>sharedBudgetConfig({...env,...change}))
})
test('replica requests hash the verified tenant identically and do not send raw identity or retry',async()=>{
 const sent=[];let count=0
 const fetcher=async(url,options)=>{if(url.includes('/oidc/token'))return Response.json({token_type:'Bearer',access_token:'workload'});sent.push(JSON.parse(options.body));return Response.json({allowed:++count<=1,retry_after_seconds:30})}
 const a=createSharedBudget(sharedBudgetConfig(env),fetcher),b=createSharedBudget(sharedBudgetConfig(env),fetcher)
 assert.equal(await a({sub:'alice',tenant_id:'team-a'},AbortSignal.timeout(1000)),null)
 assert.equal((await b({sub:'bob',tenant_id:'team-a'},AbortSignal.timeout(1000))).status,429)
 assert.equal(sent.length,2);assert.equal(sent[0].key,sent[1].key);assert.match(sent[0].key,/^[a-f0-9]{64}$/);assert.ok(!JSON.stringify(sent).includes('team-a'))
 assert.equal((await a({sub:'alice'},AbortSignal.timeout(1000))).code,'data_api_budget_identity_required')
})
test('uncertain coordinator results fail closed without automatic replay',async()=>{
 let attempts=0;const admit=createSharedBudget(sharedBudgetConfig(env),async()=>{attempts++;throw Error('private details')})
 const result=await admit({tenant_id:'team'},AbortSignal.timeout(1000));assert.deepEqual(result,{status:503,code:'data_api_shared_budget_unavailable'});assert.equal(attempts,1)
})
test('a failed shared admission refuses a mutation before customer SQL dispatch',async t=>{
 let dispatched=0
 const upstream=http.createServer((req,res)=>{dispatched++;res.end('[]')})
 await new Promise(resolve=>upstream.listen(0,'127.0.0.1',resolve))
 const config=runtimeConfig({DATABASE_URL:'postgres://api:password@127.0.0.1/app',DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'app'})
 // A verified user without the owner-required tenant claim is refused locally.
 config.sharedBudget=sharedBudgetConfig(env)
 const gateway=createServer(config,async()=>({sub:'alice',exp:Date.now()/1000+60}),upstream.address().port,upstream.address().port)
 await new Promise(resolve=>gateway.listen(0,'127.0.0.1',resolve))
 t.after(async()=>{for(const server of [gateway,upstream]){server.closeAllConnections();await new Promise(resolve=>server.close(resolve))}})
 const response=await fetch(`http://127.0.0.1:${gateway.address().port}/rest/v1/notes`,{method:'POST',headers:{Authorization:'Bearer test','Content-Type':'application/json'},body:'{"body":"test"}'})
 assert.equal(response.status,403);assert.equal((await response.json()).code,'data_api_budget_identity_required');assert.equal(dispatched,0)
})

test('independent gateways share admission and never dispatch an exhausted or unavailable write',async t=>{
 let used=0,dispatched=0,unavailable=false
 const control=http.createServer((req,res)=>{
  req.resume();res.setHeader('Content-Type','application/json')
  if(req.url==='/identity')return res.end(JSON.stringify({token_type:'Bearer',access_token:'workload'}))
  if(unavailable){res.writeHead(503);res.end('{}');return}
  res.end(JSON.stringify({allowed:++used<=1,retry_after_seconds:30}))
 })
 const upstream=http.createServer((req,res)=>{req.resume();dispatched++;res.end('[]')})
 const servers=[control,upstream]
 await Promise.all(servers.map(s=>new Promise(resolve=>s.listen(0,'127.0.0.1',resolve))))
 const config=runtimeConfig({DATABASE_URL:'postgres://api:password@127.0.0.1/app',DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'app'})
 config.sharedBudget={requests:1,window_seconds:60,claim:'tenant_id',identity:`http://127.0.0.1:${control.address().port}/identity`,endpoint:`http://127.0.0.1:${control.address().port}/budget`}
 const verify=async()=>({sub:'alice',tenant_id:'team',exp:Date.now()/1000+60})
 for(let i=0;i<2;i++){const g=createServer(config,verify,upstream.address().port,upstream.address().port,()=>{});await new Promise(resolve=>g.listen(0,'127.0.0.1',resolve));servers.push(g)}
 t.after(async()=>{for(const s of servers){s.closeAllConnections();await new Promise(resolve=>s.close(resolve))}})
 const write=server=>fetch(`http://127.0.0.1:${server.address().port}/rest/v1/notes`,{method:'POST',headers:{Authorization:'Bearer test','Content-Type':'application/json'},body:'{"body":"test"}'})
 assert.equal((await write(servers[2])).status,200)
 let response=await write(servers[3]);assert.equal(response.status,429);assert.equal(response.headers.get('retry-after'),'30');assert.equal((await response.json()).code,'data_api_rate_limited')
 unavailable=true;response=await write(servers[3]);assert.equal(response.status,503);assert.equal((await response.json()).code,'data_api_shared_budget_unavailable');assert.equal(dispatched,1)
})
