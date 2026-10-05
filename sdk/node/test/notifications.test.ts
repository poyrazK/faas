import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';
import type { ObjectNotificationRule } from '../src/generated/index.js';

test('notification replacement preserves owned destination wire data', async () => {
  const oldFetch=globalThis.fetch, oldBase=OpenAPI.BASE, oldToken=OpenAPI.TOKEN;
  const calls:string[]=[];
  const rules:ObjectNotificationRule[]=[{id:'images',destination:'arn:gregale:sqs:us-east-1:11111111-1111-4111-8111-111111111111:22222222-2222-4222-8222-222222222222/storage',events:['s3:ObjectCreated:Put'],prefix:'images/目录'}];
  OpenAPI.BASE='https://api.example.test'; OpenAPI.TOKEN='token';
  globalThis.fetch=async (input,init)=>{
    const method=init?.method??'GET'; calls.push(method);
    assert.equal(new URL(String(input)).pathname,'/v1/apps/demo/buckets/bucket/notifications');
    assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer token');
    if(method==='PUT') assert.deepEqual(JSON.parse(String(init?.body)),{rules});
    return new Response(JSON.stringify({bucket_id:'bucket',revision:2,rules:method==='DELETE'?[]:rules}),{status:200,headers:{'Content-Type':'application/json'}});
  };
  try {
    const params={slug:'demo',bucket:'bucket'};
    const put=await StorageService.putObjectBucketNotifications({...params,requestBody:{rules}});
    const get=await StorageService.getObjectBucketNotifications(params);
    const clear=await StorageService.deleteObjectBucketNotifications(params);
    assert.ok('rules' in put && 'rules' in get && 'rules' in clear);
    assert.deepEqual(get.rules,rules); assert.deepEqual(clear.rules,[]);
    assert.deepEqual(calls,['PUT','GET','DELETE']);
  } finally {globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;OpenAPI.TOKEN=oldToken;}
});
