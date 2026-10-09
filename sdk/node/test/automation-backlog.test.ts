import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, AlertRulesService, type AlertRuleResponse } from '../src/index.js';

test('automation backlog preset decodes the due-age metric and preserves notification defaults', async t => {
  const rule: AlertRuleResponse = {
    id: '0123456789abcdef0123456789abcdef', app_id: '1123456789abcdef0123456789abcdef',
    name: 'Automation backlog exceeds five minutes', enabled: true, metric: 'workflow_due_age_seconds',
    comparison: 'gte', threshold: 300, window_spec: '5m', webhook_url: 'https://example.com/hook',
    webhook_secret_sealed_masked: '***', cooldown_minutes: 30, action: 'webhook', state: 'ok',
    created_at: '2026-10-07T12:00:00Z', updated_at: '2026-10-07T12:00:00Z',
  };
  const server = createServer(async (req, res) => {
    assert.equal(req.method, 'POST');
    assert.equal(req.url, '/v1/apps/billing/alert-presets/automation_backlog/enable');
    let raw = '';
    for await (const chunk of req) raw += chunk;
    const body = JSON.parse(raw);
    assert.equal(body.webhook_url, rule.webhook_url);
    assert.equal(body.webhook_secret, 'test-secret');
    assert.equal(body.action, undefined);
    assert.equal(body.cooldown_minutes, undefined);
    res.writeHead(201, { 'content-type': 'application/json' });
    res.end(JSON.stringify(rule));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await AlertRulesService.enableAlertPreset({
    slug: 'billing', name: 'automation_backlog', requestBody: { webhook_url: rule.webhook_url, webhook_secret: 'test-secret' },
  });
  assert.deepEqual(response, rule);
});
