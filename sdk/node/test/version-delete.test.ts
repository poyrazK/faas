import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('immutable version deletion preserves the exact public selector', async () => {
  const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
  const key = '目录 /+%.txt', version = 'ad7b43a1-4a3f-4f6d-9518-d1f0e4c79b78';
  OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input));
    assert.equal(init?.method, 'DELETE');
    assert.equal(url.pathname, '/v1/apps/demo/buckets/bucket/objects/versions');
    assert.equal(url.searchParams.get('key'), key);
    assert.equal(url.searchParams.get('version_id'), version);
    return new Response(JSON.stringify({version_id: version, delete_marker: true}), {status: 200, headers: {'Content-Type': 'application/json'}});
  };
  try {
    const result = await StorageService.deleteObjectBucketVersion({slug: 'demo', bucket: 'bucket', key, versionId: version});
    assert.ok('version_id' in result && 'delete_marker' in result);
    assert.equal(result.version_id, version); assert.equal(result.delete_marker, true);
  } finally {
    globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken;
  }
});

test('retained-version inventory progress is exposed by the typed client', async () => {
  const oldFetch = globalThis.fetch, oldBase = OpenAPI.BASE, oldToken = OpenAPI.TOKEN;
  OpenAPI.BASE = 'https://api.example.test'; OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(init?.method, 'GET');
    assert.equal(new URL(String(input)).pathname, '/v1/apps/demo/buckets/bucket/capacity-reconciliations/job');
    return new Response(JSON.stringify({
      id: 'job', bucket_id: 'bucket', state: 'scanning', inventory_scope: 'all_versions',
      scanned_pages: 3, scanned_bytes: 17, scanned_versions: 2,
      before_bytes: 100, before_keys: 3, after_bytes: 100, after_keys: 3,
      reclaimed_bytes: 0, reclaimed_keys: 0, pending_writes: 0,
      created_at: '2026-10-02T10:00:00Z', updated_at: '2026-10-02T10:00:00Z',
    }), {status: 200, headers: {'Content-Type': 'application/json'}});
  };
  try {
    const result = await StorageService.getObjectCapacityReconciliation({slug: 'demo', bucket: 'bucket', reconciliation: 'job'});
    assert.ok('inventory_scope' in result);
    assert.equal(result.inventory_scope, 'all_versions');
    assert.equal(result.scanned_pages, 3); assert.equal(result.scanned_bytes, 17); assert.equal(result.scanned_versions, 2);
  } finally {
    globalThis.fetch = oldFetch; OpenAPI.BASE = oldBase; OpenAPI.TOKEN = oldToken;
  }
});
