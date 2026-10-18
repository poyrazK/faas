import {orderOperations,parseCriteria} from './order-operations.js'
import type {Criteria,OrderCursor} from './order-operations.js'
import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import * as contract from './database.types.js'
import type {teamsClient,Status,Order} from './orders.js'
export type ImportItem=Readonly<{id:string;total_cents:number;status:Status}>
export type ImportPreview=Readonly<{items:readonly ImportItem[];errors:readonly {row:number;message:string;code?:string;path?:readonly (string|number)[]}[];total:number;valid:number;invalid:number;errorsTruncated:boolean}>
export type ColumnMapping={id?:string;total:string;status:string}
export type ImportJob=Database['api']['Tables']['team_order_imports']['Row']
export type ImportSummary=Omit<ImportJob,'manifest'>
const summaryFields='import_id,tenant_id,actor,source_name,total,processed,inserted,skipped,created_at,completed_at' as const
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const states=['draft','submitted','fulfilled']
/** Strict RFC-style CSV records, including quoted commas, CRLF and newlines. */
export function parseCsv(source:string):string[][] {
  if(new TextEncoder().encode(source).length>1048576)throw new Error('CSV exceeds 1 MiB.')
  source=source.replace(/^\uFEFF/,'')
  const rows:string[][]=[];let row:string[]=[],field='',quoted=false,closed=false
  const cell=()=>{row.push(field);field='';closed=false}
  const record=()=>{cell();rows.push(row);row=[];if(rows.length>1001)throw new Error('CSV exceeds 1,000 data records.')}
  for(let i=0;i<source.length;i++){
    const c=source[i]
    if(quoted){if(c==='"'){if(source[i+1]==='"'){field+='"';i++}else{quoted=false;closed=true}}else field+=c;continue}
    if(c===','){cell();continue}
    if(c==='\r'||c==='\n'){if(c==='\r'&&source[i+1]==='\n')i++;record();continue}
    if(closed)throw new Error('Unexpected text after a quoted CSV field.')
    if(c==='"'){if(field)throw new Error('Quote inside an unquoted CSV field.');quoted=true}else field+=c
  }
  if(quoted)throw new Error('Unclosed quoted CSV field.')
  if(field||closed||row.length)record()
  if(!rows.length)throw new Error('CSV is empty.')
  rows[0]=rows[0].map(v=>v.trim())
  if(rows[0].some(v=>!v)||new Set(rows[0]).size!==rows[0].length)throw new Error('Use distinct, nonempty column headers.')
  return rows
}
type RowCheck = {ok:boolean;valid:number;invalid:number;errors:readonly {row:number;code:string;path:readonly (string|number)[]}[];truncated:boolean}
const generated = contract as unknown as { inputValidators?: {api:{team_orders:{insert(input:unknown):unknown}}}; validateInputRows?: (rows:readonly unknown[],parse:(input:unknown)=>unknown)=>RowCheck }
export function previewCsv(source:string,mapping:ColumnMapping,identity?:{tenant:string;subject:string}):ImportPreview {
  const [headers,...rows]=parseCsv(source)
  if(!rows.length)throw new Error('CSV has no data records.')
  const total=headers.indexOf(mapping.total),status=headers.indexOf(mapping.status),id=mapping.id?headers.indexOf(mapping.id):-1
  if(total<0||status<0||mapping.id&&id<0||new Set([total,status,...(id<0?[]:[id])]).size!==(id<0?2:3))throw new Error('Map distinct columns for cents, status and optional order ID.')
  const items:ImportItem[]=[],positions:number[]=[],errors:{row:number;message:string;code?:string;path?:readonly (string|number)[]}[]=[],seen=new Set<string>()
  for(const [index,row] of rows.entries()){
    if(row.length!==headers.length){errors.push({row:index+2,message:'Column count differs from the header.'});continue}
    const value=row[total].trim(),state=row[status].trim(),orderId=(id<0||!row[id].trim()?crypto.randomUUID():row[id].trim()).toLowerCase()
    if(!uuid.test(orderId)){errors.push({row:index+2,message:'Order ID must be a UUID.'});continue}
    if(seen.has(orderId)){errors.push({row:index+2,message:'Duplicate order ID in this CSV.'});continue}seen.add(orderId)
    if(!/^\d+$/.test(value)||!Number.isSafeInteger(Number(value))||Number(value)>2147483647){errors.push({row:index+2,message:'Total must be whole cents from 0 to 2147483647.'});continue}
    if(!states.includes(state)){errors.push({row:index+2,message:'Status must be draft, submitted or fulfilled.'});continue}
    items.push(Object.freeze({id:orderId,total_cents:Number(value),status:state as Status}));positions.push(index+2)
  }
  let valid=items.length,invalid=errors.length,truncated=false
  if(identity&&generated.inputValidators&&generated.validateInputRows){
    const checked=generated.validateInputRows(items.map(item=>({...item,tenant_id:identity.tenant,created_by:identity.subject})),generated.inputValidators.api.team_orders.insert)
    valid=checked.valid;invalid+=checked.invalid;truncated=checked.truncated
    for(const error of checked.errors) errors.push({row:positions[error.row-1],code:error.code,path:error.path,message:`${error.path.length?error.path.join(' → '):'Record'}: ${error.code.replaceAll('_',' ')}.`})
    if(!checked.ok) items.length=0
  }
  truncated ||= errors.length>100
  return Object.freeze({items:Object.freeze(items),errors:Object.freeze(errors.slice(0,100).map(e=>Object.freeze(e))),total:rows.length,valid,invalid,errorsTruncated:truncated})
}
const validItems=(value:unknown):value is ImportItem[]=>Array.isArray(value)&&value.length>=1&&value.length<=1000&&value.every(r=>r&&typeof r==='object'&&Object.keys(r).length===3&&typeof r.id==='string'&&uuid.test(r.id)&&Number.isSafeInteger(r.total_cents)&&r.total_cents>=0&&r.total_cents<=2147483647&&states.includes(r.status))&&new Set(value.map(r=>r.id.toLowerCase())).size===value.length
function parseSummary(value:unknown):ImportSummary {
  const r=value as ImportSummary
  if(!r||!uuid.test(r.import_id)||typeof r.tenant_id!=='string'||!r.tenant_id||typeof r.actor!=='string'||!r.actor||typeof r.source_name!=='string'||!r.source_name||r.source_name.length>128||![r.total,r.processed,r.inserted,r.skipped].every(v=>Number.isSafeInteger(v)&&v>=0)||r.total<1||r.total>1000||r.processed!==r.inserted+r.skipped||r.processed>r.total||typeof r.created_at!=='string'||!Number.isFinite(Date.parse(r.created_at))||(r.processed===r.total)!==(r.completed_at!==null)||r.completed_at!==null&&(typeof r.completed_at!=='string'||!Number.isFinite(Date.parse(r.completed_at))))throw new Error('Invalid import response')
  return Object.fromEntries(summaryFields.split(',').map(k=>[k,(r as unknown as Record<string,unknown>)[k]])) as ImportSummary
}
export function parseImportJob(value:unknown):ImportJob {
  const summary=parseSummary(value),manifest=(value as ImportJob).manifest
  if(!validItems(manifest)||manifest.length!==summary.total)throw new Error('Invalid import manifest')
  return Object.freeze({...summary,manifest:Object.freeze(manifest.map(r=>Object.freeze({...r})))}) as unknown as ImportJob
}
export function orderImports(client:ReturnType<typeof teamsClient>){return {
  async history(after?:string){
    if(after&&!uuid.test(after))throw new Error('Invalid import cursor')
    let query=client.db.from('team_order_imports').select(summaryFields).eq('tenant_id',client.tenant).order('import_id').limit(21)
    if(after)query=query.gt('import_id',after)
    const result=await dataRequest(query,{timeoutMs:5000})
    const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseSummary)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant))throw new Error('Could not load import history')
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.import_id:null}
  },
}}
type Pending=Readonly<{kind:'start';id:string;actor:string;source:string;items:readonly ImportItem[]}|{kind:'advance';id:string;actor:string;processed:number}>
export function createImportForm(client:ReturnType<typeof teamsClient>){
  let job:ImportJob|null=null,pending:Pending|null=null,busy=false
  async function execute(kind:'start'|'advance'|'recover'|'resume',input?:unknown){
    if(busy)return {outcome:'busy' as const}
    if(kind!=='recover'&&pending)return {outcome:'pending' as const}
    if(kind==='recover'&&!pending)return {outcome:'empty' as const}
    busy=true
    try{
      let prepared:{source:string;items:ImportItem[]}|undefined
      if(kind==='start'){
        const v=input as {source?:unknown;items?:unknown}
        if(!v||typeof v.source!=='string'||!v.source.trim()||v.source.length>128||!validItems(v.items)||new TextEncoder().encode(JSON.stringify(v.items)).length>1048576)throw new Error('Invalid import plan')
        if(job&&job.processed<job.total)return {outcome:'failed' as const,message:'Finish or resume the current import before starting another.',uncertain:false}
        prepared={source:v.source,items:v.items.map(r=>({...r,id:r.id.toLowerCase()}))}
      }
      if(kind==='resume'&&(typeof input!=='string'||!uuid.test(input)))throw new Error('Invalid import ID')
      const member=await client.membership()
      if(!member||member.role==='member'||pending&&pending.actor!==member.subject||kind==='advance'&&job?.actor!==member.subject)return {outcome:'failed' as const,message:'Use the original owner or admin account for this team.',uncertain:pending!==null}
      if(kind==='resume'){
        const result=await dataRequest(client.db.from('team_order_imports').select().eq('tenant_id',client.tenant).eq('actor',member.subject).eq('import_id',input as string),{timeoutMs:5000})
        const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v)||v.length!==1)throw new Error();return parseImportJob(v[0])}})
        if(checked.outcome!=='validated'||checked.result.data.tenant_id!==client.tenant||checked.result.data.actor!==member.subject||checked.result.data.import_id!==(input as string).toLowerCase())throw new Error('Cannot resume import')
        job=checked.result.data;return {outcome:'confirmed' as const,job}
      }
      if(kind==='start')pending=Object.freeze({kind:'start',id:crypto.randomUUID(),actor:member.subject,source:prepared!.source,items:Object.freeze(prepared!.items.map(r=>Object.freeze(r)))})
      if(kind==='advance'){
        if(!job)return {outcome:'empty' as const}
        pending=Object.freeze({kind:'advance',id:job.import_id,actor:member.subject,processed:job.processed})
      }
      const op=pending!
      const query=op.kind==='start'?client.db.rpc('start_team_order_import_once',{operation_id:op.id,team_id:client.tenant,source_label:op.source,items:[...op.items]}):client.db.rpc('advance_team_order_import_once',{operation_id:op.id,team_id:client.tenant,expected_processed:op.processed})
      const result=await dataRequest(query,{timeoutMs:5000})
      const checked=validateRpcResponse(result,{parse(v){
        if(!Array.isArray(v)||v.length!==1)throw new Error()
        const row=parseImportJob(v[0])
        if(row.import_id!==op.id||row.tenant_id!==client.tenant||row.actor!==op.actor||op.kind==='start'&&(row.source_name!==op.source||canonical(row.manifest as unknown as ImportItem[])!==canonical(op.items))||op.kind==='advance'&&(row.processed<op.processed||!job||canonical(row.manifest as unknown as ImportItem[])!==canonical(job.manifest as unknown as ImportItem[])))throw new Error()
        return row
      }})
      if(checked.outcome==='validated'){job=checked.result.data;pending=null;return {outcome:'confirmed' as const,job}}
      const known=['PT400','PT409'].includes(result.error?.code??'')&&['invalid_order_import','invalid_import_row','duplicate_import_id','import_payload_conflict','import_progress_conflict','import_unavailable'].includes(result.error?.message??'')
      if(kind!=='recover'&&(known||[401,403].includes(result.status)))pending=null
      return {outcome:'failed' as const,message:known?'The import plan or progress changed. Reload its history and review before continuing.':'The import could not be confirmed. Resolve the pending operation with the original user.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'The import could not be confirmed. Resolve the original operation.':'Review a valid CSV or select an import belonging to this user.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get job(){return job},get pending(){return Boolean(pending)},start:(source:string,items:readonly ImportItem[])=>execute('start',{source,items}),advance:()=>execute('advance'),reconcile:()=>execute('recover'),resume:(id:string)=>execute('resume',id)}
}
function canonical(value:readonly ImportItem[]){return JSON.stringify(value.map(r=>({id:r.id,total_cents:r.total_cents,status:r.status})))}
/** A bounded live cursor scan, not a database snapshot. Fail rather than truncate. */
export async function exportOrders(client:ReturnType<typeof teamsClient>,filter?:Status|Criteria){
  const criteria=typeof filter==='object'?parseCriteria(filter):null,rows:Order[]=[];let after:string|OrderCursor|undefined
  do{const page=criteria?await orderOperations(client).orders(criteria,after as OrderCursor|undefined,100):await client.list(filter as Status|undefined,after as string|undefined,100);rows.push(...page.rows);if(rows.length>10000)throw new Error('Export exceeds 10,000 orders. Narrow the filters.');after=page.next??undefined}while(after)
  const cell=(v:unknown)=>{let s=String(v);if(/^(?:\s*[=+\-@]|[\t\r\n])/.test(s))s="'"+s;return '"'+s.replaceAll('"','""')+'"'}
  return ['id,total_cents,status,version,created_by',...rows.map(r=>[r.id,r.total_cents,r.status,r.version,r.created_by].map(cell).join(','))].join('\r\n')+'\r\n'
}
