import {csvExportPlan} from './csv-export.mjs'
import {keyedEdit,validateRecord} from './inspector-data.mjs'
export const bulkLimits=Object.freeze({rows:50,matchingRows:1000,previewBytes:5242880,bodyBytes:65536,responseBytes:1048576})
export function bulkPlan({model,rows,operation,changes={},fingerprint,identity,context,source='selected'}){
 if(!model||!['update','delete'].includes(operation)||!Array.isArray(rows)||!rows.length||rows.length>(source==='matching'?bulkLimits.matchingRows:bulkLimits.rows)||!['selected','matching'].includes(source)||!/^[a-f0-9]{64}$/.test(fingerprint)||typeof identity!=='string'||!identity)throw new Error('Choose bounded guarded rows and a supported operation.')
 const values=validateBulkChanges(model,operation,changes)
 const body=operation==='update'?JSON.stringify(values):undefined
 if(body&&new TextEncoder().encode(body).length>bulkLimits.bodyBytes)throw new Error('Changes exceed 64 KiB.')
 const seen=new Set(),entries=rows.map((row,index)=>{
  const edit=keyedEdit(model,row);if(!edit)throw new Error('Every selected row needs a readable primary key and integer version.')
  const key=JSON.stringify(edit.keys);if(seen.has(key))throw new Error('Select each key once.');seen.add(key)
  return Object.freeze({index:index+1,edit:Object.freeze({...edit,keys:Object.freeze({...edit.keys})}),before:Object.freeze(Object.fromEntries(Object.keys(values).map(name=>[name,row[name]])))})
 })
 return Object.freeze({schema:model.schema,table:model.table,operation,source,body,changes:JSON.parse(JSON.stringify(values)),fingerprint,identity,context,entries:Object.freeze(entries)})
}
export function bulkTarget(plan,entry,origin){
 const url=new URL(`/__gregale/contract/${plan.fingerprint}/rest/v1/${encodeURIComponent(plan.table)}`,origin)
 for(const [name,value] of Object.entries(entry.edit.keys))url.searchParams.append(name,`eq.${String(value)}`)
 url.searchParams.append(entry.edit.column,`eq.${entry.edit.version}`)
 url.searchParams.set('select',[...Object.keys(entry.edit.keys),entry.edit.column].map(name=>JSON.stringify(name)).join(','))
 return url
}
export function bulkOutcome(plan,entry,response){
 if(response?.status===200){
  if(response.rollback)return 'uncertain'
  if(!Array.isArray(response.rows))return 'uncertain'
  if(!response.rows.length)return 'conflict'
  if(response.rows.length!==1)return 'uncertain'
  const row=response.rows[0]
  if(!row||Object.entries(entry.edit.keys).some(([name,value])=>row[name]!==value))return 'uncertain'
  const version=row[entry.edit.column]
  return Number.isSafeInteger(version)&&(plan.operation==='delete'?version===entry.edit.version:version>entry.edit.version)?'confirmed':'uncertain'
 }
 if(response?.status>=400&&response.status<500&&response.status!==408&&typeof response.code==='string'&&(/^[A-Z0-9]{5}$/.test(response.code)||/^PGRST\d+$/.test(response.code)||['schema_contract_mismatch','schema_contract_unavailable'].includes(response.code)))return 'refused'
 return 'uncertain'
}
export async function runBulkWrite(plan,{send,current=()=>true,signal,onProgress=()=>{}}){
 const results=plan.entries.map(entry=>({index:entry.index,outcome:'not_sent'}))
 for(let index=0;index<plan.entries.length;index++){
  if(signal?.aborted||!current())break
  let response
  try{response=await send(plan.entries[index])}catch{}
  results[index]={index:plan.entries[index].index,outcome:bulkOutcome(plan,plan.entries[index],response),...(Number.isInteger(response?.status)?{status:response.status}:{})}
  onProgress(results.map(row=>({...row})))
  if(results[index].outcome==='uncertain'||response?.code?.startsWith('schema_contract_'))break
 }
 return results
}
export function retryBulkEntries(plan,results){return plan.entries.filter(entry=>results.some(row=>row.index===entry.index&&['refused','not_sent'].includes(row.outcome)))}

export function validateBulkChanges(model,operation,changes){
 let values={}
 if(operation==='update'){
  if(!changes||typeof changes!=='object'||Array.isArray(changes))throw new Error('Provide a JSON object of changes.')
  const allowed=model.updateFields?.filter(field=>!model.primaryKey.columns.includes(field.name)&&field.name!==model.concurrency.column).map(field=>field.name)??[]
  if(!Object.keys(changes).length||Object.keys(changes).some(name=>!allowed.includes(name)))throw new Error('Choose writable fields other than keys and the version column.')
  const checked=validateRecord(changes,model.updateSpec);if(!checked.ok)throw new Error(`Invalid change: ${checked.issue.path.join(' → ')} (${checked.issue.code}).`)
  values=checked.value
 }
 return values
}

