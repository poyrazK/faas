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
    return new Response(JSON.stringify({algorithms: ['AES256','aws:kms'], key_ids: ['owned-key'], bucket_defaults: true}), {status: 200, headers: {'Content-Type': 'application/json'}});
  };
  try {
    const out = await StorageService.getObjectBucketEncryptionCapabilities({slug: 'demo', bucket: 'bucket'});
    assert.ok('algorithms' in out && 'key_ids' in out);
    assert.deepEqual(out.algorithms, ['AES256','aws:kms']);
    assert.deepEqual(out.key_ids, ['owned-key']);
    assert.ok('bucket_defaults' in out && out.bucket_defaults);
  } finally {globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken;}
});

test('bucket default encryption config preserves selection and progress', async () => {
  const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
  OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
  const calls: string[] = [];
  globalThis.fetch = async (input, init) => {
    assert.equal(new URL(String(input)).pathname, '/v1/apps/demo/buckets/bucket/encryption');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    calls.push(String(init?.method));
    if (init?.method === 'PUT') assert.deepEqual(JSON.parse(String(init.body)), { encryption: { algorithm: 'AES256' } });
    return new Response(JSON.stringify({bucket_id: 'bucket', state: 'waiting', revision: 2, desired_encryption: {algorithm: 'AES256'}, updated_at: '2026-10-04T00:00:00Z'}), {status: init?.method === 'GET' ? 200 : 202, headers: {'Content-Type':'application/json'}});
  };
  try {
    const created = await StorageService.putObjectBucketEncryption({slug:'demo',bucket:'bucket',requestBody:{encryption:{algorithm:'AES256'}}});
    assert.ok('desired_encryption' in created && created.desired_encryption?.algorithm === 'AES256');
    const read = await StorageService.getObjectBucketEncryption({slug:'demo',bucket:'bucket'});
    assert.ok('state' in read && read.state === 'waiting');
    await StorageService.deleteObjectBucketEncryption({slug:'demo',bucket:'bucket'});
    assert.deepEqual(calls,['PUT','GET','DELETE']);
  } finally {globalThis.fetch=oldFetch; OpenAPI.BASE=oldBase; OpenAPI.TOKEN=oldToken;}
});
