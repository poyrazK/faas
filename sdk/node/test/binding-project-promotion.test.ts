import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, ProjectsService } from '../src/index.js';

test('checked project promotion preserves accepted receipts and its dedicated route', async (t) => {
 let calls = 0;
 const server = createServer((req, res) => {
  void (async () => {
   calls++;
   assert.equal(req.method, 'POST');
   assert.equal(req.url, '/v1/projects/shop/environments/production/promote-with-bindings');
   assert.equal(req.headers['idempotency-key'], 'stable');
   let body = '';
   for await (const chunk of req) body += String(chunk);
   assert.equal(JSON.parse(body).require_bindings, true);
   res.statusCode = 202;
   res.setHeader('Content-Type', 'application/json');
   res.end(JSON.stringify({ promotion_id: 'operation', status: 'running', bindings_required: true, workloads: [] }));
  })().catch(error => { res.statusCode = 500; res.end(String(error)); });
 });
 server.listen(0, '127.0.0.1');
 await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address();
 assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
 const receipt = await ProjectsService.promoteProjectEnvironmentWithBindings({ slug: 'shop', environment: 'production', idempotencyKey: 'stable', requestBody: { from_environment: 'staging', promotion_token: 'preview', require_bindings: true } });
 assert.equal(receipt.status, 'running');
 assert.equal(receipt.bindings_required, true);
 assert.equal(calls, 1);
});
