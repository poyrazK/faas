import {createHmac,timingSafeEqual,randomUUID} from 'node:crypto'
import {pathToFileURL} from 'node:url'
const uuid=/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const secretPattern=/^[0-9a-f]{64}$/i
function destination(value,local){
  const url=new URL(value)
  if(url.username||url.password||url.hash||url.search||!(url.protocol==='https:'||local&&url.protocol==='http:'&&['localhost','127.0.0.1','[::1]'].includes(url.hostname)))throw new Error('invalid_worker_configuration')
  return url.toString()
}
export function workerConfig(env){
  if(!env.OUTBOX_WORKER_TOKEN||!env.OUTBOX_TEAM||!/^[-a-zA-Z0-9_]{1,128}$/.test(env.OUTBOX_NAMESPACE??''))throw new Error('invalid_worker_configuration')
  const common={api:destination(env.DATA_API_URL,env.OUTBOX_ALLOW_LOOPBACK==='1'),token:env.OUTBOX_WORKER_TOKEN,team:env.OUTBOX_TEAM,namespace:env.OUTBOX_NAMESPACE}
  if(env.OUTBOX_SUBSCRIPTION_ID){
    if(!uuid.test(env.OUTBOX_SUBSCRIPTION_ID))throw new Error('invalid_worker_configuration')
    const values=JSON.parse(env.OUTBOX_CONFIGS_JSON??'null')
    if(!values||typeof values!=='object'||Array.isArray(values)||!Object.keys(values).length)throw new Error('invalid_worker_configuration')
    const configs=Object.create(null)
    for(const [ref,value] of Object.entries(values)){
      if(!/^[-a-zA-Z0-9_]{1,80}$/.test(ref)||!value||typeof value!=='object'||!secretPattern.test(value.secret??''))throw new Error('invalid_worker_configuration')
      configs[ref]=Object.freeze({destination:destination(value.url,env.OUTBOX_ALLOW_LOOPBACK==='1'),secret:value.secret})
    }
    return {...common,subscription:env.OUTBOX_SUBSCRIPTION_ID.toLowerCase(),configs:Object.freeze(configs)}
  }
  if(!secretPattern.test(env.OUTBOX_SIGNING_SECRET??''))throw new Error('invalid_worker_configuration')
  return {...common,destination:destination(env.OUTBOX_WEBHOOK_URL,env.OUTBOX_ALLOW_LOOPBACK==='1'),secret:env.OUTBOX_SIGNING_SECRET}
}
export function signWebhook(body,secret,timestamp=Math.floor(Date.now()/1000)){
  if(typeof body!=='string'||!secretPattern.test(secret)||!Number.isSafeInteger(timestamp)||timestamp<0)throw new Error('invalid_signature_input')
  return `t=${timestamp},v1=${createHmac('sha256',Buffer.from(secret,'hex')).update(`${timestamp}.${body}`).digest('hex')}`
}
/** Verify raw bytes before JSON parsing, then deduplicate namespace + signed id. */
export function verifyWebhook(body,signature,secret,now=Math.floor(Date.now()/1000)){
  const match=typeof signature==='string'&&/^t=([0-9]{1,12}),v1=([0-9a-f]{64})$/.exec(signature)
  if(!match||!secretPattern.test(secret)||!Number.isSafeInteger(now)||Math.abs(now-Number(match[1]))>300||!(typeof body==='string'||Buffer.isBuffer(body)))return false
  const expected=createHmac('sha256',Buffer.from(secret,'hex')).update(match[1]+'.').update(body).digest()
  return timingSafeEqual(expected,Buffer.from(match[2],'hex'))
}
function parseDelivery(row,team){
  if(!row||!uuid.test(row.event_id)||row.tenant_id!==team||row.state!=='leased'||!uuid.test(row.lease_token)||!Number.isFinite(Date.parse(row.lease_until))||!Number.isInteger(row.total_attempts)||row.total_attempts<1||!row.payload||row.payload.id!==row.event_id||row.payload.team!==team||typeof row.payload.time!=='string'||!Number.isFinite(Date.parse(row.payload.time)))throw new Error('invalid_delivery_response')
  if(row.payload.type==='webhook.test'){
    if(row.is_test!==true||!uuid.test(row.subscription_id)||row.payload.test!==true||row.payload.data?.message!=='Gregale webhook test'||typeof row.payload.data?.requested_by!=='string'||!row.payload.data.requested_by)throw new Error('invalid_delivery_response')
  }else if(row.is_test===true||row.payload.test!==undefined||!['order.submitted','order.fulfilled'].includes(row.payload.type)||row.payload.data?.order_id!==row.order_id||row.payload.data?.version!==row.version||row.payload.data?.status!==(row.payload.type==='order.submitted'?'submitted':'fulfilled')||!Number.isInteger(row.payload.data?.total_cents)||row.payload.data.total_cents<0||row.payload.data.total_cents>2147483647||typeof row.payload.data?.actor!=='string'||!row.payload.data.actor)throw new Error('invalid_delivery_response')
  return row
}
/** One leased delivery. Schedule invocations with your own worker/cron; never run
 * it in the browser or keep a scale-to-zero application awake with a busy loop. */
