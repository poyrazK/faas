import {dataRequest,validateRpcResponse} from '@gregale/data'
import type {Database} from './database.types.js'
import type {teamsClient} from './orders.js'
import {parseSubscription,parseOperation} from './subscriptions.js'
import {parseDelivery} from './deliveries.js'
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const date=(v:unknown)=>typeof v==='string'&&Number.isFinite(Date.parse(v))
const ref=/^[A-Za-z0-9_-]{1,80}$/
export type TestReceipt=Database['api']['Tables']['team_webhook_test_receipts']['Row']
export type Attempt=Database['api']['Tables']['team_subscription_attempts']['Row']
export function parseTestReceipt(v:unknown):TestReceipt{
  const r=v as TestReceipt
  if(!r||!uuid.test(r.request_id)||!uuid.test(r.subscription_id)||!uuid.test(r.event_id)||!uuid.test(r.expected_revision)||typeof r.actor!=='string'||!r.actor||typeof r.tenant_id!=='string'||!r.tenant_id||!Number.isSafeInteger(r.subscription_version)||r.subscription_version<1||typeof r.config_ref!=='string'||!ref.test(r.config_ref)||!date(r.occurred_at))throw new Error('Invalid test receipt')
  return {tenant_id:r.tenant_id,actor:r.actor,request_id:r.request_id,subscription_id:r.subscription_id,expected_revision:r.expected_revision,event_id:r.event_id,subscription_version:r.subscription_version,config_ref:r.config_ref,occurred_at:r.occurred_at}
}
export function parseAttempt(v:unknown):Attempt{
  const r=v as Attempt
  if(!r||!uuid.test(r.event_id)||!uuid.test(r.subscription_id)||typeof r.tenant_id!=='string'||!r.tenant_id||!Number.isSafeInteger(r.attempt)||r.attempt<1||!date(r.started_at)||!['leased','delivered','failed','expired'].includes(r.outcome)||(r.outcome==='leased')!==(r.finished_at===null)||r.finished_at!==null&&!date(r.finished_at)||r.http_status!==null&&(!Number.isInteger(r.http_status)||r.http_status<100||r.http_status>599)||r.failure_reason!==null&&(!['configuration_missing','network_error','http_error'].includes(r.failure_reason)||r.outcome!=='failed'))throw new Error('Invalid attempt')
  return {event_id:r.event_id,subscription_id:r.subscription_id,tenant_id:r.tenant_id,attempt:r.attempt,started_at:r.started_at,finished_at:r.finished_at,outcome:r.outcome,http_status:r.http_status,failure_reason:r.failure_reason}
}
export function webhookDiagnostics(client:ReturnType<typeof teamsClient>){
  const owner=async()=>{if((await client.membership())?.role!=='owner')throw new Error('Owner required')}
  return {
    async deliveries(id:string,after?:string){
      if(!uuid.test(id)||after&&!uuid.test(after))throw new Error('Invalid cursor');await owner()
      let query=client.db.from('team_subscription_outbox').select('event_id,tenant_id,subscription_id,subscription_version,config_ref,is_test,order_id,version,state,attempts,total_attempts,generation,available_at,lease_until,delivered_at').eq('tenant_id',client.tenant).eq('subscription_id',id).order('event_id').limit(21)
      if(after)query=query.gt('event_id',after)
      const result=await dataRequest(query,{timeoutMs:5000})
      if(result.error||!Array.isArray(result.data))throw new Error('Could not load deliveries')
      const checked=result.data.map(r=>{const delivery=parseDelivery(r);if(r.tenant_id!==client.tenant||r.subscription_id!==id||typeof r.is_test!=='boolean'||!Number.isSafeInteger(r.subscription_version)||r.subscription_version<1||!ref.test(r.config_ref))throw new Error();return {...delivery,subscription_id:r.subscription_id,subscription_version:r.subscription_version,config_ref:r.config_ref,is_test:r.is_test}})
      const rows=checked.slice(0,20);return {rows,next:checked.length>20?rows.at(-1)!.event_id:null}
    },
    async attempts(subscription:string,event:string,after=0){
      if(!uuid.test(subscription)||!uuid.test(event)||!Number.isSafeInteger(after)||after<0)throw new Error('Invalid cursor');await owner()
      const result=await dataRequest(client.db.from('team_subscription_attempts').select('event_id,tenant_id,subscription_id,attempt,started_at,finished_at,outcome,http_status,failure_reason').eq('tenant_id',client.tenant).eq('subscription_id',subscription).eq('event_id',event).gt('attempt',after).order('attempt').limit(21),{timeoutMs:5000})
      const checked=validateRpcResponse(result,{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseAttempt)}})
      if(checked.outcome!=='validated'||checked.result.data.some(r=>r.tenant_id!==client.tenant||r.subscription_id!==subscription||r.event_id!==event||r.attempt<=after))throw new Error('Could not load attempts')
      const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.attempt:null}
    },
    async history(subscription:string,after?:string){
      if(!uuid.test(subscription)||after&&!uuid.test(after))throw new Error('Invalid cursor');await owner()
      let query=client.db.from('team_webhook_subscription_operations').select('request_id,tenant_id,actor,subscription_id,action,payload,snapshot,occurred_at').eq('tenant_id',client.tenant).eq('subscription_id',subscription).order('request_id').limit(21)
      if(after)query=query.gt('request_id',after)
      const checked=validateRpcResponse(await dataRequest(query,{timeoutMs:5000}),{parse(v){if(!Array.isArray(v))throw new Error();return v.map(parseOperation)}})
      if(checked.outcome!=='validated'||checked.result.data.some(r=>r.snapshot.tenant_id!==client.tenant||r.snapshot.subscription_id!==subscription))throw new Error('Could not load history')
      const rows=checked.result.data.slice(0,20);return {rows,next:checked.result.data.length>20?rows.at(-1)!.receipt.request_id:null}
    },
  }
}
export function createWebhookTestForm(client:ReturnType<typeof teamsClient>){
  let pending:Readonly<{actor:string;args:Database['api']['Functions']['queue_webhook_test_once']['Args'];version:number;config:string}>|null=null,busy=false
  async function execute(value:unknown,confirmed:boolean,recovering=false){
    if(busy)return {outcome:'busy' as const};if(!recovering&&pending)return {outcome:'pending' as const};if(recovering&&!pending)return {outcome:'empty' as const};busy=true
    try{
      const selected=recovering?undefined:parseSubscription(value)
      if(!recovering&&(confirmed!==true||selected!.tenant_id!==client.tenant))throw new Error()
      const check=(v:unknown)=>{if(!Array.isArray(v)||v.length!==1)throw new Error();const r=parseTestReceipt(v[0]),p=pending!;if(r.actor!==p.actor||r.tenant_id!==client.tenant||r.request_id!==p.args!.request_id||r.subscription_id!==p.args!.subscription_id||r.expected_revision!==p.args!.expected_revision||r.subscription_version!==p.version||r.config_ref!==p.config)throw new Error();return r}
      if(recovering){const result=await dataRequest(client.db.rpc('get_webhook_test_receipt',{team_id:client.tenant,operation_key:pending!.args!.request_id}),{timeoutMs:5000});if(result.error)throw new Error();if(Array.isArray(result.data)&&result.data.length){const receipt=check(result.data);pending=null;return {outcome:'queued' as const,receipt}}}
      const member=await client.membership()
      if(member?.role!=='owner'||pending&&member.subject!==pending.actor)return {outcome:'failed' as const,message:'Only the original current owner can queue or retry a test.',uncertain:pending!==null}
      pending??=Object.freeze({actor:member.subject,args:Object.freeze({team_id:client.tenant,request_id:crypto.randomUUID(),subscription_id:selected!.subscription_id,expected_revision:selected!.revision}),version:selected!.version,config:selected!.config_ref})
      const result=await dataRequest(client.db.rpc('queue_webhook_test_once',pending.args!),{timeoutMs:5000}),checked=validateRpcResponse(result,{parse:check})
      if(checked.outcome==='validated'){pending=null;return {outcome:'queued' as const,receipt:checked.result.data}}
      const known=['PT400','PT409'].includes(result.error?.code??'')&&['invalid_webhook_test','subscription_revision_conflict','webhook_test_payload_conflict','webhook_test_pending'].includes(result.error?.message??'')
      if(!recovering&&(known||[401,403].includes(result.status)))pending=null
      return {outcome:'failed' as const,message:known?'Subscription changed or a test is already pending. Reload and review.':'Could not confirm the test. Resolve the original operation before another test.',uncertain:pending!==null}
    }catch{return {outcome:'failed' as const,message:pending?'Resolve the original test operation before another test.':'Select a saved subscription and confirm the sample delivery.',uncertain:pending!==null}}
    finally{busy=false}
  }
  return {get pending(){return Boolean(pending)},submit:(subscription:unknown,confirmed:boolean)=>execute(subscription,confirmed),reconcile:()=>execute(undefined,false,true)}
}
