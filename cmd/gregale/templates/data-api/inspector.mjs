import {emptyHistory,retainBulkHistory,validateBulkHistory,historyLimits} from './bulk-history.mjs'
import {bulkPlan,bulkTarget,runBulkWrite,retryBulkEntries,bulkLimits,bulkReport,reconciliationEntries,reconciliationTarget,reconcileBulk,preflightTarget,preflightModel,runBulkPreflight,matchingQuery,collectMatchingRows} from './bulk-write.mjs'
import {csvExportPlan,exportTarget,runCsvExport,parseExactJSON,exportLimits} from './csv-export.mjs'
import {parseImportCsv,previewCsvImport,runCsvImport,retryableImportRows,importSummary,csvLimits} from './csv-import.mjs'
import {mutationScaffold,lifecycleScaffold} from './guided-mutations.mjs'
import {accessScaffold,validateGuidedAccess} from './guided-access.mjs'
import { validateRecord, keyedEdit, editTarget, relationshipLinks, validateViews, viewProblem } from './inspector-data.mjs'
import { replayRequest, collectionScenarios } from './request-replay.mjs'
const element = id => document.getElementById(id)
const key = location.hash.slice(1)
history.replaceState(null, '', location.pathname)
let session, selected = '', fingerprint = '', busy = false
const show = (id, value) => { element(id).textContent = typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function showResource(kind, resource) {
  const computedRelationships = session.snapshot.functions.filter(f => f.schema === resource.schema && f.setof?.from === resource.name)
  show('schema', kind === 'tables' && computedRelationships.length ? { ...resource, computedRelationships: computedRelationships.map(f => ({ name: f.name, target: f.setof.to, one_to_one: f.setof.one_to_one })) } : resource)
  const json = kind === 'tables' && resource.columns.find(c => c.type === 'Json' && !/["\\\u0000-\u001f\u007f]/.test(c.name))
  element('json-help').hidden = !json
  if (json) show('json-example', `select=status:"${json.name}"->>status,details:"${json.name}"->details\n${json.name}->>status=eq.paid\nselectJson(db.from(${JSON.stringify(resource.name)}), '', { status: jsonText(${JSON.stringify(json.name)}, 'status') })`)
}
function selectResource(preserveNavigation=false) {
  if(preserveNavigation!==true){dataScope=null;dataTrail=[];activeView=null}
  selected = element('resource').value
  const [kind, name] = JSON.parse(selected)
  const resource = session.snapshot[kind].find(item => `${item.schema}.${item.name}` === name)
  showResource(kind, resource)
  element('path').value = `/rest/v1/${kind === 'functions' ? 'rpc/' : ''}${encodeURIComponent(resource.name)}${kind === 'tables' ? '?select=*' : ''}`
  element('method').value = kind === 'functions' ? 'POST' : 'GET'
  element('body').value = '{}'
  renderData()
}
element('resource').addEventListener('change', selectResource)
async function refresh() {
  try {
    const response = await fetch('session', { headers: { Authorization: `Bearer ${key}` }, cache: 'no-store' })
    if (!response.ok) throw new Error('Open the inspector URL printed by gregale data-api dev to connect.')
    const previousContext=session && JSON.stringify([session.fingerprint,session.input_model?.revision,element('identity').value])
    session = await response.json()
    const identity = element('identity').value
    element('identity').replaceChildren()
    for (const [name, value] of Object.entries(session.identities)) {
      const option = document.createElement('option'); option.value = name; option.textContent = `${name} (${value.subject})`; element('identity').append(option)
    }
    if (Object.hasOwn(session.identities, identity)) element('identity').value = identity
    show('status', `${session.status} · API ${session.ready ? 'ready' : 'paused'} · client checks ${session.client_checked ? 'passed' : 'not verified'}`)
    show('detail', session.detail ?? '')
    show('fingerprint', `Schema: ${session.fingerprint}`)
    show('compatibility', session.compatibility ?? 'Initial local schema; no comparison yet.')
    if (fingerprint !== session.fingerprint) {
      fingerprint = session.fingerprint
      element('resource').replaceChildren()
      for (const kind of ['tables', 'functions']) for (const resource of session.snapshot[kind]) {
        const option = document.createElement('option')
        option.value = JSON.stringify([kind, `${resource.schema}.${resource.name}`]); option.textContent = `${kind === 'tables' ? 'Table' : resource.setof?.from ? 'RPC + relationship' : 'RPC'} · ${resource.schema}.${resource.name}`
        element('resource').append(option)
      }
      if ([...element('resource').options].some(option => option.value === selected)) {
        element('resource').value = selected
        const [kind, name] = JSON.parse(selected)
        showResource(kind, session.snapshot[kind].find(item => `${item.schema}.${item.name}` === name))
      } else if (element('resource').value) selectResource()
    }
    const nextContext=JSON.stringify([session.fingerprint,session.input_model?.revision,element('identity').value])
    if(previousContext!==nextContext){dataScope=null;dataTrail=[];activeView=null;renderData();if(viewCollection)renderViewOptions()}
    syncData()
    element('send').disabled = !session.ready || busy || csvBusy || exportBusy || bulkBusy || (readOnlyIdentity()&&!['GET','HEAD'].includes(element('method').value))
  } catch (error) { show('status', error.message); element('send').disabled = true; if(session)session={...session,ready:false};syncData() }
}
element('send').addEventListener('click', async () => {
  if(readOnlyIdentity()&&!['GET','HEAD'].includes(element('method').value)){show('result-status','This identity or view is read-only.');return}
  busy = true; element('send').disabled = true
  try {
    const path = element('path').value
    const target = new URL(path, location.origin)
    if (target.origin !== location.origin || !target.pathname.startsWith('/rest/v1/') || target.username || target.password || target.hash) throw new Error('Use a local /rest/v1/ API path.')
    const expectedFingerprint = element('expected-fingerprint').value.trim()
    if (expectedFingerprint && !/^[a-f0-9]{64}$/.test(expectedFingerprint)) throw new Error('Use a 64-character lowercase schema fingerprint.')
    if (expectedFingerprint) target.pathname = `/__gregale/contract/${expectedFingerprint}${target.pathname}`
    const method = element('method').value
    const body = ['POST', 'PATCH'].includes(method) ? JSON.stringify(JSON.parse(element('body').value)) : undefined
    const response = await fetch(target, { method, body, signal: AbortSignal.timeout(30000), headers: { Authorization: `Bearer ${session.identities[element('identity').value].token}`, 'Content-Type': 'application/json', Prefer: 'return=representation' } })
    show('result-status', `${response.status} ${response.statusText} · request ${response.headers.get('x-request-id') ?? '—'}`)
    const text = await response.text()
    try { show('response', JSON.parse(text)) } catch { show('response', text) }
  } catch (error) { show('result-status', error.message); show('response', '') }
  finally { busy = false; syncData() }
})

let dataOffset=0,dataBusy=false,dataPlan=null,dataMore=false,editingRow=null,dataScope=null,dataTrail=[],activeView=null,viewCollection=null,viewBusy=false,accessRevision=null,accessBusy=false
let csvDataset=null,csvPlan=null,csvBusy=false,csvController=null,csvHistory=null,csvKey='',csvGeneration=0,csvPage=0
let exportBusy=false,exportController=null
let bulkBusy=false,bulkController=null,bulkSelected=new Map(),bulkReview=null,bulkHistory=null,bulkLastReport=null,bulkReadiness=null,bulkReconciled=null
let operationHistory=null,historyBusy=false,historyWrites=Promise.resolve(),sessionRuns=new Map()
const readOnlyIdentity=()=>Boolean(session?.identities[element('identity').value]?.read_only||activeView?.mode==='read_only')
const visibleColumns=()=>[...element('data-columns').selectedOptions].map(option=>option.value)
const tableResource=()=>{
 try{const [kind,name]=JSON.parse(element('resource').value);return kind==='tables'?session?.snapshot.tables.find(table=>`${table.schema}.${table.name}`===name):undefined}catch{return undefined}
}
const insertModel=()=>{const table=tableResource();return table&&!table.view&&!readOnlyIdentity()?session?.input_model?.tables.find(model=>model.schema===table.schema&&model.table===table.name):undefined}
const editorModel=()=>{const model=insertModel();return !model||!editingRow?model:{...model,fields:model.updateFields.filter(field=>!model.primaryKey.columns.includes(field.name)&&field.name!==model.concurrency.column),spec:model.updateSpec}}
const dataContext=()=>JSON.stringify([element('resource').value,element('identity').value,session?.fingerprint,session?.input_model?.revision,dataScope,visibleColumns(),element('data-sort').value,element('data-direction').value])
function invalidatePlan(){dataPlan=null;show('data-plan','');element('data-approved').checked=false;syncData()}
function syncData(){
 const table=tableResource(),ready=Boolean(session?.ready&&!dataBusy)
 show('data-access',readOnlyIdentity()?'Read-only support access · database grants and RLS still govern visible data.':'')
 element('data-new').disabled=readOnlyIdentity()
 for(const id of ['access-mutations','access-lifecycle'])element(id).hidden=readOnlyIdentity()
 element('view-save').disabled=!ready||viewBusy||!viewCollection||!table
 for(const id of ['view-load','view-delete'])element(id).disabled=!ready||viewBusy||!viewCollection
 element('view-reload').disabled=viewBusy
 for(const id of ['access-generate','access-mutations','access-lifecycle','access-save','access-run'])element(id).disabled=!ready||accessBusy
 element('access-reload').disabled=accessBusy
 syncCsv();syncExport();syncBulk();syncOperationHistory()
 element('data-back').disabled=!ready||!dataTrail.length
 show('data-scope',dataScope?.label??'')
 element('data-read').disabled=!table||!ready
 element('data-prev').disabled=!table||!ready||dataOffset===0
 element('data-next').disabled=!table||!ready||!dataMore
 element('data-review').disabled=!insertModel()||!ready
 element('data-insert').disabled=!dataPlan||dataPlan.context!==dataContext()||!element('data-approved').checked||!ready
 element('data-approved').disabled=!dataPlan||!ready
}
function renderData(reset=true){
 exportController?.abort();resetBulk();resetCsvContext()
 if(reset){editingRow=null;dataOffset=0;dataMore=false}
 invalidatePlan()
 show('data-title',editingRow?'Edit selected row':'Create a row')
 if(reset){element('data-rows').replaceChildren();show('data-status','')}show('data-write-status','')
 element('data-filter').replaceChildren();const none=document.createElement('option');none.value='';none.textContent='No filter';element('data-filter').append(none)
 for(const column of tableResource()?.columns??[]){if(!/^[A-Za-z_][A-Za-z0-9_]*$/.test(column.name)||['select','limit','offset','order','and','or','not'].includes(column.name))continue;const option=document.createElement('option');option.value=column.name;option.textContent=column.name;element('data-filter').append(option)}
 if(reset){
  element('data-columns').replaceChildren();element('data-sort').replaceChildren()
  const none=document.createElement('option');none.value='';none.textContent='Database order';element('data-sort').append(none)
  for(const column of tableResource()?.columns??[]){const option=document.createElement('option');option.value=column.name;option.textContent=column.name;option.selected=true;element('data-columns').append(option);if(/^[A-Za-z_][A-Za-z0-9_]*$/.test(column.name)&&!['select','limit','offset','order','and','or','not'].includes(column.name)){const sort=document.createElement('option');sort.value=column.name;sort.textContent=column.name;element('data-sort').append(sort)}}
 }
 element('data-fields').replaceChildren()
 const model=editorModel()
 if(!model){show('data-write-status',readOnlyIdentity()?'This identity or view is read-only. Browse, search and export visible rows.':session?.input_model?.unavailable?'Insert forms exceed local model limits. Use the request explorer.':'Select a writable table to create a row. Views and RPCs are browsed through reads or the request explorer.');syncData();return}
 model.fields.forEach((field,index)=>{
  const container=document.createElement('p'),label=document.createElement('label'),mode=document.createElement('select')
  label.textContent=`${field.name}${field.required?' (required)':''} `
  for(const value of [...(!field.required?['omit']:[]),'value',...(field.nullable?['null']:[])]){const option=document.createElement('option');option.value=value;option.textContent=value==='omit'?'Omit / database default':value==='null'?'NULL':'Provide value';mode.append(option)}
  mode.id=`data-mode-${index}`
  const input=field.control==='select'?document.createElement('select'):field.control==='custom'?document.createElement('textarea'):document.createElement('input')
  input.id=`data-field-${index}`;input.dataset.field=field.name
  if(field.control==='select')for(const value of field.options??[]){const option=document.createElement('option');option.value=value;option.textContent=value;input.append(option)}
  if(input.tagName==='INPUT'){input.type=field.control==='number'?'number':field.control==='checkbox'?'checkbox':'text';if(field.min!==undefined)input.min=String(field.min);if(field.max!==undefined)input.max=String(field.max);if(field.control==='number')input.step=String(field.step??'any')}
  const error=document.createElement('span');error.id=`data-error-${index}`;error.setAttribute('role','alert');input.setAttribute('aria-describedby',error.id)
  const update=()=>{input.disabled=mode.value!=='value';invalidatePlan()};mode.addEventListener('change',update);input.addEventListener('input',invalidatePlan);input.addEventListener('change',invalidatePlan)
  if(editingRow&&Object.hasOwn(editingRow.row,field.name)){const value=editingRow.row[field.name];if(field.control==='checkbox')input.checked=Boolean(value);else input.value=field.control==='custom'?JSON.stringify(value):value===null?'':String(value)}
  input.disabled=mode.value!=='value';label.append(mode,input);container.append(label,error)
  if(field.control==='custom'){const hint=document.createElement('small');hint.textContent=' Enter JSON; review complex and domain rules in your database.';container.append(hint)}
  if(field.maxLength!==undefined){const hint=document.createElement('small');hint.textContent=` Up to ${field.maxLength} Unicode characters.`;container.append(hint)}
  if(field.requiresReview){const hint=document.createElement('small');hint.textContent=' Additional database rules require review.';container.append(hint)}
  element('data-fields').append(container)
 });syncData()
}
async function readRows(){
 const table=tableResource();if(!table||dataBusy||!session?.ready)return
 resetBulk();
 const context=JSON.stringify([dataContext(),element('data-filter').value,element('data-operator').value,element('data-value').value,dataOffset]);dataBusy=true;syncData()
 try{
  const target=new URL(`${activeView?`/__gregale/contract/${activeView.fingerprint}`:''}/rest/v1/${encodeURIComponent(table.name)}`,location.origin);target.searchParams.set('select',table.columns.map(column=>JSON.stringify(column.name)).join(','));target.searchParams.set('limit','51');target.searchParams.set('offset',String(dataOffset))
  const sort=element('data-sort').value,direction=element('data-direction').value
  if(sort){if(![...element('data-sort').options].some(option=>option.value===sort)||!['asc','desc'].includes(direction))throw new Error('Choose supported sorting.');target.searchParams.set('order',`${sort}.${direction}`)}
  if(!visibleColumns().length)throw new Error('Select at least one visible column.')
  const column=element('data-filter').value,operator=element('data-operator').value
  if(column){if(![...element('data-filter').options].some(option=>option.value===column)||!['eq','neq','gt','gte','lt','lte','is','ilike'].includes(operator))throw new Error('Choose a supported filter.');target.searchParams.append(column,`${operator}.${element('data-value').value}`)}
  for(const [name,value] of dataScope?.predicates??[])target.searchParams.append(name,value)
  const response=await fetch(target,{signal:AbortSignal.timeout(30000),headers:{Authorization:`Bearer ${session.identities[element('identity').value].token}`,'Accept-Profile':table.schema}})
  if(context!==JSON.stringify([dataContext(),element('data-filter').value,element('data-operator').value,element('data-value').value,dataOffset]))return
  if(!response.ok)throw new Error(`Read refused (${response.status}). Check identity and filters.`)
  const rows=await response.json();if(context!==JSON.stringify([dataContext(),element('data-filter').value,element('data-operator').value,element('data-value').value,dataOffset]))return;if(!Array.isArray(rows))throw new Error('Unexpected row response.')
  dataMore=rows.length>50;element('data-rows').replaceChildren()
  const header=document.createElement('tr');for(const column of table.columns.filter(column=>visibleColumns().includes(column.name))){const cell=document.createElement('th');cell.scope='col';cell.textContent=column.name;header.append(cell)}const actionHeader=document.createElement('th');actionHeader.scope='col';actionHeader.textContent='Actions';header.append(actionHeader);element('data-rows').append(header)
  for(const row of rows.slice(0,50)){const tr=document.createElement('tr');for(const column of table.columns.filter(column=>visibleColumns().includes(column.name))){const cell=document.createElement('td');const value=Object.hasOwn(row,column.name)?row[column.name]:null;cell.textContent=typeof value==='string'?value:JSON.stringify(value);tr.append(cell)}const actions=document.createElement('td'),model=insertModel(),edit=keyedEdit(model,row);if(edit){const select=document.createElement('input');select.type='checkbox';select.style.width='auto';select.dataset.bulkRow='';select.setAttribute('aria-label','Select row for bulk operation');select.addEventListener('change',()=>{if(dataBusy){select.checked=bulkSelected.has(tr);return}if(select.checked)bulkSelected.set(tr,JSON.parse(JSON.stringify(row)));else bulkSelected.delete(tr);invalidateBulk();syncBulk()});actions.append(select);const button=document.createElement('button');button.textContent='Edit';button.addEventListener('click',()=>{if(dataBusy)return;editingRow={row:JSON.parse(JSON.stringify(row)),edit,context:dataContext()};renderData(false)});actions.append(button)}for(const link of relationshipLinks(session.snapshot,table,row)){const button=document.createElement('button');button.textContent=(nextViewStep(link)?'Continue saved view · ':'')+link.label;button.dataset.relationship=link.label;button.addEventListener('click',()=>navigateRelationship(link));actions.append(button)}tr.append(actions);element('data-rows').append(tr)}
  show('data-status',`${Math.min(rows.length,50)} visible rows · offset ${dataOffset}. Pages reflect current RLS visibility; refresh after data changes.`)
 }catch(error){if(context===JSON.stringify([dataContext(),element('data-filter').value,element('data-operator').value,element('data-value').value,dataOffset]))show('data-status',error.message)}finally{dataBusy=false;syncData()}
}
function browseState(){return {columns:visibleColumns(),sort:element('data-sort').value,direction:element('data-direction').value,resource:element('resource').value,scope:dataScope,offset:dataOffset,filter:element('data-filter').value,operator:element('data-operator').value,value:element('data-value').value}}
function navigateRelationship(link){
 if(dataBusy||!session?.ready)return
 const next=nextViewStep(link);if(activeView&&!next)activeView=null
 dataTrail.push(browseState());if(dataTrail.length>32){dataTrail.shift();activeView=null}
 dataScope=link;element('resource').value=JSON.stringify(['tables',`${link.schema}.${link.table}`]);selectResource(true)
 element('data-value').value='';if(next)applyViewStep(next);void readRows()
}
element('data-back').addEventListener('click',()=>{
 if(dataBusy||!session?.ready||!dataTrail.length)return
 const previous=dataTrail.pop();dataScope=previous.scope;element('resource').value=previous.resource;selectResource(true)
 restoreChoices(previous);dataOffset=previous.offset;element('data-filter').value=previous.filter;element('data-operator').value=previous.operator;element('data-value').value=previous.value;void readRows()
})
element('method').addEventListener('change',syncData)
element('identity').addEventListener('change',()=>{dataScope=null;dataTrail=[];activeView=null;renderData()})
element('data-new').addEventListener('click',()=>renderData())
element('data-read').addEventListener('click',()=>{dataOffset=0;void readRows()})
element('data-prev').addEventListener('click',()=>{dataOffset=Math.max(0,dataOffset-50);void readRows()})
element('data-next').addEventListener('click',()=>{dataOffset+=50;void readRows()})
for(const id of ['data-filter','data-operator','data-value','data-columns','data-sort','data-direction'])element(id).addEventListener('input',()=>{dataOffset=0;dataMore=false;syncData()})

function restoreChoices(state){
 for(const option of element('data-columns').options)option.selected=state.columns.includes(option.value)
 element('data-sort').value=state.sort;element('data-direction').value=state.direction
}
function applyViewStep(step){
 restoreChoices({columns:step.columns,sort:step.sort?.column??'',direction:step.sort?.direction??'asc'})
 element('data-filter').value=step.filter?.column??'';element('data-operator').value=step.filter?.operator??'eq';element('data-value').value=step.filter?.value??''
 show('view-status',activeView&&dataTrail.length<activeView.steps.length-1?'Choose a visible row and its Continue saved view relationship.':'Saved view path opened under the selected identity.')
}
function nextViewStep(link){
 const step=activeView?.steps[dataTrail.length+1]
 return step&&step.schema===link.schema&&step.table===link.table&&step.via.foreignKey===link.via.foreignKey&&step.via.direction===link.via.direction?step:null
}
function portableStep(state){
 const [,name]=JSON.parse(state.resource),table=session.snapshot.tables.find(table=>`${table.schema}.${table.name}`===name)
 return {schema:table.schema,table:table.name,columns:state.columns,filter:state.filter?{column:state.filter,operator:state.operator,value:state.value}:null,sort:state.sort?{column:state.sort,direction:state.direction}:null,via:state.scope?.via??null}
}
async function viewsRequest(method='GET',value){
 const response=await fetch('views',{method,headers:{Authorization:`Bearer ${key}`,'Content-Type':'application/json',...(value?{'If-Match':viewCollection.revision}:{})},...(value?{body:JSON.stringify(value)}:{}),signal:AbortSignal.timeout(15000)})
 if(!response.ok)throw new Error(response.status===409?'Views changed; reload before saving.':'Cannot load or save views. Check the definition file and reload.')
 const result=await response.json();validateViews({version:result.version,views:result.views});viewCollection=result
 renderViewOptions();syncData()
}
function renderViewOptions(){
 const selected=element('data-views').value;element('data-views').replaceChildren()
 for(const view of viewCollection.views){const option=document.createElement('option');option.value=view.name;option.textContent=view.name+(viewProblem(view,session.snapshot,session.fingerprint)?' (needs review)':'');element('data-views').append(option)}
 if(viewCollection.views.some(view=>view.name===selected))element('data-views').value=selected
 syncData()
}
const viewAction=action=>async()=>{
 if(viewBusy)return;viewBusy=true;syncData()
 try{await action()}catch(error){show('view-status',error.message)}finally{viewBusy=false;syncData()}
}
element('view-reload').addEventListener('click',viewAction(async()=>{await viewsRequest();show('view-status','Views reloaded.')}))
element('view-save').addEventListener('click',viewAction(async()=>{
 if(dataBusy||!session?.ready||!tableResource()||!viewCollection)return
 if(element('view-read-only').checked&&!session.identities[element('identity').value]?.read_only)throw new Error('Choose a configured read-only identity before saving a support view.')
 const view={...(element('view-read-only').checked?{mode:'read_only',identity:element('identity').value}:{}),name:element('view-name').value.trim(),fingerprint:session.fingerprint,steps:[...dataTrail,browseState()].map(portableStep)}
 const value={version:1,views:[...viewCollection.views.filter(saved=>saved.name!==view.name),view]};validateViews(value)
 const problem=viewProblem(view,session.snapshot,session.fingerprint);if(problem)throw new Error(problem)
 await viewsRequest('PUT',value);element('data-views').value=view.name;show('view-status','View saved. Review filter values before sharing data-api.views.json.')
}))
element('view-load').addEventListener('click',viewAction(async()=>{
 if(dataBusy||!session?.ready)return
 const view=viewCollection?.views.find(view=>view.name===element('data-views').value);if(!view)return
 const problem=viewProblem(view,session.snapshot,session.fingerprint);if(problem)throw new Error(problem)
 if(view.mode==='read_only'){if(!session.identities[view.identity]?.read_only)throw new Error('This support view requires its configured read-only identity.');element('identity').value=view.identity;resetBulk();resetCsvContext()}
 element('view-read-only').checked=view.mode==='read_only'
 dataScope=null;dataTrail=[];activeView=view
 const first=view.steps[0];element('resource').value=JSON.stringify(['tables',`${first.schema}.${first.table}`]);selectResource(true);applyViewStep(first);element('view-name').value=view.name;await readRows()
}))
element('view-delete').addEventListener('click',viewAction(async()=>{
 if(!viewCollection)return
 await viewsRequest('PUT',{version:1,views:viewCollection.views.filter(view=>view.name!==element('data-views').value)});show('view-status','View deleted.')
}))


async function accessRequest(method='GET',value){
 const response=await fetch('access',{method,headers:{Authorization:`Bearer ${key}`,'Content-Type':'application/json',...(value?{'If-Match':accessRevision}:{})},...(value?{body:JSON.stringify(value)}:{}),signal:AbortSignal.timeout(15000)})
 if(!response.ok)throw new Error(response.status===409?'Checks changed; reload before saving.':'Cannot load or save checks. Review the manifest and reload.')
 const result=await response.json();accessRevision=result.revision
 if(method==='GET'){const {revision,...manifest}=result;element('access-manifest').value=result.status==='empty'?'':JSON.stringify(manifest,null,2)}
}
const accessAction=action=>async()=>{
 if(accessBusy)return;accessBusy=true;syncData();show('access-results','')
 try{await action()}catch(error){show('access-status',error.message)}finally{accessBusy=false;syncData()}
}
element('access-generate').addEventListener('click',accessAction(async()=>{
 const view=viewCollection?.views.find(view=>view.name===element('data-views').value);if(!view)throw new Error('Select a saved view first.')
 const problem=viewProblem(view,session.snapshot,session.fingerprint);if(problem)throw new Error(problem)
 const manifest=accessScaffold(view,Object.keys(session.identities))
 if(!accessRevision)await accessRequest()
 element('access-manifest').value=JSON.stringify(manifest,null,2);show('access-status','Scaffold ready. Supply named synthetic fixture keys and per-identity row counts; no visibility rules were inferred.')
}))
element('access-mutations').addEventListener('click',accessAction(async()=>{
 const view=viewCollection?.views.find(view=>view.name===element('data-views').value);if(!view)throw new Error('Select a saved view first.')
 const problem=viewProblem(view,session.snapshot,session.fingerprint);if(problem)throw new Error(problem)
 const scaffold=mutationScaffold(accessScaffold(view,Object.keys(session.identities)))
 const blob=new Blob([JSON.stringify(scaffold,null,2)+'\n'],{type:'application/json'}),url=URL.createObjectURL(blob),link=document.createElement('a')
 link.href=url;link.download='data-api.mutations.json';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000)
 show('access-status','Mutation scaffold exported. Review fixture keys, payloads and expectations, then run it with the disposable CLI verifier. Inspector execution is read-only.')
}))
element('access-lifecycle').addEventListener('click',accessAction(async()=>{
 const view=viewCollection?.views.find(view=>view.name===element('data-views').value);if(!view)throw new Error('Select a saved view first.')
 const problem=viewProblem(view,session.snapshot,session.fingerprint);if(problem)throw new Error(problem)
 const scaffold=lifecycleScaffold(accessScaffold(view,Object.keys(session.identities)))
 const blob=new Blob([JSON.stringify(scaffold,null,2)+'\n'],{type:'application/json'}),url=URL.createObjectURL(blob),link=document.createElement('a')
 link.href=url;link.download='data-api.lifecycle.json';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000)
 show('access-status','Lifecycle scaffold exported. Review identities, membership fixtures, changes and checkpoints, then run the disposable CLI verifier. Existing tokens are reused.')
}))
element('access-reload').addEventListener('click',accessAction(async()=>{await accessRequest();show('access-status','Checks reloaded.')}))
element('access-save').addEventListener('click',accessAction(async()=>{
 if(!accessRevision)throw new Error('Reload checks or generate a scaffold before saving.')
 let manifest;try{manifest=JSON.parse(element('access-manifest').value)}catch{throw new Error('Enter valid manifest JSON.')}
 validateGuidedAccess(manifest,Object.keys(session.identities));await accessRequest('PUT',manifest);show('access-status','Checks saved to data-api.access.json. Run them against seeded synthetic fixtures.')
}))
element('access-run').addEventListener('click',accessAction(async()=>{
 if(!session?.ready||dataBusy)return
 const response=await fetch('access-run',{method:'POST',headers:{Authorization:`Bearer ${key}`},signal:AbortSignal.timeout(125000)})
 if(!response.ok)throw new Error('Cannot run checks. Save a reviewed manifest and wait for API readiness.')
 const report=await response.json();show('access-results',report.results);show('access-status',`Saved access checks ${report.status}. Results show fixture names and counts, excluding row contents.`)
}))


const csvContext=()=>JSON.stringify([element('resource').value,element('identity').value,session?.identities[element('identity').value]?.subject,session?.fingerprint,session?.input_model?.revision])
function syncCsv(){
 const ready=Boolean(session?.ready&&!dataBusy&&!csvBusy),valid=csvPlan?.context===csvContext()
 element('resource').disabled=csvBusy||exportBusy||bulkBusy;element('identity').disabled=csvBusy||exportBusy||bulkBusy
 for(const id of ['csv-source','csv-file','csv-encoding'])element(id).disabled=csvBusy
 for(const select of element('csv-mapping').querySelectorAll('select'))select.disabled=csvBusy
 element('csv-load').disabled=!ready||!insertModel()
 element('csv-review').disabled=!ready||!csvDataset||!insertModel()
 element('csv-approved').disabled=!ready||!valid
 element('csv-import').disabled=!ready||!valid||!element('csv-approved').checked
 element('csv-stop').disabled=!csvBusy
 element('csv-retry').disabled=!ready||!csvHistory||csvHistory.context!==csvContext()||!retryableImportRows(csvHistory.entries,csvHistory.rows).length
 element('csv-report').disabled=!csvHistory||csvBusy
 element('csv-preview-prev').disabled=csvBusy||!valid||csvPage===0
 element('csv-preview-next').disabled=csvBusy||!valid||(csvPage+1)*50>=csvPlan.entries.length
}
function invalidateCsv(clearHistory=false){
 csvGeneration++;csvPlan=null;csvPage=0;element('csv-approved').checked=false;element('csv-preview').replaceChildren()
 if(clearHistory){csvHistory=null;show('csv-results','');show('csv-progress','')}
 syncCsv()
}
function resetCsvContext(){
 const context=csvContext();if(context===csvKey)return
 csvKey=context;csvController?.abort();invalidateCsv(!csvBusy);renderCsvMapping()
 show('csv-status',csvDataset?'Table, identity or schema changed. Review mappings and preview again.':'')
}
function renderCsvMapping(){
 element('csv-mapping').replaceChildren();const model=insertModel();if(!model||!csvDataset)return
 csvDataset.headers.forEach((header,index)=>{
  const label=document.createElement('label'),target=document.createElement('select'),empty=document.createElement('select')
  label.textContent=`${header} → `;target.dataset.csvColumn=String(index);empty.dataset.csvEmpty=String(index)
  for(const field of [{name:'',label:'Ignore this CSV column'},...model.fields]){const option=document.createElement('option');option.value=field.name;option.textContent=field.label??field.name;target.append(option)}
  if(model.fields.some(field=>field.name===header))target.value=header
  for(const [value,text] of [['value','Blank is a value'],['omit','Blank uses database default'],['null','Blank becomes NULL']]){const option=document.createElement('option');option.value=value;option.textContent=text;empty.append(option)}
  for(const select of [target,empty])select.addEventListener('change',()=>{invalidateCsv(true);show('csv-status','Mapping changed. Validate and preview again.')})
  label.append(target,empty);element('csv-mapping').append(label)
 })
}
function csvMapping(){return csvDataset.headers.map((_,index)=>({field:element('csv-mapping').querySelector(`[data-csv-column="${index}"]`).value,empty:element('csv-mapping').querySelector(`[data-csv-empty="${index}"]`).value}))}
function showCsvPreview(){
 const plan=csvPlan;if(!plan)return
 const payloads=plan.entries.map(entry=>JSON.parse(entry.json)),fields=insertModel().fields.filter(field=>payloads.some(payload=>Object.hasOwn(payload,field.name)))
 element('csv-preview').replaceChildren();const header=document.createElement('tr')
 for(const name of ['CSV record',...fields.map(field=>field.name)]){const th=document.createElement('th');th.scope='col';th.textContent=name;header.append(th)}element('csv-preview').append(header)
 for(const entry of plan.entries.slice(csvPage*50,csvPage*50+50)){
  const row=document.createElement('tr'),number=document.createElement('td'),payload=JSON.parse(entry.json);number.textContent=String(entry.record);row.append(number)
  for(const field of fields){const cell=document.createElement('td'),value=payload[field.name];cell.textContent=!Object.hasOwn(payload,field.name)?'(omitted)':typeof value==='string'?value:JSON.stringify(value);row.append(cell)}element('csv-preview').append(row)
 }
 show('csv-status',`${plan.entries.length} validated inserts into ${plan.schema}.${plan.table} as ${plan.identity}. Preview page ${csvPage+1} of ${Math.ceil(plan.entries.length/50)}. Database constraints and RLS are checked when importing.`)
 syncCsv()
}
function csvReport(){if(!csvHistory)return;show('csv-progress',`Import into ${csvHistory.schema}.${csvHistory.table} as ${csvHistory.identity}: ${JSON.stringify(importSummary(csvHistory.rows))}`);show('csv-results',csvHistory.rows);syncCsv()}
for(const id of ['csv-source'])element(id).addEventListener('input',()=>{csvDataset=null;invalidateCsv(true);element('csv-mapping').replaceChildren();show('csv-status','CSV changed. Load headers again.')})
element('csv-file').addEventListener('change',async()=>{
 const file=element('csv-file').files[0];if(!file)return
 csvDataset=null;invalidateCsv(true);element('csv-mapping').replaceChildren();const generation=csvGeneration
 try{if(file.size>csvLimits.bytes)throw new Error('Use a CSV file of at most 1 MiB.');const source=await file.text();if(generation!==csvGeneration||csvBusy)return;element('csv-source').value=source;show('csv-status','File loaded. Load headers to map columns.')}catch(error){if(generation===csvGeneration)show('csv-status',error.message)}
})
element('csv-load').addEventListener('click',()=>{
 if(csvBusy||dataBusy||!insertModel())return;csvDataset=null;invalidateCsv(true)
 try{csvDataset=parseImportCsv(element('csv-source').value);renderCsvMapping();show('csv-status',`${csvDataset.records.length} data records loaded. Review writable-column mappings and blank-cell handling.`)}catch(error){element('csv-mapping').replaceChildren();show('csv-status',error.message)}syncCsv()
})
element('csv-review').addEventListener('click',()=>{
 if(csvBusy||dataBusy||!session?.ready||!csvDataset||!insertModel())return;invalidateCsv(true)
 try{
  const model=insertModel(),preview=previewCsvImport(csvDataset,csvMapping(),model,element('csv-encoding').value)
  if(!preview.ok){show('csv-status',`${preview.invalid} invalid of ${preview.total} records; no writes sent. ${JSON.stringify(preview.errors)}${preview.truncated?' (additional errors omitted)':''}`);return}
  csvPlan={context:csvContext(),schema:model.schema,table:model.table,identity:element('identity').value,fingerprint:session.fingerprint,entries:preview.entries};showCsvPreview()
 }catch(error){show('csv-status',error.message)}finally{syncCsv()}
})
element('csv-encoding').addEventListener('change',()=>{invalidateCsv(true);show('csv-status','Cell encoding changed. Validate and preview again.')})
element('csv-approved').addEventListener('change',syncCsv)
for(const [id,direction] of [['csv-preview-prev',-1],['csv-preview-next',1]])element(id).addEventListener('click',()=>{csvPage+=direction;showCsvPreview()})
element('csv-retry').addEventListener('click',()=>{
 if(csvBusy||dataBusy||!csvHistory||csvHistory.context!==csvContext())return
 const entries=retryableImportRows(csvHistory.entries,csvHistory.rows);if(!entries.length)return
 invalidateCsv();csvPlan={...csvHistory,entries};csvPage=0;showCsvPreview()
})
element('csv-stop').addEventListener('click',()=>csvController?.abort())
element('csv-import').addEventListener('click',async()=>{
 if(csvBusy||dataBusy||!session?.ready||!csvPlan||csvPlan.context!==csvContext()||!element('csv-approved').checked)return
 const plan=csvPlan,token=session.identities[plan.identity].token;csvBusy=true;dataBusy=true;csvController=new AbortController();const controller=csvController
 csvPlan=null;element('csv-approved').checked=false
 if(!csvHistory)csvHistory={...plan,rows:plan.entries.map(entry=>({record:entry.record,outcome:'not_sent'}))}
 const history=csvHistory;syncData()
 try{
  await runCsvImport(plan.entries,{signal:controller.signal,current:()=>Boolean(session?.ready&&plan.context===csvContext()),onProgress:rows=>{const latest=new Map(rows.map(row=>[row.record,row]));history.rows=history.rows.map(row=>latest.get(row.record)??row);csvReport()},sendBatch:async(body,signal)=>{
   const response=await fetch(`/__gregale/contract/${plan.fingerprint}/rest/v1/${encodeURIComponent(plan.table)}`,{method:'POST',body,redirect:'error',signal:AbortSignal.any([signal,AbortSignal.timeout(30000)]),headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json','Content-Profile':plan.schema,Prefer:'return=minimal,count=exact'}})
   if(response.status===201){await response.body?.cancel().catch(()=>{});const count=response.headers.get('content-range')?.match(/\/(\d+)$/)?.[1];return {status:201,count:count===undefined?undefined:Number(count),rollback:response.headers.get('preference-applied')?.includes('tx=rollback')}}
   let json;try{const reader=response.body?.getReader();let bytes=0,text='';try{if(reader)for(;;){const item=await reader.read();if(item.done)break;bytes+=item.value.length;if(bytes>16384)throw new Error('Response exceeds bounds');text+=new TextDecoder().decode(item.value,{stream:true})}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}json=JSON.parse(text)}catch{}
   return {status:response.status,code:json?.error?.code??json?.code}
  }})
  show('csv-status',history.rows.some(row=>row.outcome==='uncertain')?'Import stopped with uncertain rows. Read the table before deciding whether to import those records manually.':'Import finished or stopped. Confirmed rows are excluded from follow-up previews; refused and unsent rows can be reviewed again.')
 }catch{show('csv-status','Import stopped. Review the outcome report before another attempt.')}finally{csvBusy=false;dataBusy=false;csvController=null;csvReport();syncData()}
})
element('csv-report').addEventListener('click',()=>{
 if(!csvHistory||csvBusy)return
 const {schema,table,identity,fingerprint,rows}=csvHistory,report={version:1,schema,table,identity,fingerprint,summary:importSummary(rows),rows}
 const blob=new Blob([JSON.stringify(report,null,2)+'\n'],{type:'application/json'}),url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='data-api.import-report.json';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000)
})


const exportContext=()=>JSON.stringify([dataContext(),element('data-filter').value,element('data-operator').value,element('data-value').value,session?.identities[element('identity').value]?.subject,element('export-mode').value,element('export-limit').value])
function syncExport(){
 element('data-export').disabled=!session?.ready||dataBusy||exportBusy||!tableResource()||!visibleColumns().length
 element('export-stop').disabled=!exportBusy
 for(const id of ['export-mode','export-limit','data-columns','data-sort','data-direction','data-filter','data-operator','data-value'])element(id).disabled=exportBusy||bulkBusy
 element('send').disabled=!session?.ready||busy||csvBusy||exportBusy||bulkBusy||(readOnlyIdentity()&&!['GET','HEAD'].includes(element('method').value))
}
element('export-stop').addEventListener('click',()=>exportController?.abort())
for(const id of ['data-columns','data-sort','data-direction','data-filter','data-operator','data-value','export-mode','export-limit'])element(id).addEventListener('input',()=>exportController?.abort())
element('data-export').addEventListener('click',async()=>{
 if(exportBusy||dataBusy||!session?.ready||!tableResource())return
 let plan
 try{plan=csvExportPlan({table:tableResource(),columns:visibleColumns(),filter:element('data-filter').value?{column:element('data-filter').value,operator:element('data-operator').value,value:element('data-value').value}:null,scope:dataScope,sort:element('data-sort').value||null,direction:element('data-direction').value,fingerprint:session.fingerprint,mode:element('export-mode').value,rowLimit:Number(element('export-limit').value)})}catch(error){show('export-status',error.message);return}
 const context=exportContext(),identity=element('identity').value,token=session.identities[identity].token
 exportBusy=true;dataBusy=true;const controller=new AbortController();exportController=controller;const timer=setTimeout(()=>controller.abort(),120000);syncData()
 show('export-status',`Exporting ${plan.schema}.${plan.table} as ${identity}; order ${plan.order.map(([column,direction])=>column+'.'+direction).join(', ')}.`)
 try{
  const result=await runCsvExport(plan,{signal:controller.signal,current:()=>Boolean(session?.ready&&context===exportContext()),onProgress:progress=>show('export-status',`Exporting ${plan.schema}.${plan.table} as ${identity}: ${progress.rows} rows, ${progress.bytes} bytes.`),fetchPage:async(offset,limit)=>{
   const response=await fetch(exportTarget(plan,location.origin,offset,limit),{method:'GET',redirect:'error',headers:{Authorization:`Bearer ${token}`,'Accept-Profile':plan.schema,Accept:'application/json'},signal:AbortSignal.any([controller.signal,AbortSignal.timeout(30000)])})
   if(!response.ok){await response.body?.cancel().catch(()=>{});throw new Error(`Export read refused (${response.status}). No file downloaded.`)}
   const reader=response.body?.getReader(),chunks=[];let bytes=0
   try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>exportLimits.pageBytes)throw new Error('Export page exceeds 1 MiB. Narrow the selected columns or filters.');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
   const body=new Uint8Array(bytes);let index=0;for(const chunk of chunks){body.set(chunk,index);index+=chunk.length}return parseExactJSON(new TextDecoder('utf-8',{fatal:true}).decode(body))
  }})
  if(controller.signal.aborted||context!==exportContext())throw new Error('Export cancelled or context changed. No file downloaded.')
  const blob=new Blob([result.csv],{type:'text/csv;charset=utf-8'}),url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='data-api.export.csv';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000)
  show('export-status',`Exported ${result.rows} rows from ${plan.schema}.${plan.table} as ${identity}${result.limited?' (row limit reached; more rows remain)':''}. Pages reflect live reads.`)
 }catch(error){show('export-status',controller.signal.aborted?'Export cancelled. No file downloaded.':error instanceof TypeError?'Export read failed. No file downloaded.':error.message)}finally{clearTimeout(timer);exportController=null;exportBusy=false;dataBusy=false;syncData()}
})

