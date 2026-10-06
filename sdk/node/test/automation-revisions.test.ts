import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('automation revision SDK lists, reads, and restores immutable revisions as drafts', async t => {
  const server = createServer((req, res) => {
    void (async () => {
      res.setHeader('content-type', 'application/json');
      if (req.method === 'GET' && req.url === '/v1/apps/billing/automations/paid-invoice/revisions?limit=10') {
        res.end(JSON.stringify({ revisions: [{ version: 42, definition: { name: 'paid-invoice', steps: [] }, definition_hash: 'a'.repeat(64), recorded_at: '2026-10-04T12:00:00Z', legacy_snapshot: false, published_by_account_id: 'account' }], total: 1, limit: 10, offset: 0 }));
        return;
      }
      if (req.method === 'GET' && req.url === '/v1/apps/billing/automations/paid-invoice/revisions/42') {
        res.end(JSON.stringify({ version: 42, definition: { name: 'paid-invoice', steps: [] }, definition_hash: 'a'.repeat(64), recorded_at: '2026-10-04T12:00:00Z', legacy_snapshot: false, published_by_account_id: 'account' }));
        return;
      }
      if (req.method === 'POST' && req.url === '/v1/apps/billing/automations/paid-invoice/revisions/42/restore') {
        assert.equal(req.headers['idempotency-key'], 'restore-v42');
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(Buffer.from(chunk));
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { expected_version: 47 });
        res.end(JSON.stringify({ name: 'paid-invoice', version: 48, source: 'dashboard', draft: { name: 'paid-invoice', steps: [] }, enabled: true }));
        return;
      }
      res.statusCode = 404;
      res.end(JSON.stringify({ error: `unexpected ${req.method} ${req.url}` }));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });

  const page = await WorkflowsService.listAutomationRevisions({ slug: 'billing', name: 'paid-invoice', limit: 10 });
  assert.equal(page.total, 1);
  assert.equal(page.revisions[0]?.version, 42);
  const revision = await WorkflowsService.getAutomationRevision({ slug: 'billing', name: 'paid-invoice', version: 42 });
  assert.equal(revision.definition_hash, 'a'.repeat(64));
  const restored = await WorkflowsService.restoreAutomationRevision({
    slug: 'billing', name: 'paid-invoice', version: 42,
    requestBody: { expected_version: 47 }, idempotencyKey: 'restore-v42',
  });
  assert.equal(restored.version, 48);
  assert.equal(restored.draft.name, 'paid-invoice');
});
