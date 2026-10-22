import {validateView,viewLink,linkedView,preferenceKey,loadViews} from './views.mjs'
import {parseExactJSON,exactJSON,decodeJsonCell,runCsvExport} from './csv.mjs'
const el=id=>document.getElementById(id),show=value=>{el('status').textContent=value}
let actionState=null
let customerOffset=0,customerMore=false,customerKeys=null
let timelineState=null
let personalViews=[],storageKey=null
let staff=null,definition=null,resource=null,offset=0,more=false,busy=false,scope=null,trail=[],controller=null
const context=()=>JSON.stringify([staff?.staff,staff?.tenant,resource?.id,el('search').value,el('filter').value,el('sort').value,el('direction').value,el('queue').value,scope])
function sync(){const locked=busy||!!actionState?.uncertain;for(const id of ['customer-field','customer-value','customer-find','customer-more','saved-view','view-name','save-view','delete-view','share-view','queue','resource','search','filter','sort','direction','read','export','export-limit','export-mode','back'])el(id).disabled=locked||!staff;el('previous').disabled=locked||!offset;el('next').disabled=locked||!more;el('stop').disabled=!busy||!controller;el('customer-more').disabled=locked||!staff||!customerMore}
function choices(){el('queue').value='';el('queue-label').hidden=definition.queues?.resource!==resource.id;for(const option of el('queue').options)if(['due_soon','overdue'].includes(option.value))option.hidden=!definition.queues?.deadline;closeAction();closeTimeline();el('resource-title').textContent=resource.label;el('search').placeholder='Search '+(resource.labels?.[resource.search]??resource.search).toLowerCase()+'…';el('filter').replaceChildren(new Option('All',''));for(const value of resource.filter?.values??[])el('filter').append(new Option(value,value));el('filter').parentElement.hidden=!resource.filter;el('sort').replaceChildren(...resource.columns.map(name=>new Option(resource.labels?.[name]??name,name)));el('sort').value=resource.keys[0];el('search').value='';el('direction').value='asc';el('scope').textContent=scope?'Related records from the selected row.':'';el('back').hidden=!trail.length;el('rows').replaceChildren();offset=0;more=false;sync()}
function query(pageOffset,limit,purpose){const params=new URLSearchParams({resource:resource.id,search:el('search').value,filter:el('filter').value,sort:el('sort').value,direction:el('direction').value,offset:String(pageOffset),limit:String(limit)});if(el('queue').value)params.set('queue',el('queue').value);if(purpose)params.set('purpose',purpose);if(scope)params.set('scope',JSON.stringify(scope));return '/api/data?'+params}
async function page(pageOffset,limit,signal,purpose){const response=await fetch(query(pageOffset,limit,purpose),{signal,redirect:'error'});if(response.status===401){staff=null;closeAction();closeAudit();el('workspace').hidden=true;el('welcome').hidden=false;el('logout').hidden=true;throw new Error('Your staff session expired. Sign in again.')}if(!response.ok){let error;try{error=await response.json()}catch{}throw new Error(error?.error??'Data read failed.')}const text=await response.text();if(new TextEncoder().encode(text).length>1048576)throw new Error('Data response exceeds bounds.');return parseExactJSON(text)}
function render(rows){const header=document.createElement('tr');for(const name of [...resource.columns,'Related records']){const th=document.createElement('th');th.scope='col';th.textContent=resource.labels?.[name]??name;header.append(th)}el('rows').replaceChildren(header);for(const row of rows){const tr=document.createElement('tr');for(const name of resource.columns){const td=document.createElement('td'),value=row[name];td.textContent=typeof value==='string'?value:value===null?'—':exactJSON(value);tr.append(td)}const actions=document.createElement('td');for(const relation of resource.relationships){let values;try{values=relation.from.map(name=>decodeJsonCell(exactJSON(row[name])));if(values.some(value=>value===null||typeof value==='object'))continue}catch{continue}const button=document.createElement('button');button.textContent=relation.label;button.addEventListener('click',()=>{if(busy||actionState?.uncertain)return;trail.push({resource:resource.id,scope,search:el('search').value,filter:el('filter').value,sort:el('sort').value,direction:el('direction').value,queue:el('queue').value,offset});scope={source:resource.id,relationship:relation.id,values};resource=definition.resources.find(item=>item.id===relation.target);el('resource').value=resource.id;choices();void read()});actions.append(button)}for(const timeline of definition.timelines??[]){if(timeline.resource!==resource.id)continue;let values;try{values=timeline.from.map(name=>decodeJsonCell(exactJSON(row[name])));if(values.some(value=>value===null||typeof value==='object'))continue}catch{continue}const button=document.createElement('button');button.textContent=timeline.label;button.addEventListener('click',()=>{if(busy||actionState?.uncertain)return;timelineState={id:timeline.id,values,snapshot:null,next:0};el('timeline-title').textContent=timeline.label;el('timeline-events').replaceChildren();el('timeline-panel').hidden=false;void timelinePage()});actions.append(button)}actions.append(actionControls(row,resource));tr.append(actions);el('rows').append(tr)}}
async function read(){if(busy||!staff)return;const captured=context();busy=true;sync();try{const rows=await page(offset,51,AbortSignal.timeout(20000));if(captured!==context())return;more=rows.length>50;render(rows.slice(0,50));el('page-status').textContent=Math.min(rows.length,50)+' records · starting at '+(offset+1);show(rows.length?'':'No records match this view.')}catch(error){show(error.message)}finally{busy=false;sync()}}
function savedChoices(){
 el('saved-view').replaceChildren(new Option('Choose a saved view',''))
 for(const [kind,views] of [['shared',definition.views??[]],['personal',personalViews]])for(const [index,view] of views.entries())el('saved-view').append(new Option((kind==='shared'?'Team · ':'Personal · ')+view.name,kind+':'+index))
}
function currentView(){if(scope)throw new Error('Return to an unscoped resource before saving or sharing.');return validateView({name:el('view-name').value,resource:resource.id,search:el('search').value,filter:el('filter').value,sort:el('sort').value,direction:el('direction').value,...(el('queue').value?{queue:el('queue').value}:{})},definition)}
function openView(view){view=validateView(view,definition);scope=null;trail=[];resource=definition.resources.find(r=>r.id===view.resource);el('resource').value=resource.id;choices();for(const key of ['search','filter','sort','direction','queue'])el(key).value=view[key]??'';el('queue').value=view.queue??'';el('view-name').value=view.name;el('view-link').replaceChildren();void read()}
el('saved-view').addEventListener('change',()=>{const [kind,index]=el('saved-view').value.split(':');const view=(kind==='shared'?definition.views:personalViews)?.[Number(index)];if(view)openView(view)})
el('save-view').addEventListener('click',()=>{try{const view=currentView(),next=personalViews.filter(v=>v.name!==view.name);next.push(view);if(next.length>20)throw new Error('Save at most 20 personal views.');localStorage.setItem(storageKey,JSON.stringify(next));personalViews=next;savedChoices();show('Saved on this browser for your staff account and tenant.')}catch(error){show(error.message)}})
el('delete-view').addEventListener('click',()=>{try{const [kind,index]=el('saved-view').value.split(':');if(kind!=='personal')throw new Error('Choose a personal view to delete.');const next=personalViews.filter((_,i)=>i!==Number(index));localStorage.setItem(storageKey,JSON.stringify(next));personalViews=next;savedChoices();show('Personal view deleted.')}catch(error){show(error.message)}})
el('share-view').addEventListener('click',()=>{try{const link=document.createElement('a');link.href=viewLink(location.origin,currentView(),definition);link.textContent=link.href;el('view-link').replaceChildren(document.createTextNode('This link includes your search and filters. Review them before sharing. Recipients use their own access. '),link)}catch(error){show(error.message)}})
el('resource').addEventListener('change',()=>{controller?.abort();scope=null;trail=[];resource=definition.resources.find(item=>item.id===el('resource').value);choices();void read()})
el('read').addEventListener('click',()=>{offset=0;void read()});el('search').addEventListener('keydown',event=>{if(event.key==='Enter'){offset=0;void read()}})
for(const id of ['search','filter','sort','direction','queue'])el(id).addEventListener('input',()=>{controller?.abort();offset=0;more=false;sync()})
el('previous').addEventListener('click',()=>{offset=Math.max(0,offset-50);void read()});el('next').addEventListener('click',()=>{offset+=50;void read()})
el('back').addEventListener('click',()=>{if(busy||!trail.length)return;const previous=trail.pop();resource=definition.resources.find(item=>item.id===previous.resource);scope=previous.scope;el('resource').value=resource.id;choices();for(const key of ['search','filter','sort','direction','queue'])el(key).value=previous[key]??'';offset=previous.offset;void read()})
el('logout').addEventListener('click',async()=>{controller?.abort();closeAction();closeAudit();closeTimeline();el('customer-profile').replaceChildren();el('customer-matches').replaceChildren();el('customer-tools').hidden=true;customerKeys=null;staff=null;el('recent-open').hidden=true;el('recent-panel').hidden=true;el('recent-rows').replaceChildren();el('audit-open').hidden=true;personalViews=[];storageKey=null;el('view-link').replaceChildren();el('saved-view').replaceChildren();definition=null;scope=null;trail=[];el('rows').replaceChildren();el('workspace').hidden=true;el('welcome').hidden=false;el('logout').hidden=true;el('staff').textContent='';await fetch('/auth/logout',{method:'POST'});show('Signed out.')})
el('stop').addEventListener('click',()=>controller?.abort())
el('export').addEventListener('click',async()=>{
 if(busy||!staff)return;const rowLimit=Number(el('export-limit').value),mode=el('export-mode').value;if(!Number.isInteger(rowLimit)||rowLimit<1||rowLimit>10000){show('Choose an export limit from 1 to 10,000.');return}
 const captured=context(),abort=new AbortController();controller=abort;busy=true;sync();const timer=setTimeout(()=>abort.abort(),120000)
 try{const result=await runCsvExport({columns:resource.columns,rowLimit,mode},{signal:abort.signal,current:()=>staff&&captured===context(),fetchPage:(offset,limit)=>page(offset,limit,AbortSignal.any([abort.signal,AbortSignal.timeout(20000)]),'export'),onProgress:progress=>show('Exporting '+progress.rows+' records…')});abort.signal.throwIfAborted();const url=URL.createObjectURL(new Blob([result.csv],{type:'text/csv;charset=utf-8'})),link=document.createElement('a');link.href=url;link.download='support-export.csv';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);show('Exported '+result.rows+' records'+(result.limited?' · maximum reached; more records remain':'')+'.')}catch(error){show(abort.signal.aborted?'Export cancelled. No file downloaded.':error.message)}finally{clearTimeout(timer);controller=null;busy=false;sync()}
})
document.querySelector('#welcome .button').addEventListener('click',event=>{if(location.hash){try{if(location.hash.length>3000)throw new Error('Saved view link is too large.');sessionStorage.setItem('gregale.support.pending-view',location.hash)}catch(error){event.preventDefault();show(error.message)}}})
try{const session=await fetch('/api/session');if(session.ok){staff=await session.json();const response=await fetch('/api/config');if(!response.ok)throw new Error('Cannot load approved support views.');definition=await response.json();el('access-mode').textContent=staff.actions?.length?'Approved support actions':'Read-only';el('recent-open').hidden=!staff.action_recovery;el('audit-open').hidden=!staff.audit_admin;el('staff').textContent=staff.staff;el('tenant').textContent=staff.tenant;el('logout').hidden=false;el('welcome').hidden=true;el('workspace').hidden=false;el('resource').replaceChildren(...definition.resources.map(r=>new Option(r.label,r.id)));resource=definition.resources[0];storageKey=preferenceKey(staff);personalViews=loadViews(localStorage,storageKey,definition);savedChoices();choices();setupCustomers();let view;try{const pending=sessionStorage.getItem('gregale.support.pending-view');sessionStorage.removeItem('gregale.support.pending-view');const hash=location.hash||pending;if(hash?.startsWith('#customer=')){const params=new URLSearchParams(hash.slice(1));if(params.getAll('customer').length!==1||[...params.keys()].some(k=>k!=='customer'))throw new Error('Invalid customer link.');await openCustomer(JSON.parse(params.get('customer')))}else view=linkedView(hash,definition)}catch(error){show(error.message);history.replaceState(null,'','/');throw error}if(view)openView(view);else await read()}else if(location.hash){if(location.hash.length>3000)throw new Error('Saved view link is too large.');sessionStorage.setItem('gregale.support.pending-view',location.hash)}}catch(error){show(error.message)}

