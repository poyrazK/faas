import test from 'node:test';
import assert from 'node:assert/strict';
import {OpenAPI, StorageService} from '../src/generated/index.js';

for (const selector of ['null','12345678-1234-4234-8234-123456789abc']) {
test(`durable deletion preserves selector ${selector} and retry identity`, async () => {
  const oldFetch=globalThis.fetch, oldBase=OpenAPI.BASE, oldToken=OpenAPI.TOKEN;
  OpenAPI.BASE='https://api.example.test'; OpenAPI.TOKEN='token';
  const id='ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78';
  globalThis.fetch=async (input,init) => {
    const path=new URL(String(input)).pathname;
    if (init?.method==='POST') {
      assert.equal(path,'/v1/apps/demo/buckets/bucket/objects/deletions');
      assert.deepEqual(JSON.parse(String(init.body)),{id,key:'目录 /+%.txt',version_id:selector});
    } else assert.equal(path,`/v1/apps/demo/buckets/bucket/objects/deletions/${id}`);
    return new Response(JSON.stringify({id,bucket_id:'bucket',key:'目录 /+%.txt',selector,state:'dispatched',delete_marker:false,last_error_code:'provider_uncertain',created_at:'2026-10-02T12:00:00Z',updated_at:'2026-10-02T12:00:00Z'}),{status:init?.method==='POST'?202:200,headers:{'Content-Type':'application/json'}});
  };
  try {
    const j=await StorageService.createObjectDeletion({slug:'demo',bucket:'bucket',requestBody:{id,key:'目录 /+%.txt',version_id:selector}});
    assert.ok('state' in j); assert.equal(j.state,'dispatched'); assert.equal(j.last_error_code,'provider_uncertain');
    const read=await StorageService.getObjectDeletion({slug:'demo',bucket:'bucket',deletion:id});assert.ok('id' in read); assert.equal(read.id,id);
  } finally {globalThis.fetch=oldFetch;OpenAPI.BASE=oldBase;OpenAPI.TOKEN=oldToken;}
});
}