export async function dispatchOne(config,db,{fetchImpl=fetch,now=()=>Math.floor(Date.now()/1000)}={}){
  const token=randomUUID(),claimed=await db.claim(config.team,token)
  if(!Array.isArray(claimed)||claimed.length>1)throw new Error('invalid_delivery_response')
  if(!claimed.length)return 'idle'
  const event=parseDelivery(claimed[0],config.team)
  if(config.subscription&&(event.subscription_id!==config.subscription||!Number.isInteger(event.subscription_version)||event.subscription_version<1||typeof event.config_ref!=='string'||!/^[-a-zA-Z0-9_]{1,80}$/.test(event.config_ref)))throw new Error('invalid_delivery_response')
  if(event.is_test&&!config.subscription)throw new Error('invalid_delivery_response')
  if(event.lease_token!==token)throw new Error('invalid_delivery_response')
  // Only the immutable snapshot is sent, never mutable order data or raw worker
  // credentials. Reject unexpected fields rather than forwarding arbitrary metadata.
  const body=JSON.stringify({id:event.event_id,type:event.payload.type,time:event.payload.time,team:config.team,namespace:config.namespace,...(config.subscription?{subscription_id:event.subscription_id,subscription_version:event.subscription_version}:{}),...(event.is_test?{test:true}:{}),data:event.is_test?{message:'Gregale webhook test',requested_by:event.payload.data.requested_by}:{order_id:event.order_id,version:event.version,status:event.payload.data.status,total_cents:event.payload.data.total_cents,actor:event.payload.data.actor}})
  let succeeded=false,status=null,failureReason=null
  const target=config.subscription?config.configs[event.config_ref]:config
  if(!target)failureReason='configuration_missing'
  else try{
    const response=await fetchImpl(target.destination,{method:'POST',redirect:'manual',headers:{'content-type':'application/json','gregale-event-id':`${config.namespace}:${event.event_id}`,'gregale-signature':signWebhook(body,target.secret,now())},body,signal:AbortSignal.timeout(10000)})
    status=response.status>=100&&response.status<=599?response.status:null;succeeded=status!==null&&status>=200&&status<=299
    failureReason=succeeded?null:'http_error'
    try{await response.body?.cancel()}catch{/* A body cleanup failure does not change the HTTP outcome. */}
  }catch{failureReason='network_error'/* Provider errors and bodies are never persisted or logged. */}
  const finished=await db.finish(config.team,event.event_id,token,succeeded,status,...(config.subscription?[failureReason]:[]))
  if(!Array.isArray(finished)||finished.length>1)throw new Error('invalid_delivery_response')
  if(!finished.length)return 'lease-lost'
  const row=finished[0]
  if(row.event_id!==event.event_id||row.tenant_id!==config.team||row.total_attempts!==event.total_attempts||row.state!==(succeeded?'delivered':event.attempts>=5?'dead':'pending'))throw new Error('invalid_delivery_response')
  return succeeded?'delivered':row.state==='dead'?'dead-lettered':'retry-scheduled'
}
export async function main(env=process.env){
  const config=workerConfig(env)
  const {workerDatabase}=await import(new URL('../client/worker-api.mjs',import.meta.url))
  return dispatchOne(config,workerDatabase(config))
}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href){
  main().then(outcome=>console.log(JSON.stringify({outcome}))).catch(()=>{console.error('Webhook worker failed; check configuration, trusted worker credentials and connectivity.');process.exitCode=1})
}
