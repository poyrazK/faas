import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import type {teamsClient} from './orders.js'
import {parseView,parseCriteria} from './order-operations.js'
export type SharedView=Database['api']['Tables']['team_shared_order_views']['Row']
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const object=(v:unknown)=>{if(!v||typeof v!=='object'||Array.isArray(v))throw new Error();return v as Record<string,unknown>}
export function parseSharedView(v:unknown):SharedView{
  const r=object(v),view=parseView({...r,subject:r.changed_by})
  if(!Number.isSafeInteger(r.version)||(r.version as number)<1||(r.version as number)>2147483647)throw new Error()
  return {view_id:view.view_id.toLowerCase(),tenant_id:view.tenant_id,name:view.name,status_filter:view.status_filter,minimum_total:view.minimum_total,maximum_total:view.maximum_total,sort_order:view.sort_order,revision:view.revision.toLowerCase(),version:r.version as number,changed_by:view.subject,changed_at:view.changed_at}
}
export function parseSharedOperation(v:unknown){
  const r=object(v),shared=parseSharedView(r.shared_snapshot),personal=r.personal_snapshot===null?null:parseView(r.personal_snapshot)
  if(typeof r.actor!=='string'||!r.actor||typeof r.request_id!=='string'||!uuid.test(r.request_id)||r.view_id!==shared.view_id||r.tenant_id!==shared.tenant_id||!['publish','update','copy'].includes(r.action as string)||typeof r.occurred_at!=='string'||!Number.isFinite(Date.parse(r.occurred_at))||(r.action==='copy')!==(personal!==null)||personal&&(personal.subject!==r.actor||personal.tenant_id!==shared.tenant_id||personal.status_filter!==shared.status_filter||personal.minimum_total!==shared.minimum_total||personal.maximum_total!==shared.maximum_total||personal.sort_order!==shared.sort_order)||r.action!=='copy'&&shared.changed_by!==r.actor)throw new Error()
  return {tenant_id:shared.tenant_id,actor:r.actor,request_id:r.request_id,view_id:shared.view_id,action:r.action as 'publish'|'update'|'copy',payload:object(r.payload),shared,personal,occurred_at:r.occurred_at}
}
export function sharedOrderViews(client:ReturnType<typeof teamsClient>){return {
  async list(after?:string){
    if(after&&!uuid.test(after))throw new Error();if(!await client.membership())throw new Error()
    let query=client.db.from('team_shared_order_views').select().eq('tenant_id',client.tenant).order('view_id').limit(21);if(after)query=query.gt('view_id',after)
    const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseSharedView)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant))throw new Error()
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.view_id:null}
  },
  async history(id:string,after?:string){
    if(!uuid.test(id)||after&&!uuid.test(after))throw new Error();id=id.toLowerCase();if(!await client.membership())throw new Error()
    let query=client.db.from('team_shared_view_operations').select().eq('tenant_id',client.tenant).eq('view_id',id).neq('action','copy').order('request_id').limit(21);if(after)query=query.gt('request_id',after)
    const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseSharedOperation)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant||r.view_id!==id||r.action==='copy'))throw new Error()
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.request_id:null}
  },
}}
type SaveArgs=NonNullable<Database['api']['Functions']['save_shared_order_view_once']['Args']>
type CopyArgs=NonNullable<Database['api']['Functions']['copy_shared_order_view_once']['Args']>
export function createSharedViewForm(client:ReturnType<typeof teamsClient>){
  let pending:Readonly<{actor:string;kind:'save';args:Readonly<SaveArgs>}|{actor:string;kind:'copy';args:Readonly<CopyArgs>;source:Readonly<SharedView>}>|null=null,busy=false
  async function execute(kind:'save'|'copy'|'recover',value:unknown){
    if(busy)return {outcome:'busy' as const};if(kind!=='recover'&&pending)return {outcome:'pending' as const};if(kind==='recover'&&!pending)return {outcome:'empty' as const};busy=true
    try{
      let prepared:Omit<Extract<NonNullable<typeof pending>,{kind:'save'}>,'actor'>|Omit<Extract<NonNullable<typeof pending>,{kind:'copy'}>,'actor'>|undefined
      if(kind==='save'){
        const v=object(value),c=parseCriteria(v.criteria)
        if(typeof v.name!=='string'||!v.name.trim()||v.name.length>80||v.id!==undefined&&(typeof v.id!=='string'||!uuid.test(v.id))||!(v.revision===null||typeof v.revision==='string'&&uuid.test(v.revision))||(v.id===undefined)!==(v.revision===null))throw new Error()
        prepared={kind:'save',args:Object.freeze({team_id:client.tenant,request_id:crypto.randomUUID(),view_id:typeof v.id==='string'?v.id.toLowerCase():crypto.randomUUID(),expected_revision:(v.revision as string|null)?.toLowerCase()??null,view_name:v.name.trim(),status_filter:c.status,minimum_total:c.minimum,maximum_total:c.maximum,sort_order:c.sort})}
      }
      if(kind==='copy'){
        const v=object(value),source=parseSharedView(v.source)
        if(source.tenant_id!==client.tenant||typeof v.name!=='string'||!v.name.trim()||v.name.length>80)throw new Error()
        prepared={kind:'copy',source:Object.freeze(source),args:Object.freeze({team_id:client.tenant,request_id:crypto.randomUUID(),shared_view_id:source.view_id,expected_revision:source.revision,personal_view_id:crypto.randomUUID(),personal_name:v.name.trim()})}
      }
      const check=(v:unknown)=>{
        if(!Array.isArray(v)||v.length!==1)throw new Error();const r=parseSharedOperation(v[0]),p=pending!
        if(r.tenant_id!==client.tenant||r.actor!==p.actor||r.request_id!==p.args.request_id||r.payload.revision!==p.args.expected_revision)throw new Error()
        if(p.kind==='save'){const a=p.args;if(r.action!==(a.expected_revision===null?'publish':'update')||r.view_id!==a.view_id||r.shared.name!==a.view_name||r.shared.status_filter!==a.status_filter||r.shared.minimum_total!==a.minimum_total||r.shared.maximum_total!==a.maximum_total||r.shared.sort_order!==a.sort_order)throw new Error()}
        else {const a=p.args;if(r.action!=='copy'||r.view_id!==a.shared_view_id||JSON.stringify({...r.shared,changed_at:Date.parse(r.shared.changed_at)})!==JSON.stringify({...p.source,changed_at:Date.parse(p.source.changed_at)})||r.personal!.view_id!==a.personal_view_id||r.personal!.name!==a.personal_name)throw new Error()}
        return r
      }
      if(kind==='recover'){const result=await dataRequest(client.db.rpc('get_shared_order_view_receipt',{team_id:client.tenant,operation_key:pending!.args.request_id}),{timeoutMs:5000});if(result.error)throw new Error();if(Array.isArray(result.data)&&result.data.length){const receipt=check(result.data);pending=null;return {outcome:'confirmed' as const,receipt}}}
      const member=await client.membership(),effective=pending?.kind??kind
      if(!member||effective==='save'&&member.role==='member'||pending&&member.subject!==pending.actor)return {outcome:'failed' as const,message:'Use the original current team member to resolve this operation. Publishing requires owner or admin access.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,...prepared!})
      const result=await dataRequest(pending.kind==='save'?client.db.rpc('save_shared_order_view_once',pending.args):client.db.rpc('copy_shared_order_view_once',pending.args),{timeoutMs:5000}),checked=validateRpcResponse(result,{parse:check})
      if(checked.outcome==='validated'){pending=null;return {outcome:'confirmed' as const,receipt:checked.result.data}}
      const known=['PT400','PT409'].includes(result.error?.code??'')&&['invalid_shared_view','shared_view_revision_conflict','shared_view_payload_conflict','order_view_revision_conflict','order_view_payload_conflict'].includes(result.error?.message??'')||result.error?.code==='23505'
      if(known&&(kind!=='recover'||result.error?.message==='shared_view_revision_conflict')||kind!=='recover'&&[401,403].includes(result.status))pending=null
      return {outcome:'failed' as const,message:known?'The shared view changed or the destination name is already used. Reload and review.':'Could not confirm this operation. Resolve it before another change.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'Resolve the original shared-view operation before another change.':'Choose valid saved filters and a name, or select a shared view to copy.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},save:(value:unknown)=>execute('save',value),copy:(source:unknown,name:string)=>execute('copy',{source,name}),reconcile:()=>execute('recover',undefined)}
}
