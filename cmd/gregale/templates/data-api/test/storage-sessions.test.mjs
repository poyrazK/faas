import test from 'node:test'
import assert from 'node:assert/strict'
import { PassThrough } from 'node:stream'
import { storageActor,handleUploadSession } from '../storage-sessions.mjs'

test('upload sessions retain custom claim and token-ID isolation across timestamp refreshes',()=>{
  const claims={sub:'alice',tenant:'a',gregale_tenant:'private-a',jti:'session-a',exp:10,iat:1,gregale_sql_deadline:{expires_at_ms:10}}
  const actor=storageActor(claims)
  assert.equal(storageActor({...claims,exp:20,iat:11,gregale_sql_deadline:{expires_at_ms:20}}),actor)
  for(const changes of [{sub:'bob'},{tenant:'b'},{gregale_tenant:'private-b'},{jti:'session-b'}])assert.notEqual(storageActor({...claims,...changes}),actor)
  assert.equal(storageActor({sub:'alice',context:{b:2,a:1}}),storageActor({context:{a:1,b:2},sub:'alice'}))
})

test('an expired session-start body never reaches SQL',async()=>{
  const req=new PassThrough(),controller=new AbortController();let queries=0,problem
  const pending=handleUploadSession({req,res:{},route:{action:'sessionStart',bucket:'uploads'},config:{storage:{buckets:new Map()}},deadline:{signal:controller.signal},problem:(_res,status,code)=>{problem={status,code}}},{rows:()=>{queries++;throw new Error('unexpected SQL')}})
  controller.abort()
  await pending
  assert.equal(queries,0)
  assert.equal(problem.code,'storage_invalid_session')
})
