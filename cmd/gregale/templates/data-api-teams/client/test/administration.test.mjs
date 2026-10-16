import test from 'node:test'
import assert from 'node:assert/strict'
import { teamsClient } from '../dist/orders.js'
import { createMembershipForm, teamAdministration, parseMembershipEvent } from '../dist/administration.js'
const key='10000000-0000-4000-8000-000000000001',revision='20000000-0000-4000-8000-000000000001',nextRevision='20000000-0000-4000-8000-000000000002'
const ok=value=>new Response(JSON.stringify(value))
const event=(args,transfer=false)=>({event_id:'30000000-0000-4000-8000-000000000001',tenant_id:'shop-a',actor:'alice',request_key:args.request_key,action:transfer?'transfer':args.expected_revision===null?'add':args.new_role===null?'remove':'change',target_subject:args.target_subject,before_role:args.expected_revision===null?null:'member',after_role:transfer?'owner':args.new_role,before_revision:args.expected_revision,after_revision:!transfer&&args.new_role===null?null:nextRevision,actor_before_revision:revision,actor_after_revision:transfer?nextRevision:revision,actor_after_role:transfer?'admin':'owner',occurred_at:'2026-10-12T01:02:03.123456+00:00'})
const input={kind:'change',target:'ann',revision,role:'admin'}
function fixture(handler) {
  let actor='alice',role='owner',tokens=0
  const calls=[]
  const client=teamsClient({url:'https://team.example',accessToken:()=>{tokens++;return 'test'},fetch:async(url,init)=>{
    const args=init.body?JSON.parse(init.body):null
    const path=new URL(url).pathname;calls.push({path,args})
    if(path.endsWith('/team_members')) return ok(actor?[{tenant_id:'shop-a',subject:actor,role}]:[])
    return handler(path,args)
  }},'shop-a')
  return {client,form:createMembershipForm(client,{createKey:()=>key}),calls,get tokens(){return tokens},set actor(v){actor=v},set role(v){role=v}}
}
test('invalid access changes cannot retrieve tokens, query membership or mutate',async()=>{
  const f=fixture(()=>{throw new Error('must not request')})
  for(const value of [{...input,target:''},{...input,revision:'bad'},{...input,role:'superadmin'},{...input,kind:'transfer',revision:null,role:'owner'}]) assert.equal((await f.form.submit(value)).outcome,'invalid')
  assert.equal(f.tokens,0);assert.equal(f.calls.length,0)
})
test('only current owners construct administration mutation requests',async()=>{
  const f=fixture(()=>{throw new Error('must not mutate')});f.role='admin'
  const result=await f.form.submit(input);assert.equal(result.outcome,'failed');assert.equal(result.uncertain,false);assert.equal(f.calls.length,1)
})
test('confirmed role changes preserve expected revision and schema-derived role',async()=>{
  const f=fixture((path,args)=>ok([event(args)]))
  const result=await f.form.submit(input)
  assert.equal(result.outcome,'changed');assert.equal(result.event.after_role,'admin');assert.equal(f.form.pending,null)
  assert.deepEqual(f.calls[1].args,{request_key:key,team_id:'shop-a',target_subject:'ann',expected_revision:revision,new_role:'admin'})
})
test('last-owner and stale-revision failures provide safe actionable feedback',async()=>{
  for(const message of ['last_team_owner','membership_revision_conflict','unknown_team_user']) {
    const f=fixture(()=>new Response(JSON.stringify({code:message==='unknown_team_user'?'PT400':'PT409',message,details:'private'}),{status:message==='unknown_team_user'?400:409}))
    const result=await f.form.submit(input);assert.equal(result.uncertain,false);assert.equal(f.form.pending,null);assert.ok(!result.message.includes('private'));assert.ok(!result.message.includes(message))
  }
})
test('committed transfer recovery reads a receipt after actor is demoted, without another write',async()=>{
  let saved,mutations=0
  const f=fixture((path,args)=>{
    if(path.endsWith('/get_team_change_receipt'))return ok([saved])
    mutations++;saved=event(args,true);throw new TypeError('private lost response')
  })
  assert.equal((await f.form.submit({...input,kind:'transfer',role:'owner'})).uncertain,true)
  f.role='admin';assert.equal((await f.form.reconcile()).outcome,'changed');assert.equal(mutations,1);assert.equal(f.form.pending,null)
})
test('a missing receipt cannot replay as a different owner',async()=>{
  let mutations=0
  const f=fixture(path=>{if(path.endsWith('/get_team_change_receipt'))return ok([]);mutations++;throw new TypeError('lost')})
  await f.form.submit(input);const original=f.form.pending
  assert.ok(Object.isFrozen(original));assert.ok(Object.isFrozen(original.intent))
  f.actor='amy';const result=await f.form.reconcile();assert.equal(result.uncertain,true);assert.equal(mutations,1);assert.equal(f.form.pending,original)
})
test('no receipt replays only as the original owner using the immutable original arguments',async()=>{
  let mutations=0
  const f=fixture((path,args)=>{if(path.endsWith('/get_team_change_receipt'))return ok([]);if(++mutations===1)throw new TypeError('lost');return ok([event(args)])})
  await f.form.submit(input);const original=f.form.pending
  assert.equal((await f.form.submit({...input,role:'owner'})).outcome,'pending')
  assert.equal((await f.form.reconcile()).outcome,'changed')
  const writes=f.calls.filter(c=>c.path.endsWith('/change_team_member_once'));assert.deepEqual(writes[0].args,writes[1].args);assert.equal(writes[0].args.request_key,original.intent.key)
})
test('malformed, wrong-actor or mismatched receipts preserve uncertain state',async()=>{
  for(const mutate of [value=>({...value,actor:'amy'}),value=>({...value,after_role:'owner'}),value=>({...value,before_revision:nextRevision}),()=>({})]) {
    let saved
    const f=fixture((path,args)=>{if(path.endsWith('/get_team_change_receipt'))return ok([mutate(saved)]);saved=event(args);throw new TypeError('lost')})
    await f.form.submit(input);const pending=f.form.pending
    assert.equal((await f.form.reconcile()).uncertain,true);assert.equal(f.form.pending,pending)
  }
})
test('malformed successful mutation response does not imply rollback',async()=>{
  const f=fixture(()=>ok([{}]));assert.equal((await f.form.submit(input)).uncertain,true);assert.ok(f.form.pending)
})
test('overlapping access changes are suppressed',async()=>{
  let finish
  const f=fixture((path,args)=>new Promise(resolve=>{finish=()=>resolve(ok([event(args)]))}))
  const first=f.form.submit(input);while(!finish)await new Promise(r=>setTimeout(r,1))
  assert.equal((await f.form.submit(input)).outcome,'busy');finish();assert.equal((await first).outcome,'changed')
})
test('member pagination uses the last displayed subject and rejects foreign-team responses',async()=>{
  const f=fixture(()=>ok([{tenant_id:'shop-a',subject:'alice',role:'owner',revision},{tenant_id:'shop-a',subject:'ann',role:'member',revision:nextRevision}]))
  const api=teamAdministration(f.client),page=await api.members('',1)
  assert.equal(page.rows.length,1);assert.equal(page.next,'alice')
  assert.deepEqual(f.calls[0].args,{team_id:'shop-a',after_subject:'',page_size:1})
  await assert.rejects(api.members('',0))
  const other=fixture(()=>ok([{tenant_id:'shop-b',subject:'ann',role:'member',revision}]))
  await assert.rejects(teamAdministration(other.client).members())
})
test('audit parser omits unexpected payloads and rejects malformed times/roles',()=>{
  const record=event({request_key:key,target_subject:'ann',expected_revision:revision,new_role:'admin'})
  assert.deepEqual(parseMembershipEvent({...record,payload:{private:'secret'},extra:'secret'}),record)
  assert.throws(()=>parseMembershipEvent({...record,occurred_at:'not a date'}));assert.throws(()=>parseMembershipEvent({...record,after_role:'root'}))
})
