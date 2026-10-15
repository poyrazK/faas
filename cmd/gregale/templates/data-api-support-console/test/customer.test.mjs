import test from 'node:test'
import assert from 'node:assert/strict'
import {startDemo} from '../demo.mjs'
import {validateCustomers,customerRequest} from '../customer.mjs'
async function login(demo,agent){
 const start=await fetch(demo.url+'/auth/login',{redirect:'manual'}),cookie=start.headers.getSetCookie()[0].split(';')[0],url=new URL(start.headers.get('location'));url.searchParams.set('agent',agent)
 const auth=await fetch(url,{redirect:'manual'}),callback=await fetch(auth.headers.get('location'),{redirect:'manual',headers:{Cookie:cookie}})
 return callback.headers.getSetCookie().find(v=>v.startsWith('support_session=')).split(';')[0]
}
test('customer lookup handles multiple matches, profiles intersect RLS and hide private columns',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close)
 const a=await login(demo,'a'),b=await login(demo,'b')
 const get=(cookie,params)=>fetch(demo.url+'/api/customer?'+new URLSearchParams(params),{headers:{Cookie:cookie}})
 assert.equal((await fetch(demo.url+'/api/customer?field=email&value=alex@example.test')).status,401)
 const matches=await(await get(a,{field:'email',value:'alex@example.test'})).json();assert.equal(matches.length,2);assert.ok(matches.every(r=>r.tenant==='shop-a'))
 const profile=await(await get(a,{keys:JSON.stringify(['shop-a',1])})).json();assert.equal(profile.row.name,'Alex Customer');assert.equal(profile.related[0].rows.length,5);assert.equal(profile.related[0].more,true);assert.ok(!JSON.stringify(profile).includes('private'));assert.ok(profile.related.every(g=>g.rows.every(r=>r.tenant==='shop-a')))
 assert.equal((await get(b,{keys:JSON.stringify(['shop-a',1])})).status,404)
 assert.equal((await get(a,{field:'internal_notes',value:'private'})).status,400)
 assert.equal((await get(a,{keys:'[{},1]'})).status,400)
 assert.equal((await get(a,{field:'email',value:'missing'})).status,200)
 assert.equal((await get(a,{field:'email',value:'x',keys:'[]'})).status,400)
 const bad=structuredClone(demo.definition);bad.customer.relationships=['private'];assert.throws(()=>validateCustomers(bad))
 assert.throws(()=>customerRequest(demo.config,demo.definition,new URLSearchParams({field:'email',value:'x',offset:'1'})))
 demo.data.customers.splice(0,1)
 assert.equal((await get(a,{keys:JSON.stringify(['shop-a',1])})).status,404)
})
