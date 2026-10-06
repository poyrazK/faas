import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, AppsService, DeploymentsService, type AppManifest, type BindingApplicationAdoption } from '../src/index.js';

test('strict promotion uses its dedicated route and preserves adoption evidence', async (t) => {
 const id = '01234567-89ab-cdef-0123-456789abcdef';
 const adoption: BindingApplicationAdoption = {
  source: 'application_ack', status: 'current', observed_at: '2026-10-03T00:00:00Z', complete: true,
  secrets_expected: 1, secrets_observed: 1,
  reload: { current: 1, failed: 0, stale: 0, unknown: 0 },
  application: { current: 1, failed: 0, stale: 0, unknown: 0 },
  targets: [{ deployment_id: id, instance_id: id, workload_name: 'worker', runtime_state: 'running', key: 'DATABASE_URL', reload_support: 'enabled', current_version: 2, reload_version: 1, application_ack_version: 2, application_ack: 'applied', process_generation: 'a'.repeat(32), application_ack_generation: 'a'.repeat(32) }],
 };
 let requests = 0;
 const server = createServer((req, res) => {
  void (async () => {
   requests++;
   assert.equal(req.url, `/v1/deployments/${id}/promote-with-application-ack`);
   assert.equal(req.method, 'POST');
   assert.equal(req.headers.authorization, 'Bearer fixture-token');
   let body = '';
   for await (const chunk of req) body += String(chunk);
   assert.deepEqual(JSON.parse(body), { require_application_ack: true });
   res.setHeader('Content-Type', 'application/json');
   res.end(JSON.stringify({ deployment: { id, traffic_percent: 100 }, from_percent: 0, to_percent: 100, already_promoted: false,
    bindings_check: { require_application_ack: true, passed: true, bindings: [{ type: 'postgres', name: 'primary', scope: 'default', status: 'passed', application_adoption: adoption }] } }));
  })().catch(error => { res.statusCode = 500; res.end(String(error)); });
 });
 server.listen(0, '127.0.0.1');
 await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address();
 assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 const receipt = await DeploymentsService.promoteDeploymentWithApplicationAck({ id, requestBody: { require_application_ack: true } });
 assert.equal(receipt.bindings_check.require_application_ack, true);
 assert.deepEqual(receipt.bindings_check.bindings[0]?.application_adoption, adoption);
 assert.equal(requests, 1);
});

test('app response preserves typed secret reload readiness', async (t) => {
 const manifest: AppManifest = { entrypoint: ['node', 'server.js'], secret_reload_signal: 'SIGHUP', secret_reload_readiness: true };
 const server = createServer((req, res) => {
  assert.equal(req.method, 'GET');
  assert.equal(req.url, '/v1/apps/worker');
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify({ slug: 'worker', manifest }));
 });
 server.listen(0, '127.0.0.1');
 await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address();
 assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 const app = await AppsService.getApp({ slug: 'worker' });
 assert.equal(app.manifest?.secret_reload_readiness, true);
 assert.deepEqual(app.manifest, manifest);
});
