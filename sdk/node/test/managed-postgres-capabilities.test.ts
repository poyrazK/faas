import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ManagedPostgresService, type ManagedPostgresCapabilities } from '../src/index.js';

test('PostgreSQL capabilities preserve reader support with a closed rollout gate', async (t) => {
  const capabilities: ManagedPostgresCapabilities = {
    contract_version: 2, region: 'eu-central-1', provisioning_enabled: false,
    database_limit: 1, postgres_majors: [16, 17], service_classes: ['development'],
    availability: ['single_zone'], credential_access: ['read_only', 'read_write', 'migration'],
    scale_to_zero: true, always_on: false, pooled_connections: true,
    point_in_time_restore: true, class_resize: true, storage_limit_bytes: 10737418240, restore_window_seconds: 604800,
  };
  let requests = 0;
  const server = createServer((req, res) => {
    requests++;
    assert.equal(req.method, 'GET');
    assert.equal(req.url, '/v1/postgres/capabilities?region=eu-central-1');
    assert.equal(req.headers.authorization, 'Bearer fixture-token');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(capabilities));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const result = await ManagedPostgresService.getManagedPostgresCapabilities({ region: 'eu-central-1' });
  assert.deepEqual(result, capabilities);
  assert.equal(requests, 1);
});
