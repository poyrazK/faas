import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/index.js';

test('live verification APIs retain target fences and a stable explicit request key', async t => {
  const request = { expected_version: 7, expected_published_version: 6, expected_deployment_id: 'compact-deployment-id', case_name: 'synthetic', confirm_real_actions: true };
  const view = { run_id: '018e7310-2222-4333-8444-555555555555', case_name: 'synthetic', deployment_id: 'compact-deployment-id', inputs_hash: 'a'.repeat(64), run_status: 'pending', version: 7, published_version: 6, created_at: '2026-10-15T10:00:00Z', outdated: false, report_current: false };
  const received: string[] = [];
  const server = createServer((req, res) => {
    void (async () => {
      received.push(req.method + ' ' + req.url);
      res.setHeader('content-type', 'application/json');
      if (req.method === 'POST' && !req.url?.endsWith('/check')) {
        assert.equal(req.headers['idempotency-key'], 'stable-live-key');
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(Buffer.from(chunk));
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), request);
        res.statusCode = 201;
      }
      if (received.length === 2) res.end(JSON.stringify({ published_version: 6, deployment_id: view.deployment_id, verifications: [view] }));
      else res.end(JSON.stringify(view));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await WorkflowsService.startAutomationLiveVerification({ slug: 'billing', name: 'report', idempotencyKey: 'stable-live-key', requestBody: request }), view);
  const listing = await WorkflowsService.listAutomationLiveVerifications({ slug: 'billing', name: 'report' });
  assert.deepEqual(listing.verifications, [view]);
  assert.deepEqual(await WorkflowsService.getAutomationLiveVerification({ slug: 'billing', name: 'report', runId: view.run_id }), view);
  assert.deepEqual(await WorkflowsService.checkAutomationLiveVerification({ slug: 'billing', name: 'report', runId: view.run_id }), view);
  const root = '/v1/apps/billing/automations/report/live-verifications';
  assert.deepEqual(received, ['POST ' + root, 'GET ' + root, 'GET ' + root + '/' + view.run_id, 'POST ' + root + '/' + view.run_id + '/check']);
});

test('shared live suites save only frozen metadata and read retained history', async t => {
  const suiteId = '00000000-0000-4000-8000-000000000010';
  const runId = '00000000-0000-4000-8000-000000000011';
  const deploymentId = '00000000-0000-4000-8000-000000000002';
  const request = { expected_version: 7, expected_published_version: 4, expected_deployment_id: deploymentId, cases: [{ name: 'first', run_id: runId }, { name: 'second' }] };
  const view = { id: suiteId, name: 'report', version: 7, published_version: 4, deployment_id: deploymentId, created_at: '2026-10-15T10:00:00Z', updated_at: '2026-10-15T10:00:00Z', case_count: 2, linked_runs: 1, outcome: 'pending', outdated: false, observed_at: '2026-10-15T10:00:00Z', summary: { selected: 2, passed: 0, failed: 0, pending: 1, not_started: 1, unavailable: 0, outdated: 0 }, cases: [{ name: 'first', run_id: runId, outcome: 'pending' }, { name: 'second', outcome: 'not_started' }] };
  const root = '/v1/apps/billing/automations/report/live-suites';
  const received: string[] = [];
  const server = createServer((req, res) => {
    void (async () => {
      received.push(req.method + ' ' + req.url);
      res.setHeader('content-type', 'application/json');
      if (req.method === 'PUT') {
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(Buffer.from(chunk));
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), request);
      }
      res.end(JSON.stringify(req.url === root ? { suites: [view] } : view));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await WorkflowsService.saveAutomationLiveSuite({ slug: 'billing', name: 'report', suiteId, requestBody: request }), view);
  assert.deepEqual(await WorkflowsService.getAutomationLiveSuite({ slug: 'billing', name: 'report', suiteId }), view);
  assert.equal((await WorkflowsService.listAutomationLiveSuites({ slug: 'billing', name: 'report' })).suites.length, 1);
  assert.deepEqual(received, ['PUT ' + root + '/' + suiteId, 'GET ' + root + '/' + suiteId, 'GET ' + root]);
});
