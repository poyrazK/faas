import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type SetMCPReadinessFallbackRequest, type MCPReadinessFallbackPolicy, type MCPReadinessDeliveryRouting, type AlertDeliveryResponse } from '../src/preview.js';

test('fallback configuration preserves zero versions and receiver progress', async (t) => {
  const rule = '00000000-0000-0000-0000-000000000001';
  const path = `/v1/apps/demo/alerts/${rule}/mcp-readiness-fallback`;
  const request: SetMCPReadinessFallbackRequest = { expected_version: 0, enabled: true, webhook_url: 'https://fallback.example.com/hook', webhook_secret: 'private-key', after_failures: 2 };
  const policy: MCPReadinessFallbackPolicy = { version: 1, enabled: true, webhook_url: request.webhook_url, webhook_secret_sealed_masked: '***', after_failures: 2 };
  const progress: MCPReadinessDeliveryRouting = { policy_version: 1, last_destination: 'fallback', failover_reason: 'primary_failure_threshold', primary_attempts: 2, fallback_attempts: 1, primary_failures: 2, primary_delivered: false, fallback_delivered: true };
  const delivery: AlertDeliveryResponse = { id: rule, rule_id: rule, account_id: rule, idempotency_key: 'claim', status: 'delivered', attempt_count: 1, observed_value: 1, fired_at: '2026-10-17T12:00:00Z', is_test: false, readiness_event_id: rule, readiness_destination: 'fallback', readiness_failover_reason: progress.failover_reason };
  assert.equal(delivery.readiness_destination, progress.last_destination);
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++;
    assert.equal(req.url, path);
    res.setHeader('Content-Type', 'application/json');
    if (req.method === 'PUT') {
      let body = ''; for await (const chunk of req) body += chunk;
      const data = JSON.parse(body);
      if (calls === 2) assert.deepEqual(data, request);
      else assert.deepEqual(data, { expected_version: 1, enabled: false });
    }
    res.end(JSON.stringify(calls === 3 ? { version: 2, enabled: false } : policy));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await McpService.getMcpReadinessFallback({ slug: 'demo', id: rule }), policy);
  assert.deepEqual(await McpService.setMcpReadinessFallback({ slug: 'demo', id: rule, requestBody: request }), policy);
  assert.deepEqual(await McpService.setMcpReadinessFallback({ slug: 'demo', id: rule, requestBody: { expected_version: 1, enabled: false } }), { version: 2, enabled: false });
  assert.equal(calls, 3);
});
