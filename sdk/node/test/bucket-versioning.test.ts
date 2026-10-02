import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('bucket versioning preserves intent, provider truth and progress', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const calls: string[] = [];
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(String(input), 'https://api.example.test/v1/apps/demo/buckets/bucket/versioning');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    calls.push(init?.method ?? 'GET');
    if (init?.method === 'PUT') assert.deepEqual(JSON.parse(String(init.body)), { status: 'Enabled' });
    return new Response(JSON.stringify({bucket_id:'bucket',desired_status:'Enabled',observed_status:'Enabled',state:'propagating',revision:1,versions_required:true,updated_at:'2026-10-02T00:00:00Z'}), { status: init?.method === 'PUT' ? 202 : 200, headers: {'Content-Type':'application/json'} });
  };
  try {
    const accepted = await StorageService.putObjectBucketVersioning({slug:'demo',bucket:'bucket',requestBody:{status:'Enabled'}});
    const read = await StorageService.getObjectBucketVersioning({slug:'demo',bucket:'bucket'});
    assert.ok('state' in accepted && 'state' in read);
    assert.equal(read.state, 'propagating');
    assert.equal(read.observed_status, 'Enabled');
    assert.equal(read.versions_required, true);
    assert.deepEqual(calls, ['PUT','GET']);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
    OpenAPI.TOKEN = oldToken;
  }
});