export function matchingQuery({table,model,operation,changes,fingerprint,filter,scope,sort,direction,rowLimit=1000}){
 if(!model?.primaryKey?.filterable||model.concurrency?.kind!=='integer_version')throw new Error('Matching writes require a readable primary key and declared integer version.')
 if(!['update','delete'].includes(operation)||!Number.isInteger(rowLimit)||rowLimit<1||rowLimit>bulkLimits.matchingRows)throw new Error('Choose a matching-row limit from 1 to 1,000.')
 const values=validateBulkChanges(model,operation,changes)
 const columns=[...new Set([...model.primaryKey.columns,model.concurrency.column,...Object.keys(values)])]
 return csvExportPlan({table,columns,filter,scope,sort,direction,fingerprint,rowLimit,mode:'json'})
}
export async function collectMatchingRows(query,{fetchPage,current=()=>true,signal,onProgress=()=>{}}){
 const rows=[];let bytes=0,pages=0
 for(;;){
  signal?.throwIfAborted();if(!current())throw new Error('View changed. Preview again.')
  if(++pages>query.rowLimit+1)throw new Error('Matching preview exceeds the page bound.')
  const limit=Math.min(100,query.rowLimit-rows.length+1),page=await fetchPage(rows.length,limit)
  signal?.throwIfAborted();if(!current())throw new Error('View changed. Preview again.')
  if(!Array.isArray(page)||page.length>limit||page.some(row=>!row||typeof row!=='object'||Array.isArray(row)||query.columns.some(column=>!Object.hasOwn(row,column))))throw new Error('Unexpected matching-row response.')
  if(rows.length+page.length>query.rowLimit)throw new Error('More rows match than the preview limit. Increase the limit or narrow the view; no operation was approved.')
  bytes+=new TextEncoder().encode(JSON.stringify(page)).length;if(bytes>bulkLimits.previewBytes)throw new Error('Matching preview exceeds 5 MiB. Narrow the view.')
  rows.push(...page);onProgress({rows:rows.length,bytes})
  if(!page.length)break
 }
 if(!rows.length)throw new Error('No rows match under the selected identity.')
 return rows
}

const bulkOutcomes=['confirmed','refused','conflict','uncertain','not_sent']
const uuid=value=>typeof value==='string'&&/^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$/i.test(value)
export function bulkReport(plan,results,{runId,startedAt,finishedAt,termination='completed'}){
 const fail=()=>{throw new Error('Cannot create a valid bulk-operation report.')}
 const start=Date.parse(startedAt),finish=Date.parse(finishedAt)
 if(!uuid(runId)||!Number.isFinite(start)||!Number.isFinite(finish)||finish<start||!['completed','cancelled','context_changed','stopped'].includes(termination)||!Array.isArray(results)||results.length!==plan.entries.length||results.length>bulkLimits.matchingRows)fail()
 const parent=plan.retryOf??plan.followupOf
 if(plan.retryOf&&plan.followupOf)fail()
 if(parent&&(!uuid(parent.runId)||!Array.isArray(parent.indexes)||parent.indexes.length!==results.length||new Set(parent.indexes).size!==parent.indexes.length||parent.indexes.some(index=>!Number.isInteger(index)||index<1||index>bulkLimits.matchingRows)))fail()
 const rows=results.map((row,index)=>{
  if(row.index!==plan.entries[index].index||!bulkOutcomes.includes(row.outcome))fail()
  return {index:row.index,outcome:row.outcome,...(Number.isInteger(row.status)&&row.status>=100&&row.status<=599?{status:row.status}:{}),...(parent?{source_index:parent.indexes[index]}:{})}
 })
 const summary=Object.fromEntries(bulkOutcomes.map(outcome=>[outcome,rows.filter(row=>row.outcome===outcome).length]))
 // Build explicitly from approved metadata and redacted outcomes, never spread a plan or response.
 return {version:1,run_id:runId,...(parent?{[plan.followupOf?'reconciliation_of':'retry_of']:parent.runId}:{}),operation:plan.operation,source:plan.source,schema:plan.schema,table:plan.table,identity:plan.identity,fingerprint:plan.fingerprint,started_at:new Date(start).toISOString(),finished_at:new Date(finish).toISOString(),termination,total:rows.length,summary,rows,reconciliation:{conflicts:rows.filter(row=>row.outcome==='conflict').map(row=>row.index),uncertain:rows.filter(row=>row.outcome==='uncertain').map(row=>row.index)},retryable:rows.filter(row=>['refused','not_sent'].includes(row.outcome)).map(row=>row.index)}
}

