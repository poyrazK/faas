import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('workflow run history filters are sent as query parameters', async t => {
  const server = createServer((req, res) => {
    assert.equal(req.method, 'GET');
    const url = new URL(req.url ?? '/', 'http://localhost');
    assert.equal(url.pathname, '/v1/apps/billing/workflows/runs');
    assert.equal(url.searchParams.get('workflow_name'), 'paid-invoice');
    assert.equal(url.searchParams.get('status'), 'failed');
    assert.equal(url.searchParams.get('created_after'), '2026-10-01T00:00:00Z');
    assert.equal(url.searchParams.get('created_before'), '2026-10-05T23:59:59Z');
    assert.equal(url.searchParams.get('limit'), '10');
    assert.equal(url.searchParams.get('offset'), '20');
    res.setHeader('content-type', 'application/json');
    res.end(JSON.stringify({ runs: [], total: 0 }));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await WorkflowsService.listWorkflowRuns({
    slug: 'billing', workflowName: 'paid-invoice', status: 'failed',
    createdAfter: '2026-10-01T00:00:00Z', createdBefore: '2026-10-05T23:59:59Z',
    limit: 10, offset: 20,
  });
  assert.equal(response.total, 0);
});

test('workflow run creation sends an optional idempotency key', async t => {
  const server = createServer((req, res) => {
    void (async () => {
      assert.equal(req.method, 'POST');
      assert.equal(req.url, '/v1/apps/billing/workflows/paid-invoice/runs');
      assert.equal(req.headers['idempotency-key'], 'invoice-event-42');
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { invoice_id: '42' });
      res.statusCode = 201;
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({
        id: 'run-1', app_id: 'app-1', workflow_name: 'paid-invoice', status: 'pending',
        scheduled_for: '2026-10-05T12:00:00Z', created_at: '2026-10-05T12:00:00Z',
        updated_at: '2026-10-05T12:00:00Z',
      }));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await WorkflowsService.createWorkflowRun({
    slug: 'billing', name: 'paid-invoice', idempotencyKey: 'invoice-event-42',
    requestBody: { invoice_id: '42' },
  });
  assert.equal(response.id, 'run-1');
});
