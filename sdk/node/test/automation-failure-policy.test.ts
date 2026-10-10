import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('failure policy preserves explicit disable, generation and retained work preview', async t => {
  const calls: string[] = [];
  const body = { expected_version: 2, enabled: false, failure_threshold: 3, min_completed_runs: 5, window_seconds: 300 };
  const server = createServer((req, res) => {
    void (async () => {
      calls.push(`${req.method} ${req.url}`);
      assert.equal(req.headers.authorization, 'Bearer token');
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      if (req.method === 'PUT') assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), body);
      if (req.method === 'POST') assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { expected_generation: 7 });
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ policy: { version: 3, enabled: false, failure_threshold: 3, min_completed_runs: 5, window_seconds: 300 }, paused: req.method !== 'POST', generation: req.method === 'POST' ? 8 : 7, observed_failures: 3, observed_completed_runs: 5, pending_runs: 2, running_runs: 1, waiting_runs: 0, retained_events: 4, history: [] }));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const preview = await WorkflowsService.getAutomationFailurePolicy({ slug: 'billing', name: 'invoice' });
  assert.equal(preview.paused, true); assert.equal(preview.retained_events, 4);
  await WorkflowsService.setAutomationFailurePolicy({ slug: 'billing', name: 'invoice', requestBody: body });
  const resumed = await WorkflowsService.resumeAutomationFailurePause({ slug: 'billing', name: 'invoice', requestBody: { expected_generation: 7 } });
  assert.equal(resumed.paused, false); assert.equal(resumed.generation, 8);
  assert.deepEqual(calls, ['GET /v1/apps/billing/automations/invoice/failure-policy', 'PUT /v1/apps/billing/automations/invoice/failure-policy', 'POST /v1/apps/billing/automations/invoice/failure-policy/resume']);
});