window.addEventListener('hashchange',()=>{if(!staff||busy)return;try{if(location.hash.startsWith('#customer=')){const params=new URLSearchParams(location.hash.slice(1));if(params.getAll('customer').length!==1||[...params.keys()].some(k=>k!=='customer'))throw new Error('Invalid customer link.');void openCustomer(JSON.parse(params.get('customer')))}else{const view=linkedView(location.hash,definition);if(view)openView(view)}}catch(error){show(error.message)}})

function closeTimeline(){timelineState=null;el('timeline-panel').hidden=true;el('timeline-events').replaceChildren()}
async function timelinePage(){
 if(!timelineState||busy||!staff)return
 const captured=timelineState;busy=true;sync();el('timeline-next').disabled=true
 try{
  const params=new URLSearchParams({timeline:captured.id,values:JSON.stringify(captured.values),offset:String(captured.next)});if(captured.snapshot)params.set('snapshot',captured.snapshot)
  const response=await fetch('/api/timeline?'+params,{redirect:'error',signal:AbortSignal.timeout(20000)})
  if(!response.ok){if(response.status===401){closeTimeline();closeAudit()}const error=await response.json();throw new Error(error.error??'Timeline read failed.')}
  const result=parseExactJSON(await response.text());if(timelineState!==captured||!staff)return
  captured.snapshot=result.snapshot;captured.next=decodeJsonCell(exactJSON(result.next))
  for(const event of result.events){const item=document.createElement('li'),heading=document.createElement('p');heading.textContent=event.time+' · '+event.label+' · '+event.title;const detail=document.createElement('details'),summary=document.createElement('summary');summary.textContent='Open approved record';detail.append(summary);const r=definition.resources.find(r=>r.id===event.resource);for(const column of r.columns){const field=document.createElement('p');field.textContent=(r.labels?.[column]??column)+': '+(typeof event.row[column]==='string'?event.row[column]:exactJSON(event.row[column]));detail.append(field)}item.append(heading,detail);el('timeline-events').append(item)}
  show(result.events.length?'':'No currently accessible events on this page.')
 }catch(error){show(error.message)}finally{busy=false;sync();el('timeline-next').disabled=!timelineState||timelineState.next===null}
}
el('timeline-next').addEventListener('click',()=>void timelinePage())
el('timeline-close').addEventListener('click',closeTimeline)

