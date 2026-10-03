import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('encryption discovery preserves owned enrollment and authorization', async () => {
  const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
  OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(new URL(String(input)).pathname, '/v1/apps/demo/buckets/bucket/encryption-capabilities');
    assert.equal(init?.method, 'GET');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    return new Response(JSON.stringify({algorithms: ['AES256','aws:kms'], key_ids: ['owned-key']}), {status: 200, headers: {'Content-Type': 'application/json'}});
  };
  try {
    const out = await StorageService.getObjectBucketEncryptionCapabilities({slug: 'demo', bucket: 'bucket'});
    assert.ok('algorithms' in out && 'key_ids' in out);
    assert.deepEqual(out.algorithms, ['AES256','aws:kms']);
    assert.deepEqual(out.key_ids, ['owned-key']);
  } finally {globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken;}
});
