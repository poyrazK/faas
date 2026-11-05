import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ManagedPostgresService } from '../src/preview.js';

test('company PostgreSQL clients keep the tenant in every resource path', async (t) => {
  const paths: string[] = [];
  const server = createServer((req, res) => {
    paths.push(req.url ?? '');
    assert.equal(req.headers.authorization, 'Bearer company-token');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ items: [] }));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'company-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  await ManagedPostgresService.listOrgManagedPostgresDatabases({ organization: 'my company' });
  await ManagedPostgresService.listOrgManagedPostgresBindings({ organization: 'my company', id: '33333333-3333-4333-8333-333333333333' });
  assert.deepEqual(paths, [
    '/v1/orgs/my%20company/postgres/databases',
    '/v1/orgs/my%20company/postgres/databases/33333333-3333-4333-8333-333333333333/bindings',
  ]);
});
