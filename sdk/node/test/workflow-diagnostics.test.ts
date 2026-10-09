import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService, type WorkflowRunDiagnosticsResponse } from '../src/index.js';

test('workflow diagnostics use read-only account and tenant routes with typed recovery blockers', async t => {
  const paths: string[] = [];
  const payload: WorkflowRunDiagnosticsResponse = {
    run_id: 'run', workflow_name: 'invoice', status: 'dead', observed_at: '2026-10-07T16:00:00Z',
    deployment_id: 'original-code', legacy_unpinned: false, state_reason: 'dead', due_age_seconds: 0,
    stale_lease: false, steps: [{ step_name: 'charge', kind: 'action', status: 'dead', attempt: 1, retry_base: 0 }],
    resume: { eligible: false, expected_resume_count: 0, reopened_steps: [], preserved_steps: ['charge'],
      blockers: [{ code: 'unsafe_mutation', message: 'The attempted external mutation cannot be repeated.', step_name: 'charge' }] },
  };
  const server = createServer((req, res) => {
    assert.equal(req.method, 'GET');
    assert.equal(req.headers.authorization, 'Bearer fixture-token');
    assert.equal(req.headers['idempotency-key'], undefined);
    paths.push(req.url ?? '');
    res.setHeader('content-type', 'application/json');
    res.setHeader('cache-control', 'no-store');
    res.end(JSON.stringify(payload));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address();
  assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'fixture-token', retry: { maxAttempts: 1, backoffMs: 0 } });
  const account = await WorkflowsService.getWorkflowRunDiagnostics({ id: 'run' });
  const tenant = await WorkflowsService.getPlatformTenantSelfWorkflowRunDiagnostics({ id: 'run' });
  assert.deepEqual(account, payload);
  assert.deepEqual(tenant, payload);
  assert.deepEqual(paths, ['/v1/workflows/runs/run/diagnostics', '/v1/platform-tenant-self/workflows/runs/run/diagnostics']);
  assert.equal(account.resume.expected_resume_count, 0);
  assert.equal(account.resume.blockers[0]?.code, 'unsafe_mutation');
});
