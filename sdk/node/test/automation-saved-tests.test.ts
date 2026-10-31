import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/preview.js';

test('saved test APIs preserve null assertions, safe failure fields, and suite removal', async t => {
  const suite = { sample: { input: null }, cases: [{ name: 'null output', simulation: { mock_outputs: { main: null } }, expectations: [{ step: 'main', output: null }] }] };
  const requests: unknown[] = [];
  const server = createServer((req, res) => {
    void (async () => {
      assert.equal(req.headers.authorization, 'Bearer token');
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      requests.push(JSON.parse(Buffer.concat(chunks).toString()));
      res.setHeader('content-type', 'application/json');
      if (req.method === 'POST') {
        assert.equal(req.url, '/v1/apps/billing/automations/tests/tests/run');
        res.end(JSON.stringify({ checked_version: 8, checked_at: '2026-10-15T10:00:00Z', inputs_hash: 'a'.repeat(64), passed: false, cases: [{ name: 'null output', passed: false, issue: 'assertion_failed', steps: [{ step: 'main', state: 'mocked', passed: false, mismatches: ['output', 'attempt_count'] }] }] }));
      } else {
        assert.equal(req.method, 'PUT');
        assert.equal(req.url, '/v1/apps/billing/automations/tests/tests');
        res.end(JSON.stringify({ name: 'tests', version: 8, test_suite: suite }));
      }
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const saved = await WorkflowsService.saveAutomationTestSuite({ slug: 'billing', name: 'tests', requestBody: { expected_version: 7, suite } });
  assert.deepEqual(saved.test_suite, suite);
  const report = await WorkflowsService.runAutomationTests({ slug: 'billing', name: 'tests', requestBody: { expected_version: 8 } });
  const step = report.cases[0]?.steps[0];
  assert.ok(step);
  assert.deepEqual(step.mismatches, ['output', 'attempt_count']);
  await WorkflowsService.saveAutomationTestSuite({ slug: 'billing', name: 'tests', requestBody: { expected_version: 8, suite: null } });
  assert.deepEqual(requests, [{ expected_version: 7, suite }, { expected_version: 8 }, { expected_version: 8, suite: null }]);
});
