import test from 'node:test'
import assert from 'node:assert/strict'
import { teamsClient, createOrderForm, parseOrder } from '../dist/orders.js'
const id='00000000-0000-4000-8000-000000000001', requestKey='10000000-0000-4000-8000-000000000001'
const row={id,tenant_id:'shop-a',created_by:'alice',total_cents:1200,status:'draft',version:1}
const ok=data=>new Response(JSON.stringify(data),{status:200})
const rpcRow=args=>({...row,id:args.order_id,total_cents:args.order_total,status:args.order_state,version:args.expected_version+1})
function fixture(rpc, role='owner') {
  let actor='alice', tokens=0
  const calls=[]
  const client=teamsClient({url:'https://orders.example',accessToken:()=>{tokens++;return 'test-token'},fetch:async (url,init)=>{
    const args=init.body ? JSON.parse(init.body) : null
    calls.push({url:String(url),args})
    if(String(url).includes('/team_members')) return ok([{tenant_id:'shop-a',subject:actor,role}])
    return rpc(args,url,init)
  }},'shop-a')
  let n=0
  const form=createOrderForm(client,{createKey:()=>++n%2 ? requestKey : id})
  return {client,form,calls,get tokens(){return tokens},set actor(value){actor=value}}
}
test('invalid fields never look up tokens or send membership/mutation requests',async()=>{
  const f=fixture(()=>{throw new Error('must not request')})
  for(const input of [{total:'-1',status:'draft'},{total:'1.5',status:'draft'},{total:'2147483648',status:'draft'},{total:'4',status:'invalid'},{total:'4',status:'submitted'},{total:'4',status:'fulfilled'},{total:'4',status:'draft',id,version:0}]) assert.equal((await f.form.submit(input)).outcome,'invalid')
  assert.equal(f.tokens,0);assert.equal(f.calls.length,0);assert.equal(f.form.pending,null)
})
test('confirmed create and update retain generated RPC fields and version',async()=>{
  const f=fixture(args=>ok([rpcRow(args)]))
  assert.deepEqual(await f.form.submit({total:'1200',status:'draft'}),{outcome:'saved',order:row})
  const updated=await f.form.submit({total:'1500',status:'draft',id,version:1})
  assert.equal(updated.order.version,2);assert.equal(updated.order.status,'draft')
  assert.deepEqual(f.calls[1].args,{request_key:requestKey,team_id:'shop-a',order_id:id,expected_version:0,order_total:1200,order_state:'draft'})
  assert.equal(f.form.pending,null)
})
test('member reads cannot construct a save RPC',async()=>{
  const f=fixture(()=>{throw new Error('must not save')},'member')
  const result=await f.form.submit({total:'1200',status:'draft'})
  assert.equal(result.outcome,'failed');assert.equal(result.uncertain,false);assert.equal(f.calls.length,1)
})
test('uncertain save keeps original payload, blocks new saves and verifies original identity on recovery',async()=>{
  let attempts=0
  const f=fixture(args=>++attempts===1?Promise.reject(new TypeError('private network failure')):ok([rpcRow(args)]))
  const first=await f.form.submit({total:'1200',status:'draft'})
  assert.equal(first.uncertain,true);assert.ok(Object.isFrozen(f.form.pending));assert.ok(Object.isFrozen(f.form.pending.args))
  const original=f.form.pending
  assert.equal((await f.form.submit({total:'9',status:'fulfilled'})).outcome,'pending')
  f.actor='amy'
  const switched=await f.form.reconcile();assert.equal(switched.uncertain,true);assert.equal(attempts,1);assert.equal(f.form.pending,original)
  f.actor='alice'
  assert.equal((await f.form.reconcile()).outcome,'saved');assert.equal(attempts,2);assert.equal(f.form.pending,null)
  assert.deepEqual(f.calls.filter(c=>c.args).map(c=>c.args),[original.args,original.args])
})
test('malformed or mismatched successful RPC responses remain uncertain',async()=>{
  for(const value of [null,[],[{}],[{...row,tenant_id:'shop-b'}],[{...row,version:9}],[{...row,total_cents:9}],[row,row]]) {
    const f=fixture(()=>ok(value))
    const result=await f.form.submit({total:'1200',status:'draft'})
    assert.equal(result.outcome,'failed');assert.equal(result.uncertain,true);assert.ok(f.form.pending)
    assert.ok(!result.message.includes('shop-b'))
  }
})
test('version conflicts require reloading and raw server details are not displayed',async()=>{
  const f=fixture(()=>new Response(JSON.stringify({code:'PT409',message:'order_version_conflict',details:'private data'}),{status:409}))
  const result=await f.form.submit({total:'1200',status:'draft',id,version:1})
  assert.equal(result.uncertain,false);assert.equal(f.form.pending,null);assert.match(result.message,/Reload/);assert.ok(!result.message.includes('private'))
})
test('auth failure during recovery preserves earlier uncertain state',async()=>{
  let attempts=0
  const f=fixture(()=>++attempts===1?Promise.reject(new TypeError('lost')):new Response(JSON.stringify({code:'PGRST301',message:'private'}),{status:401}))
  await f.form.submit({total:'1200',status:'draft'});const pending=f.form.pending
  assert.equal((await f.form.reconcile()).uncertain,true);assert.equal(f.form.pending,pending)
})
test('concurrent submissions cannot overlap',async()=>{
  let finish
  const f=fixture(args=>new Promise(resolve=>{finish=()=>resolve(ok([rpcRow(args)]))}))
  const first=f.form.submit({total:'1200',status:'draft'})
  while(!finish) await new Promise(resolve=>setTimeout(resolve,1))
  assert.equal((await f.form.submit({total:'1300',status:'draft'})).outcome,'busy')
  finish();assert.equal((await first).outcome,'saved')
})
test('filtered UUID pagination binds the tenant and uses the last displayed row',async()=>{
  let called
  const client=teamsClient({url:'https://orders.example',accessToken:'test',fetch:async(url)=>{called=new URL(url);return ok([row,{...row,id:'00000000-0000-4000-8000-000000000002'}])}},'shop-a')
  assert.deepEqual(await client.list('draft',id,1),{rows:[row],next:id})
  assert.equal(called.searchParams.get('tenant_id'),'eq.shop-a');assert.equal(called.searchParams.get('status'),'eq.draft');assert.equal(called.searchParams.get('id'),`gt.${id}`);assert.equal(called.searchParams.get('limit'),'2')
  await assert.rejects(client.list('bad'));await assert.rejects(client.list(undefined,'bad'));await assert.rejects(client.list(undefined,undefined,0))
})
test('foreign-team list responses are rejected and rows are parsed without arbitrary metadata',async()=>{
  const client=teamsClient({url:'https://orders.example',accessToken:'test',fetch:async()=>ok([{...row,tenant_id:'shop-b'}])},'shop-a')
  await assert.rejects(client.list(),/Could not load/)
  assert.deepEqual(parseOrder({...row,secret:'private'}),row)
})
