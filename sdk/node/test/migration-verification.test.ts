import assert from 'node:assert/strict';
import test from 'node:test';
import { AppsService, OpenAPI } from '../src/preview.js';

test('migration verification preserves receipt identity, checks and cold-wake consent', async () => {
  const originalFetch = globalThis.fetch;
  const originalBase = OpenAPI.BASE;
  OpenAPI.BASE = 'https://api.invalid';
  const id = '33333333-3333-4333-8333-333333333333';
  const body = { id, deployment_id: '11111111-1111-4111-8111-111111111111', checks: { postgres_env_key: 'DATABASE_URL', service_connections: [{ env_key: 'API_URL', service: 'api', deployment_id: '22222222-2222-4222-8222-222222222222' }] }, smoke_path: '/ready', cold_wake: true };
  const requests: string[] = [];
  try {
    globalThis.fetch = async (input, init) => {
      const path = new URL(String(input)).pathname; requests.push(`${init?.method} ${path}`);
      if (path.endsWith('/migration-verifications') && init?.method === 'POST') assert.deepEqual(JSON.parse(String(init.body)), body);
      return new Response(JSON.stringify(path.endsWith('/migration-verifications') && init?.method === 'GET' ? { runs: [] } : { id, status: 'pending', request: body }), { headers: { 'Content-Type': 'application/json' } });
    };
    assert.equal((await AppsService.createMigrationVerification({ slug: 'shop-api', requestBody: body })).id, id);
    await AppsService.getMigrationVerification({ slug: 'shop-api', run: id });
    await AppsService.advanceMigrationVerification({ slug: 'shop-api', run: id });
    await AppsService.cancelMigrationVerification({ slug: 'shop-api', run: id });
    await AppsService.listMigrationVerifications({ slug: 'shop-api' });
    assert.deepEqual(requests, [`POST /v1/apps/shop-api/migration-verifications`, `GET /v1/apps/shop-api/migration-verifications/${id}`, `POST /v1/apps/shop-api/migration-verifications/${id}/advance`, `POST /v1/apps/shop-api/migration-verifications/${id}/cancel`, `GET /v1/apps/shop-api/migration-verifications`]);
  } finally { globalThis.fetch = originalFetch; OpenAPI.BASE = originalBase; }
});