function setupCustomers(){
 const c=definition.customer;el('customer-tools').hidden=!c;if(!c)return
 const r=definition.resources.find(r=>r.id===c.resource);el('customer-field').replaceChildren(...c.lookup.map(column=>new Option(r.labels?.[column]??column,column)))
}
async function customerRead(params){
 const response=await fetch('/api/customer?'+params,{redirect:'error',signal:AbortSignal.timeout(20000)})
 if(!response.ok){if(response.status===401){closeAudit();el('customer-profile').replaceChildren();el('customer-matches').replaceChildren();closeTimeline()}const error=await response.json();throw new Error(error.error??'Customer read failed.')}
 const text=await response.text();if(new TextEncoder().encode(text).length>1048576)throw new Error('Customer response exceeds bounds.');return parseExactJSON(text)
}
function fields(row,r){const box=document.createElement('dl');for(const column of r.columns){const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=r.labels?.[column]??column;dd.textContent=typeof row[column]==='string'?row[column]:exactJSON(row[column]);box.append(dt,dd)}return box}
async function findCustomers(){
 if(busy||actionState?.uncertain||!staff||!definition.customer)return;busy=true;sync();const identity=staff
 try{const rows=await customerRead(new URLSearchParams({field:el('customer-field').value,value:el('customer-value').value,offset:String(customerOffset)}));if(staff!==identity)return;customerMore=rows.length>25;const r=definition.resources.find(r=>r.id===definition.customer.resource);el('customer-matches').replaceChildren();el('customer-profile').replaceChildren();closeTimeline()
 for(const row of rows.slice(0,25)){const button=document.createElement('button');button.textContent=String(row[definition.customer.title])+' · '+r.keys.map(k=>typeof row[k]==='string'?row[k]:exactJSON(row[k])).join(' / ');let keys;try{keys=r.keys.map(k=>decodeJsonCell(exactJSON(row[k])))}catch{button.disabled=true}button.addEventListener('click',()=>void openCustomer(keys));el('customer-matches').append(button)}
 show(rows.length?(rows.length>1?'Choose a customer from the matching records.':'One customer matches. Open their profile.'):'No customer matches under your permissions.')
 }catch(error){show(error.message)}finally{busy=false;sync()}
}
async function openCustomer(keys){
 if(!definition.customer)throw new Error('Customer profiles are not configured.');if(busy||actionState?.uncertain||!staff)return;busy=true;sync();const identity=staff;el('customer-profile').replaceChildren();closeTimeline()
 try{const result=await customerRead(new URLSearchParams({keys:JSON.stringify(keys)}));if(staff!==identity)return;customerKeys=keys
 const c=definition.customer,r=definition.resources.find(r=>r.id===c.resource),heading=document.createElement('h2');heading.textContent=String(result.row[c.title]);el('customer-profile').append(heading,fields(result.row,r))
 const link=document.createElement('a'),url=new URL('/',location.origin);url.hash=new URLSearchParams({customer:JSON.stringify(keys)}).toString();link.href=url.href;link.textContent='Customer link (includes identifiers; recipients use their own access)';el('customer-profile').append(link)
 for(const group of result.related){const rel=r.relationships.find(rel=>rel.id===group.relationship),target=definition.resources.find(r=>r.id===group.resource),title=document.createElement('h3');title.textContent=rel.label;el('customer-profile').append(title);for(const row of group.rows)el('customer-profile').append(fields(row,target),actionControls(row,target));const button=document.createElement('button');button.textContent=group.more?'Browse all '+rel.label:'Browse '+rel.label;button.addEventListener('click',()=>{if(busy||actionState?.uncertain)return;scope={source:r.id,relationship:rel.id,values:rel.from.map(column=>decodeJsonCell(exactJSON(result.row[column])))};trail=[];resource=target;el('resource').value=target.id;choices();void read()});el('customer-profile').append(button)}
 if(c.timeline){const button=document.createElement('button');button.textContent='Open customer timeline';button.addEventListener('click',()=>{if(busy||actionState?.uncertain)return;const t=definition.timelines.find(t=>t.id===c.timeline);timelineState={id:t.id,values:t.from.map(column=>decodeJsonCell(exactJSON(result.row[column]))),snapshot:null,next:0};el('timeline-title').textContent=t.label;el('timeline-events').replaceChildren();el('timeline-panel').hidden=false;void timelinePage()});el('customer-profile').append(button)}
 show('')
 }catch(error){show(error.message)}finally{busy=false;sync()}
}
el('customer-find').addEventListener('click',()=>{customerOffset=0;void findCustomers()})
el('customer-more').addEventListener('click',()=>{customerOffset+=25;void findCustomers()})
for(const id of ['customer-value','customer-field'])el(id).addEventListener('input',()=>{customerMore=false;customerOffset=0;sync()})

