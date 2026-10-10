import assert from 'node:assert/strict';
import { test } from 'node:test';
import { StorageService, OpenAPI } from '../src/generated/index.js';

for (const condition of [{ if_match: '"old"' }, { if_none_match: '*' as const }]) {
  test(`conditional PUT sends ${Object.keys(condition)[0]}`, async () => {
    const headers = 'if_match' in condition ? { 'If-Match': condition.if_match } : { 'If-None-Match': condition.if_none_match };
    const oldFetch = globalThis.fetch;
    const oldBase = OpenAPI.BASE;
    OpenAPI.BASE = 'https://api.example.test';
    globalThis.fetch = async (_input, init) => {
      assert.deepEqual(JSON.parse(String(init?.body)), { method: 'PUT', key: 'key', size_bytes: 3, ...condition });
      return new Response(JSON.stringify({ url: 'https://s3.example.test/assets/key', method: 'PUT', headers, expires_at: '2026-10-08T20:00:00Z' }), { headers: { 'Content-Type': 'application/json' } });
    };
    try {
      const out = await StorageService.signBucketObject({ slug: 'demo', bucket: 'bucket', requestBody: { method: 'PUT', key: 'key', size_bytes: 3, ...condition } });
      assert.ok('headers' in out);
      assert.deepEqual(out.headers, headers);
    } finally {
      globalThis.fetch = oldFetch;
      OpenAPI.BASE = oldBase;
    }
  });
}
