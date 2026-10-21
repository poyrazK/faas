import test from 'node:test'
import assert from 'node:assert/strict'
import {auditLog,auditSettings} from '../audit.mjs'
import {auditFilters,auditCSV} from '../audit-postgres.mjs'
import {startDemo} from '../demo.mjs'
async function login(demo,agent){
 const start=await fetch(demo.url+'/auth/login',{redirect:'manual'}),cookie=start.headers.getSetCookie()[0].split(';')[0],url=new URL(start.headers.get('location'));url.searchParams.set('agent',agent)
 const auth=await fetch(url,{redirect:'manual'}),callback=await fetch(auth.headers.get('location'),{redirect:'manual',headers:{Cookie:cookie}})
 return callback.headers.getSetCookie().find(v=>v.startsWith('support_session=')).split(';')[0]
}
test('audit retains bounded redacted events and scopes administrator access',()=>{
 let now=Date.now();const sink=[],log=auditLog({admins:[{subject:'admin',tenant:'one'}],days:1,limit:2},event=>sink.push(event),()=>now)
 const claims={sub:'admin',tenant:'one',token:'secret'}
 assert.equal(log.admin(claims),true);assert.equal(log.admin({...claims,tenant:'two'}),false)
 for(let i=0;i<3;i++)log.record({claims,action:'customer.lookup',status:200,search:'private',row:{secret:true}})
 assert.equal(log.read(claims).length,2);assert.ok(!JSON.stringify(sink).includes('secret'));assert.deepEqual(log.read({...claims,tenant:'two'}),[])
 sink[0].subject='tampered';assert.equal(log.read(claims)[0].subject,'admin')
 now+=86400001;assert.equal(log.read(claims).length,0)
 assert.throws(()=>auditSettings({SUPPORT_AUDIT_RETENTION_DAYS:'0'}));assert.throws(()=>auditSettings({SUPPORT_AUDIT_ADMINS:'[{"subject":"x"}]'}))
})
test('support endpoints emit redacted outcomes; only approved tenant administrator reads audit',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close);const a=await login(demo,'a'),b=await login(demo,'b')
 const get=(path,cookie=a)=>fetch(demo.url+path,{headers:{Cookie:cookie}})
 await get('/api/customer?field=email&value=alex@example.test')
 await get('/api/customer?keys='+encodeURIComponent('["shop-a",1]'))
 await get('/api/timeline?timeline=case_history&values='+encodeURIComponent('["shop-a",1]'))
 await get('/api/data?resource=cases&purpose=export')
 await get('/api/customer?field=secret&value=private')
 assert.equal((await get('/api/audit',b)).status,403)
 await get('/api/data?resource=cases',b)
 assert.equal((await fetch(demo.url+'/api/audit')).status,401)
 const response=await get('/api/audit');assert.equal(response.status,200);const result=await response.json()
 assert.ok(result.events.every(e=>e.tenant==='shop-a'));assert.ok(result.events.some(e=>e.action==='customer.lookup'&&e.status===400))
 for(const action of ['customer.lookup','customer.profile','customer.timeline','data.export_page'])assert.ok(result.events.some(e=>e.action===action&&e.outcome==='success'))
 assert.ok(demo.auditEvents.some(e=>e.action==='audit.read'&&e.outcome==='denied'&&e.tenant==='shop-b'))
 const text=JSON.stringify(demo.auditEvents);for(const secret of ['alex@example.test','private','support_session','Bearer','Tracking information','shop-a",1'])assert.ok(!text.includes(secret))
 const filtered=await(await get('/api/audit?action=customer.profile')).json();assert.ok(filtered.events.every(e=>e.action==='customer.profile'))
 const csv=await get('/api/audit?format=csv&action=customer.profile');assert.equal(csv.status,200);assert.ok((await csv.text()).includes('customer.profile'));assert.equal((await get('/api/audit?tenant=shop-b')).status,400)
 assert.equal((await get('/api/data?resource=cases&purpose=other')).status,400)
})

test('audit filters and exports are bounded, parameterized inputs and spreadsheet safe',()=>{
 assert.throws(()=>auditFilters(new URLSearchParams('tenant=foreign')))
 assert.throws(()=>auditFilters(new URLSearchParams('subject=a&subject=b')))
 assert.throws(()=>auditFilters(new URLSearchParams('from=bad')))
 assert.throws(()=>auditFilters(new URLSearchParams('outcome=unknown')))
 assert.deepEqual(auditFilters(new URLSearchParams('subject=staff&outcome=success')),{subject:'staff',outcome:'success'})
 assert.ok(auditCSV([{subject:'=formula',tenant:'one',status:200}]).includes("'=formula"))
})
