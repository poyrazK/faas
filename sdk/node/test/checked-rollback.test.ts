import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, DeploymentsService, type ServiceAbortReceipt, type RollbackOperation } from '../src/index.js';

// adr: 20261104144000
// adr: 20261104173000
for (const scope of ['default', 'staging']) {
test(`service abort acceptance lookup preserves ${scope} identity and scopes write metadata`, async (t) => {
 const attempt = '3e9f323aade6442b8444c91da107fe44';
 const receipt: ServiceAbortReceipt = { attempt_id: attempt, request_hash: 'a'.repeat(64), app_id: '142b7504-f03a-4ee2-aeb3-14d922a845d4', deployment_id: 'a2b9cc53-907f-4b5c-88a4-fd0c21214556', predecessor_deployment_id: '5b87c415-7c93-4932-acab-a3c90e98be86', request_id: 'd5c0a595-f6d7-415e-a9d8-d6634052f03a', scope, ...(scope === 'staging' ? { project_id: 'e377519c-cbca-4dfa-a3e7-26b1321e0de4', environment_id: '8677f406-6b97-4b24-bf26-cf795e040713' } : {}), accepted_at: new Date().toISOString() };
 const body = { action: 'abort' as const, deployment_id: receipt.deployment_id, expected_predecessor_deployment_id: receipt.predecessor_deployment_id, expected_review: { scope, ...(scope === 'staging' ? { project_id: receipt.project_id, environment_id: receipt.environment_id } : {}), app_id: receipt.app_id, configuration_hash: 'review', latest_deployment_id: receipt.deployment_id, serving_deployment_ids: [receipt.predecessor_deployment_id] } };
 const server = createServer((req, res) => { void (async () => {
  res.setHeader('Content-Type', 'application/json');
  if (req.method === 'POST') {
   assert.equal(req.url, '/v1/apps/api/rollouts/recover');
   assert.equal(req.headers['service-abort-attempt-id'], attempt);
   let wire = ''; for await (const chunk of req) wire += String(chunk);
   assert.deepEqual(JSON.parse(wire), body);
   res.statusCode = 202; res.end(JSON.stringify({ deployment: { id: receipt.deployment_id }, audit_id: '1', service_recovery: { deployment_id: receipt.deployment_id, predecessor_deployment_id: receipt.predecessor_deployment_id, request_id: receipt.request_id, status: 'accepted' } }));
  } else {
   assert.equal(req.method, 'GET'); assert.equal(req.url, `/v1/apps/api/service-aborts/${attempt}`);
   assert.equal(req.headers['service-abort-attempt-id'], undefined); assert.equal(req.headers['idempotency-key'], undefined);
   res.end(JSON.stringify(receipt));
  }
 })().catch(error => { res.statusCode = 500; res.end(String(error)); }); });
 server.listen(0, '127.0.0.1'); await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address(); assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 const accepted = await DeploymentsService.recoverRollout({ slug: 'api', requestBody: body, serviceAbortAttemptId: attempt });
 assert.equal(accepted.service_recovery?.request_id, receipt.request_id);
 assert.deepEqual(await DeploymentsService.getServiceAbortReceipt({ slug: 'api', attemptId: attempt }), receipt);
});
}

test('historical rollback accepts exact pair and reads its operation', async (t) => {
 const operation: RollbackOperation = { id: '3e9f323a-ade6-442b-8444-c91da107fe44', app_id: '142b7504-f03a-4ee2-aeb3-14d922a845d4', scope: 'default', target_deployment_id: 'a2b9cc53-907f-4b5c-88a4-fd0c21214556', current_deployment_id: '5b87c415-7c93-4932-acab-a3c90e98be86', status: 'preparing', service: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() };
 const server = createServer((req, res) => { void (async () => {
  res.setHeader('Content-Type', 'application/json');
  if (req.method === 'POST') {
   assert.equal(req.url, '/v1/apps/api/rollback');
   assert.equal(req.headers['rollback-attempt-id'], '3e9f323aade6442b8444c91da107fe44');
   let body = ''; for await (const chunk of req) body += String(chunk);
   assert.deepEqual(JSON.parse(body), { target_deployment_id: operation.target_deployment_id, expected_current_deployment_id: operation.current_deployment_id });
   res.statusCode = 202; res.end(JSON.stringify({ id: operation.target_deployment_id, app_id: operation.app_id, status: 'snapshotting', rollback_operation: operation }));
  } else {
   assert.equal(req.headers['rollback-attempt-id'], undefined); assert.equal(req.headers['idempotency-key'], undefined);
   assert.equal(req.method, 'GET'); assert.equal(req.url, `/v1/apps/api/rollbacks/${operation.id}`); res.end(JSON.stringify(operation));
  }
 })().catch(error => { res.statusCode = 500; res.end(String(error)); }); });
 server.listen(0, '127.0.0.1'); await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address(); assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 const accepted = await DeploymentsService.rollbackApp({ slug: 'api', rollbackAttemptId: '3e9f323aade6442b8444c91da107fe44', requestBody: { target_deployment_id: operation.target_deployment_id, expected_current_deployment_id: operation.current_deployment_id } });
 assert.deepEqual(accepted.rollback_operation, operation);
 assert.deepEqual(await DeploymentsService.getRollbackOperation({ slug: 'api', operation: operation.id }), operation);
});
