import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';
import type { AutomationCheckEvidence } from '../src/generated/models/AutomationCheckEvidence.js';

test('checked publication sends evidence and revision reads retain advisory gaps', async t => {
  const evidence: AutomationCheckEvidence = {
    definition_hash: 'a'.repeat(64), checked_version: 7, checked_at: '2026-10-09T10:00:00Z',
    scenarios: [{ name: 'success', passed: true, definition_valid: true, complete: true }],
    coverage_required: false, coverage_passed: false, coverage_remaining: 1,
    exclusions: [{ step: 'approval', code: 'wait_timeout_missing', reason: 'reviewed' }],
  };
  const server = createServer((req, res) => {
    void (async () => {
      assert.equal(req.headers.authorization, 'Bearer token');
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      res.setHeader('content-type', 'application/json');
      if (req.method === 'POST') {
        assert.equal(req.url, '/v1/apps/billing/automations/invoice/publish');
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { expected_version: 7, check_evidence: evidence });
        res.end(JSON.stringify({ name: 'invoice', version: 9, source: 'dashboard', draft: { name: 'invoice', steps: [] }, enabled: true }));
      } else {
        assert.equal(req.url, '/v1/apps/billing/automations/invoice/revisions/9');
        res.end(JSON.stringify({ version: 9, definition: { name: 'invoice', steps: [] }, definition_hash: evidence.definition_hash, recorded_at: evidence.checked_at, legacy_snapshot: false, published_by_account_id: 'actor', check_evidence: evidence }));
      }
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  await WorkflowsService.publishAutomation({ slug: 'billing', name: 'invoice', requestBody: { expected_version: 7, check_evidence: evidence } });
  const revision = await WorkflowsService.getAutomationRevision({ slug: 'billing', name: 'invoice', version: 9 });
  assert.deepEqual(revision.check_evidence, evidence);
});
