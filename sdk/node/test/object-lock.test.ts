import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('Object Lock preserves nested defaults and durable observation separately', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const configuration = {enabled:true,default_retention:{mode:'COMPLIANCE' as const,days:3,default_event_hold:{years:1}}};
  const calls: string[] = [];
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    const path = String(input);
    assert.ok(path.startsWith('https://api.example.test/v1/apps/demo/buckets/bucket/object-lock'));
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    calls.push(init?.method ?? 'GET');
    if (path.endsWith('-capabilities')) return Response.json({bucket_configuration:true,default_event_hold:true});
    if (init?.method === 'PUT') assert.deepEqual(JSON.parse(String(init.body)), {configuration});
    return Response.json({bucket_id:'bucket',state:'waiting',revision:1,enabled_required:true,observed_known:false,desired_configuration:configuration,last_error_code:'versioning_pending',updated_at:'2026-10-04T00:00:00Z'}, {status:init?.method === 'PUT' ? 202 : 200});
  };
  try {
    const caps = await StorageService.getObjectBucketObjectLockCapabilities({slug:'demo',bucket:'bucket'});
    assert.ok('default_event_hold' in caps && caps.default_event_hold);
    const accepted = await StorageService.putObjectBucketObjectLock({slug:'demo',bucket:'bucket',requestBody:{configuration}});
    const read = await StorageService.getObjectBucketObjectLock({slug:'demo',bucket:'bucket'});
    assert.ok('state' in accepted && 'state' in read);
    assert.equal(read.state, 'waiting');
    assert.equal(read.enabled_required, true);
    assert.equal(read.observed_known, false);
    assert.deepEqual(read.desired_configuration, configuration);
    assert.deepEqual(calls, ['GET','PUT','GET']);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
    OpenAPI.TOKEN = oldToken;
  }
});
