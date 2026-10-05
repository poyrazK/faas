import test from 'node:test';
import assert from 'node:assert/strict';
import { OpenAPI, StorageService } from '../src/generated/index.js';

test('upload route clients preserve owned encryption', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  const oldToken = OpenAPI.TOKEN;
  const encryption = { algorithm: 'aws:kms' as const, key_id: 'owned-key', bucket_key_enabled: false };
  OpenAPI.BASE = 'https://api.example.test';
  OpenAPI.TOKEN = 'token';
  globalThis.fetch = async (input, init) => {
    assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
    if (init?.method === 'GET') {
      assert.equal(String(input), 'https://api.example.test/v1/apps/demo/buckets/bucket/write-receipts/receipt');
      return new Response(JSON.stringify({ id: 'receipt', status: 'completed', encryption }), {
        status: 200, headers: { 'Content-Type': 'application/json' },
      });
    }
    assert.equal(String(input), 'https://api.example.test/v1/apps/demo/upload-routes');
    assert.deepEqual(JSON.parse(String(init?.body)), { name: 'files', bucket_id: 'bucket', encryption });
    return new Response(JSON.stringify({
      id: 'route', name: 'files', bucket_id: 'bucket', max_bytes: 3, enabled: true,
      created_at: '2026-10-04T00:00:00Z', updated_at: '2026-10-04T00:00:00Z', encryption,
    }), { status: 201, headers: { 'Content-Type': 'application/json' } });
  };
  try {
    const out = await StorageService.createObjectUploadRoute({
      slug: 'demo', requestBody: { name: 'files', bucket_id: 'bucket', encryption },
    });
    assert.ok('encryption' in out);
    assert.deepEqual(out.encryption, encryption);
    const receipt = await StorageService.getObjectWriteReceipt({ slug: 'demo', bucket: 'bucket', receipt: 'receipt' });
    assert.ok('encryption' in receipt);
    assert.deepEqual(receipt.encryption, encryption);
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
    OpenAPI.TOKEN = oldToken;
  }
});
