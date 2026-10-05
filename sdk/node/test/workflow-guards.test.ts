import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { once } from 'node:events';

import { FaaSClient, WorkflowsService, type WorkflowSpec } from '../src/index.js';

test('workflow guards preserve nested literals and false inspection decisions', async (t) => {
  const definition: WorkflowSpec = {
    name: 'invoice',
    steps: [{ name: 'send', run: 'send', when: { all: [
      { ref: 'input.amount', op: 'gt', value: 100 },
      { any: [
        { ref: 'input.active', op: 'eq', value: true },
        { not: { ref: 'input.deleted', op: 'eq', value: null } },
      ] },
    ] } }],
  };
  let posted: unknown;
  const server = createServer((req, res) => {
    void (async () => {
      res.setHeader('Content-Type', 'application/json');
      if (req.method === 'PUT') {
        let body = '';
        for await (const chunk of req) body += String(chunk);
        posted = JSON.parse(body);
        res.end(JSON.stringify({ name: 'invoice', version: 1, source: 'draft', draft: definition, enabled: true }));
      } else {
        res.end(JSON.stringify({ steps: [{
          step_name: 'send', status: 'skipped', attempt: 0,
          created_at: '2026-10-03T12:00:00Z', when_matched: false,
          when_evaluated_at: '2026-10-03T12:01:00Z', skip_reason: 'when_false',
        }] }));
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
  assert.deepEqual(saved.draft.steps[0]?.when, definition.steps[0]?.when);
  const inspected = await WorkflowsService.listWorkflowSteps({ id: 'run' });
  assert.equal(inspected.steps[0]?.when_matched, false);
  assert.equal(inspected.steps[0]?.skip_reason, 'when_false');
  assert.equal(inspected.steps[0]?.attempt, 0);
});
