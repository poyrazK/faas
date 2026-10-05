import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('copy source grants preserve prefix and private-free responses', async () => {
 const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
 const calls: string[] = [];
 const grant = { source_bucket_id:'source',prefix:'allowed/',created_at:'2026-10-04T00:00:00Z',updated_at:'2026-10-04T00:00:00Z' };
 OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
 globalThis.fetch = async (input,init) => {
  const method = init?.method ?? 'GET'; calls.push(method);
  const base = 'https://api.example.test/v1/apps/demo/buckets/bucket/s3-credentials/credential/copy-sources';
  assert.equal(String(input), base + (method === 'GET' ? '' : '/source'));
  assert.equal(new Headers(init?.headers).get('Authorization'),'Bearer token');
  if (method === 'PUT') assert.deepEqual(JSON.parse(String(init?.body)),{prefix:'allowed/'});
  if (method === 'DELETE') return new Response(null,{status:204});
  return new Response(JSON.stringify(method === 'GET' ? {items:[grant]} : grant),{status:200,headers:{'Content-Type':'application/json'}});
 };
 try {
  const params = {slug:'demo',bucket:'bucket',credential:'credential'};
  const created = await StorageService.setObjectS3CopySource({...params,source:'source',requestBody:{prefix:'allowed/'}});
  assert.deepEqual(created,grant);
  const list = await StorageService.listObjectS3CopySources(params); assert.deepEqual(list,{items:[grant]});
  await StorageService.deleteObjectS3CopySource({...params,source:'source'});
  assert.deepEqual(calls,['PUT','GET','DELETE']);
 } finally { globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken; }
});
