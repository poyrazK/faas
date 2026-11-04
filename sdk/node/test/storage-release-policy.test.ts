import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/preview.js';
import type { SetObjectStorageBindingReleasePolicyRequest } from '../src/preview.js';

test('storage policy keeps explicit requirements and clearing, and never falls back', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  try {
    for (const scenario of ['set', 'clear', 'old server']) {
      let calls = 0;
      const requirements: SetObjectStorageBindingReleasePolicyRequest['object_storage_requirements'] = scenario === 'clear' ? {} : { ASSETS: 'read_write' };
      globalThis.fetch = async (input, init) => {
        calls++;
        assert.equal(new URL(String(input)).pathname, '/v1/apps/demo/bindings/release-policy-with-object-storage');
        assert.equal(init?.method, 'PUT');
        assert.deepEqual(JSON.parse(String(init?.body)).object_storage_requirements, requirements);
        return new Response(JSON.stringify({
          app_id: '11111111-1111-4111-8111-111111111111', scope: 'production', mode: 'enforce', revision: 1,
          max_verification_age: '10m0s', require_application_ack: false, object_storage_requirements: requirements,
        }), { status: scenario === 'old server' ? 404 : 200, headers: { 'Content-Type': 'application/json' } });
      };
      const body: SetObjectStorageBindingReleasePolicyRequest = {
        mode: 'enforce', expected_revision: 0, object_storage_requirements: requirements,
      };
      const request = AppsService.setObjectStorageBindingReleasePolicy({ slug: 'demo', scope: 'production', requestBody: body });
      if (scenario === 'old server') {
        await assert.rejects(request, (error: { status?: number }) => error.status === 404);
      } else {
        assert.deepEqual((await request).object_storage_requirements, requirements);
      }
      assert.equal(calls, 1);
    }
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
