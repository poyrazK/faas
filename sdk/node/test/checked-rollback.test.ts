import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, DeploymentsService, type RollbackOperation } from '../src/index.js';

test('historical rollback accepts exact pair and reads its operation', async (t) => {
 const operation: RollbackOperation = { id: '3e9f323a-ade6-442b-8444-c91da107fe44', app_id: '142b7504-f03a-4ee2-aeb3-14d922a845d4', scope: 'default', target_deployment_id: 'a2b9cc53-907f-4b5c-88a4-fd0c21214556', current_deployment_id: '5b87c415-7c93-4932-acab-a3c90e98be86', status: 'preparing', service: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
 const server = createServer((req, res) => { void (async () => {
  res.setHeader('Content-Type', 'application/json');
  if (req.method === 'POST') {
   assert.equal(req.url, '/v1/apps/api/rollback');
   let body = ''; for await (const chunk of req) body += String(chunk);
   assert.deepEqual(JSON.parse(body), { target_deployment_id: operation.target_deployment_id, expected_current_deployment_id: operation.current_deployment_id });
   res.statusCode = 202; res.end(JSON.stringify({ id: operation.target_deployment_id, app_id: operation.app_id, status: 'snapshotting', rollback_operation: operation }));
  } else {
   assert.equal(req.method, 'GET'); assert.equal(req.url, `/v1/apps/api/rollbacks/${operation.id}`); res.end(JSON.stringify(operation));
  }
 })().catch(error => { res.statusCode = 500; res.end(String(error)); }); });
 server.listen(0, '127.0.0.1'); await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address(); assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 const accepted = await DeploymentsService.rollbackApp({ slug: 'api', requestBody: { target_deployment_id: operation.target_deployment_id, expected_current_deployment_id: operation.current_deployment_id } });
 assert.deepEqual(accepted.rollback_operation, operation);
 assert.deepEqual(await DeploymentsService.getRollbackOperation({ slug: 'api', operation: operation.id }), operation);
});
