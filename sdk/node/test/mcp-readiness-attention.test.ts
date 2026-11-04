import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type MCPReadinessAttentionAction, type MCPReadinessAttentionReceipt, type MCPReadinessIncidentAttention } from '../src/preview.js';

test('incident attention preserves zero versions, filters, and immutable receipts', async (t) => {
  const incident = '00000000-0000-0000-0000-000000000001';
  const request = '00000000-0000-0000-0000-000000000002';
  const actor = '00000000-0000-0000-0000-000000000003';
  const action: MCPReadinessAttentionAction = { request_id: request, expected_version: 0, action: 'configure_reminders', reason: 'Investigating', reminder_policy: { interval_seconds: 60, max_reminders: 3 } };
  const receipt: MCPReadinessAttentionReceipt = { request_id: request, incident_id: incident, action: 'configure_reminders', reason: 'Investigating', created_by: actor, created_at: '2026-10-17T12:00:00Z', version: 1, reminder_policy: action.reminder_policy };
  const attention: MCPReadinessIncidentAttention = { incident_id: incident, rule_id: actor, rule_name: 'Readiness', rule_enabled: true, opened_at: receipt.created_at, version: 1, state: 'unacknowledged', reminder_policy: action.reminder_policy!, reminders_created: 0, last_action: receipt };
  let calls = 0;
  const actionPath = `/v1/apps/demo/mcp-readiness-incidents/${incident}/attention-actions`;
  const server = createServer(async (req, res) => {
    calls++;
    const url = new URL(req.url!, 'http://localhost');
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'POST') {
      assert.equal(url.pathname, actionPath);
      let body = ''; for await (const chunk of req) body += chunk;
      assert.deepEqual(JSON.parse(body), action);
      res.end(JSON.stringify(receipt));
    } else if (url.pathname === actionPath) {
      assert.equal(url.searchParams.get('before'), 'cursor');
      res.end(JSON.stringify({ actions: [receipt] }));
    } else {
      assert.equal(url.pathname, '/v1/apps/demo/mcp-readiness-attention');
      assert.equal(url.searchParams.get('scope'), 'staging');
      assert.equal(url.searchParams.get('include_resolved'), 'true');
      assert.equal(url.searchParams.get('after'), incident);
      res.end(JSON.stringify({ incidents: [attention] }));
    }
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await McpService.listMcpReadinessAttention({ slug: 'demo', scope: 'staging', includeResolved: true, after: incident }), { incidents: [attention] });
  assert.deepEqual(await McpService.actMcpReadinessAttention({ slug: 'demo', id: incident, requestBody: action }), receipt);
  assert.deepEqual(await McpService.actMcpReadinessAttention({ slug: 'demo', id: incident, requestBody: action }), receipt);
  assert.deepEqual(await McpService.listMcpReadinessAttentionHistory({ slug: 'demo', id: incident, before: 'cursor' }), { actions: [receipt] });
  assert.equal(calls, 4);
});
