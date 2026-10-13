import test from 'node:test'
import assert from 'node:assert/strict'
import {startDemo} from '../demo.mjs'
async function signIn(demo,agent='a'){
 const login=await fetch(demo.url+'/auth/login',{redirect:'manual'}),cookie=login.headers.getSetCookie()[0].split(';')[0],url=new URL(login.headers.get('location'));url.searchParams.set('agent',agent)
 const authorized=await fetch(url,{redirect:'manual'}),callback=await fetch(authorized.headers.get('location'),{redirect:'manual',headers:{Cookie:cookie}})
 return {cookie:callback.headers.getSetCookie().find(v=>v.startsWith('support_session=')).split(';')[0]}
}
import {validateTimelines,timelineRequest} from '../timeline.mjs'
test('timeline merges sources, pages stable membership and rechecks current tenant access',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close)
 for(let i=3;i<=32;i++)demo.data.replies.push({tenant:'shop-a',customer_id:1,id:i,case_id:1,body:'Event '+i,occurred_at:'2026-01-02T00:00:00.000Z'})
 const a=await signIn(demo),b=await signIn(demo,'b')
 const params=new URLSearchParams({timeline:'case_history',values:JSON.stringify(['shop-a',1])})
 const get=async(cookie,p=params)=>fetch(demo.url+'/api/timeline?'+p,{headers:{Cookie:cookie}})
 assert.equal((await fetch(demo.url+'/api/timeline?'+params)).status,401)
 const first=await(await get(a.cookie)).json();assert.equal(first.events.length,25);assert.equal(first.total,33);assert.equal(first.next,25);assert.ok(first.events.every(e=>e.row.tenant==='shop-a'));assert.ok(!JSON.stringify(first).includes('private'))
 const next=new URLSearchParams(params);next.set('snapshot',first.snapshot);next.set('offset','25')
 assert.equal((await get(b.cookie,next)).status,409)
 const original=await(await get(a.cookie,next)).json()
 demo.data.replies.push({tenant:'shop-a',customer_id:1,id:99,case_id:1,body:'New event',occurred_at:'2026-01-03T00:00:00Z'})
 const removed=original.events.find(e=>e.resource==='replies');demo.data.replies.find(r=>'["replies","shop-a",'+r.id+']'===removed.id).tenant='shop-b'
 const updated=await(await get(a.cookie,next)).json();assert.equal(updated.total,33);assert.ok(!updated.events.some(e=>e.id===removed.id||e.title==='New event'))
 assert.deepEqual(updated.events.map(e=>e.id),original.events.filter(e=>e.id!==removed.id).map(e=>e.id))
 assert.equal((await(await get(b.cookie)).json()).events.length,0)
 next.set('scope','secret');assert.equal((await get(a.cookie,next)).status,400)
 assert.throws(()=>timelineRequest(demo.definition,new URLSearchParams({timeline:'case_history',values:'[{},1]'})))
 const invalid=structuredClone(demo.definition);invalid.timelines[0].sources[0].title='internal_notes';assert.throws(()=>validateTimelines(invalid))
 for(let i=100;i<201;i++)demo.data.replies.push({tenant:'shop-a',customer_id:1,id:i,case_id:1,body:'Overflow',occurred_at:'2026-01-01T00:00:00Z'})
 assert.equal((await get(a.cookie)).status,422)
})
