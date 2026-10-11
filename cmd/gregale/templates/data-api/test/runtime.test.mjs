import test from 'node:test'
import assert from 'node:assert/strict'
import http from 'node:http'
import { jwtVerify } from 'jose'
import { runtimeConfig, databaseURL, schemaNames } from '../config.mjs'
import { createServer } from '../server.mjs'
import { normalize, generate } from '../types.mjs'

test('configuration requires an explicit schema and verified remote TLS', () => {
  assert.throws(() => schemaNames('api,pg_catalog'))
  assert.throws(() => schemaNames('api,api'))
  assert.throws(() => databaseURL('postgres://u:p@db.example/db?sslmode=disable'))
  assert.equal(new URL(databaseURL('postgres://u:p@db.example/db?sslmode=require')).searchParams.get('sslmode'), 'verify-full')
  assert.throws(() => runtimeConfig({ DATABASE_URL: 'postgres://u:p@localhost/db' }))
})

test('generated types distinguish nullable, defaulted and generated columns', () => {
  const columns = [
    {schema:'api',relation:'notes',kind:'r',name:'id',type:'1',required:true,identity:'a',generated:'',defaulted:true,insertable:true,updatable:true},
    {schema:'api',relation:'notes',kind:'r',name:'body',type:'2',required:true,identity:'',generated:'',defaulted:false,insertable:true,updatable:true},
    {schema:'api',relation:'notes',kind:'r',name:'state',type:'3',required:false,identity:'',generated:'',defaulted:false,insertable:true,updatable:true},
  ]
  const types = [{id:'1',schema:'pg_catalog',name:'int8',element:'0'},{id:'2',schema:'pg_catalog',name:'text',element:'0'},{id:'3',schema:'api',name:'state',kind:'e',element:'0',labels:['open','closed']}]
  const snapshot = normalize(columns, types, [], ['api'])
  const output = generate(snapshot)
  assert.match(output, /"id"\?: never/)
  assert.match(output, /"body": string/)
  assert.match(output, /"state"\?: "open" \| "closed" \| null/)
  assert.equal(generate(snapshot),output)
  assert.notEqual(generate(normalize(columns.slice(1),types,[],['api'])),output)
})

test('proxy rejects unauthenticated requests and normalizes untrusted SQL roles', async t => {
  const config = runtimeConfig({ DATABASE_URL:'postgres://restricted:p@localhost/db',DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'notes',DATA_API_ALLOWED_ORIGINS:'https://app.example' })
  let received
  const upstream=http.createServer(async (req,res)=>{
    received=(await jwtVerify(req.headers.authorization.slice(7),new TextEncoder().encode(config.secret.toString('base64url')))).payload
    assert.equal(req.headers['x-faas-consumer-id'],undefined)
    res.end('[]')
  })
  await new Promise(r=>upstream.listen(0,'127.0.0.1',r))
  const server=createServer(config,async token=>{if(token!=='valid')throw Error();return {sub:'alice',exp:Math.floor(Date.now()/1000)+60,role:'postgres'}},upstream.address().port)
  await new Promise(r=>server.listen(0,'127.0.0.1',r))
  t.after(()=>{server.closeAllConnections();server.close();upstream.closeAllConnections();upstream.close()})
  const base=`http://127.0.0.1:${server.address().port}`
  assert.equal((await fetch(base+'/rest/v1/notes')).status,401)
  assert.equal((await fetch(base+'/rest/v1/notes',{headers:{Authorization:'Bearer invalid'}})).status,401)
  assert.equal((await fetch(base+'/rest/v1/notes',{headers:{Authorization:'Bearer valid','X-FaaS-Consumer-Id':'forged'}})).status,200)
  assert.equal(received.role,'restricted')
  assert.equal(received.sub,'alice')
  assert.equal((await fetch(base+'/rest/v1/notes',{method:'OPTIONS',headers:{Origin:'https://evil.example'}})).status,403)
  assert.equal((await fetch(base+'/rest/v1/notes',{method:'OPTIONS',headers:{Origin:'https://app.example'}})).status,204)
  assert.equal((await fetch(base+'/__gregale/types',{headers:{Authorization:'Bearer valid'}})).status,404)
  assert.equal((await fetch(base+'/rest/v1/rpc%2Funsafe',{headers:{Authorization:'Bearer valid'}})).status,404)
  assert.equal((await fetch(base+'/rest/v1/rpc/unsafe',{headers:{Authorization:'Bearer valid'}})).status,404)
})
