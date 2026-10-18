import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import type {teamsClient} from './orders.js'
import {parseDelivery} from './deliveries.js'
import type {Delivery} from './deliveries.js'
export type Health=Readonly<{team:string;paused:boolean;revision:string|null;pending:number;leased:number;delivered:number;dead:number;oldest_pending_seconds:number|null}>
export type DeliveryOperation=Database['api']['Tables']['team_delivery_operations']['Row']
export type ReviewedDelivery=Readonly<{event_id:string;generation:number}>
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const object=(v:unknown)=>{if(!v||typeof v!=='object'||Array.isArray(v))throw new Error();return v as Record<string,unknown>}
export function parseHealth(value:unknown):Health{
  const r=object(value)
  if(typeof r.team!=='string'||!r.team||typeof r.paused!=='boolean'||r.revision!==null&&(typeof r.revision!=='string'||!uuid.test(r.revision))||!['pending','leased','delivered','dead'].every(k=>Number.isSafeInteger(r[k])&&(r[k] as number)>=0)||!(r.oldest_pending_seconds===null||Number.isSafeInteger(r.oldest_pending_seconds)&&(r.oldest_pending_seconds as number)>=0)||((r.pending===0)!==(r.oldest_pending_seconds===null)))throw new Error('Invalid health response')
  return {team:r.team,paused:r.paused,revision:r.revision as string|null,pending:r.pending as number,leased:r.leased as number,delivered:r.delivered as number,dead:r.dead as number,oldest_pending_seconds:r.oldest_pending_seconds as number|null}
}
const reviewed=(value:unknown):ReviewedDelivery[]=>{
  if(!Array.isArray(value)||!value.length||value.length>20)throw new Error('Invalid replay preview')
  const rows=value.map(v=>{const r=object(v);if(typeof r.event_id!=='string'||!uuid.test(r.event_id)||!Number.isInteger(r.generation)||(r.generation as number)<0||(r.generation as number)>2147483646)throw new Error();return {event_id:r.event_id.toLowerCase(),generation:r.generation as number}})
  if(new Set(rows.map(r=>r.event_id)).size!==rows.length)throw new Error()
  return rows.sort((a,b)=>a.event_id.localeCompare(b.event_id))
}
export function parseOperation(value:unknown):DeliveryOperation{
  const r=object(value)
  if(typeof r.operation_id!=='string'||!uuid.test(r.operation_id)||typeof r.tenant_id!=='string'||!r.tenant_id||typeof r.actor!=='string'||!r.actor||!['pause','resume','batch_replay','replay'].includes(r.action as string)||typeof r.occurred_at!=='string'||!Number.isFinite(Date.parse(r.occurred_at)))throw new Error('Invalid operation response')
  const payload=r.action==='batch_replay'?{}:object(r.payload),result=object(r.result)
  if(r.action==='pause'||r.action==='resume'){
    if(payload.revision!==null&&(typeof payload.revision!=='string'||!uuid.test(payload.revision))||payload.paused!==(r.action==='pause')||result.paused!==payload.paused||typeof result.revision!=='string'||!uuid.test(result.revision))throw new Error()
  }else if(r.action==='batch_replay'){
    const before=reviewed(r.payload),after=reviewed(result.replayed)
    if(before.length!==after.length||before.some((row,i)=>row.event_id!==after[i].event_id||row.generation+1!==after[i].generation))throw new Error()
  }else if(typeof payload.event_id!=='string'||!uuid.test(payload.event_id)||!Number.isInteger(payload.generation)||result.event_id!==payload.event_id||result.generation!==(payload.generation as number)+1)throw new Error()
  return {operation_id:r.operation_id,tenant_id:r.tenant_id,actor:r.actor,action:r.action as string,occurred_at:r.occurred_at,payload:r.payload as DeliveryOperation['payload'],result:r.result as DeliveryOperation['result']}
}
export function deliveryControls(client:ReturnType<typeof teamsClient>){return {
  async health(){const checked=validateRpcResponse(await dataRequest(client.db.rpc('get_team_delivery_health',{team_id:client.tenant}),{timeoutMs:5000}),{parse:parseHealth});if(checked.outcome!=='validated'||checked.result.data.team!==client.tenant)throw new Error('Owner health unavailable');return checked.result.data},
  async preview(){
    const checked=validateRpcResponse(await dataRequest(client.db.rpc('preview_team_delivery_replay',{team_id:client.tenant,page_size:20}).select('event_id,tenant_id,order_id,version,state,attempts,total_attempts,generation,available_at,lease_until,delivered_at'),{timeoutMs:5000}),{parse(v){if(!Array.isArray(v)||v.length>20)throw new Error();return v.map(parseDelivery)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant||!(r.state==='dead'||r.state==='leased'&&r.attempts===5&&Date.parse(r.lease_until!)<=Date.now())))throw new Error('Replay preview unavailable')
    return checked.result.data
  },
  async history(after?:string){
    if(after&&!uuid.test(after))throw new Error('Invalid operation cursor')
    if((await client.membership())?.role!=='owner')throw new Error('Owner required')
    let query=client.db.from('team_delivery_operations').select().eq('tenant_id',client.tenant).order('operation_id').limit(21)
    if(after)query=query.gt('operation_id',after)
    const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseOperation)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant))throw new Error('Operation history unavailable')
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.operation_id:null}
  },
}}
type Intent=Readonly<{kind:'pause';paused:boolean;revision:string|null}|{kind:'batch';items:readonly ReviewedDelivery[]}>
type Pending=Readonly<{actor:string;key:string;intent:Intent}>
export function createDeliveryControlForm(client:ReturnType<typeof teamsClient>){
  let pending:Pending|null=null,busy=false
  const same=(operation:DeliveryOperation,op:Pending)=>{
    if(operation.tenant_id!==client.tenant||operation.actor!==op.actor||operation.operation_id!==op.key)throw new Error()
    const intent=op.intent
    if(intent.kind==='pause'){
      const payload=object(operation.payload)
      if(operation.action!==(intent.paused?'pause':'resume')||payload.paused!==intent.paused||payload.revision!==intent.revision)throw new Error()
    }else if(operation.action!=='batch_replay'||JSON.stringify(reviewed(operation.payload))!==JSON.stringify(intent.items))throw new Error()
    return operation
  }
  async function execute(input:unknown,recovering=false){
    if(busy)return {outcome:'busy' as const}
    if(!recovering&&pending)return {outcome:'pending' as const}
    if(recovering&&!pending)return {outcome:'empty' as const}
    busy=true
    try{
      let intent:Intent|undefined
      if(!recovering){
        const v=object(input)
        if(v.kind==='pause'){if(typeof v.paused!=='boolean'||v.revision!==null&&(typeof v.revision!=='string'||!uuid.test(v.revision)))throw new Error();intent=Object.freeze({kind:'pause',paused:v.paused,revision:v.revision as string|null})}
        else if(v.kind==='batch')intent=Object.freeze({kind:'batch',items:Object.freeze(reviewed(v.items).map(r=>Object.freeze(r)))})
        else throw new Error()
      }else{
        const result=await dataRequest(client.db.rpc('get_team_delivery_operation',{team_id:client.tenant,request_id:pending!.key}),{timeoutMs:5000})
        const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v)||v.length>1)throw new Error();return v.length?same(parseOperation(v[0]),pending!):null}})
        if(checked.outcome!=='validated')return {outcome:'failed' as const,message:'Could not verify this operation. Use the original signed-in user to resolve it.',uncertain:true}
        if(checked.result.data){const operation=checked.result.data;pending=null;return {outcome:'confirmed' as const,operation}}
        intent=pending!.intent
      }
      const member=await client.membership()
      if(member?.role!=='owner'||pending&&pending.actor!==member.subject)return {outcome:'failed' as const,message:'Only the original current owner can perform or retry this operation.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,key:crypto.randomUUID(),intent:intent!})
      const op=pending,args={team_id:client.tenant,operation_id:op.key}
      const query=op.intent.kind==='pause'?client.db.rpc('set_team_delivery_pause_once',{...args,expected_revision:op.intent.revision,pause_delivery:op.intent.paused}):client.db.rpc('batch_replay_team_deliveries_once',{...args,reviewed:[...op.intent.items]})
      const result=await dataRequest(query,{timeoutMs:5000}),checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v)||v.length!==1)throw new Error();return same(parseOperation(v[0]),op)}})
      if(checked.outcome==='validated'){pending=null;return {outcome:'confirmed' as const,operation:checked.result.data}}
      const known=['PT400','PT409'].includes(result.error?.code??'')&&['invalid_delivery_operation','invalid_replay_preview','delivery_payload_conflict','delivery_revision_conflict','delivery_control_unchanged','delivery_preview_stale'].includes(result.error?.message??'')
      if(!recovering&&(known||[401,403].includes(result.status)))pending=null
      return {outcome:'failed' as const,message:known?'Settings or the reviewed failures changed. Reload health or preview before another operation.':'The operation could not be confirmed. Resolve its original request before another change.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'Resolve the original operation before another change.':'Choose valid current settings or review 1–20 distinct failed deliveries.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},pause:(paused:unknown,revision:unknown)=>execute({kind:'pause',paused,revision}),replay:(items:unknown)=>execute({kind:'batch',items}),reconcile:()=>execute(undefined,true)}
}
