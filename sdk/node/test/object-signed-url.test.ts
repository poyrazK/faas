import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('signed object requests preserve owned encryption and receipt identity', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const encryption = { algorithm: 'aws:kms' as const, key_id: 'arn:gregale:kms:us-east-1:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222', bucket_key_enabled: false };
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(String(input), 'https://api.example.test/v1/apps/demo/buckets/bucket/signed-url');
    assert.equal(init?.method, 'POST');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    assert.deepEqual(JSON.parse(String(init?.body)), {method:'PUT',key:'file',size_bytes:3,encryption});
    return new Response(JSON.stringify({url:'https://s3.gregale.dev/assets/file',method:'PUT',headers:{'X-Amz-Server-Side-Encryption':'aws:kms'},expires_at:'2026-10-04T00:00:00Z',upload_id:'33333333-3333-4333-8333-333333333333'}), {status:200,headers:{'Content-Type':'application/json'}});
  };
  try {
    const out = await StorageService.signBucketObject({slug:'demo',bucket:'bucket',requestBody:{method:'PUT',key:'file',size_bytes:3,encryption}});
    assert.ok('upload_id' in out);
    assert.equal(out.upload_id, '33333333-3333-4333-8333-333333333333');
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
    OpenAPI.TOKEN = oldToken;
  }
});
