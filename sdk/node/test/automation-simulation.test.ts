import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';
import type { SimulateAutomationRequest } from '../src/generated/models/SimulateAutomationRequest.js';

test('simulation SDK preserves null mocks, loop order and false decisions', async t => {
  const requestBody: SimulateAutomationRequest = {
    definition: { name: 'sample', steps: [{ name: 'a', run: 'a' }, { name: 'b', run: 'b' }, { name: 'approval', wait_for_callback: true }] },
    input: { active: false }, mock_outputs: { a: null }, mock_item_outputs: { batch: [false, null] },
    mock_item_attempts: { retry_batch: { '0': [{ outcome: 'timeout' }, { outcome: 'success', output: null }], '2': [{ outcome: 'failure', http_status: 400 }] } },
    mock_attempts: { approval: [{ outcome: 'success', output: { approved_by: 'reviewer-42' } }], b: [{ outcome: 'failure', http_status: 503 }, { outcome: 'success', output: null }] },
  };
  const server = createServer((req, res) => {
    void (async () => {
      assert.equal(req.method, 'POST');
      assert.equal(req.url, '/v1/apps/billing/automations:simulate');
      assert.equal(req.headers.authorization, 'Bearer token');
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), requestBody);
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ definition_valid: true, definition_hash: 'hash', complete: false, issues: [], warnings: [], step_order: ['b'], trace: [{ step_name: 'b', kind: 'run', state: 'mocked', path: '/contacts/contact%2042', raw_query: 'email=a%2Bb%40example.com', output: null, when_matched: false, attempts: [{ attempt: 1, outcome: 'failure', http_status: 503 }, { attempt: 2, outcome: 'success' }] }, { step_name: 'approval', kind: 'callback_wait', state: 'mocked', reason: 'callback_received_mocked', output: { approved_by: 'reviewer-42' }, attempts: [{ attempt: 1, outcome: 'success' }] }] }));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const response = await WorkflowsService.simulateAutomation({ slug: 'billing', requestBody });
  assert.equal(response.definition_valid, true);
  assert.equal(response.complete, false);
  const row = response.trace[0]; assert.ok(row);
  assert.equal(row.output, null);
  assert.equal(row.path, '/contacts/contact%2042');
  assert.equal(row.raw_query, 'email=a%2Bb%40example.com');
  assert.equal(row.when_matched, false);
  assert.equal(row.attempts?.length, 2);
  assert.equal(row.attempts?.[0]?.http_status, 503);
  assert.equal(response.trace[1]?.reason, 'callback_received_mocked');
  assert.deepEqual(response.trace[1]?.output, { approved_by: 'reviewer-42' });
});
