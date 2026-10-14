import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import {parseOrder} from './orders.js'
import type {Order,teamsClient} from './orders.js'
type Action='submitted'|'fulfilled'
export type OrderEvent=Database['api']['Tables']['team_order_events']['Row']
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
function parseEvent(value:unknown):OrderEvent {
  const r=value as OrderEvent
  if(!r||!uuid.test(r.order_id)||typeof r.tenant_id!=='string'||!r.tenant_id||typeof r.actor!=='string'||!r.actor||!Number.isInteger(r.version)||r.version<2||r.version>2147483647||!Number.isInteger(r.total_cents)||r.total_cents<0||r.total_cents>2147483647||typeof r.occurred_at!=='string'||!Number.isFinite(Date.parse(r.occurred_at))||!(r.action==='edit'&&r.before_status==='draft'&&r.after_status==='draft'||r.action==='submit'&&r.before_status==='draft'&&r.after_status==='submitted'||r.action==='fulfil'&&r.before_status==='submitted'&&r.after_status==='fulfilled'))throw new Error('Invalid order event')
  return {tenant_id:r.tenant_id,order_id:r.order_id,version:r.version,actor:r.actor,action:r.action,before_status:r.before_status,after_status:r.after_status,total_cents:r.total_cents,occurred_at:r.occurred_at}
}
export function orderWorkflow(client:ReturnType<typeof teamsClient>){return {
  async history(id:string,after=1){
    if(!uuid.test(id)||!Number.isInteger(after)||after<1||after>2147483647)throw new Error('Invalid history cursor')
    const result=await dataRequest(client.db.from('team_order_events').select('tenant_id,order_id,version,actor,action,before_status,after_status,total_cents,occurred_at').eq('tenant_id',client.tenant).eq('order_id',id).gt('version',after).order('version').limit(21),{timeoutMs:5000})
    const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseEvent)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant||r.order_id!==id.toLowerCase()||r.version<=after))throw new Error('Could not load order history')
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.version:null}
  },
}}
type Pending=Readonly<{actor:string;key:string;order:Readonly<Order>;next:Action}>
export function createWorkflowForm(client:ReturnType<typeof teamsClient>,options:{createKey?:()=>string}={}){
  let pending:Pending|null=null,busy=false
  async function execute(input:unknown,recovering=false){
    if(busy)return {outcome:'busy' as const}
    if(!recovering&&pending)return {outcome:'pending' as const}
    if(recovering&&!pending)return {outcome:'empty' as const}
    busy=true
    try{
      let prepared:{order:Order;next:Action;key:string}|undefined
      if(!recovering){
        const v=input as {order:unknown;next:unknown},order=parseOrder(v?.order)
        if(order.tenant_id!==client.tenant||order.version>=2147483647||!(order.status==='draft'&&v.next==='submitted'||order.status==='submitted'&&v.next==='fulfilled'))return {outcome:'invalid' as const,message:'Choose a current draft to submit or a submitted order to fulfil.'}
        const key=(options.createKey??(()=>crypto.randomUUID()))();if(!uuid.test(key))throw new Error()
        prepared={order,next:v.next as Action,key}
      }
      const member=await client.membership()
      if(!member||member.role==='member'||pending&&member.subject!==pending.actor)return {outcome:'failed' as const,message:'Use the original owner or admin to resolve this order action.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,key:prepared!.key,order:Object.freeze({...prepared!.order}),next:prepared!.next})
      const operation=pending
      const result=await dataRequest(client.db.rpc('transition_team_order_once',{request_key:operation.key,team_id:client.tenant,order_id:operation.order.id,expected_version:operation.order.version,next_state:operation.next}),{timeoutMs:5000})
      const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v)||v.length!==1)throw new Error();const row=parseOrder(v[0]);if(row.id!==operation.order.id||row.tenant_id!==client.tenant||row.version!==operation.order.version+1||row.status!==operation.next||row.total_cents!==operation.order.total_cents||row.created_by!==operation.order.created_by)throw new Error();return row}})
      if(checked.outcome==='validated'){pending=null;return {outcome:'changed' as const,order:checked.result.data}}
      const messages:Record<string,string>={order_version_conflict:'The order changed or is unavailable. Reload before taking another action.',invalid_order_transition:'That transition is not allowed. Reload the order.',order_not_editable:'Submitted and fulfilled orders cannot be edited.',idempotency_payload_conflict:'This request key belongs to a different order action.',invalid_order_action:'Choose a valid order action and current version.'}
      const message=['PT400','PT409'].includes(result.error?.code??'')?messages[result.error!.message]:undefined
      if(!recovering&&(message||[401,403].includes(result.status)))pending=null
      return {outcome:'failed' as const,message:message??'The action could not be confirmed. Resolve the original operation before changing the order again.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'The action could not be confirmed. Resolve the original operation.':'Choose a valid current order before taking an action.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},submit:(order:unknown,next:unknown)=>execute({order,next}),reconcile:()=>execute(undefined,true)}
}
