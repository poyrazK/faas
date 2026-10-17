import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/index.js';
import type { CreateBindingVerificationRunRequest } from '../src/index.js';
test('verification receipts preserve caller identity, consent and atomic step routes', async () => {
 const oldFetch=globalThis.fetch,oldBase=OpenAPI.BASE;
 OpenAPI.BASE='https://api.invalid';
 const id='11111111-1111-4111-8111-111111111111';
 const body:CreateBindingVerificationRunRequest={id,deployment_id:'22222222-2222-4222-8222-222222222222',review_digest:'a'.repeat(64),ttl_seconds:60,object_storage_write:false,queue_publish:true};
 const calls:Array<[string,string]>=[];
 try {
  globalThis.fetch=async(input,init)=>{
   const path=new URL(String(input)).pathname;calls.push([String(init?.method),path]);
   if(path.endsWith('/review'))return new Response(JSON.stringify({ready:true,deployment_id:body.deployment_id,service_targets:{billing:body.deployment_id},review_digest:'b'.repeat(64)}),{status:200,headers:{'Content-Type':'application/json'}});
   if(path.endsWith('/verification-runs'))assert.deepEqual(JSON.parse(String(init?.body)),body);
   else assert.ok(path.includes('/'+id));
   const result=path.endsWith('/admit')?{plan:{type:'queue',name:'orders',binding:'orders',scope:'production',action:'queue-canary'},resource_id:body.deployment_id,resource_kind:'queue',owned:true,status:'published'}:{id,status:'pending'};
   return new Response(JSON.stringify(result),{status:200,headers:{'Content-Type':'application/json'}});
  };
  const review=await AppsService.reviewBindingVerification({slug:'app',requestBody:{}});
  body.deployment_id=review.deployment_id;body.service_targets=review.service_targets;body.review_digest=review.review_digest;assert.equal(body.review_digest,'b'.repeat(64));
  await AppsService.createBindingVerificationRun({slug:'app',requestBody:body});
  await AppsService.getBindingVerificationRun({slug:'app',run:id});
  await AppsService.resumeBindingVerificationRun({slug:'app',run:id});
  const step=await AppsService.admitBindingVerificationRunStep({slug:'app',run:id,step:2});assert.equal(step.owned,true);
  await AppsService.completeBindingVerificationRun({slug:'app',run:id});
  await AppsService.cancelBindingVerificationRun({slug:'app',run:id});
  assert.equal(calls.length,7);assert.deepEqual(calls[4],['POST',`/v1/apps/app/bindings/verification-runs/${id}/steps/2/admit`]);assert.equal(calls[6]?.[0],'DELETE');
 }finally{globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