function closeAudit(){el('audit-panel').hidden=true;el('audit-rows').replaceChildren();el('audit-open').hidden=!staff?.audit_admin}
function auditParams(){const params=new URLSearchParams();for(const key of ['subject','action','outcome','from','to']){const value=el('audit-'+key).value;if(value)params.set(key,['from','to'].includes(key)?new Date(value).toISOString():value)}return params}
async function openAudit(){
 if(!staff?.audit_admin||busy)return;busy=true;sync();const identity=staff
 try{const response=await fetch('/api/audit?'+auditParams(),{redirect:'error',signal:AbortSignal.timeout(10000)});if(!response.ok){closeAudit();const error=await response.json();throw new Error(error.error??'Audit access refused.')}const result=await response.json();if(staff!==identity)return
 el('audit-panel').hidden=false;el('audit-retention').textContent=result.scope+' Retention: '+result.retention_days+' days. '+(result.delivery?.mode==='postgres'?'PostgreSQL delivery: '+result.delivery.pending+' queued events'+(result.delivery.last_failure?' · delivery failed; retrying.':' · no reported failure.'):'Recent in-memory history: maximum '+result.max_events+' events on this instance.')
 const columns=['time','subject','tenant','action','resource','outcome','status'],head=document.createElement('tr');for(const column of columns){const th=document.createElement('th');th.textContent=column;head.append(th)}el('audit-rows').replaceChildren(head)
 for(const event of result.events){const row=document.createElement('tr');for(const column of columns){const td=document.createElement('td');td.textContent=String(event[column]??'');row.append(td)}el('audit-rows').append(row)}
 }catch(error){show(error.message)}finally{busy=false;sync()}
}
el('audit-open').addEventListener('click',()=>void openAudit())
el('audit-refresh').addEventListener('click',()=>void openAudit())
el('audit-close').addEventListener('click',closeAudit)

