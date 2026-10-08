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

test('relationship metadata preserves composite order and excludes inaccessible or cross-schema targets', () => {
  const column = (schema, relation, name) => ({ schema, relation, name, kind: 'r', type: '1', required: true, identity: '', generated: '', defaulted: false, insertable: true, updatable: true })
  const columns = [column('api', 'notes', 'subject'), column('api', 'notes', 'id'),
    column('api', 'comments', 'subject'), column('api', 'comments', 'note_id'),
    column('other', 'notes', 'subject'), column('other', 'notes', 'id')]
  const fk = { schema: 'api', relation: 'comments', name: 'comments_note_fkey', referenced_schema: 'api', referenced_relation: 'notes', columns: ['subject', 'note_id'], referenced_columns: ['subject', 'id'], one_to_one: false }
  const types = [{ id: '1', schema: 'pg_catalog', name: 'text', element: '0' }]
  const snapshot = normalize(columns, types, [fk, { ...fk, name: 'other_notes_fkey', referenced_schema: 'other' },
    { ...fk, name: 'hidden_column_fkey', referenced_columns: ['subject', 'secret'] }], ['api', 'other'])
  assert.deepEqual(snapshot.tables.find(t => t.name === 'comments').relationships, [{
    foreignKeyName: 'comments_note_fkey', columns: ['subject', 'note_id'], isOneToOne: false,
    referencedRelation: 'notes', referencedColumns: ['subject', 'id'],
  }])
  const before = generate(snapshot)
  const unique = generate(normalize(columns, types, [{ ...fk, one_to_one: true }], ['api', 'other']))
  assert.notEqual(unique, before, 'cardinality changes must change the contract fingerprint')
  assert.match(unique, /"isOneToOne":true/)
  assert.notEqual(generate(normalize(columns, types, [{ ...fk, name: 'renamed_fkey' }], ['api', 'other'])), before)
})

test('proxy rejects unauthenticated requests and normalizes untrusted SQL roles', async t => {
  const config = runtimeConfig({ DATABASE_URL:'postgres://restricted:p@localhost/db',DATA_API_ISSUER:'https://issuer.example',DATA_API_JWKS_URL:'https://issuer.example/jwks',DATA_API_AUDIENCE:'notes',DATA_API_ALLOWED_ORIGINS:'https://app.example' })
  config.functions = [{ schema: 'api', name: 'approved' }]
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
  assert.equal((await fetch(base+'/rest/v1/rpc/approved',{method:'POST',headers:{Authorization:'Bearer valid'}})).status,200)
  assert.equal((await fetch(base+'/rest/v1/rpc/approved',{method:'POST',headers:{Authorization:'Bearer valid','Content-Profile':'other'}})).status,404)
  assert.equal((await fetch(base+'/rest/v1/rpc%2Fapproved',{method:'POST',headers:{Authorization:'Bearer valid'}})).status,404)
  assert.equal((await fetch(base+'/rest/v1/rpc/approved',{headers:{Authorization:'Bearer valid'}})).status,405)
  assert.equal(received.role,'restricted')
  assert.equal(received.sub,'alice')
  assert.equal((await fetch(base+'/rest/v1/notes',{method:'OPTIONS',headers:{Origin:'https://evil.example'}})).status,403)
  assert.equal((await fetch(base+'/rest/v1/notes',{method:'OPTIONS',headers:{Origin:'https://app.example'}})).status,204)
  assert.equal((await fetch(base+'/__gregale/types',{headers:{Authorization:'Bearer valid'}})).status,404)
  assert.equal((await fetch(base+'/rest/v1/rpc%2Funsafe',{headers:{Authorization:'Bearer valid'}})).status,404)
  assert.equal((await fetch(base+'/rest/v1/rpc/unsafe',{headers:{Authorization:'Bearer valid'}})).status,404)
})

