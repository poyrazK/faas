import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, DeploymentsService, type ServiceRolloutBindingGate, type ServiceRolloutRecoveryReceipt } from '../src/index.js';

test('exact service abort returns acceptance and reads routing status', async (t) => {
 const candidate = 'a2b9cc53-907f-4b5c-88a4-fd0c21214556';
 const predecessor = '5b87c415-7c93-4932-acab-a3c90e98be86';
 const requestID = '3e9f323a-ade6-442b-8444-c91da107fe44';
 const gate: ServiceRolloutBindingGate = { request_id: requestID, action: 'abort', deployment_id: predecessor, status: 'pending' };
 const receipt: ServiceRolloutRecoveryReceipt = { deployment_id: candidate, predecessor_deployment_id: predecessor, request_id: requestID, status: 'accepted' };
 const server = createServer((req, res) => {
  void (async () => {
   res.setHeader('Content-Type', 'application/json');
   if (req.method === 'POST' && req.url === '/v1/apps/api/rollouts/recover') {
    let body = '';
    for await (const chunk of req) body += String(chunk);
    assert.deepEqual(JSON.parse(body), { action: 'abort', deployment_id: candidate, expected_predecessor_deployment_id: predecessor });
    res.statusCode = 202;
    res.end(JSON.stringify({ deployment: { id: candidate, rollout_state: 'rolling_out', service_rollout_handoff: { action: 'abort', phase: 'pending', retry_count: 0, bindings_check: gate } }, audit_id: '42', service_recovery: receipt }));
   } else {
    assert.equal(req.method, 'GET');
    assert.equal(req.url, `/v1/deployments/${candidate}`);
    res.end(JSON.stringify({ id: candidate, rollout_state: 'rolling_out', service_rollout_handoff: { action: 'abort', phase: 'routing', retry_count: 1, bindings_check: { ...gate, status: 'passed', audit_id: '43' } } }));
   }
  })().catch(error => { res.statusCode = 500; res.end(String(error)); });
 });
 server.listen(0, '127.0.0.1');
 await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address();
 assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 const accepted = await DeploymentsService.recoverRollout({ slug: 'api', requestBody: { action: 'abort', deployment_id: candidate, expected_predecessor_deployment_id: predecessor } });
 assert.deepEqual(accepted.service_recovery, receipt);
 assert.equal(accepted.recovery, undefined);
 assert.equal(accepted.deployment.service_rollout_handoff?.bindings_check?.status, 'pending');
 const status = await DeploymentsService.getDeployment({ id: candidate });
 assert.equal(status.rollout_state, 'rolling_out');
 assert.equal(status.service_rollout_handoff?.phase, 'routing');
 assert.equal(status.service_rollout_handoff?.bindings_check?.audit_id, '43');
});
