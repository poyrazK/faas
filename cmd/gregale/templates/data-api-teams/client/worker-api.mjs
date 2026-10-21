import {createDataClient,dataRequest} from '@gregale/data'
/** Resolve the verified SDK from the installed client package, using its ESM
 * export. Worker credentials never pass through browser code. */
export function workerDatabase(config){
  const client=createDataClient({url:config.api,accessToken:config.token}).schema('api')
  const rpc=async(name,args)=>{const result=await dataRequest(client.rpc(name,args),{timeoutMs:5000});if(result.error)throw new Error('worker_database_request_failed');return result.data}
  return {claim:(team,token)=>rpc(config.subscription?'claim_subscription_webhook':'claim_team_order_webhook',{team_id:team,...(config.subscription?{subscription_id:config.subscription}:{}),claim_token:token}),finish:(team,id,token,success,status,failureReason=null)=>rpc(config.subscription?'finish_subscription_webhook':'finish_team_order_webhook',{team_id:team,...(config.subscription?{subscription_id:config.subscription,failure_reason:failureReason}:{}),delivery_id:id,claim_token:token,succeeded:success,response_status:status})}
}
