import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

import { FaaSClient, WorkflowsService, type WorkflowSpec } from '../src/index.js';

test('branch joins preserve selection order and inspected source and value', async (t) => {
  const definition: WorkflowSpec = {
    name: 'invoice',
    steps: [
      { name: 'reminder', run: 'reminder' },
      { name: 'receipt', run: 'receipt' },
      { name: 'merge', depends_on: ['reminder', 'receipt'], join: { output_from: ['receipt', 'reminder'] } },
      { name: 'crm', run: 'crm', depends_on: ['merge'], input: '{{steps.merge.output.value}}' },
    ],
  };
  let posted: unknown;
  const output = { source: 'receipt', value: { done: true, items: [null, 12] } };
  const server = createServer((req, res) => {
    void (async () => {
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'PUT') {
        let body = '';
        for await (const chunk of req) body += String(chunk);
        posted = JSON.parse(body);
        res.end(JSON.stringify({ name: 'invoice', version: 1, source: 'draft', draft: definition, enabled: true }));
      } else {
        res.end(JSON.stringify({ steps: [{ step_name: 'merge', status: 'succeeded', attempt: 0, output, created_at: '2026-10-03T12:00:00Z' }] }));
      }
    })().catch(() => { res.statusCode = 500; res.end('{}'); });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const saved = await WorkflowsService.saveAutomationDraft({ slug: 'billing', name: 'invoice', requestBody: { expected_version: 0, definition } });
  assert.deepEqual(posted, { expected_version: 0, definition });
  assert.deepEqual(saved.draft.steps[2]?.join?.output_from, ['receipt', 'reminder']);
  const inspected = await WorkflowsService.listWorkflowSteps({ id: 'run' });
  assert.deepEqual(inspected.steps[0]?.output, output);
  assert.equal(inspected.steps[0]?.attempt, 0);
});
