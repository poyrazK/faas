import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import {parseOrder} from './orders.js'
import type {Order,Status,teamsClient} from './orders.js'
export type Criteria={status:Status|null;minimum:number|null;maximum:number|null;sort:'id_asc'|'id_desc'|'total_asc'|'total_desc'}
export type View=Database['api']['Tables']['team_order_views']['Row']
export type Selection={id:string;version:number}
export type Preview={id:string;expected_version:number;reason:'eligible'|'unavailable'|'changed'|'not_submitted';current:Order|null}
export type OrderCursor={id:string;total:number|null}
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const integer=(v:unknown,max=2147483647)=>Number.isSafeInteger(v)&&(v as number)>=0&&(v as number)<=max
const date=(v:unknown)=>typeof v==='string'&&Number.isFinite(Date.parse(v))
const object=(v:unknown)=>{if(!v||typeof v!=='object'||Array.isArray(v))throw new Error();return v as Record<string,unknown>}
export function parseCriteria(v:unknown):Criteria{
  const r=object(v)
  if(!(r.status===null||['draft','submitted','fulfilled'].includes(r.status as string))||!(r.minimum===null||integer(r.minimum))||!(r.maximum===null||integer(r.maximum))||r.minimum!==null&&r.maximum!==null&&(r.minimum as number)>(r.maximum as number)||!['id_asc','id_desc','total_asc','total_desc'].includes(r.sort as string))throw new Error('Invalid view filters')
  return {status:r.status as Criteria['status'],minimum:r.minimum as number|null,maximum:r.maximum as number|null,sort:r.sort as Criteria['sort']}
}
export function parseView(v:unknown):View{
  const r=object(v),c=parseCriteria({status:r.status_filter,minimum:r.minimum_total,maximum:r.maximum_total,sort:r.sort_order})
  if(typeof r.view_id!=='string'||!uuid.test(r.view_id)||typeof r.tenant_id!=='string'||!r.tenant_id||typeof r.subject!=='string'||!r.subject||typeof r.name!=='string'||!r.name.trim()||r.name.length>80||typeof r.revision!=='string'||!uuid.test(r.revision)||!date(r.changed_at))throw new Error('Invalid saved view')
  return {view_id:r.view_id,tenant_id:r.tenant_id,subject:r.subject,name:r.name,status_filter:c.status,minimum_total:c.minimum,maximum_total:c.maximum,sort_order:c.sort,revision:r.revision,changed_at:r.changed_at as string}
}
export function parseSelection(v:unknown):Selection[]{
  if(!Array.isArray(v)||v.length<1||v.length>20)throw new Error('Select 1 to 20 orders')
  const rows=v.map(value=>{const r=object(value);if(Object.keys(r).length!==2||typeof r.id!=='string'||!uuid.test(r.id)||!integer(r.version,2147483646)||(r.version as number)<1)throw new Error();return {id:r.id.toLowerCase(),version:r.version as number}}).sort((a,b)=>a.id.localeCompare(b.id))
  if(new Set(rows.map(r=>r.id)).size!==rows.length)throw new Error('Duplicate selection')
  return rows
}
function parsePreview(v:unknown):Preview[]{
  if(!Array.isArray(v))throw new Error()
  return v.map(value=>{const r=object(value);parseSelection([{id:r.id,version:r.expected_version}]);if(!['eligible','unavailable','changed','not_submitted'].includes(r.reason as string))throw new Error();const current=r.current===null?null:parseOrder(r.current);if((r.reason==='unavailable')!==(current===null)||current&&current.id!==r.id||r.reason==='eligible'&&(current!.status!=='submitted'||current!.version!==r.expected_version)||r.reason==='changed'&&current!.version===r.expected_version||r.reason==='not_submitted'&&(current!.status==='submitted'||current!.version!==r.expected_version))throw new Error();return {id:r.id as string,expected_version:r.expected_version as number,reason:r.reason as Preview['reason'],current}})
}
export function parseBulkOperation(v:unknown){
  const r=object(v),reviewed=parseSelection(r.reviewed)
  if(typeof r.tenant_id!=='string'||!r.tenant_id||typeof r.actor!=='string'||!r.actor||typeof r.operation_id!=='string'||!uuid.test(r.operation_id)||r.action!=='fulfil'||!date(r.occurred_at)||!Array.isArray(r.result)||r.result.length!==reviewed.length)throw new Error()
  const result=r.result.map(parseOrder).sort((a,b)=>a.id.localeCompare(b.id))
  if(result.some((row,i)=>row.id!==reviewed[i]!.id||row.version!==reviewed[i]!.version+1||row.status!=='fulfilled'||row.tenant_id!==r.tenant_id))throw new Error()
  return {tenant_id:r.tenant_id,actor:r.actor,operation_id:r.operation_id,action:'fulfil' as const,reviewed,result,occurred_at:r.occurred_at as string}
}
export function orderOperations(client:ReturnType<typeof teamsClient>){return {
  async views(after?:string){
    if(after&&!uuid.test(after))throw new Error();const member=await client.membership();if(!member)throw new Error()
    let query=client.db.from('team_order_views').select().eq('tenant_id',client.tenant).order('view_id').limit(21);if(after)query=query.gt('view_id',after)
    const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseView)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant||r.subject!==member.subject))throw new Error('Could not load saved views')
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.view_id:null}
  },
  async orders(value:unknown,after?:OrderCursor,pageSize=20){
    const c=parseCriteria(value),totalSort=c.sort.startsWith('total_')
    if(!integer(pageSize,100)||pageSize<1||after&&(!uuid.test(after.id)||!(after.total===null||integer(after.total))||totalSort!==(after.total!==null)))throw new Error('Invalid cursor')
    const checked=validateRpcResponse(await dataRequest(client.db.rpc('list_view_orders',{team_id:client.tenant,status_filter:c.status,minimum_total:c.minimum,maximum_total:c.maximum,sort_order:c.sort,after_id:after?.id??null,after_total:after?.total??null,page_size:pageSize}).select('id,tenant_id,created_by,total_cents,status,version'),{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseOrder)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant||c.status!==null&&r.status!==c.status||c.minimum!==null&&r.total_cents<c.minimum||c.maximum!==null&&r.total_cents>c.maximum))throw new Error('Could not load orders. Check your session and try again.')
    const rows=checked.result.data.slice(0,pageSize),last=rows.at(-1);return {rows,next:checked.result.data.length>pageSize?{id:last!.id,total:totalSort?last!.total_cents:null}:null}
  },
  async preview(value:unknown){
    const selection=parseSelection(value),member=await client.membership();if(!member||member.role==='member')throw new Error('Writer required')
    const checked=validateRpcResponse(await dataRequest(client.db.rpc('preview_team_order_fulfilment',{team_id:client.tenant,reviewed:selection}),{timeoutMs:5000}),{parse:parsePreview})
    if(checked.outcome!=='validated'||checked.result.data.length!==selection.length||checked.result.data.some((r,i)=>r.id!==selection[i]!.id||r.expected_version!==selection[i]!.version||r.current&&r.current.tenant_id!==client.tenant))throw new Error('Could not preview selection')
    return checked.result.data
  },
  async history(after?:string){
    if(after&&!uuid.test(after))throw new Error();let query=client.db.from('team_bulk_order_operations').select().eq('tenant_id',client.tenant).order('operation_id').limit(21);if(after)query=query.gt('operation_id',after)
    const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseBulkOperation)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant))throw new Error()
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.operation_id:null}
  },
}}
export function createBulkFulfilmentForm(client:ReturnType<typeof teamsClient>){
  let pending:Readonly<{actor:string;key:string;selection:readonly Readonly<Selection>[];orders:readonly Readonly<Order>[]}>|null=null,busy=false
  async function execute(value:unknown,confirmed:boolean,recovering=false){
    if(busy)return {outcome:'busy' as const};if(!recovering&&pending)return {outcome:'pending' as const};if(recovering&&!pending)return {outcome:'empty' as const};busy=true
    try{
      let preview:Preview[]|undefined,selection:Selection[]|undefined
      if(!recovering){if(confirmed!==true)throw new Error();preview=parsePreview(value);selection=parseSelection(preview.map(r=>({id:r.id,version:r.expected_version})));if(preview.length!==selection.length||preview.some(r=>r.reason!=='eligible'||r.current!.tenant_id!==client.tenant))throw new Error();preview.sort((a,b)=>a.id.localeCompare(b.id))}
      const check=(v:unknown)=>{if(!Array.isArray(v)||v.length!==1)throw new Error();const receipt=parseBulkOperation(v[0]),p=pending!;if(receipt.actor!==p.actor||receipt.tenant_id!==client.tenant||receipt.operation_id!==p.key||JSON.stringify(receipt.reviewed)!==JSON.stringify(p.selection)||receipt.result.some((r,i)=>r.total_cents!==p.orders[i]!.total_cents||r.created_by!==p.orders[i]!.created_by))throw new Error();return receipt}
      if(recovering){const result=await dataRequest(client.db.rpc('get_team_bulk_order_receipt',{team_id:client.tenant,operation_key:pending!.key}),{timeoutMs:5000});if(result.error)throw new Error();if(Array.isArray(result.data)&&result.data.length){const receipt=check(result.data);pending=null;return {outcome:'fulfilled' as const,receipt}}}
      const member=await client.membership();if(!member||member.role==='member'||pending&&member.subject!==pending.actor)return {outcome:'failed' as const,message:'Use the original current owner or admin to apply or retry this batch.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,key:crypto.randomUUID(),selection:Object.freeze(selection!.map(r=>Object.freeze({...r}))),orders:Object.freeze(preview!.map(r=>Object.freeze({...r.current!})))})
      const result=await dataRequest(client.db.rpc('fulfil_team_orders_once',{team_id:client.tenant,operation_id:pending.key,reviewed:pending.selection.map(r=>({...r}))}),{timeoutMs:5000}),checked=validateRpcResponse(result,{parse:check})
      if(checked.outcome==='validated'){pending=null;return {outcome:'fulfilled' as const,receipt:checked.result.data}}
      const known=['PT400','PT409'].includes(result.error?.code??'')&&['invalid_order_selection','bulk_order_payload_conflict','bulk_order_preview_stale'].includes(result.error?.message??'')
      // A serialized stale-batch refusal checks for the original receipt first,
      // so it proves this key did not commit and releases the reviewed selection.
      if(known&&(!recovering||result.error?.message==='bulk_order_preview_stale')||!recovering&&[401,403].includes(result.status))pending=null
      return {outcome:'failed' as const,message:known?'The reviewed batch changed or is unavailable. Nothing was applied; reload and preview again.':'Could not confirm the batch. Resolve the original operation before another change.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'Resolve the original batch operation before another change.':'Preview 1 to 20 eligible submitted orders and approve the reviewed batch.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},submit:(preview:unknown,confirmed:boolean)=>execute(preview,confirmed),reconcile:()=>execute(undefined,false,true)}
}
export function createOrderViewForm(client:ReturnType<typeof teamsClient>){
  let pending:Readonly<{actor:string;args:NonNullable<Database['api']['Functions']['save_order_view_once']['Args']>}>|null=null,busy=false
  async function execute(value:unknown,recovering=false){
    if(busy)return {outcome:'busy' as const};if(!recovering&&pending)return {outcome:'pending' as const};if(recovering&&!pending)return {outcome:'empty' as const};busy=true
    try{
      let prepared:NonNullable<typeof pending>['args']|undefined
      if(!recovering){const v=object(value),c=parseCriteria(v.criteria);if(typeof v.name!=='string'||!v.name.trim()||v.name.length>80||v.id!==undefined&&(typeof v.id!=='string'||!uuid.test(v.id))||!(v.revision===null||typeof v.revision==='string'&&uuid.test(v.revision))||(v.id===undefined)!==(v.revision===null))throw new Error();prepared={team_id:client.tenant,request_id:crypto.randomUUID(),view_id:v.id as string|undefined??crypto.randomUUID(),expected_revision:v.revision as string|null,view_name:v.name.trim(),status_filter:c.status,minimum_total:c.minimum,maximum_total:c.maximum,sort_order:c.sort}}
      const check=(v:unknown)=>{if(!Array.isArray(v)||v.length!==1)throw new Error();const r=object(v[0]),snapshot=parseView(r.snapshot),payload=object(r.payload),p=pending!,a=p.args;if(r.tenant_id!==client.tenant||r.actor!==p.actor||r.request_id!==a.request_id||r.view_id!==a.view_id||snapshot.subject!==p.actor||snapshot.tenant_id!==client.tenant||snapshot.view_id!==a.view_id||snapshot.name!==a.view_name||snapshot.status_filter!==a.status_filter||snapshot.minimum_total!==a.minimum_total||snapshot.maximum_total!==a.maximum_total||snapshot.sort_order!==a.sort_order||payload.revision!==a.expected_revision)throw new Error();return snapshot}
      if(recovering){const result=await dataRequest(client.db.rpc('get_order_view_receipt',{team_id:client.tenant,operation_key:pending!.args.request_id}),{timeoutMs:5000});if(result.error)throw new Error();if(Array.isArray(result.data)&&result.data.length){const view=check(result.data);pending=null;return {outcome:'saved' as const,view}}}
      const member=await client.membership();if(!member||pending&&member.subject!==pending.actor)return {outcome:'failed' as const,message:'Use the original current team member to save or retry this view.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,args:Object.freeze({...prepared!})})
      const result=await dataRequest(client.db.rpc('save_order_view_once',pending.args),{timeoutMs:5000}),checked=validateRpcResponse(result,{parse:check})
      if(checked.outcome==='validated'){pending=null;return {outcome:'saved' as const,view:checked.result.data}}
      const known=['PT400','PT409'].includes(result.error?.code??'')&&['invalid_order_view','order_view_revision_conflict','order_view_payload_conflict'].includes(result.error?.message??'')||result.error?.code==='23505'
      if(known&&(!recovering||result.error?.message==='order_view_revision_conflict')||!recovering&&[401,403].includes(result.status))pending=null
      return {outcome:'failed' as const,message:known?'The saved view changed or its name is already used. Reload and review.':'Could not confirm the view. Resolve the original operation before another change.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'Resolve the original saved-view operation before another change.':'Choose a name, valid status, whole-cent range and sort order.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},submit:(value:unknown)=>execute(value),reconcile:()=>execute(undefined,true)}
}
