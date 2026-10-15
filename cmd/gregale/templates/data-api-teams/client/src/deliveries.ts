import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import type {teamsClient} from './orders.js'
const fields='event_id,tenant_id,order_id,version,state,attempts,total_attempts,generation,available_at,lease_until,delivered_at' as const
export type Delivery=Pick<Database['api']['Tables']['team_order_outbox']['Row'],'event_id'|'tenant_id'|'order_id'|'version'|'state'|'attempts'|'total_attempts'|'generation'|'available_at'|'lease_until'|'delivered_at'>
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const date=(value:unknown)=>typeof value==='string'&&Number.isFinite(Date.parse(value))
export function parseDelivery(value:unknown):Delivery{
  const r=value as Delivery
  if(!r||!uuid.test(r.event_id)||!uuid.test(r.order_id)||typeof r.tenant_id!=='string'||!r.tenant_id||!['pending','leased','delivered','dead'].includes(r.state)||![r.version,r.attempts,r.total_attempts,r.generation].every(n=>Number.isSafeInteger(n)&&n>=0)||r.version<2||r.attempts>5||r.total_attempts<r.attempts||!date(r.available_at)||!(r.lease_until===null||date(r.lease_until))||!(r.delivered_at===null||date(r.delivered_at))||(r.state==='leased')!==(r.lease_until!==null)||(r.state==='delivered')!==(r.delivered_at!==null))throw new Error('Invalid delivery')
  return Object.fromEntries(fields.split(',').map(key=>[key,(r as unknown as Record<string,unknown>)[key]])) as Delivery
}
export function webhookDeliveries(client:ReturnType<typeof teamsClient>){return {
  async list(after?:string){
    if(after&&!uuid.test(after))throw new Error('Invalid delivery cursor')
    if((await client.membership())?.role!=='owner')throw new Error('Owner required')
    let query=client.db.from('team_order_outbox').select(fields).eq('tenant_id',client.tenant).order('event_id').limit(21)
    if(after)query=query.gt('event_id',after)
    const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseDelivery)}})
    if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant))throw new Error('Could not load deliveries')
    const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.event_id:null}
  },
  async attempts(id:string,after=0){
    if(!uuid.test(id)||!Number.isInteger(after)||after<0)throw new Error('Invalid attempt cursor')
    if((await client.membership())?.role!=='owner')throw new Error('Owner required')
    const result=await dataRequest(client.db.from('team_order_delivery_attempts').select('event_id,tenant_id,attempt,started_at,finished_at,outcome,http_status').eq('tenant_id',client.tenant).eq('event_id',id).gt('attempt',after).order('attempt').limit(21),{timeoutMs:5000})
    if(result.error||!Array.isArray(result.data)||result.data.some(r=>r.event_id!==id||r.tenant_id!==client.tenant||!Number.isSafeInteger(r.attempt)||r.attempt<=after||!date(r.started_at)||!['leased','delivered','failed','expired'].includes(r.outcome)||(r.outcome==='leased')!==(r.finished_at===null)||r.finished_at!==null&&!date(r.finished_at)||r.http_status!==null&&(!Number.isInteger(r.http_status)||r.http_status<100||r.http_status>599)))throw new Error('Could not load attempts')
    const rows=result.data.slice(0,20);return {rows,next:result.data.length>20?rows.at(-1)!.attempt:null}
  },
}}
export function createReplayForm(client:ReturnType<typeof teamsClient>,subscription?:string){
  let pending:Readonly<{actor:string;row:Readonly<Delivery>}>|null=null,busy=false
  async function execute(input:unknown,recovering=false){
    if(busy)return {outcome:'busy' as const}
    if(!recovering&&pending)return {outcome:'pending' as const}
    if(recovering&&!pending)return {outcome:'empty' as const}
    busy=true
    try{
      let row:Delivery|undefined
      if(!recovering){row=parseDelivery(input);if(row.tenant_id!==client.tenant||!(row.state==='dead'||row.state==='leased'&&row.attempts===5&&Date.parse(row.lease_until!)<=Date.now()))return {outcome:'failed' as const,message:'Only a failed or expired final delivery can be replayed.',uncertain:false}}
      const member=await client.membership()
      if(member?.role!=='owner'||pending&&pending.actor!==member.subject)return {outcome:'failed' as const,message:'Use the original owner to resolve this replay.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,row:Object.freeze({...row!})})
      const op=pending.row
      const query=recovering?client.db.from(subscription?'team_subscription_outbox':'team_order_outbox').select(fields).eq('tenant_id',client.tenant).eq('event_id',op.event_id):subscription?client.db.rpc('replay_subscription_webhook',{team_id:client.tenant,subscription_id:subscription,delivery_id:op.event_id,expected_generation:op.generation}).select(fields):client.db.rpc('replay_team_order_webhook',{team_id:client.tenant,delivery_id:op.event_id,expected_generation:op.generation}).select(fields)
      const result=await dataRequest(query,{timeoutMs:5000})
      if(!recovering&&subscription&&result.error?.code==='PT409'&&result.error.message==='webhook_test_pending'){pending=null;return {outcome:'failed' as const,message:'Another sample test is pending. Wait for it to finish before replaying this sample.',uncertain:false}}
      if(!recovering&&(result.status===401||result.status===403||!result.error&&Array.isArray(result.data)&&result.data.length===0)){pending=null;return {outcome:'failed' as const,message:'Replay was refused or the generation changed. Reload deliveries before another replay.',uncertain:false}}
      const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v)||v.length!==1)throw new Error();return parseDelivery(v[0])}})
      if(checked.outcome==='validated'&&checked.result.data.event_id===op.event_id&&checked.result.data.tenant_id===client.tenant&&checked.result.data.generation===op.generation+1){pending=null;return {outcome:'replayed' as const,row:checked.result.data}}
      if(!recovering&&checked.outcome==='validated')pending=null
      // Empty or lost results require read-only reconciliation. Never send a
      // fresh generation automatically, since another owner may have replayed it.
      return {outcome:'failed' as const,message:'Replay could not be confirmed. Reload deliveries or resolve the original generation before replaying again.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'Resolve this replay before starting another.':'Choose a current failed delivery to replay.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},submit:(row:unknown)=>execute(row),reconcile:()=>execute(undefined,true)}
}