const bulkContext=()=>JSON.stringify([dataContext(),element('data-filter').value,element('data-operator').value,element('data-value').value,session?.identities[element('identity').value]?.subject,element('bulk-operation').value,element('bulk-changes').value,element('bulk-scope').value,element('bulk-limit').value])
function invalidateBulk(){bulkReadiness=null;show('bulk-preflight-status','');show('bulk-preflight-results','');bulkReview=null;element('bulk-approved').checked=false;show('bulk-preview','')}
function resetBulk(){bulkController?.abort();bulkSelected.clear();bulkHistory=null;clearReconciliation();invalidateBulk();for(const checkbox of element('data-rows').querySelectorAll('[data-bulk-row]'))checkbox.checked=false;show('bulk-results','');show('bulk-status','')}
function syncBulk(){
 const ready=Boolean(session?.ready&&!dataBusy),model=insertModel()
 show('bulk-selection',`${bulkSelected.size} selected rows (current page).`)
 show('bulk-fields',model?`Writable update fields: ${model.updateFields.filter(field=>!model.primaryKey?.columns.includes(field.name)&&field.name!==model.concurrency?.column).map(field=>field.name).join(', ')}`:'Select a writable table with guarded rows.')
 element('bulk-review').disabled=!ready||!model||(element('bulk-scope').value==='selected'&&!bulkSelected.size)
 element('bulk-preflight').disabled=readOnlyIdentity()||!ready||!bulkReview||bulkReview.context!==bulkContext()
 const checked=Boolean(bulkReadiness?.plan===bulkReview&&bulkReadiness.expires>Date.now())
 element('bulk-approved').disabled=!ready||!bulkReview||bulkReview.context!==bulkContext()||!checked
 element('bulk-run').disabled=readOnlyIdentity()||!ready||!bulkReview||bulkReview.context!==bulkContext()||!checked||!element('bulk-approved').checked
 element('bulk-stop').disabled=!bulkBusy
 element('bulk-report').disabled=!bulkLastReport||bulkBusy
 element('bulk-reconcile').disabled=!ready||!bulkHistory||bulkHistory.plan.context!==bulkContext()||!reconciliationEntries(bulkHistory.plan,bulkHistory.results).length
 element('bulk-reconcile-preview').disabled=!ready||!bulkReconciled||!element('bulk-reconcile-rows').querySelector('input:checked')
 for(const input of element('bulk-reconcile-rows').querySelectorAll('input'))input.disabled=!ready
 element('bulk-reconcile-ack').disabled=!ready
 element('bulk-retry').disabled=!ready||!bulkHistory||bulkHistory.plan.context!==bulkContext()||!retryBulkEntries(bulkHistory.plan,bulkHistory.results).length
 for(const id of ['bulk-operation','bulk-changes','bulk-scope','bulk-limit'])element(id).disabled=bulkBusy
 for(const checkbox of element('data-rows').querySelectorAll('[data-bulk-row]'))checkbox.disabled=dataBusy
}
for(const id of ['bulk-operation','bulk-changes','bulk-scope','bulk-limit'])element(id).addEventListener('input',()=>{bulkController?.abort();invalidateBulk();bulkHistory=null;clearReconciliation();syncBulk()})
for(const id of ['data-filter','data-operator','data-value','data-columns','data-sort','data-direction'])for(const event of ['input','change'])element(id).addEventListener(event,()=>{resetBulk();syncBulk()})
element('bulk-approved').addEventListener('change',syncBulk)
function reviewBulk(rows,source=element('bulk-scope').value,retryOf=null,followupOf=null){
 const parent=retryOf??followupOf
 invalidateBulk()
 try{
  let changes={};if(element('bulk-operation').value==='update'){try{changes=JSON.parse(element('bulk-changes').value)}catch{throw new Error('Provide valid JSON changes.')}}
  const plan=bulkPlan({model:insertModel(),rows,operation:element('bulk-operation').value,changes,fingerprint:session.fingerprint,identity:element('identity').value,context:bulkContext(),source})
  bulkReview=Object.freeze({...plan,...(parent?{[followupOf?'followupOf':'retryOf']:Object.freeze({runId:parent.runId,indexes:Object.freeze([...parent.indexes])})}:{})});element('bulk-approved').checked=false
  show('bulk-preview',{identity:plan.identity,table:`${plan.schema}.${plan.table}`,operation:plan.operation,source:plan.source,count:plan.entries.length,...(parent?{[followupOf?'reconciliation_of':'retry_of']:parent.runId}:{}),rows:plan.entries.map(entry=>({row:entry.index,...(parent?{source_index:parent.indexes[entry.index-1]}:{}),key:entry.edit.keys,expectedVersion:entry.edit.version,...(plan.operation==='update'?{changes:Object.fromEntries(Object.entries(plan.changes).map(([name,after])=>[name,{before:entry.before[name],after}]))}:{delete:true})}))})
  show('bulk-status','Review these exact rows and versions, check readiness, then approve. Rows commit independently.')
 }catch(error){invalidateBulk();show('bulk-status',error.message)}syncBulk()
}
element('bulk-review').addEventListener('click',async()=>{
 if(dataBusy||!session?.ready)return
 if(element('bulk-scope').value==='selected'){reviewBulk([...bulkSelected.values()]);return}
 let query
 try{
  const changes=element('bulk-operation').value==='update'?JSON.parse(element('bulk-changes').value):{}
  query=matchingQuery({table:tableResource(),model:insertModel(),operation:element('bulk-operation').value,changes,fingerprint:session.fingerprint,filter:element('data-filter').value?{column:element('data-filter').value,operator:element('data-operator').value,value:element('data-value').value}:null,scope:dataScope,sort:element('data-sort').value||null,direction:element('data-direction').value,rowLimit:Number(element('bulk-limit').value)})
 }catch{invalidateBulk();show('bulk-status','Choose valid changes, a guarded table and a matching-row limit from 1 to 1,000.');syncBulk();return}
 const context=bulkContext(),token=session.identities[element('identity').value].token,controller=new AbortController()
 bulkController=controller;bulkBusy=true;dataBusy=true;invalidateBulk();bulkHistory=null;syncData();const timer=setTimeout(()=>controller.abort(),120000)
 try{
  const rows=await collectMatchingRows(query,{signal:controller.signal,current:()=>session?.ready&&context===bulkContext(),onProgress:progress=>show('bulk-status',`Reading matching rows: ${progress.rows} rows, ${progress.bytes} bytes. No writes sent.`),fetchPage:async(offset,limit)=>{
   const response=await fetch(exportTarget(query,location.origin,offset,limit),{redirect:'error',headers:{Authorization:`Bearer ${token}`,'Accept-Profile':query.schema},signal:AbortSignal.any([controller.signal,AbortSignal.timeout(30000)])})
   if(!response.ok){await response.body?.cancel().catch(()=>{});throw new Error(`Matching read refused (${response.status}). No writes sent.`)}
   const reader=response.body?.getReader(),chunks=[];let bytes=0
   try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>bulkLimits.responseBytes)throw new Error('Matching response exceeds 1 MiB. Narrow the view.');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
   const body=new Uint8Array(bytes);let index=0;for(const chunk of chunks){body.set(chunk,index);index+=chunk.length}
   try{return JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(body))}catch{throw new Error('Cannot decode matching rows. No writes sent.')}
  }})
  if(!controller.signal.aborted&&context===bulkContext())reviewBulk(rows,'matching')
 }catch(error){invalidateBulk();show('bulk-status',controller.signal.aborted?'Matching preview cancelled. No writes sent.':error.message)}finally{clearTimeout(timer);bulkController=null;bulkBusy=false;dataBusy=false;syncData()}
})
element('bulk-retry').addEventListener('click',()=>{
 if(dataBusy||!bulkHistory||bulkHistory.plan.context!==bulkContext())return
 const entries=retryBulkEntries(bulkHistory.plan,bulkHistory.results)
 // Reuse the approved key/version snapshots, never refresh them silently.
 const rows=entries.map(entry=>({...entry.before,...entry.edit.keys,[entry.edit.column]:entry.edit.version}))
 reviewBulk(rows,bulkHistory.plan.source,{runId:bulkHistory.report.run_id,indexes:entries.map(entry=>entry.index)})
})
function clearReconciliation(){bulkReconciled=null;element('bulk-reconcile-rows').replaceChildren();element('bulk-reconcile-ack').checked=false;show('bulk-reconcile-status','')}
element('bulk-reconcile-ack').addEventListener('change',syncBulk)
element('bulk-reconcile').addEventListener('click',async()=>{
 if(dataBusy||!bulkHistory||bulkHistory.plan.context!==bulkContext())return
 const history=bulkHistory,plan=history.plan;clearReconciliation();invalidateBulk()
 if(!preflightModel(plan,insertModel(),session?.fingerprint)){show('bulk-reconcile-status','Schema or writable fields changed. Read fresh rows and start a new preview.');syncBulk();return}
 const controller=new AbortController(),token=session.identities[plan.identity].token;bulkController=controller;bulkBusy=true;dataBusy=true;syncData();const timer=setTimeout(()=>controller.abort(),120000)
 try{
  const result=await reconcileBulk(plan,history.results,{signal:controller.signal,current:()=>session?.ready&&bulkHistory===history&&plan.context===bulkContext()&&preflightModel(plan,insertModel(),session.fingerprint),onProgress:rows=>show('bulk-reconcile-status',`Read ${rows.filter(row=>row.state!=='unchecked').length} of ${rows.length} unresolved rows as ${plan.identity}. No writes sent.`),read:async entry=>{
   const response=await fetch(reconciliationTarget(plan,entry,location.origin),{redirect:'error',headers:{Authorization:`Bearer ${token}`,'Accept-Profile':plan.schema},signal:AbortSignal.any([controller.signal,AbortSignal.timeout(30000)])})
   if(response.status!==200){await response.body?.cancel().catch(()=>{});return {status:response.status}}
   const reader=response.body?.getReader(),chunks=[];let bytes=0
   try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>bulkLimits.responseBytes)throw new Error('Read response exceeds bounds.');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
   const body=new Uint8Array(bytes);let offset=0;for(const chunk of chunks){body.set(chunk,offset);offset+=chunk.length}
   let rows;try{rows=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(body))}catch{}
   return {status:response.status,rows}
  }})
  if(controller.signal.aborted||history!==bulkHistory||plan.context!==bulkContext()){clearReconciliation();show('bulk-reconcile-status','Reconciliation cancelled or context changed. Read again. No writes sent.');return}
  bulkReconciled={history,result}
  for(const row of result.rows){
   const entry=plan.entries.find(entry=>entry.index===row.index),container=document.createElement('div'),label=document.createElement('label')
   if(row.record){const checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.style.width='auto';checkbox.dataset.reconcileIndex=String(row.index);checkbox.addEventListener('change',syncBulk);label.append(checkbox)}
   label.append(document.createTextNode(`Row ${row.index}: ${row.outcome} · ${row.state}`));container.append(label)
   const detail=document.createElement('pre');detail.textContent=JSON.stringify({key:entry.edit.keys,reviewedVersion:entry.edit.version,...(row.record?{currentVersion:row.record[entry.edit.column]}:{}),...(plan.operation==='update'?{changes:Object.fromEntries(Object.entries(plan.changes).map(([name,desired])=>[name,{reviewed:entry.before[name],desired,...(row.record?{current:row.record[name]}:{})}]))}:{delete:true})},null,2);container.append(detail);element('bulk-reconcile-rows').append(container)
  }
  show('bulk-reconcile-status',`Read under ${plan.identity}. Original outcomes remain unchanged. Select visible rows for a new operation; matching values or unavailable rows do not establish whether an uncertain write committed.`)
 }finally{clearTimeout(timer);bulkController=null;bulkBusy=false;dataBusy=false;syncData()}
})
element('bulk-reconcile-preview').addEventListener('click',()=>{
 if(dataBusy||!bulkReconciled||bulkHistory!==bulkReconciled.history||bulkHistory.plan.context!==bulkContext())return
 const selected=[...element('bulk-reconcile-rows').querySelectorAll('input:checked')].map(input=>bulkReconciled.result.rows.find(row=>row.index===Number(input.dataset.reconcileIndex))).filter(row=>row?.record)
 if(!selected.length)return
 if(selected.some(row=>row.outcome==='uncertain')&&!element('bulk-reconcile-ack').checked){show('bulk-reconcile-status','Acknowledge that uncertain operations may have committed before previewing a new write.');return}
 reviewBulk(selected.map(row=>row.record),bulkHistory.plan.source,null,{runId:bulkHistory.report.run_id,indexes:selected.map(row=>row.index)})
 show('bulk-reconcile-status','Fresh versions are now in a new preview. Check readiness and approve this new operation explicitly. Original outcomes remain unresolved.')
})
function rememberSessionRun(run){
 sessionRuns.set(run.report.run_id,run)
 while(sessionRuns.size>historyLimits.runs||new TextEncoder().encode(JSON.stringify([...sessionRuns.values()])).length>historyLimits.sessionBytes)sessionRuns.delete(sessionRuns.keys().next().value)
}
function syncOperationHistory(){
 const report=operationHistory?.reports.find(report=>report.run_id===element('bulk-history-runs').value),snapshot=report&&sessionRuns.get(report.run_id),idle=!historyBusy&&!dataBusy
 for(const id of ['bulk-history-reload','bulk-history-retain'])element(id).disabled=!idle
 element('bulk-history-download').disabled=!idle||!report
 element('bulk-history-open').disabled=!idle||!snapshot||!session?.ready||snapshot.report.fingerprint!==session.fingerprint||session.identities[snapshot.plan.identity]?.subject!==snapshot.subject
 element('bulk-history-clear').disabled=!idle||!operationHistory||!element('bulk-history-clear-approved').checked
 show('bulk-history-detail',report?{run_id:report.run_id,identity:report.identity,table:`${report.schema}.${report.table}`,operation:report.operation,source:report.source,finished_at:report.finished_at,termination:report.termination,summary:report.summary,...(report.retry_of?{retry_of:report.retry_of,parent_retained:operationHistory.reports.some(item=>item.run_id===report.retry_of)}:{}),...(report.reconciliation_of?{reconciliation_of:report.reconciliation_of,parent_retained:operationHistory.reports.some(item=>item.run_id===report.reconciliation_of)}:{}),session_snapshot:Boolean(snapshot)}:'')
}
function renderOperationHistory(){
 const selected=element('bulk-history-runs').value;element('bulk-history-runs').replaceChildren()
 for(const report of operationHistory?.reports??[]){const option=document.createElement('option');option.value=report.run_id;option.textContent=`${report.finished_at} · ${report.operation} ${report.schema}.${report.table} · ${report.summary.confirmed}/${report.total} confirmed · ${report.run_id}`;element('bulk-history-runs').append(option)}
 if(operationHistory?.reports.some(report=>report.run_id===selected))element('bulk-history-runs').value=selected
 if(operationHistory){element('bulk-history-count').value=operationHistory.retention.runs;element('bulk-history-days').value=operationHistory.retention.days;for(const id of sessionRuns.keys())if(!operationHistory.reports.some(report=>report.run_id===id))sessionRuns.delete(id)}
 syncOperationHistory()
}
async function readOperationHistory(){
 const response=await fetch('bulk-history',{headers:{Authorization:`Bearer ${key}`},signal:AbortSignal.timeout(15000)})
 if(!response.ok)throw new Error('Cannot load operation history. Check its format, size and file type.')
 const {revision,...value}=await response.json();validateBulkHistory(value);operationHistory={...value,revision};renderOperationHistory();return operationHistory
}
async function writeOperationHistory(value,revision){
 const response=await fetch('bulk-history',{method:'PUT',headers:{Authorization:`Bearer ${key}`,'Content-Type':'application/json','If-Match':revision},body:JSON.stringify(value),signal:AbortSignal.timeout(15000)})
 if(!response.ok){const error=new Error(response.status===409?'Operation history changed; reload before saving.':'Cannot save operation history. Download the report to retain it.');error.status=response.status;throw error}
 const {revision:nextRevision,...next}=await response.json();validateBulkHistory(next);operationHistory={...next,revision:nextRevision};renderOperationHistory()
}
async function saveOperationReport(report){
 const operation=historyWrites.then(async()=>{
  historyBusy=true;syncOperationHistory()
  try{
   for(let attempt=0;attempt<2;attempt++){
    const {revision,...value}=await readOperationHistory();if(pendingSessionRun)rememberSessionRun(pendingSessionRun)
    try{await writeOperationHistory(retainBulkHistory(value,report),revision);show('bulk-history-status','Redacted report saved to data-api.bulk-history.json.');return}catch(error){if(error.status!==409||attempt)throw error}
   }
  }catch(error){show('bulk-history-status',error.message)}finally{historyBusy=false;syncOperationHistory()}
 })
 // Keep the new snapshot across reloading history before the report has been appended.
 const pendingSessionRun=sessionRuns.get(report.run_id)
 historyWrites=operation.catch(()=>{});await operation
}
async function historyAction(action){if(historyBusy||dataBusy)return;historyBusy=true;syncOperationHistory();try{await action()}catch(error){show('bulk-history-status',error.message)}finally{historyBusy=false;syncOperationHistory()}}
element('bulk-history-runs').addEventListener('change',syncOperationHistory)
element('bulk-history-clear-approved').addEventListener('change',syncOperationHistory)
element('bulk-history-reload').addEventListener('click',()=>historyAction(async()=>{await readOperationHistory();show('bulk-history-status','History reloaded. Persisted reports contain no write snapshots.')}))
element('bulk-history-retain').addEventListener('click',()=>historyAction(async()=>{
 if(!operationHistory)throw new Error('Reload history first.')
 const {revision,...value}=operationHistory,next={...value,retention:{runs:Number(element('bulk-history-count').value),days:Number(element('bulk-history-days').value)}}
 // Validate the policy independently before pruning the existing list.
 validateBulkHistory({...next,reports:[]});const pruned=retainBulkHistory({...next,reports:next.reports.slice(0,next.retention.runs)});await writeOperationHistory(pruned,revision)
 show('bulk-history-status','Retention applied; removed reports and snapshots cannot be reopened.')
}))
element('bulk-history-clear').addEventListener('click',()=>historyAction(async()=>{
 if(!operationHistory||!element('bulk-history-clear-approved').checked)return
 await writeOperationHistory({version:1,retention:{...operationHistory.retention},reports:[]},operationHistory.revision);sessionRuns.clear();bulkHistory=null;bulkLastReport=null;clearReconciliation();invalidateBulk();element('bulk-history-clear-approved').checked=false;show('bulk-history-status','Stored reports and session snapshots cleared.');syncData()
}))
function downloadOperationReport(report){const blob=new Blob([JSON.stringify(report,null,2)+'\n'],{type:'application/json'}),url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='data-api.bulk-report.json';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000)}
element('bulk-history-download').addEventListener('click',()=>{if(historyBusy||dataBusy)return;const report=operationHistory?.reports.find(item=>item.run_id===element('bulk-history-runs').value);if(report)downloadOperationReport(report)})
element('bulk-history-open').addEventListener('click',()=>{
 if(historyBusy||dataBusy||!session?.ready)return
 const snapshot=sessionRuns.get(element('bulk-history-runs').value)
 if(!snapshot||session.identities[snapshot.plan.identity]?.read_only||snapshot.report.fingerprint!==session.fingerprint||session.identities[snapshot.plan.identity]?.subject!==snapshot.subject)return
 const resource=JSON.stringify(['tables',`${snapshot.plan.schema}.${snapshot.plan.table}`]);if(![...element('resource').options].some(option=>option.value===resource))return
 element('identity').value=snapshot.plan.identity;element('identity').dispatchEvent(new Event('change'));element('resource').value=resource;selectResource()
 dataScope=JSON.parse(JSON.stringify(snapshot.view.scope));for(const option of element('data-columns').options)option.selected=snapshot.view.columns.includes(option.value)
 element('data-sort').value=snapshot.view.sort;element('data-direction').value=snapshot.view.direction;element('data-filter').value=snapshot.view.filter;element('data-operator').value=snapshot.view.operator;element('data-value').value=snapshot.view.value
 element('bulk-operation').value=snapshot.plan.operation;element('bulk-changes').value=snapshot.plan.body??'{}';element('bulk-scope').value=snapshot.plan.source;element('bulk-limit').value=snapshot.view.limit
 if(!preflightModel(snapshot.plan,insertModel(),session.fingerprint)){show('bulk-history-status','Snapshot metadata is unavailable. Read fresh rows and start a new preview.');syncData();return}
 bulkHistory={...snapshot,plan:Object.freeze({...snapshot.plan,context:bulkContext()})};bulkLastReport=snapshot.report;show('bulk-results',snapshot.results);show('bulk-history-status',`Opened ${snapshot.report.run_id} under its original identity. Read unresolved rows or create a reviewed retry; no writes sent.`);syncData()
})
element('bulk-report').addEventListener('click',()=>{
 if(!bulkLastReport||bulkBusy)return
 const blob=new Blob([JSON.stringify(bulkLastReport,null,2)+'\n'],{type:'application/json'}),url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download='data-api.bulk-report.json';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000)
})
element('bulk-preflight').addEventListener('click',async()=>{
 if(readOnlyIdentity()||!session?.ready||dataBusy||!bulkReview||bulkReview.context!==bulkContext())return
 const plan=bulkReview;bulkReadiness=null;element('bulk-approved').checked=false
 if(!preflightModel(plan,insertModel(),session.fingerprint)){invalidateBulk();show('bulk-preflight-status','Schema or writable fields changed. Create a fresh preview. No writes sent.');syncBulk();return}
 const controller=new AbortController(),token=session.identities[plan.identity].token;bulkController=controller;bulkBusy=true;dataBusy=true;syncData();const timer=setTimeout(()=>controller.abort(),120000)
 show('bulk-preflight-status','Checking current visibility and reviewed versions. No writes sent.')
 try{
  const result=await runBulkPreflight(plan,{signal:controller.signal,current:()=>session?.ready&&bulkReview===plan&&plan.context===bulkContext()&&preflightModel(plan,insertModel(),session.fingerprint),onProgress:rows=>show('bulk-preflight-status',`Checked ${rows.filter(row=>row.status!=='unchecked').length} of ${rows.length} rows. No writes sent.`),read:async entry=>{
   const response=await fetch(preflightTarget(plan,entry,location.origin),{redirect:'error',headers:{Authorization:`Bearer ${token}`,'Accept-Profile':plan.schema},signal:AbortSignal.any([controller.signal,AbortSignal.timeout(30000)])})
   if(response.status!==200){await response.body?.cancel().catch(()=>{});return {status:response.status}}
   const reader=response.body?.getReader(),chunks=[];let bytes=0
   try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>bulkLimits.responseBytes)throw new Error('Read response exceeds bounds.');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
   const body=new Uint8Array(bytes);let offset=0;for(const chunk of chunks){body.set(chunk,offset);offset+=chunk.length}
   let rows;try{rows=JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(body))}catch{}
   return {status:response.status,rows}
  }})
  if(result.ready){
   const readiness={plan,expires:Date.now()+60000};bulkReadiness=readiness
   show('bulk-preflight-status',`${result.total} rows ready. Approve within 60 seconds. Checks verify reads and versions; write policies, constraints and triggers are enforced during execution.`)
   setTimeout(()=>{if(bulkReadiness===readiness){bulkReadiness=null;element('bulk-approved').checked=false;show('bulk-preflight-status','Preflight expired. Check readiness again before approval.');syncBulk()}},60001)
  }else{invalidateBulk();show('bulk-preflight-status','Preflight failed or was cancelled. Create a fresh preview. No writes sent.')}
  show('bulk-preflight-results',result)
 }finally{clearTimeout(timer);bulkController=null;bulkBusy=false;dataBusy=false;syncData()}
})
element('bulk-stop').addEventListener('click',()=>bulkController?.abort())
element('bulk-run').addEventListener('click',async()=>{
 if(readOnlyIdentity()||!session?.ready||dataBusy||!bulkReview||bulkReview.context!==bulkContext()||bulkReadiness?.plan!==bulkReview||bulkReadiness.expires<=Date.now()||!element('bulk-approved').checked)return
 const plan=bulkReview,token=session.identities[plan.identity].token,controller=new AbortController();bulkController=controller;bulkBusy=true;dataBusy=true;invalidateBulk();syncData()
 const runId=crypto.randomUUID(),startedAt=new Date().toISOString(),runView={scope:JSON.parse(JSON.stringify(dataScope)),columns:visibleColumns(),sort:element('data-sort').value,direction:element('data-direction').value,filter:element('data-filter').value,operator:element('data-operator').value,value:element('data-value').value,limit:element('bulk-limit').value},runSubject=session.identities[plan.identity].subject
 const timer=setTimeout(()=>controller.abort(),120000)
 try{
  const results=await runBulkWrite(plan,{signal:controller.signal,current:()=>session?.ready&&plan.context===bulkContext(),onProgress:rows=>show('bulk-results',rows),send:async entry=>{
   const response=await fetch(bulkTarget(plan,entry,location.origin),{method:plan.operation==='update'?'PATCH':'DELETE',...(plan.body?{body:plan.body}:{}),headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json','Content-Profile':plan.schema,Prefer:'return=representation'},signal:AbortSignal.any([controller.signal,AbortSignal.timeout(30000)])})
   const reader=response.body?.getReader(),chunks=[];let bytes=0
   try{if(reader)for(;;){const {done,value}=await reader.read();if(done)break;bytes+=value.length;if(bytes>bulkLimits.responseBytes)throw new Error('Response exceeds bound.');chunks.push(value)}}finally{if(reader){await reader.cancel().catch(()=>{});reader.releaseLock()}}
   const body=new Uint8Array(bytes);let offset=0;for(const chunk of chunks){body.set(chunk,offset);offset+=chunk.length}
   let parsed;try{parsed=JSON.parse(new TextDecoder().decode(body))}catch{}
   return {status:response.status,rollback:/\btx=rollback\b/.test(response.headers.get('preference-applied')??''),...(response.status===200?{rows:parsed}:{code:parsed?.code})}
  }})
  const termination=controller.signal.aborted?'cancelled':plan.context!==bulkContext()?'context_changed':results.some(row=>row.outcome==='not_sent'||row.outcome==='uncertain')?'stopped':'completed'
  const report=bulkReport(plan,results,{runId,startedAt,finishedAt:new Date().toISOString(),termination})
  rememberSessionRun({plan,results,report,view:runView,subject:runSubject});await saveOperationReport(report)
  bulkLastReport=report;show('bulk-report-status',`Report ${report.run_id}: ${report.total} rows, ${report.termination}. Download before closing the inspector; download or revisit it in operation history.`)
  show('bulk-reconciliation',{run_id:report.run_id,conflicts:report.reconciliation.conflicts,uncertain:report.reconciliation.uncertain,retryable:report.retryable})
  if(plan.context===bulkContext()){
   clearReconciliation();bulkHistory={plan,results,report};show('bulk-results',results)
   show('bulk-status',`Finished: ${results.filter(row=>row.outcome==='confirmed').length} confirmed; ${results.filter(row=>row.outcome==='refused').length} refused; ${results.filter(row=>row.outcome==='conflict').length} conflicts; ${results.filter(row=>row.outcome==='uncertain').length} uncertain; ${results.filter(row=>row.outcome==='not_sent').length} unsent. Read fresh rows before another selection.`)
   for(const checkbox of element('data-rows').querySelectorAll('[data-bulk-row]'))checkbox.checked=false;bulkSelected.clear()
  }
 }finally{clearTimeout(timer);bulkController=null;bulkBusy=false;dataBusy=false;syncData()}
})

element('data-approved').addEventListener('change',syncData)
element('data-form').addEventListener('submit',event=>{
 event.preventDefault();invalidatePlan();if(dataBusy||!session?.ready)return
 const model=editorModel();if(!model)return
 const payload={}
 for(let index=0;index<model.fields.length;index++){
  const field=model.fields[index],mode=element(`data-mode-${index}`).value,input=element(`data-field-${index}`);show(`data-error-${index}`,'');input.removeAttribute('aria-invalid')
  if(mode==='omit')continue
  let value
  if(mode==='null')value=null
  else if(field.control==='number')value=input.value.trim()?Number(input.value):NaN
  else if(field.control==='checkbox')value=input.checked
  else if(field.control==='custom'){try{value=JSON.parse(input.value)}catch{show(`data-error-${index}`,'Enter valid JSON.');input.setAttribute('aria-invalid','true');return}}
  else value=input.value
  Object.defineProperty(payload,field.name,{value,enumerable:true})
 }
 const checked=validateRecord(payload,model.spec)
 if(!checked.ok){const index=model.fields.findIndex(field=>field.name===checked.issue.path[0]);if(index>=0){show(`data-error-${index}`,`${checked.issue.path.join(' → ')}: ${checked.issue.code.replaceAll('_',' ')}`);element(`data-field-${index}`).setAttribute('aria-invalid','true')}show('data-write-status','Fix the highlighted fields before review.');return}
 if(editingRow&&!Object.keys(checked.value).length){show('data-write-status','Choose at least one field to change.');return}
 dataPlan={edit:editingRow?.edit??null,context:dataContext(),schema:model.schema,table:model.table,identity:element('identity').value,body:JSON.stringify(checked.value),fingerprint:session.fingerprint}
 show('data-plan',`Identity: ${dataPlan.identity}
Table: ${model.schema}.${model.table}
${JSON.stringify(editingRow?{key:editingRow.edit.keys,expectedVersion:editingRow.edit.version,changes:Object.fromEntries(Object.entries(checked.value).map(([name,after])=>[name,{before:editingRow.row[name],after}]))}:checked.value,null,2)}`);show('data-write-status',editingRow?'Review these exact changes and version, then approve.':'Review this exact insert, then approve it.');syncData()
})
element('data-insert').addEventListener('click',async()=>{
 if(!dataPlan||!session?.ready||dataBusy||!element('data-approved').checked||dataPlan.context!==dataContext())return
 const plan=dataPlan;dataBusy=true;invalidatePlan();syncData()
 try{
  const response=await fetch(plan.edit?editTarget(plan,location.origin):`/__gregale/contract/${plan.fingerprint}/rest/v1/${encodeURIComponent(plan.table)}`,{method:plan.edit?'PATCH':'POST',body:plan.body,signal:AbortSignal.timeout(30000),headers:{Authorization:`Bearer ${session.identities[plan.identity].token}`,'Content-Type':'application/json','Content-Profile':plan.schema,Prefer:plan.edit?'return=representation':'return=minimal'}})
  if(plan.edit&&response.ok){
   const rows=await response.json()
   if(Array.isArray(rows)&&rows.length===0){show('data-write-status','Row changed, was deleted or is unavailable. Read it again before editing.');editingRow=null;return}
   if(!Array.isArray(rows)||rows.length!==1||!Number.isSafeInteger(rows[0][plan.edit.column])||rows[0][plan.edit.column]<=plan.edit.version||Object.entries(plan.edit.keys).some(([name,value])=>rows[0][name]!==value)){show('data-write-status','Update returned an unexpected key or version. Read the row and review the declared database version rule.');editingRow=null;return}
  }
  if(response.ok)show('data-write-status',`${plan.edit?'Update':'Insert'} into ${plan.schema}.${plan.table} as ${plan.identity} confirmed (${response.status}). Refresh rows to see current visibility.`)
  else show('data-write-status',`${plan.edit?'Update':'Insert'} into ${plan.schema}.${plan.table} as ${plan.identity} refused (${response.status}). Review database rules and the selected identity; validate again before another attempt.`)
 }catch{show('data-write-status','Write outcome is uncertain. Read the table before deciding whether to submit again. No automatic retry occurred.')}
 finally{dataBusy=false;if(plan.edit){const message=element('data-write-status').textContent;editingRow=null;renderData(false);element('data-rows').replaceChildren();show('data-status','Read fresh rows before editing again.');show('data-write-status',message)}syncData()}
})

let collection
async function collectionRequest(method = 'GET', value) {
  const response = await fetch('requests', { method, headers: { Authorization: `Bearer ${key}`, 'Content-Type': 'application/json', ...(value ? { 'If-Match': collection.revision } : {}) }, ...(value ? { body: JSON.stringify(value) } : {}), signal: AbortSignal.timeout(15000) })
  const result = await response.json()
  if (!response.ok) throw new Error(result.error ?? 'Cannot load saved requests.')
  collection = result
  const selectedScenario = element('scenario').value
  element('scenario').replaceChildren()
  for (const scenario of collectionScenarios(collection)) {
    const option = document.createElement('option'); option.value = scenario.name; option.textContent = scenario.name; element('scenario').append(option)
  }
  if (collectionScenarios(collection).some(scenario => scenario.name === selectedScenario)) element('scenario').value = selectedScenario
  renderRequests()
  return result
}
const currentScenario = () => collection && collectionScenarios(collection).find(scenario => scenario.name === element('scenario').value)
function renderRequests() {
  const selected = element('saved').value
  element('saved').replaceChildren()
  for (const request of currentScenario()?.requests ?? []) {
    const option = document.createElement('option'); option.value = request.name; option.textContent = request.name; element('saved').append(option)
  }
  if (currentScenario()?.requests.some(request => request.name === selected)) element('saved').value = selected
}
const updatedCollection = requests => collection.version === 1 ? { version: 1, requests } : { version: 2, scenarios: collection.scenarios.map(scenario => scenario.name === element('scenario').value ? { ...scenario, requests } : scenario) }
element('scenario').addEventListener('change', renderRequests)
async function loadCollection() {
  try { await collectionRequest(); show('saved-status', 'Collection loaded.') }
  catch (error) { show('saved-status', error.message) }
}
const selectedRequest = () => currentScenario()?.requests.find(request => request.name === element('saved').value)
const savedAction = action => async () => {
  try { await action() } catch (error) { show('saved-status', error.message) }
}
element('refresh-saved').addEventListener('click', loadCollection)
element('load-saved').addEventListener('click', savedAction(async () => {
  const request = selectedRequest()
  if (!request) throw new Error('Select a saved request.')
  element('request-name').value = request.name; element('method').value = request.method; element('path').value = request.path; element('identity').value = request.identity
  element('body').value = JSON.stringify(request.body ?? {}, null, 2); element('expected-status').value = request.expect.status
  element('expected-json').value = 'json' in request.expect ? JSON.stringify(request.expect.json, null, 2) : ''
  element('expected-fingerprint').value = request.expectedSchemaFingerprint ?? ''
  element('capture').value = request.capture ? JSON.stringify(request.capture, null, 2) : ''
  show('saved-status', `Loaded ${request.name}.`)
}))
element('save-request').addEventListener('click', savedAction(async () => {
  if (!collection) throw new Error('Reload the collection first.')
  const method = element('method').value
  const request = { ...(element('expected-fingerprint').value.trim() ? { expectedSchemaFingerprint: element('expected-fingerprint').value.trim() } : {}), name: element('request-name').value.trim(), method, path: element('path').value, identity: element('identity').value, ...(element('capture').value.trim() ? { capture: JSON.parse(element('capture').value) } : {}), ...(['POST', 'PATCH'].includes(method) ? { body: JSON.parse(element('body').value) } : {}), expect: { status: Number(element('expected-status').value), ...(element('expected-json').value.trim() ? { json: JSON.parse(element('expected-json').value) } : {}) } }
  const requests = [...currentScenario().requests]; const index = requests.findIndex(item => item.name === request.name)
  if (index < 0) requests.push(request); else requests[index] = request
  await collectionRequest('PUT', updatedCollection(requests)); element('saved').value = request.name
  show('saved-status', `Saved ${request.name}.`)
}))
element('delete-saved').addEventListener('click', savedAction(async () => {
  const request = selectedRequest()
  if (!request) throw new Error('Select a saved request.')
  await collectionRequest('PUT', updatedCollection(currentScenario().requests.filter(item => item.name !== request.name)))
  show('saved-status', `Deleted ${request.name}.`)
}))
async function replay(scenarios) {
  if (!session?.ready || busy) throw new Error('Wait until the local API is ready.')
  if (!scenarios.some(scenario => scenario.requests.length)) throw new Error('Select or save a request first.')
  busy = true; element('send').disabled = true
  for (const id of ['replay', 'replay-all', 'replay-scenarios']) element(id).disabled = true
  const results = [], summaries = []
  show('scenario-results', summaries)
  try {
    for (const scenario of scenarios) {
      const variables = new Map()
      const start = results.length
      for (const request of scenario.requests) {
        results.push({ scenario: scenario.name, ...await replayRequest(request, { url: location.origin, identities: session.identities, variables }) })
        show('replay-results', results)
      }
      const passed = results.slice(start).filter(result => result.passed).length
      summaries.push({ scenario: scenario.name, total: scenario.requests.length, passed, failed: scenario.requests.length - passed })
      show('scenario-results', summaries)
    }
    show('saved-status', `${results.filter(result => result.passed).length}/${results.length} checks passed.`)
  } finally {
    busy = false; element('send').disabled = !session?.ready
    for (const id of ['replay', 'replay-all', 'replay-scenarios']) element(id).disabled = false
  }
}
element('replay').addEventListener('click', savedAction(() => replay([{ name: currentScenario()?.name, requests: selectedRequest() ? [selectedRequest()] : [] }])))
element('replay-all').addEventListener('click', savedAction(() => replay(currentScenario() ? [currentScenario()] : [])))

element('replay-scenarios').addEventListener('click', savedAction(() => replay(collection ? collectionScenarios(collection) : [])))
element('create-scenario').addEventListener('click', savedAction(async () => {
  if (!collection) throw new Error('Reload the collection first.')
  const name = element('scenario-name').value.trim()
  const scenarios = collectionScenarios(collection)
  if (!/^[A-Za-z][A-Za-z0-9_-]{0,63}$/.test(name) || scenarios.some(scenario => scenario.name === name)) throw new Error('Use a unique scenario identifier starting with a letter.')
  await collectionRequest('PUT', { version: 2, scenarios: [...scenarios, { name, requests: [] }] })
  element('scenario').value = name; renderRequests(); show('saved-status', `Created scenario ${name}.`)
}))
element('delete-scenario').addEventListener('click', savedAction(async () => {
  if (!collection) throw new Error('Reload the collection first.')
  const scenarios = collectionScenarios(collection), name = currentScenario().name
  if (scenarios.length === 1) throw new Error('Keep at least one scenario; delete its requests to empty it.')
  await collectionRequest('PUT', { version: 2, scenarios: scenarios.filter(scenario => scenario.name !== name) })
  show('saved-status', `Deleted scenario ${name}.`)
}))

await refresh()
await loadCollection()
await historyAction(()=>readOperationHistory())
await viewAction(()=>viewsRequest())()
setInterval(refresh, 1000)
