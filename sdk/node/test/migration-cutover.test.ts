import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/index.js';

test('saved cutover identity, recovery records and explicit limited-traffic consent survive the wire', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const id = '44444444-4444-4444-8444-444444444444';
  const body = { id, verification_id: '33333333-3333-4333-8333-333333333333', hostname: 'api.example.test', previous_records: [{ type: 'CNAME' as const, value: 'old.provider.test', ttl: 300 }], previous_proxy_enabled: true, max_5xx_rate_bps: 100, max_p95_ms: 1000 };
  const requests: string[] = [];
  try {
    globalThis.fetch = async (input, init) => {
      const path = new URL(String(input)).pathname;
      requests.push(`${init?.method} ${path}`);
      if (path.endsWith('/migration-cutovers') && init?.method === 'POST') assert.deepEqual(JSON.parse(String(init.body)), body);
      if (path.endsWith('/actions')) assert.deepEqual(JSON.parse(String(init?.body)), { action: 'complete', acknowledge_limited_traffic: true });
      return new Response(JSON.stringify(path.endsWith('/migration-cutovers') && init?.method === 'GET' ? { plans: [] } : { id, status: 'prepared', request: body }), { headers: { 'Content-Type': 'application/json' } });
    };
    assert.equal((await AppsService.createMigrationCutover({ slug: 'shop-api', requestBody: body })).id, id);
    await AppsService.getMigrationCutover({ slug: 'shop-api', plan: id });
    await AppsService.actOnMigrationCutover({ slug: 'shop-api', plan: id, requestBody: { action: 'complete', acknowledge_limited_traffic: true } });
    await AppsService.listMigrationCutovers({ slug: 'shop-api' });
    assert.deepEqual(requests, ['POST /v1/apps/shop-api/migration-cutovers', `GET /v1/apps/shop-api/migration-cutovers/${id}`, `POST /v1/apps/shop-api/migration-cutovers/${id}/actions`, 'GET /v1/apps/shop-api/migration-cutovers']);
  } finally { globalThis.fetch = originalFetch; OpenAPI.BASE = originalBase; }
});
