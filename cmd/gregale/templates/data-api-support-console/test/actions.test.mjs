import test from 'node:test'
import assert from 'node:assert/strict'
import {startDemo} from '../demo.mjs'
import {validateActions,permissions,actionInput} from '../actions.mjs'
import {seal,unseal} from '../auth.mjs'
async function login(demo,agent){
 const start=await fetch(demo.url+'/auth/login',{redirect:'manual'}),cookie=start.headers.getSetCookie()[0].split(';')[0],url=new URL(start.headers.get('location'));url.searchParams.set('agent',agent)
 const auth=await fetch(url,{redirect:'manual'}),callback=await fetch(auth.headers.get('location'),{redirect:'manual',headers:{Cookie:cookie}})
 return callback.headers.getSetCookie().find(v=>v.startsWith('support_session=')).split(';')[0]
}
test('approved support actions preview, require signed permission and origin, reject stale versions and retry idempotently',async t=>{
 const demo=await startDemo({port:0});t.after(demo.close);const a=await login(demo,'a'),b=await login(demo,'b')
 const post=(path,body,cookie=a,origin=demo.url)=>fetch(demo.url+'/api/actions/'+path,{method:'POST',headers:{Cookie:cookie,Origin:origin,'Content-Type':'application/json'},body:JSON.stringify(body)})
 assert.throws(()=>actionInput({action:'add_reply',keys:['x'.repeat(257),1],body:'reply'},{gregale_support_actions:['add_reply']},demo.definition))
 const input={action:'add_reply',keys:['shop-a',1],body:'Customer follow-up'}
 assert.equal((await post('preview',input,b)).status,403)
 assert.equal((await post('preview',input,a,'https://foreign.example')).status,403)
 assert.equal((await post('preview',{...input,keys:['shop-b',121]})).status,409)
 assert.equal((await post('preview',{...input,action:'sql'})).status,403)
 const reply=await(await post('preview',input)).json(),close=await(await post('preview',{action:'close_case',keys:['shop-a',1]})).json()
 const before=demo.data.replies.length
 const result=await(await post('confirm',{receipt:reply.receipt})).json();assert.equal(result.result.action,'add_reply')
 const again=await(await post('confirm',{receipt:reply.receipt})).json();assert.deepEqual(again,result);assert.equal(demo.data.replies.length,before+1)
 assert.equal((await post('confirm',{receipt:close.receipt})).status,409)
 assert.equal((await post('confirm',{receipt:reply.receipt},b)).status,409)
 const expired=unseal(reply.receipt,demo.config.key,'action');expired.expires=Date.now()-1;assert.equal((await post('confirm',{receipt:seal(expired,demo.config.key)})).status,409)
 const current=await(await post('preview',{action:'close_case',keys:['shop-a',1]})).json()
 assert.equal((await post('confirm',{receipt:current.receipt})).status,200);assert.equal(demo.data.cases[0].status,'closed')
 const history=await(await fetch(demo.url+'/api/actions/recent',{headers:{Cookie:a}})).json();assert.ok(history.actions.some(r=>r.id===reply.id&&r.state==='completed'));assert.ok(history.actions.some(r=>r.id===close.id&&r.state==='rejected'));assert.ok(!JSON.stringify(history).includes('Customer follow-up'))
 const signedAgain=await login(demo,'a');const recovered=await(await fetch(demo.url+'/api/actions/recent?id='+reply.id,{headers:{Cookie:signedAgain}})).json();assert.equal(recovered.actions[0].result.reply_id,result.result.reply_id)
 const foreign=await(await fetch(demo.url+'/api/actions/recent?id='+reply.id,{headers:{Cookie:b}})).json();assert.deepEqual(foreign.actions,[])
 assert.ok(demo.auditEvents.some(e=>e.action==='support.add_reply'&&e.status===200))
 assert.ok(!JSON.stringify(demo.auditEvents).includes('Customer follow-up'))
 const assignment=await(await post('preview',{action:'assign_case',keys:['shop-a',3]})).json();assert.equal((await post('confirm',{receipt:assignment.receipt})).status,200);assert.equal((await post('confirm',{receipt:assignment.receipt})).status,200)
 const mine=await(await fetch(demo.url+'/api/data?resource=cases&queue=mine',{headers:{Cookie:a}})).json();assert.deepEqual(mine.map(row=>row.id),[3]);assert.equal(demo.data.assignmentEvents.length,1)
 const unassigned=await(await fetch(demo.url+'/api/data?resource=cases&queue=unassigned&limit=100',{headers:{Cookie:a}})).json();assert.ok(!unassigned.some(row=>row.id===3));assert.ok(unassigned.every(row=>row.status==='open'&&row.assignee===null))
 const timeline=await(await fetch(demo.url+'/api/timeline?timeline=case_history&values='+encodeURIComponent('["shop-a",3]'),{headers:{Cookie:a}})).json();assert.ok(timeline.events.some(event=>event.title==='Case assigned'))
 const removal=await(await post('preview',{action:'unassign_case',keys:['shop-a',3]})).json();assert.equal((await post('confirm',{receipt:removal.receipt})).status,200)
 assert.equal((await(await fetch(demo.url+'/api/data?resource=cases&queue=mine',{headers:{Cookie:a}})).json()).length,0);assert.equal(demo.data.assignmentEvents.length,2)
 assert.equal((await fetch(demo.url+'/api/data?resource=replies&queue=mine',{headers:{Cookie:a}})).status,400)
 assert.ok(demo.auditEvents.some(event=>event.action==='support.assign_case'))
 const overdue=await(await fetch(demo.url+'/api/data?resource=cases&queue=overdue',{headers:{Cookie:a}})).json();assert.deepEqual(overdue.map(row=>row.id),[5])
 const soon=await(await fetch(demo.url+'/api/data?resource=cases&queue=due_soon',{headers:{Cookie:a}})).json();assert.deepEqual(soon.map(row=>row.id),[7])
 const priorityPreview=await(await post('preview',{action:'set_priority',keys:['shop-a',5],body:'urgent'})).json();const priorityResult=await(await post('confirm',{receipt:priorityPreview.receipt})).json();assert.equal(priorityResult.result.priority,'urgent');assert.equal((await post('confirm',{receipt:priorityPreview.receipt})).status,200);assert.equal(demo.data.priorityEvents.length,1)
 assert.equal((await post('preview',{action:'set_priority',keys:['shop-a',5],body:'invalid'})).status,400)
 assert.ok((await(await fetch(demo.url+'/api/timeline?timeline=case_history&values='+encodeURIComponent('["shop-a",5]'),{headers:{Cookie:a}})).json()).events.some(e=>e.title==='Priority changed to urgent'))
 const repliesBefore=demo.data.replies.length
 const note=await(await post('preview',{action:'add_note',keys:['shop-a',7],body:'Staff handover reasoning'})).json()
 assert.equal((await post('confirm',{receipt:note.receipt})).status,200);assert.equal((await post('confirm',{receipt:note.receipt})).status,200)
 assert.equal(demo.data.notes.length,1);assert.equal(demo.data.replies.length,repliesBefore);assert.equal(demo.data.cases.find(r=>r.id===7).first_response_at,null)
 assert.equal((await fetch(demo.url+'/api/data?resource=notes',{headers:{Cookie:b}})).status,403)
 const basic=await(await fetch(demo.url+'/api/config',{headers:{Cookie:b}})).json();assert.ok(!basic.resources.some(r=>r.id==='notes'));assert.ok(!basic.timelines.some(t=>t.sources.some(s=>s.resource==='notes')))
 const activity=await(await fetch(demo.url+'/api/timeline?timeline=case_history&values='+encodeURIComponent('["shop-a",7]'),{headers:{Cookie:a}})).json();assert.ok(activity.events.some(e=>e.label.includes('staff only')&&e.title==='Staff handover reasoning'))
 assert.ok(!JSON.stringify(demo.auditEvents).includes('Staff handover reasoning'))
 const noteRecovered=await(await fetch(demo.url+'/api/actions/recent?id='+note.id,{headers:{Cookie:a}})).json();assert.equal(noteRecovered.actions[0].state,'completed');assert.ok(!JSON.stringify(noteRecovered).includes('Staff handover reasoning'))
 const bad=structuredClone(demo.definition);bad.actions[0].version='internal_notes';assert.throws(()=>validateActions(bad));assert.deepEqual(permissions({gregale_support_actions:'add_reply'},demo.definition),[])
})
