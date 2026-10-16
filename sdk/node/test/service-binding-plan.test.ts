import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/index.js';
import type { UpdateAppRequest } from '../src/index.js';

test('service binding preconditions survive the wire and conflicts are never retried unguarded', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const fingerprint = 'a'.repeat(64);
  try {
    for (const status of [200, 409]) {
      let calls = 0;
      globalThis.fetch = async (input, init) => {
        calls++;
        assert.equal(new URL(String(input)).pathname, '/v1/apps/demo');
        assert.equal(init?.method, 'PATCH');
        const body = JSON.parse(String(init?.body));
        assert.equal(body.expected_service_binding_fingerprint, fingerprint);
        assert.deepEqual(body.allowed_service_callers, []);
        return new Response(JSON.stringify(status === 200
          ? { id: 'app1', service_binding_fingerprint: 'b'.repeat(64) }
          : { title: 'Service binding plan is stale', status: 409, code: 'conflict' }),
        { status, headers: { 'Content-Type': 'application/json' } });
      };
      const requestBody: UpdateAppRequest = { expected_service_binding_fingerprint: fingerprint, allowed_service_callers: [] };
      const request = AppsService.updateApp({ slug: 'demo', requestBody });
      if (status === 409) {
        await assert.rejects(request, (error: { status?: number }) => error.status === 409);
      } else {
        assert.equal((await request).service_binding_fingerprint, 'b'.repeat(64));
      }
      assert.equal(calls, 1);
    }
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
