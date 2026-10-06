import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, AlertRulesService, type AlertRollback } from '../src/index.js';

test('automatic rollback status preserves the fire and pinned pair through GETs', async (t) => {
 const receipt: AlertRollback = { id: '3e9f323a-ade6-442b-8444-c91da107fe44', rule_id: 'a2b9cc53-907f-4b5c-88a4-fd0c21214556', account_id: 'b3b9cc53-907f-4b5c-88a4-fd0c21214556', app_id: '142b7504-f03a-4ee2-aeb3-14d922a845d4', scope: 'default', candidate_deployment_id: '5b87c415-7c93-4932-acab-a3c90e98be86', predecessor_deployment_id: '6b87c415-7c93-4932-acab-a3c90e98be86', status: 'blocked', reason: 'alert fired', observed_value: 42, fired_at: new Date().toISOString(), updated_at: new Date().toISOString(), code: 'binding_verification_missing', blockers: [{ code: 'binding_verification_missing', message: 'verify predecessor' }] };
 let reads = 0;
	Object.assign(receipt, { service: true, service_request_id: receipt.id, service_phase: 'pending' });
 const historical: AlertRollback = { ...receipt, historical: true, rollback_operation_id: receipt.id, rollback_phase: 'preparing', deployment_evidence: { version: 1, deployment_id: receipt.candidate_deployment_id!, metric: 'error_rate_pct', comparison: 'gt', threshold: 1, window_spec: '5m', cutover_at: receipt.fired_at, window_start: receipt.fired_at, window_end: receipt.fired_at, requests: 20, server_errors: 2, minimum_requests: 20, error_rate_pct: 10, status: 'breached' } };
 delete historical.service_request_id; delete historical.service_phase;
 const server = createServer((req, res) => {
  reads++;
  assert.equal(req.method, 'GET');
  res.setHeader('Content-Type', 'application/json');
  if (req.url === '/v1/apps/api/alert-rollbacks') res.end(JSON.stringify([receipt]));
  else { assert.equal(req.url, `/v1/apps/api/alert-rollbacks/${receipt.id}`); res.end(JSON.stringify(historical)); }
 });
 server.listen(0, '127.0.0.1'); await once(server, 'listening');
 t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
 const address = server.address(); assert.ok(address && typeof address !== 'string');
 new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
 assert.deepEqual(await AlertRulesService.listAlertRollbacks({ slug: 'api' }), [receipt]);
 assert.deepEqual(await AlertRulesService.getAlertRollback({ slug: 'api', fire: receipt.id }), historical);
 assert.equal(reads, 2);
});
