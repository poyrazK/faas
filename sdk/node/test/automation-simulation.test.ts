import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('simulation SDK preserves null mocks, loop order and false decisions', async t => {
  const requestBody = {
    definition: { name: 'sample', steps: [{ name: 'a', run: 'a' }] },
    input: { active: false }, mock_outputs: { a: null }, mock_item_outputs: { batch: [false, null] },
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
      res.end(JSON.stringify({ definition_valid: true, definition_hash: 'hash', complete: false, issues: [], warnings: [], step_order: ['a'], trace: [{ step_name: 'a', kind: 'run', state: 'mocked', output: null, when_matched: false }] }));
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
  assert.equal(row.when_matched, false);
});
