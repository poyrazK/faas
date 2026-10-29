import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type SetMCPReadinessReminderDefaultsRequest, type MCPReadinessReminderDefaults, type MCPReadinessIncidentAttention } from '../src/preview.js';

test('rule reminder defaults preserve explicit zero policy and inherited version zero', { timeout: 10000 }, async (t) => {
  const rule = '00000000-0000-0000-0000-000000000001';
  const path = `/v1/apps/demo/alerts/${rule}/mcp-readiness-reminder-defaults`;
  const request: SetMCPReadinessReminderDefaultsRequest = { expected_version: 0, reminder_policy: { interval_seconds: 0, max_reminders: 0 } };
  const policy: MCPReadinessReminderDefaults = { version: 1, reminder_policy: request.reminder_policy };
  const incident: MCPReadinessIncidentAttention = { incident_id: rule, rule_id: rule, rule_name: 'Readiness', rule_enabled: true, opened_at: '2026-10-17T12:00:00Z', version: 0, state: 'unacknowledged', reminder_policy: request.reminder_policy, reminders_created: 0, reminder_policy_source: 'rule_default', reminder_defaults_version: 0 };
  assert.equal(incident.reminder_defaults_version, 0);
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++;
    assert.equal(req.url, path);
    if (req.method === 'PUT') {
      let body = ''; for await (const chunk of req) body += chunk;
      assert.deepEqual(JSON.parse(body), request);
    }
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(policy));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => { server.closeAllConnections(); server.close(); });
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await McpService.getMcpReadinessReminderDefaults({ slug: 'demo', id: rule }), policy);
  assert.deepEqual(await McpService.setMcpReadinessReminderDefaults({ slug: 'demo', id: rule, requestBody: request }), policy);
  assert.equal(calls, 2);
});