el('audit-export').addEventListener('click',async()=>{
 if(busy||!staff?.audit_admin)return;busy=true;sync();const identity=staff
 try{const params=auditParams();params.set('format','csv');const response=await fetch('/api/audit?'+params,{redirect:'error',signal:AbortSignal.timeout(20000)});if(!response.ok){const error=await response.json();throw new Error(error.error??'Audit export failed.')}const blob=await response.blob();if(staff!==identity)return;const url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='support-audit.csv';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);show('Exported redacted audit events.')
 }catch(error){show(error.message)}finally{busy=false;sync()}
})

function closeAction(){actionState=null;el('action-panel').hidden=true;el('action-body').value='';el('action-summary').replaceChildren();el('action-confirm').disabled=true;el('action-cancel').disabled=false;el('action-priority').disabled=el('action-body').disabled=false;el('action-preview').disabled=false}
function actionControls(row,r){
 const box=document.createElement('span')
 for(const action of definition.actions??[]){if(action.resource!==r.id||!staff?.actions?.includes(action.id))continue
 const button=document.createElement('button');button.textContent=action.label
 button.addEventListener('click',()=>{if(busy||actionState?.uncertain)return;closeAction();let keys;try{keys=r.keys.map(k=>decodeJsonCell(exactJSON(row[k])))}catch{show('Action keys cannot be represented safely.');return}actionState={action,keys,receipt:null};el('action-title').textContent=action.label;el('priority-label').hidden=action.id!=='set_priority';el('action-priority').value=row.priority??'normal';el('reply-label').hidden=!['add_reply','add_note'].includes(action.id);el('note-warning').hidden=action.id!=='add_note';el('action-body-label').textContent=action.id==='add_note'?'Internal note':'Customer reply';el('action-panel').hidden=false})
 box.append(button)}
 return box
}
async function actionPost(path,body){
 const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),redirect:'error',signal:AbortSignal.timeout(20000)})
 const result=await response.json();if(!response.ok){const error=new Error(result.error??'Action failed.');error.status=response.status;throw error}return result
}
el('action-body').addEventListener('input',()=>{if(actionState&&!actionState.uncertain)actionState.receipt=null;el('action-summary').replaceChildren();el('action-confirm').disabled=true})
el('action-cancel').addEventListener('click',()=>{if(!busy&&!actionState?.uncertain)closeAction()})
el('action-preview').addEventListener('click',async()=>{
 if(busy||!actionState||actionState.uncertain||!staff)return;const captured=actionState,identity=staff;busy=true;sync();el('action-priority').disabled=el('action-body').disabled=true;el('action-preview').disabled=true;el('action-confirm').disabled=true
 try{const result=await actionPost('/api/actions/preview',{action:captured.action.id,keys:captured.keys,...(['add_reply','add_note'].includes(captured.action.id)?{body:el('action-body').value}:captured.action.id==='set_priority'?{body:el('action-priority').value}:{})});if(actionState!==captured||staff!==identity)return
 captured.receipt=result.receipt;captured.id=result.id;el('action-summary').textContent=result.preview.label+' · Case '+result.preview.case_id+' · Version '+result.preview.version+(result.preview.body?(captured.action.id==='set_priority'?' · Priority: ':captured.action.id==='add_note'?' · Internal note: ':' · Reply: ')+result.preview.body:'')+' · Action ID: '+result.id+' · Confirmation expires in five minutes.';el('action-confirm').disabled=false;show('Review the preview, then confirm.')
 }catch(error){captured.receipt=null;show(error.message)}finally{busy=false;sync();el('action-priority').disabled=el('action-body').disabled=false;el('action-preview').disabled=false}
})
el('action-confirm').addEventListener('click',async()=>{
 if(busy||!actionState?.receipt||!staff)return;const captured=actionState,identity=staff;busy=true;sync();el('action-confirm').disabled=true;el('action-preview').disabled=true;el('action-priority').disabled=el('action-body').disabled=true
 try{const result=await actionPost('/api/actions/confirm',{receipt:captured.receipt});if(actionState!==captured||staff!==identity)return;closeAction();show('Action completed: '+result.result.action+'. Refresh the case or customer profile to see current data.')}
 catch(error){if(error.status===409||error.status===403){captured.receipt=null;show(error.message)}else{captured.uncertain=true;show(error.message+' Retry this same confirmation to resolve an uncertain outcome.')}}
 finally{busy=false;sync();el('action-preview').disabled=!!actionState?.uncertain;el('action-priority').disabled=el('action-body').disabled=!!actionState?.uncertain;el('action-cancel').disabled=!!actionState?.uncertain;el('action-confirm').disabled=!actionState?.receipt}
})

