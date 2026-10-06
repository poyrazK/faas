import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

import { FaaSClient, WorkflowsService, type WorkflowSpec } from '../src/index.js';

test('iteration preserves action mappings, ordered output and zero inspection indexes', async (t) => {
  const definition: WorkflowSpec = {
    name: 'batch',
    steps: [{ name: 'send', for_each: { items: 'input.items', max_parallel: 4, on_item_failure: 'continue', action: {
      outbound: { integration_id: '00000000-0000-0000-0000-000000000001', method: 'POST', path: '/send', idempotency_supported: true },
      input: { item: '{{input.item}}', index: '{{input.index}}', invoice: '{{input.input.invoice}}' },
      timeout: '30s', retry: { max_attempts: 3, backoff: 'exponential' },
      when: { ref: 'input.item.active', op: 'eq', value: true },
    } } }],
  };
  const output = [{ status: 200, body: { items: [true, null] } }, { status: 200, body: '{{input.secret}}' }];
  let posted: unknown;
  const server = createServer((req, res) => {
    void (async () => {
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'PUT') {
        let body = '';
        for await (const chunk of req) body += String(chunk);
        posted = JSON.parse(body);
        res.end(JSON.stringify({ name: 'batch', version: 1, source: 'draft', draft: definition, enabled: true }));
      } else {
        res.end(JSON.stringify({ steps: [
          { step_name: 'send', status: 'succeeded', attempt: 0, for_each_count: 2, output, created_at: '2026-10-03T12:00:00Z' },
          { step_name: '_foreach.c2VuZA.0', status: 'succeeded', attempt: 1, for_each_parent: 'send', for_each_index: 0, output: output[0], created_at: '2026-10-03T12:00:00Z' },
          { step_name: 'empty', status: 'succeeded', attempt: 0, for_each_count: 0, output: [], created_at: '2026-10-03T12:00:00Z' },
        ] }));
      }
    })().catch(() => { res.statusCode = 500; res.end('{}'); });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const saved = await WorkflowsService.saveAutomationDraft({ slug: 'billing', name: 'batch', requestBody: { expected_version: 0, definition } });
  assert.deepEqual(posted, { expected_version: 0, definition });
  assert.deepEqual(saved.draft, definition);
  const inspected = await WorkflowsService.listWorkflowSteps({ id: 'run' });
  assert.deepEqual(inspected.steps[0]?.output, output);
  assert.equal(inspected.steps[1]?.for_each_index, 0);
  assert.equal(inspected.steps[1]?.for_each_parent, 'send');
  assert.equal(inspected.steps[2]?.for_each_count, 0);
  assert.deepEqual(inspected.steps[2]?.output, []);
});
