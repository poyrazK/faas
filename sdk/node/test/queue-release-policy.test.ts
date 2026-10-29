import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/preview.js';
import type { SetQueueBindingReleasePolicyRequest } from '../src/preview.js';

test('queue policy preserves explicit readiness, storage requirements and clearing without fallback', async () => {
  const oldFetch = globalThis.fetch;
  const oldBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  try {
    for (const scenario of ['set', 'clear', 'old server']) {
      let calls = 0;
      const requirements: SetQueueBindingReleasePolicyRequest['queue_requirements'] = scenario === 'clear' ? {} : { orders: 'consumer_readiness' };
      globalThis.fetch = async (input, init) => {
        calls++;
        assert.equal(new URL(String(input)).pathname, '/v1/apps/demo/bindings/release-policy-with-queues');
        assert.equal(init?.method, 'PUT');
        const body = JSON.parse(String(init?.body));
        assert.deepEqual(body.queue_requirements, requirements);
        assert.deepEqual(body.object_storage_requirements, { ASSETS: 'read_write' });
        return new Response(JSON.stringify({
          app_id: '11111111-1111-4111-8111-111111111111', scope: 'production', mode: 'enforce', revision: 1,
          max_verification_age: '10m0s', require_application_ack: false, queue_requirements: requirements,
        }), { status: scenario === 'old server' ? 404 : 200, headers: { 'Content-Type': 'application/json' } });
      };
      const request = AppsService.setQueueBindingReleasePolicy({ slug: 'demo', scope: 'production', requestBody: {
        mode: 'enforce', expected_revision: 0, queue_requirements: requirements, object_storage_requirements: { ASSETS: 'read_write' },
      } });
      if (scenario === 'old server') {
        await assert.rejects(request, (error: { status?: number }) => error.status === 404);
      } else {
        assert.deepEqual((await request).queue_requirements, requirements);
      }
      assert.equal(calls, 1);
    }
  } finally {
    globalThis.fetch = oldFetch;
    OpenAPI.BASE = oldBase;
  }
});
