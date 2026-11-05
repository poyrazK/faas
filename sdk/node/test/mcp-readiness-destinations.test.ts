import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type MCPReadinessNotificationAction, type MCPReadinessNotificationQueues } from '../src/preview.js';

test('receiver queue progress and targeted decisions cross the HTTP boundary', { timeout: 10000 }, async (t) => {
  const id = '00000000-0000-0000-0000-000000000001';
  const at = '2026-10-17T12:00:00Z';
  const action: MCPReadinessNotificationAction = { request_id: id, event_id: id, action: 'retry', expected_version: 0, reason: 'Receiver repaired', destination: 'primary' };
  const page: MCPReadinessNotificationQueues = { observed_at: at, queues: [{ rule_id: id, name: 'Readiness', enabled: true, outstanding_count: 1, blocked_count: 0, oldest_age_seconds: 60, state: 'delayed', can_retry: true, can_abandon: true, destinations: [{ destination: 'primary', outstanding_count: 1, blocked_count: 0, state: 'delayed', can_retry: true, can_abandon: true, head: { id, event_id: id, incident_id: id, rule_id: id, event: 'alert.resolved', scopes: ['staging'], occurred_at: at, incident_status: 'resolved', delivery_status: 'failed', delivery_version: 0, attempt_count: 1, subjects: [], destination_deliveries: [{ destination: 'primary', status: 'failed', attempt_count: 1, next_attempt_at: at }, { destination: 'fallback', status: 'delivered', attempt_count: 1 }], delivery_routing: { policy_version: 1, primary_attempts: 1, fallback_attempts: 1, primary_failures: 1, primary_delivered: false, fallback_delivered: true, failover_reason: 'primary_queue_blocked' } } }] }] };
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++;
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'GET') {
      assert.equal(req.url, '/v1/apps/demo/mcp-readiness-notifications?limit=20');
      res.end(JSON.stringify(page));
    } else {
      assert.equal(req.url, '/v1/apps/demo/mcp-readiness-notifications/actions');
      let body = ''; for await (const chunk of req) body += chunk;
      assert.deepEqual(JSON.parse(body), action);
      res.end(JSON.stringify({ ...action, created_at: at, created_by: id, delivery_version: 1 }));
    }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => (server.closeAllConnections(), server.close(error => error ? reject(error) : resolve()))));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await McpService.listMcpReadinessNotificationQueues({ slug: 'demo' });
  assert.deepEqual(response, page);
  assert.equal(response.queues[0]?.destinations?.[0]?.head?.destination_deliveries?.[1]?.status, 'delivered');
  const receipt = await McpService.actMcpReadinessNotification({ slug: 'demo', requestBody: action });
  assert.equal(receipt.destination, 'primary');
  assert.equal(calls, 2);
});
