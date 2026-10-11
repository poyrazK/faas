import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ManagedPostgresService, type ManagedPostgresCapabilities, type ManagedPostgresResize, type ManagedPostgresComputePolicyChange } from '../src/index.js';

test('PostgreSQL capabilities preserve reader support with a closed rollout gate', async (t) => {
  const capabilities: ManagedPostgresCapabilities = {
    contract_version: 3, region: 'eu-central-1', provisioning_enabled: false,
    database_limit: 1, postgres_majors: [16, 17], service_classes: ['development', 'burstable'],
    availability: ['single_zone'], credential_access: ['read_only', 'read_write', 'migration'],
    scale_to_zero: true, always_on: false, pooled_connections: true,
    point_in_time_restore: true, class_resize: true, scale_to_zero_update: true, storage_limit_bytes: 10737418240, restore_window_seconds: 604800,
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


test('compute resize preserves the durable UUID across requests and reads progress', async (t) => {
  const id = '33333333-3333-4333-8333-333333333333';
  const progress: ManagedPostgresResize = {
    id, database_id: 'orders', from_class: 'development', target_class: 'burstable',
    generation: 2, state: 'pending', connection_interruption_expected: true,
    created_at: '2026-10-05T00:00:00Z',
  };
  let posts = 0, gets = 0;
  const server = createServer(async (req, res) => {
    assert.equal(req.headers.authorization, 'Bearer fixture');
    if (req.method === 'POST') {
      assert.equal(req.url, '/v1/postgres/databases/orders/resize');
      let body = '';
      for await (const part of req) body += part;
      assert.deepEqual(JSON.parse(body), { request_id: id, service_class: 'burstable' });
      posts++;
      res.statusCode = 202;
    } else {
      assert.equal(req.method, 'GET');
      assert.equal(req.url, `/v1/postgres/databases/orders/resizes/${id}`);
      gets++;
    }
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(progress));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  for (let i = 0; i < 2; i++) {
    assert.deepEqual(await ManagedPostgresService.resizeManagedPostgresDatabase({
      id: 'orders', requestBody: { request_id: id, service_class: 'burstable' },
    }), progress);
  }
  assert.deepEqual(await ManagedPostgresService.getManagedPostgresResize({ id: 'orders', resizeId: id }), progress);
  assert.equal(posts, 2);
  assert.equal(gets, 1);
});

test('compute policy preserves explicit false and the durable UUID across requests and reads progress', async (t) => {
  const id = '33333333-3333-4333-8333-333333333333';
  const progress: ManagedPostgresComputePolicyChange = {
    id, database_id: 'orders', from_scale_to_zero: true, target_scale_to_zero: false,
    generation: 2, state: 'pending', connection_interruption_expected: true,
    created_at: '2026-10-05T00:00:00Z',
  };
  let posts = 0, gets = 0;
  const server = createServer(async (req, res) => {
    assert.equal(req.headers.authorization, 'Bearer fixture');
    if (req.method === 'POST') {
      assert.equal(req.url, '/v1/postgres/databases/orders/compute-policy');
      let body = '';
      for await (const part of req) body += part;
      assert.deepEqual(JSON.parse(body), { request_id: id, scale_to_zero: false });
      posts++;
      res.statusCode = 202;
    } else {
      assert.equal(req.method, 'GET');
      assert.equal(req.url, `/v1/postgres/databases/orders/compute-policy-changes/${id}`);
      gets++;
    }
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(progress));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  for (let i = 0; i < 2; i++) {
    assert.deepEqual(await ManagedPostgresService.changeManagedPostgresComputePolicy({
      id: 'orders', requestBody: { request_id: id, scale_to_zero: false },
    }), progress);
  }
  assert.deepEqual(await ManagedPostgresService.getManagedPostgresComputePolicyChange({ id: 'orders', changeId: id }), progress);
  assert.equal(posts, 2);
  assert.equal(gets, 1);
});
