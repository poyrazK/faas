import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('workflow continuation sends revision zero and preserves typed history and retry budget', async t => {
  let posted: unknown;
  let key = '';
  const server = createServer((req, res) => {
    void (async () => {
      res.setHeader('content-type', 'application/json');
      if (req.url?.endsWith('/resume')) {
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(Buffer.from(chunk));
        posted = JSON.parse(Buffer.concat(chunks).toString());
        key = String(req.headers['idempotency-key'] ?? '');
        res.end(JSON.stringify({ id: 'run', app_id: 'app', deployment_id: 'deployment-started-with', workflow_name: 'batch', status: 'pending', resume_count: 1, scheduled_for: '2026-10-03T12:00:00Z', created_at: '2026-10-03T12:00:00Z', updated_at: '2026-10-03T12:00:00Z' }));
      } else if (req.url?.endsWith('/resumes')) {
        res.end(JSON.stringify({ resumes: [{ run_id: 'run', resume_number: 1, account_id: 'account', previous_status: 'dead', resumed_steps: ['send'], created_at: '2026-10-03T12:00:00Z' }] }));
      } else {
        res.end(JSON.stringify({ steps: [{ step_name: 'send', status: 'running', attempt: 4, retry_base: 3, created_at: '2026-10-03T12:00:00Z' }] }));
      }
    })().catch(() => { res.statusCode = 500; res.end('{}'); });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const resumed = await WorkflowsService.resumeWorkflowRun({ id: 'run', requestBody: { expected_resume_count: 0 } });
  assert.deepEqual(posted, { expected_resume_count: 0 });
  assert.ok(key);
  assert.equal(resumed.resume_count, 1);
  assert.equal(resumed.deployment_id, 'deployment-started-with');
  const history = await WorkflowsService.listWorkflowResumes({ id: 'run' });
  assert.deepEqual(history.resumes[0]?.resumed_steps, ['send']);
  assert.equal(history.resumes[0]?.previous_status, 'dead');
  const steps = await WorkflowsService.listWorkflowSteps({ id: 'run' });
  assert.equal(steps.steps[0]?.retry_base, 3);
  assert.equal(steps.steps[0]?.attempt, 4);
});
