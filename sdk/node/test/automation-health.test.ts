import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('automation health window is sent as query parameters', async t => {
  const server = createServer((req, res) => {
    assert.equal(req.method, 'GET');
    const url = new URL(req.url ?? '/', 'http://localhost');
    assert.equal(url.pathname, '/v1/apps/billing/automations/paid-invoice/health');
    assert.equal(url.searchParams.get('created_after'), '2026-10-01T00:00:00Z');
    assert.equal(url.searchParams.get('created_before'), '2026-10-05T23:59:59Z');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({
      app_slug: 'billing', automation_name: 'paid-invoice', run_count: 0, completed_run_count: 0, active_run_count: 0, queued_run_count: 0,
      success_rate: 0, status_counts: { pending: 0, running: 0, awaiting_event: 0, succeeded: 0, failed: 0, dead: 0 },
      failed_steps: [],
    }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await WorkflowsService.getAutomationHealth({
    slug: 'billing', name: 'paid-invoice', createdAfter: '2026-10-01T00:00:00Z', createdBefore: '2026-10-05T23:59:59Z',
  });
  assert.equal(response.run_count, 0);
  assert.deepEqual(response.failed_steps, []);
});