export function preflightTarget(plan,entry,origin){
 const url=bulkTarget(plan,entry,origin)
 url.searchParams.delete(entry.edit.column);url.searchParams.set('limit','2')
 return url
}
export function preflightModel(plan,model,fingerprint){
 if(fingerprint!==plan.fingerprint||model?.schema!==plan.schema||model?.table!==plan.table||!model.primaryKey?.filterable||model.concurrency?.kind!=='integer_version')return false
 try{validateBulkChanges(model,plan.operation,plan.changes)}catch{return false}
 return plan.entries.every(entry=>entry.edit.column===model.concurrency.column&&JSON.stringify(Object.keys(entry.edit.keys))===JSON.stringify(model.primaryKey.columns))
}
export async function runBulkPreflight(plan,{read,current=()=>true,signal,onProgress=()=>{}}){
 const rows=plan.entries.map(entry=>({index:entry.index,status:'unchecked'}))
 for(let index=0;index<plan.entries.length;index++){
  if(signal?.aborted||!current())break
  const entry=plan.entries[index];let response,status='read_failed'
  try{response=await read(entry)}catch{}
  if(signal?.aborted||!current())break
  if(response?.status===200){
   if(Array.isArray(response.rows)&&response.rows.length===0)status='unavailable'
   else if(Array.isArray(response.rows)&&response.rows.length===1){
    const row=response.rows[0]
    status=row&&Object.entries(entry.edit.keys).every(([name,value])=>row[name]===value)&&Number.isSafeInteger(row[entry.edit.column])?(row[entry.edit.column]===entry.edit.version?'ready':'changed'):'invalid_response'
   }else status='invalid_response'
  }else if(Number.isInteger(response?.status))status='read_refused'
  rows[index]={index:entry.index,status,...(Number.isInteger(response?.status)?{http_status:response.status}:{})}
  onProgress(rows.map(row=>({...row})))
  if(['read_failed','read_refused','invalid_response'].includes(status))break
 }
 const ready=!signal?.aborted&&current()&&rows.every(row=>row.status==='ready')
 return {ready,total:rows.length,summary:Object.fromEntries(['ready','changed','unavailable','read_refused','read_failed','invalid_response','unchecked'].map(status=>[status,rows.filter(row=>row.status===status).length])),rows}
}

export function reconciliationEntries(plan,results){return plan.entries.filter(entry=>results.some(row=>row.index===entry.index&&['conflict','uncertain'].includes(row.outcome)))}
export function reconciliationTarget(plan,entry,origin){
 const url=preflightTarget(plan,entry,origin)
 url.searchParams.set('select',[...new Set([...Object.keys(entry.edit.keys),entry.edit.column,...Object.keys(plan.changes)])].map(name=>JSON.stringify(name)).join(','))
 return url
}
const sameJSON=(a,b,depth=0)=>{
 if(depth>64)return false
 if(a===b)return true
 if(!a||!b||typeof a!=='object'||typeof b!=='object'||Array.isArray(a)!==Array.isArray(b))return false
 const keys=Object.keys(a);return keys.length===Object.keys(b).length&&keys.every(key=>Object.hasOwn(b,key)&&sameJSON(a[key],b[key],depth+1))
}
export async function reconcileBulk(plan,results,{read,current=()=>true,signal,onProgress=()=>{}}){
 const entries=reconciliationEntries(plan,results),rows=entries.map(entry=>({index:entry.index,outcome:results.find(row=>row.index===entry.index).outcome,state:'unchecked'}));let bytes=0
 for(let index=0;index<entries.length;index++){
  if(signal?.aborted||!current())break
  const entry=entries[index];let response,state='read_failed',record
  try{response=await read(entry)}catch{}
  if(signal?.aborted||!current())break
  if(response?.status===200){
   if(Array.isArray(response.rows)&&!response.rows.length)state='unavailable'
   else if(Array.isArray(response.rows)&&response.rows.length===1){
    const row=response.rows[0],edit=keyedEdit({primaryKey:{filterable:true,columns:Object.keys(entry.edit.keys)},concurrency:{kind:'integer_version',column:entry.edit.column}},row??{})
    if(edit&&Object.entries(entry.edit.keys).every(([name,value])=>row[name]===value)&&Object.keys(plan.changes).every(name=>Object.hasOwn(row,name))){
     bytes+=new TextEncoder().encode(JSON.stringify(row)).length
     if(bytes>bulkLimits.previewBytes)state='read_failed'
     else{record=JSON.parse(JSON.stringify(row));state=plan.operation==='update'&&Object.entries(plan.changes).every(([name,value])=>sameJSON(row[name],value))?'potentially_applied':edit.version===entry.edit.version?'current':'changed'}
    }else state='invalid_response'
   }else state='invalid_response'
  }else if(response?.status)state='read_refused'
  rows[index]={index:entry.index,outcome:rows[index].outcome,state,...(record?{record}:{})}
  onProgress(rows.map(row=>({index:row.index,outcome:row.outcome,state:row.state})))
  if(['read_failed','read_refused','invalid_response'].includes(state))break
 }
 return {complete:!signal?.aborted&&current()&&rows.every(row=>row.state!=='unchecked'),rows}
}
