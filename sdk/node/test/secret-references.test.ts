import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, SecretsService } from '../src/index.js';

test('secret references carry explicit environments and names through the public client', async () => {
  const calls: Array<{ url: URL; method: string; body: unknown }> = [];
  const client = new FaaSClient('https://api.example.test', {
    token: 'token',
    fetch: async (input, init) => {
      assert.equal(new Headers(init?.headers).get('Authorization'), 'Bearer token');
      calls.push({ url: new URL(input instanceof Request ? input.url : String(input)),
        method: String(init?.method), body: init?.body ? JSON.parse(String(init.body)) : undefined });
      if (init?.method === 'DELETE') return new Response(null, { status: 204 });
      return Response.json(init?.method === 'PUT'
        ? { environment_id: 'env', environment: 'production', key: 'DATABASE_URL', reference: 'secret:DATABASE' }
        : { environment_id: 'env', environment: 'production', references: { DATABASE_URL: 'secret:DATABASE' }, suppressed_keys: ['REMOVED'], count: 1, quota: 20 });
    },
  });
  try {
    const result = await SecretsService.setAppSecretReference({ slug: 'my app', environment: 'production',
      key: 'DATABASE_URL', requestBody: { reference: 'secret:DATABASE' } });
    assert.equal(result.reference, 'secret:DATABASE');
    assert.equal(result.environment_id, 'env');
    const listed = await SecretsService.listAppSecretReferences({ slug: 'my app', environment: 'production' });
    assert.deepEqual(listed.references, { DATABASE_URL: 'secret:DATABASE' });
    assert.equal(listed.count, 1);
    assert.deepEqual(listed.suppressed_keys, ['REMOVED']);
    await SecretsService.deleteAppSecretReference({ slug: 'my app', environment: 'production', key: 'DATABASE_URL' });
    assert.deepEqual(calls.map(c => c.method), ['PUT', 'GET', 'DELETE']);
    assert.deepEqual(calls[0]?.body, { reference: 'secret:DATABASE' });
    for (const call of calls) {
      assert.equal(call.url.search, '?environment=production');
      assert.equal(call.url.pathname, `/v1/apps/my%20app/secret-references${call.method === 'GET' ? '' : '/DATABASE_URL'}`);
    }
  } finally {
    client.uninstall();
  }
});
