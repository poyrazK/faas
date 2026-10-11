import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('publishing policy and server checks preserve receipts and explicit null assertions', async t => {
  const receipt = 'b'.repeat(64);
  const body = {
    expected_version: 7, require_coverage: true, exclusions: [],
    scenarios: [{ name: 'success', simulation: { definition: { name: 'checked', steps: [] } }, expectations: [{ step: 'send', output: null, attempts: [] }] }],
  };
  const server = createServer((req, res) => {
    void (async () => {
      assert.equal(req.headers.authorization, 'Bearer token');
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      res.setHeader('content-type', 'application/json');
      if (req.method === 'GET') {
        assert.equal(req.url, '/v1/apps/billing/automations:publish-policy');
        res.end(JSON.stringify({ mode: 'coverage', version: 2 }));
      } else if (req.url?.endsWith('/publish-check')) {
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), body);
        res.end(JSON.stringify({ receipt, expires_at: '2026-10-09T10:30:00Z', evidence: { server_verified: true } }));
      } else {
        assert.equal(req.url, '/v1/apps/billing/automations/checked/publish');
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { expected_version: 7, check_receipt: receipt });
        res.end(JSON.stringify({ name: 'checked', version: 8 }));
      }
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const policy = await WorkflowsService.getAutomationPublishPolicy({ slug: 'billing' });
  assert.equal(policy.mode, 'coverage');
  const checked = await WorkflowsService.checkAutomationPublication({ slug: 'billing', name: 'checked', requestBody: body });
  assert.equal(checked.evidence.server_verified, true);
  await WorkflowsService.publishAutomation({ slug: 'billing', name: 'checked', requestBody: { expected_version: 7, check_receipt: checked.receipt } });
});
