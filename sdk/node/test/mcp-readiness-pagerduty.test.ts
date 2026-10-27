import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';
import { FaaSClient, McpService, type SetMCPReadinessPagerDutyRequest, type MCPReadinessPagerDutyPolicy, type MCPReadinessDestinationDelivery } from '../src/preview.js';

test('PagerDuty policy preserves disabled flags and provider acceptance semantics', { timeout: 10000 }, async (t) => {
  const rule = '00000000-0000-0000-0000-000000000001';
  const path = `/v1/apps/demo/alerts/${rule}/mcp-readiness-pagerduty`;
  const request: SetMCPReadinessPagerDutyRequest = { expected_version: 0, enabled: true, integration_key: 'a'.repeat(32), region: 'eu' };
  const policy: MCPReadinessPagerDutyPolicy = { version: 1, enabled: true, region: 'eu', integration_key_masked: '***' };
  const delivery: MCPReadinessDestinationDelivery = { destination: 'primary', status: 'delivered', attempt_count: 1, provider: 'pagerduty', provider_status: 'accepted' };
  assert.equal(delivery.provider_status, 'accepted');
  let calls = 0;
  const server = createServer(async (req, res) => {
    calls++; assert.equal(req.url, path);
    if (req.method === 'PUT') {
      let body = ''; for await (const chunk of req) body += chunk;
      assert.deepEqual(JSON.parse(body), calls === 2 ? request : { expected_version: 1, enabled: false });
    }
    res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(calls === 3 ? { version: 2, enabled: false, region: 'us' } : policy));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => { server.closeAllConnections(); server.close(); });
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await McpService.getMcpReadinessPagerDuty({ slug: 'demo', id: rule }), policy);
  assert.deepEqual(await McpService.setMcpReadinessPagerDuty({ slug: 'demo', id: rule, requestBody: request }), policy);
  assert.deepEqual(await McpService.setMcpReadinessPagerDuty({ slug: 'demo', id: rule, requestBody: { expected_version: 1, enabled: false } }), { version: 2, enabled: false, region: 'us' });
  assert.equal(calls, 3);
});
