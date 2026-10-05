import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('bucket catalog exposes the configured transfer contract', async () => {
  const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
  const catalog = {items: [], enabled: true, regions: ['us-east-1'], default_region: 'us-east-1', max_upload_bytes: 5 * 2 ** 40, max_buckets_per_app: 10, max_single_put_bytes: 512 * 2 ** 20, max_part_bytes: 512 * 2 ** 20, transfer_timeout_seconds: 7200, upload_profile: 'direct'};
  OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(new URL(String(input)).pathname, '/v1/apps/demo/buckets');
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    return new Response(JSON.stringify(catalog), {status: 200, headers: {'Content-Type': 'application/json'}});
  };
  try {
    const got = await StorageService.listObjectBuckets({slug: 'demo'});
    assert.ok('upload_profile' in got); assert.deepEqual(got, catalog);
  } finally {
    globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken;
  }
});