test('proxy closes canceled upstream requests and sanitizes unavailable and timeout errors', { timeout: 25000 }, async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://test:credential-sentinel@127.0.0.1/test?sslmode=disable', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  let opened, closed
  const opening = new Promise(resolve => { opened = resolve })
  const closing = new Promise(resolve => { closed = resolve })
  const upstream = http.createServer((req, res) => { opened(); res.on('close', closed) })
  await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async () => ({ sub: 'user', exp: 9999999999 }), upstream.address().port)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); upstream.closeAllConnections(); upstream.close() })
  const url = `http://127.0.0.1:${gateway.address().port}/rest/v1/notes`
  const headers = { Authorization: 'Bearer token-sentinel' }
  const controller = new AbortController()
  const canceled = fetch(url, { headers, signal: controller.signal })
  const rejection = assert.rejects(canceled, error => error.name === 'AbortError')
  await opening
  controller.abort()
  await rejection
  await Promise.race([closing, new Promise((_, reject) => { const timer = setTimeout(() => reject(new Error('upstream_not_closed')), 2000); timer.unref() })])
  const timeout = await fetch(url, { headers })
  assert.equal(timeout.status, 504)
  const timeoutBody = await timeout.text()
  assert.equal(JSON.parse(timeoutBody).code, 'query_timeout')
  assert.doesNotMatch(timeoutBody, /credential-sentinel|token-sentinel|postgres:/)
  await new Promise(resolve => upstream.close(resolve))
  const unavailable = await fetch(url, { headers })
  assert.equal(unavailable.status, 503)
  const body = await unavailable.text()
  assert.equal(JSON.parse(body).code, 'data_api_unavailable')
  assert.doesNotMatch(body, /credential-sentinel|token-sentinel|postgres:/)
  assert.equal(unavailable.headers.get('Cache-Control'), 'no-store')
})


test('RPC contract includes defaults and nullable scalars without confusing enum labels with unsupported types', () => {
  const types = [{ id: '1', schema: 'api', name: 'state', kind: 'e', element: '0', labels: ['unknown', 'known'] }]
  const functions = [{ schema: 'api', name: 'state_value', overloads: 1, names: ['value'], arguments: ['1'], defaults: 1, return_type: '1', setof: false, modes: null }]
  const output = generate(normalize([], types, [], ['api'], functions))
  assert.match(output, /"value"\?: "unknown" \| "known" \| null/)
  assert.match(output, /Returns: "unknown" \| "known" \| null/)
  assert.notEqual(output, generate(normalize([], types, [], ['api'], [{ ...functions[0], defaults: 0 }])))
})

test('serving contract requires application auth, readiness and an immutable startup fingerprint', async t => {
  const config = runtimeConfig({ DATABASE_URL: 'postgres://restricted:p@localhost/db', DATA_API_ISSUER: 'https://issuer.example', DATA_API_JWKS_URL: 'https://issuer.example/jwks', DATA_API_AUDIENCE: 'notes' })
  config.fingerprint = 'a'.repeat(64)
  let ready = true
  const admin = http.createServer((req, res) => { assert.equal(req.url, '/ready'); res.writeHead(ready ? 200 : 503); res.end() })
  await new Promise(resolve => admin.listen(0, '127.0.0.1', resolve))
  const gateway = createServer(config, async token => { if (token !== 'valid') throw Error(); return { sub: 'alice', exp: 9999999999 } }, 1, admin.address().port)
  await new Promise(resolve => gateway.listen(0, '127.0.0.1', resolve))
  t.after(() => { gateway.closeAllConnections(); gateway.close(); admin.closeAllConnections(); admin.close() })
  const address = `http://127.0.0.1:${gateway.address().port}/__gregale/schema`
  assert.equal((await fetch(address)).status, 401)
  assert.equal((await fetch(address, { headers: { Authorization: 'Bearer invalid' } })).status, 401)
  assert.equal((await fetch(address, { method: 'POST', headers: { Authorization: 'Bearer valid' } })).status, 405)
  const response = await fetch(address, { headers: { Authorization: 'Bearer valid' } })
  assert.equal(response.headers.get('cache-control'), 'no-store')
  assert.deepEqual(await response.json(), { ready: true, version: 1, fingerprint: 'a'.repeat(64) })
  config.fingerprint = 'b'.repeat(64)
  assert.equal((await (await fetch(address, { headers: { Authorization: 'Bearer valid' } })).json()).fingerprint, 'a'.repeat(64))
  ready = false
  const unavailable = await fetch(address, { headers: { Authorization: 'Bearer valid' } })
  assert.equal(unavailable.status, 503)
  assert.deepEqual(await unavailable.json(), { ready: false })
})
