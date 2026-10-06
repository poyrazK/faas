import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('lifecycle configuration and discovery preserve public types', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const calls: string[] = [];
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  const rules = [{id:'abandoned',status:'Enabled' as const,filter:{prefix:'tmp/目录'},abort_incomplete_multipart_days:2}];
  globalThis.fetch = async (input, init) => {
    const path = new URL(String(input)).pathname;
    assert.ok(path.startsWith('/v1/apps/demo/buckets/bucket/lifecycle'));
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    calls.push(`${init?.method} ${path}`);
    if (init?.method === 'PUT') assert.deepEqual(JSON.parse(String(init.body)), {rules});
    const scan = path.includes('/scans');
    const result = scan ? {id:'scan',bucket_id:'bucket',revision:1,state:'completed',phase:'multipart',scanned_keys:0,scanned_uploads:3,created_at:'2026-10-03T00:00:00Z',updated_at:'2026-10-03T00:00:00Z'} : {bucket_id:'bucket',revision:1,rules:init?.method === 'DELETE' ? [] : rules,updated_at:'2026-10-03T00:00:00Z'};
    return new Response(JSON.stringify(result), {status:init?.method === 'POST' ? 202 : 200, headers:{'Content-Type':'application/json'}});
  };
  try {
    const input = {slug:'demo',bucket:'bucket'};
    const put = await StorageService.putObjectBucketLifecycle({...input,requestBody:{rules}});
    const get = await StorageService.getObjectBucketLifecycle(input);
    const scan = await StorageService.createObjectLifecycleScan(input);
    const read = await StorageService.getObjectLifecycleScan({...input,scan:'scan'});
    const clear = await StorageService.deleteObjectBucketLifecycle(input);
    assert.ok('rules' in put && 'rules' in get && 'rules' in clear && 'phase' in scan && 'phase' in read);
    assert.deepEqual(get.rules, rules);
    assert.equal(read.scanned_uploads,3);
    assert.equal(read.phase,'multipart');
    assert.deepEqual(clear.rules,[]);
    assert.equal(calls.length,5);
  } finally { globalThis.fetch=oldFetch; OpenAPI.BASE=oldBase; OpenAPI.TOKEN=oldToken; }
});
