import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type SetMCPReadinessPagerDutyAcknowledgementsRequest, type MCPReadinessPagerDutyAcknowledgementPolicy, type MCPReadinessPagerDutyAcknowledgementEvent, type MCPReadinessAttentionReceipt, type MCPReadinessPagerDutyAcknowledgementHold } from '../src/index.js';

test('signed PagerDuty acknowledgement settings and receipt contracts', { timeout: 10000 }, async (t) => {
  const rule = '00000000-0000-0000-0000-000000000001';
  const path = `/v1/apps/demo/alerts/${rule}/mcp-readiness-pagerduty-acknowledgements`;
  const request: SetMCPReadinessPagerDutyAcknowledgementsRequest = { expected_version: 0, enabled: true, sync_unacknowledgements: true, expected_pagerduty_version: 1, service_id: 'PSVC001', signing_secret: 's'.repeat(32) };
  const policy: MCPReadinessPagerDutyAcknowledgementPolicy = { version: 1, enabled: true, sync_unacknowledgements: true, region: 'eu', service_id: 'PSVC001', pagerduty_version: 1, signing_secret_masked: '***', webhook_path: `/v1/webhooks/mcp-readiness/pagerduty/${rule}` };
  const event: MCPReadinessPagerDutyAcknowledgementEvent = { id: rule, event_id: 'provider-1', incident_id: rule, provider_incident_id: 'PINC001', occurred_at: '2026-10-17T00:00:00Z', received_at: '2026-10-17T00:00:01Z', policy_version: 1, action: 'release', destination: 'fallback', outcome: 'released', attention_version: 1, region: 'eu', service_id: 'PSVC001' };
  const attention: MCPReadinessAttentionReceipt = { request_id: rule, incident_id: rule, created_at: event.received_at, created_by: rule, action: 'acknowledge', reason: 'Acknowledged in PagerDuty', version: 1, source: 'pagerduty', provider_event_id: event.event_id, provider_incident_id: event.provider_incident_id, provider_occurred_at: event.occurred_at };
  assert.equal(attention.source, 'pagerduty');
  const hold: MCPReadinessPagerDutyAcknowledgementHold = { destination: 'primary', provider_incident_id: 'PINC001', provider_event_id: 'ack-1', service_id: 'PSVC001', region: 'eu', occurred_at: event.occurred_at, received_at: event.received_at, policy_version: 1, release_sync_enabled: true };
  assert.equal(hold.release_sync_enabled, true);
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++;
    if (calls === 4) {
      assert.equal(req.url, `${path}/events?before=cursor&limit=1`);
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ events: [event] })); return;
    }
    if (calls === 5) {
      assert.equal(req.url, `${path}?destination=fallback`);
    } else {
      assert.equal(req.url, path);
    }
    if (req.method === 'PUT') { let body = ''; for await (const chunk of req) body += chunk; assert.deepEqual(JSON.parse(body), calls === 2 ? request : { expected_version: 1, enabled: false, destination: 'fallback' }); }
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(policy));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => { server.closeAllConnections(); server.close(); });
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await McpService.getMcpReadinessPagerDutyAcknowledgements({ slug: 'demo', id: rule }), policy);
  assert.deepEqual(await McpService.setMcpReadinessPagerDutyAcknowledgements({ slug: 'demo', id: rule, requestBody: request }), policy);
  await McpService.setMcpReadinessPagerDutyAcknowledgements({ slug: 'demo', id: rule, requestBody: { expected_version: 1, enabled: false, destination: 'fallback' } });
  assert.deepEqual(await McpService.listMcpReadinessPagerDutyAcknowledgementEvents({ slug: 'demo', id: rule, before: 'cursor', limit: 1 }), { events: [event] });
  assert.deepEqual(await McpService.getMcpReadinessPagerDutyAcknowledgements({ slug: 'demo', id: rule, destination: 'fallback' }), policy);
  assert.equal(calls, 5);
});