window.addEventListener('beforeunload',event=>{if(actionState?.uncertain){event.preventDefault();event.returnValue=''}})

async function recentActions(){
 if(busy||!staff?.action_recovery)return;busy=true;sync();const identity=staff
 try{const response=await fetch('/api/actions/recent'+(el('recent-id').value?'?'+new URLSearchParams({id:el('recent-id').value}):''),{redirect:'error',signal:AbortSignal.timeout(10000)});if(!response.ok)throw new Error('Action recovery is unavailable. No action was resubmitted.');const result=await response.json();if(staff!==identity)return
 el('recent-panel').hidden=false;el('recent-rows').replaceChildren()
 for(const record of result.actions){const item=document.createElement('p');item.textContent=record.time+' · '+record.action+' · '+record.state+' · '+record.id+(record.result?' · '+JSON.stringify(record.result):'');el('recent-rows').append(item)
 if(actionState?.id===record.id&&['completed','rejected'].includes(record.state)){closeAction();show(record.state==='completed'?'Recovered completed action. No mutation was resubmitted.':'Recovered rejected action. Review current data before a new preview.')}}
 if(!result.actions.length)el('recent-rows').textContent='No recent actions for your account and tenant.'
 }catch(error){show(error.message)}finally{busy=false;sync()}
}
el('recent-open').addEventListener('click',()=>void recentActions())
el('recent-refresh').addEventListener('click',()=>void recentActions())
el('recent-close').addEventListener('click',()=>{el('recent-panel').hidden=true;el('recent-rows').replaceChildren()})

el('action-priority').addEventListener('change',()=>{if(actionState&&!actionState.uncertain)actionState.receipt=null;el('action-summary').replaceChildren();el('action-confirm').disabled=true})
