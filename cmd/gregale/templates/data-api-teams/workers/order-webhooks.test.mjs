import test from 'node:test'
import assert from 'node:assert/strict'
import {workerConfig,signWebhook,verifyWebhook,dispatchOne} from './order-webhooks.mjs'
const secret='a'.repeat(64),id='10000000-0000-4000-8000-000000000001',order='20000000-0000-4000-8000-000000000001'
const config={team:'shop-a',namespace:'test-app',destination:'https://receiver.example/events',secret}
const row=token=>({event_id:id,tenant_id:'shop-a',order_id:order,version:2,state:'leased',attempts:1,total_attempts:1,lease_token:token,lease_until:new Date(Date.now()+30000).toISOString(),payload:{id,team:'shop-a',type:'order.submitted',time:'2026-10-12T01:00:00Z',data:{order_id:order,version:2,status:'submitted',total_cents:100,actor:'alice'}}})
test('HMAC verifies raw bytes, rejects tampering, timestamps and malformed headers',()=>{const body='{"id":"event"}',signature=signWebhook(body,secret,1000);assert.equal(verifyWebhook(Buffer.from(body),signature,secret,1000),true);assert.equal(verifyWebhook(body+' ',signature,secret,1000),false);assert.equal(verifyWebhook(body,signature,secret,1301),false);assert.equal(verifyWebhook(body,signature,secret,699),false);assert.equal(verifyWebhook(body,signature+'x',secret,1000),false)})
test('worker credentials and URL configuration reject ambiguous destinations',()=>{const env={DATA_API_URL:'https://api.example',OUTBOX_WEBHOOK_URL:'https://receiver.example/events',OUTBOX_WORKER_TOKEN:'test-only',OUTBOX_TEAM:'shop-a',OUTBOX_NAMESPACE:'app',OUTBOX_SIGNING_SECRET:secret};assert.equal(workerConfig(env).team,'shop-a');for(const url of ['http://receiver.example','https://user:password@receiver.example','https://receiver.example?secret=foo','https://receiver.example/#frag'])assert.throws(()=>workerConfig({...env,OUTBOX_WEBHOOK_URL:url}));assert.equal(workerConfig({...env,OUTBOX_ALLOW_LOOPBACK:'1',OUTBOX_WEBHOOK_URL:'http://127.0.0.1:8080/events'}).destination,'http://127.0.0.1:8080/events')})
test('delivery signs exact immutable snapshot, ignores redirects and acknowledges 2xx',async()=>{let event,ack,sent;const db={claim:async(team,token)=>{event=row(token);return [event]},finish:async(...args)=>{ack=args;return [{...event,state:'delivered'}]}};const success=await dispatchOne(config,db,{now:()=>1000,fetchImpl:async(url,init)=>{sent=init;return new Response(null,{status:204})}});assert.equal(success,'delivered');assert.equal(sent.redirect,'manual');assert.equal(verifyWebhook(sent.body,sent.headers['gregale-signature'],secret,1000),true);assert.equal(sent.headers['gregale-event-id'],'test-app:'+id);assert.equal(ack[3],true);assert.equal(ack[4],204);assert.ok(!sent.body.includes('lease_token'))})
test('failure schedules retry without leaking provider body or error',async()=>{let event,ack;const db={claim:async(team,token)=>{event=row(token);return [event]},finish:async(...args)=>{ack=args;return [{...event,state:'pending'}]}};assert.equal(await dispatchOne(config,db,{fetchImpl:async()=>new Response('private secret',{status:503})}),'retry-scheduled');assert.equal(ack[3],false);assert.equal(ack[4],503);assert.equal(ack.length,5)})
test('expired acknowledgement cannot overwrite a replacement lease',async()=>{const db={claim:async(team,token)=>[row(token)],finish:async()=>[]};assert.equal(await dispatchOne(config,db,{fetchImpl:async()=>new Response(null,{status:200})}),'lease-lost')})
test('foreign-team or malformed events cannot contact a receiver',async()=>{let sent=0;const db={claim:async(team,token)=>[{...row(token),tenant_id:'shop-b'}],finish:async()=>[]};await assert.rejects(dispatchOne(config,db,{fetchImpl:async()=>{sent++;return new Response()}}));assert.equal(sent,0)})
test('named deliveries use pinned configuration and sign subscription identity',async()=>{
  const subscription='30000000-0000-4000-8000-000000000001';let event,sent,ack
  const named={...config,subscription,configs:{'receiver-v1':{destination:'https://old.example/events',secret},'receiver-v2':{destination:'https://new.example/events',secret:'b'.repeat(64)}}}
  const db={claim:async(team,token)=>{event={...row(token),subscription_id:subscription,subscription_version:1,config_ref:'receiver-v1'};return [event]},finish:async(...args)=>{ack=args;return [{...event,state:args[3]?'delivered':'pending'}]}}
  assert.equal(await dispatchOne(named,db,{now:()=>1000,fetchImpl:async(url,init)=>{sent={url,...init};return new Response(null,{status:204})}}),'delivered')
  assert.equal(sent.url,'https://old.example/events');assert.equal(JSON.parse(sent.body).subscription_id,subscription);assert.equal(verifyWebhook(sent.body,sent.headers['gregale-signature'],secret,1000),true)
  let contacted=false
  assert.equal(await dispatchOne({...named,configs:{}},db,{fetchImpl:async()=>{contacted=true;return new Response()}}),'retry-scheduled')
  assert.equal(contacted,false);assert.equal(ack[3],false);assert.equal(ack[4],null)
})
test('named configuration validates every operator binding',()=>{
  const env={DATA_API_URL:'https://api.example',OUTBOX_WORKER_TOKEN:'test-only',OUTBOX_TEAM:'shop-a',OUTBOX_NAMESPACE:'app',OUTBOX_SUBSCRIPTION_ID:'30000000-0000-4000-8000-000000000001',OUTBOX_CONFIGS_JSON:JSON.stringify({v1:{url:'https://receiver.example',secret}})}
  assert.equal(workerConfig(env).configs.v1.secret,secret)
  for(const value of ['null','[]','{}',JSON.stringify({v1:{url:'https://user:pass@receiver.example',secret}}),JSON.stringify({v1:{url:'https://receiver.example',secret:'bad'}})])assert.throws(()=>workerConfig({...env,OUTBOX_CONFIGS_JSON:value}))
})
test('sample event is signed and contains no real order action or arbitrary private fields',async()=>{
  const subscription='30000000-0000-4000-8000-000000000001';let event,sent,ack
  const named={...config,subscription,configs:{v1:{destination:config.destination,secret}}}
  const db={claim:async(team,token)=>{event={...row(token),subscription_id:subscription,subscription_version:2,config_ref:'v1',is_test:true,payload:{id,team:'shop-a',type:'webhook.test',test:true,time:'2026-10-12T01:00:00Z',data:{message:'Gregale webhook test',requested_by:'alice',secret:'private'}}};return [event]},finish:async(...args)=>{ack=args;return [{...event,state:'delivered'}]}}
  assert.equal(await dispatchOne(named,db,{now:()=>1000,fetchImpl:async(url,init)=>{sent=init;return new Response(null,{status:204})}}),'delivered')
  const body=JSON.parse(sent.body);assert.equal(body.type,'webhook.test');assert.equal(body.test,true);assert.deepEqual(body.data,{message:'Gregale webhook test',requested_by:'alice'});assert.equal(verifyWebhook(sent.body,sent.headers['gregale-signature'],secret,1000),true);assert.equal(ack[5],null)
})
test('worker rejects malformed sample markers before contacting receivers',async()=>{
  const subscription='30000000-0000-4000-8000-000000000001';let contacted=0
  const named={...config,subscription,configs:{v1:{destination:config.destination,secret}}}
  for(const change of [{is_test:false},{payload:{id,team:'shop-a',type:'webhook.test',test:false,time:'2026-10-12T01:00:00Z',data:{message:'Gregale webhook test',requested_by:'alice'}}}]){
    const db={claim:async(team,token)=>[{...row(token),subscription_id:subscription,subscription_version:1,config_ref:'v1',is_test:true,payload:{id,team:'shop-a',type:'webhook.test',test:true,time:'2026-10-12T01:00:00Z',data:{message:'Gregale webhook test',requested_by:'alice'}},...change}],finish:async()=>[]}
    await assert.rejects(dispatchOne(named,db,{fetchImpl:async()=>{contacted++;return new Response()}}))
  }
  assert.equal(contacted,0)
})
test('named failures persist only curated categories and HTTP status',async()=>{
  const subscription='30000000-0000-4000-8000-000000000001';let event,ack,contacted=0
  const named={...config,subscription,configs:{v1:{destination:config.destination,secret}}}
  const db={claim:async(team,token)=>{event={...row(token),subscription_id:subscription,subscription_version:1,config_ref:'v1',is_test:false};return [event]},finish:async(...args)=>{ack=args;return [{...event,state:'pending'}]}}
  await dispatchOne({...named,configs:{}},db,{fetchImpl:async()=>{contacted++;return new Response()}});assert.equal(ack[5],'configuration_missing');assert.equal(contacted,0)
  await dispatchOne(named,db,{fetchImpl:async()=>{throw Error('private token in provider error')}});assert.equal(ack[5],'network_error');assert.equal(ack[4],null)
  await dispatchOne(named,db,{fetchImpl:async()=>new Response('private provider body',{status:503})});assert.equal(ack[5],'http_error');assert.equal(ack[4],503);assert.ok(!JSON.stringify(ack).includes('private'))
})
