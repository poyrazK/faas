import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService, type AutomationQueueHealth } from '../src/index.js';

const queue: AutomationQueueHealth = {
  observed_at: '2026-10-07T12:00:00Z', waiting_run_count: 3, due_run_count: 2, stale_run_count: 1,
  oldest_due_age_seconds: 42.5, app_running_count: 2, app_dispatch_limit: 2, tenant_dispatch_limit: 1,
  app_at_capacity: true,
  reason_counts: { ready: 0, scheduled: 0, retry_backoff: 0, parked_wait: 1, app_capacity: 2, tenant_capacity: 0, workflow_capacity: 0 },
};

for (const withQueue of [true, false]) {
  test(`automation health decodes ${withQueue ? 'current queue diagnostics' : 'legacy responses'} and sends the window`, async t => {
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
        ...(withQueue ? { queue } : {}),
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
    assert.deepEqual(response.queue, withQueue ? queue : undefined);
  });
}
