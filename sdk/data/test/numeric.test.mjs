import test from 'node:test'
import assert from 'node:assert/strict'
import { createDataClient, parseLosslessJSON } from '../dist/index.js'
import { liveRead } from '../dist/live/index.js'

test('lossless REST and live values agree for large integers, decimals, arrays and JSON', async () => {
  const wire = '[{"id":9007199254740993,"price":1.234567890123456789,"count":2,"values":[9007199254740993,null],"json":{"v":1e100,"text":"123"}}]'
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', numericMode: 'lossless', fetch: async () => new Response(wire, { headers: { 'content-type': 'application/json' } }) }).schema('api')
  const query = db.from('items').select('*')
  const { data, error } = await query
  assert.equal(error, null)
  assert.deepEqual(data, [{ id: '9007199254740993', price: '1.234567890123456789', count: '2', values: ['9007199254740993', null], json: { v: '1e100', text: '123' } }])
  const columns = ['int8', 'numeric', 'int4', '_int8', 'jsonb'].map((type, i) => ({ name: ['id', 'price', 'count', 'values', 'json'][i], type }))
  assert.deepEqual(liveRead(query).parse(['9007199254740993', '1.234567890123456789', '2', '{9007199254740993,NULL}', '{"v":1e100,"text":"123"}'], columns), data[0])
})

test('lossless decoder preserves lexemes and treats keys and escaped strings as text', () => {
  const data = parseLosslessJSON('{"123":-0,"a":"a\\\"123","b":[1e-1000,0.1000000000000000001]}')
  assert.deepEqual(data, { 123: '-0', a: 'a"123', b: ['1e-1000', '0.1000000000000000001'] })
  assert.throws(() => parseLosslessJSON('[01]'))
  assert.throws(() => parseLosslessJSON('[1.]'))
})

test('default numbers and lossless error status codes retain existing behavior', async () => {
  const fetch = async () => new Response('[{"n":42}]', { headers: { 'content-type': 'application/json' } })
  assert.equal((await createDataClient({ url: 'https://data.example', accessToken: 'token', fetch }).from('x').select()).data[0].n, 42)
  const error = { code: '42501', message: 'denied', details: null, hint: null }
  const db = createDataClient({ url: 'https://data.example', accessToken: 'token', numericMode: 'lossless', fetch: async () => new Response(JSON.stringify(error), { status: 403, headers: { 'content-type': 'application/json' } }) })
  assert.equal((await db.from('x').select()).error.code, '42501')
})

test('DAPI006 exact numbers retain kind and precision through REST, RPC, live and writes',async()=>{
 const {ExactNumber,parseExactJSON,stringifyExactJSON}=await import('../dist/index.js')
 const wire='[{"id":9007199254740993,"decimal":1.2300e-9,"text":"9007199254740993","json":{"$gregale.exact.number":"literal","value":9007199254740993}}]'
 const calls=[]
 const db=createDataClient({url:'https://data.example',accessToken:'token',numericMode:'exact',fetch:async(input,init)=>{calls.push({input:String(input),...init});return new Response(wire,{headers:{'content-type':'application/json'}})}}).schema('api')
 const query=db.from('numbers').select()
 const {data,error}=await query
 assert.equal(error,null)
 assert.ok(data[0].id instanceof ExactNumber)
 assert.equal(data[0].id.source,'9007199254740993')
 assert.equal(data[0].decimal.source,'1.2300e-9')
 assert.equal(data[0].text,'9007199254740993')
 assert.equal(data[0].json['$gregale.exact.number'],'literal')
 assert.ok(data[0].json.value instanceof ExactNumber)
 const {liveRead}=await import('../dist/live/index.js')
 assert.deepEqual(liveRead(query).parse(['{9007199254740993,NULL}','{"n":1.2300e-9,"s":"1.2300e-9"}'],[{name:'array',type:'_numeric'},{name:'json',type:'jsonb'}]),{array:[new ExactNumber('9007199254740993'),null],json:{n:new ExactNumber('1.2300e-9'),s:'1.2300e-9'}})
 await db.from('numbers').insert(data)
 assert.equal(calls.at(-1).body,wire)
 await db.rpc('save_number',{number:data[0].id,json:data[0].json})
 assert.equal(calls.at(-1).body,'{"number":9007199254740993,"json":{"$gregale.exact.number":"literal","value":9007199254740993}}')
 await db.rpc('save_number',{number:data[0].id,json:data[0].json},{get:true})
 const params=new URL(calls.at(-1).input).searchParams
 assert.equal(params.get('number'),'9007199254740993')
 assert.equal(params.get('json'),'{"$gregale.exact.number":"literal","value":9007199254740993}')
 assert.equal((await query.maybeSingle()).data.id.source,'9007199254740993')
 assert.equal(stringifyExactJSON(parseExactJSON('[-0,1e9999,"1e9999",null]')),'[-0,1e9999,"1e9999",null]')
 assert.throws(()=>new ExactNumber('1;drop table x'))
 assert.throws(()=>Number(new ExactNumber('1')),/explicit conversion/)
})

test('DAPI006 exact writes work without JSON.rawJSON and ordinary serialization refuses coercion',async()=>{
 const {ExactNumber,stringifyExactJSON}=await import('../dist/index.js')
 const descriptor=Object.getOwnPropertyDescriptor(JSON,'rawJSON')
 Object.defineProperty(JSON,'rawJSON',{value:undefined,configurable:true,writable:true})
 try {
  const value=new ExactNumber('9007199254740993')
  assert.throws(()=>JSON.stringify({value}),/stringifyExactJSON/)
  assert.equal(stringifyExactJSON({value,text:'9007199254740993'}),'{"value":9007199254740993,"text":"9007199254740993"}')
  let body
  const db=createDataClient({url:'https://data.example',accessToken:'token',numericMode:'exact',fetch:async(_url,init)=>{body=init.body;return Response.json([])}})
  assert.equal((await db.from('values').insert({value})).error,null)
  assert.equal(body,'{"value":9007199254740993}')
 }finally{if(descriptor)Object.defineProperty(JSON,'rawJSON',descriptor);else delete JSON.rawJSON}
})

test('DAPI006 exact numeric IDs survive direct and encrypted cursor pagination',async()=>{
 const {cursorFrom,cursorPage,createCursorPager,ExactNumber}=await import('../dist/index.js')
 const requests=[]
 const db=createDataClient({url:'https://data.example',accessToken:'token',numericMode:'exact',fetch:async input=>{requests.push(new URL(input));return new Response('[{"id":9007199254740993},{"id":9007199254740995}]',{headers:{'content-type':'application/json'}})}})
 const order=[{column:'id',unique:true}],scope={identity:'alice',schemaFingerprint:'a'.repeat(64)}
 const after=cursorFrom({id:new ExactNumber('9007199254740993')},order)
 await cursorPage(db.from('items').select(),{order,size:1,after})
 assert.match(requests.at(-1).searchParams.get('or'),/id.gt.9007199254740993/)
 const pager=await createCursorPager({key:new Uint8Array(32).fill(7)})
 const first=await pager.page(db.from('items').select(),{order,size:1,scope})
 assert.ok(first.nextCursor)
 await pager.page(db.from('items').select(),{order,size:1,scope,after:first.nextCursor})
 assert.match(requests.at(-1).searchParams.get('or'),/id.gt.9007199254740993/)
})
