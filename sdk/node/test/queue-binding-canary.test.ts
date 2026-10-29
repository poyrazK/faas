import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/preview.js';
import type { CreateQueueBindingCanaryRequest } from '../src/preview.js';
test('queue canary preserves exact deployment and explicit publication consent', async () => {
  const oldFetch=globalThis.fetch, oldBase=OpenAPI.BASE;
  OpenAPI.BASE='https://api.invalid';
  const body:CreateQueueBindingCanaryRequest={deployment_id:'11111111-1111-4111-8111-111111111111',binding:'orders',ttl_seconds:60,publish_consent:true};
  try {
    globalThis.fetch=async(input,init)=>{
      assert.equal(new URL(String(input)).pathname,'/v1/apps/api/bindings/queue-canaries');
      assert.equal(init?.method,'POST');assert.deepEqual(JSON.parse(String(init?.body)),body);
      return new Response(JSON.stringify({id:'22222222-2222-4222-8222-222222222222',binding:body.binding,deployment_id:body.deployment_id,scope:'production',release_id:'33333333-3333-4333-8333-333333333333',status:'published',published_at:'2026-10-14T00:00:00Z',expires_at:'2026-10-14T00:01:00Z'}),{status:201,headers:{'Content-Type':'application/json'}});
    };
    const result=await AppsService.createQueueBindingCanary({slug:'api',requestBody:body});
    assert.equal(result.release_id,'33333333-3333-4333-8333-333333333333');assert.equal(result.status,'published');assert.equal(result.deployment_id,body.deployment_id);assert.equal(result.completed_at,undefined);
  } finally {globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;}
});
