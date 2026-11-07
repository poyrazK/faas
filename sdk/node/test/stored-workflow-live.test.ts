import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createServer } from 'node:http';
import test from 'node:test';
import { FaaSClient, WorkflowsService } from '../src/preview.js';

test('suite comparison is an authenticated read with explicit candidate and baseline identities', async t => {
  const calls: string[] = [];
  const view = { baseline: { id: 'baseline' }, candidate: { id: 'candidate' }, baseline_outdated: true, candidate_outdated: false, summary: { regressions: 1, unknown: 1 }, cases: [{ name: 'same-case', change: 'regression', baseline: { result: 'passed' }, candidate: { result: 'failed', created_at: '2026-10-15T09:00:00Z' } }] };
  const server = createServer((req, res) => {
    calls.push(req.method + ' ' + req.url);
    assert.equal(req.headers.authorization, 'Bearer token');
    assert.equal(req.headers['content-length'], undefined);
    res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(view));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await WorkflowsService.compareAutomationLiveSuites({ slug: 'billing', name: 'case/report', suiteId: 'candidate', baselineId: 'baseline' }), view);
  assert.deepEqual(calls, ['GET /v1/apps/billing/automations/case%2Freport/live-suites/candidate/compare/baseline']);
});

test('managed suites preserve route identity and require explicit continuation authorization', async t => {
  const suiteId = '0199e820-0000-7000-8000-000000000010';
  const request = { expected_version: 7, expected_published_version: 4, expected_deployment_id: '00000000-0000-4000-8000-000000000002', cases: ['first', 'second'], confirm_real_actions: true };
  const view = { id: suiteId, managed: true, outcome: 'pending' };
  const calls: string[] = [];
  const server = createServer((req, res) => {
    void (async () => {
      calls.push(req.method + ' ' + req.url);
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), calls.length === 1 ? request : { confirm_real_actions: true });
      res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(view));
    })().catch(error => { res.statusCode = 500; res.end(JSON.stringify({ error: String(error) })); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await WorkflowsService.startAutomationLiveSuite({ slug: 'billing', name: 'case/report', suiteId, requestBody: request }), view);
  const continued = await WorkflowsService.advanceAutomationLiveSuite({ slug: 'billing', name: 'case/report', suiteId, requestBody: { confirm_real_actions: true } });
  assert.equal(continued.managed, true);
  assert.deepEqual(continued, view);
  const root = '/v1/apps/billing/automations/case%2Freport/live-suites/' + suiteId;
  assert.deepEqual(calls, ['POST ' + root + '/start', 'POST ' + root + '/advance']);
});

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

test('named baseline promotion preserves explicit revision and policy; head and history are reads', async t => {
  const calls: string[] = [];
  const policy = { version: 2 as const, required_cases: ['happy'], max_candidate_age_seconds: 3600 };
  const body = { suite_id: '00000000-0000-4000-8000-000000000010', expected_revision: 0, expected_version: 7, expected_published_version: 4, expected_deployment_id: '00000000-0000-4000-8000-000000000002', policy };
  const promotionId = '00000000-0000-4000-8000-000000000020';
  const receipt = { promotion_id: promotionId, baseline_name: 'production', revision: 1, policy, policy_hash: 'reviewed-policy' };
  const server = createServer((req, res) => {
    void (async () => {
      calls.push(req.method + ' ' + req.url);
      assert.equal(req.headers.authorization, 'Bearer token');
      if (req.method === 'PUT') {
        const chunks: Buffer[] = [];
        for await (const chunk of req) chunks.push(Buffer.from(chunk));
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), body);
      } else assert.equal(req.headers['content-length'], undefined);
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify(req.url?.endsWith('/history') ? { promotions: [receipt] } : receipt));
    })().catch(error => { res.destroy(error); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await WorkflowsService.promoteAutomationLiveBaseline({ slug: 'billing', name: 'case/report', baseline: 'production', promotionId, requestBody: body }), receipt);
  assert.deepEqual(await WorkflowsService.getAutomationLiveBaseline({ slug: 'billing', name: 'case/report', baseline: 'production' }), receipt);
  assert.deepEqual(await WorkflowsService.getAutomationLiveBaselineHistory({ slug: 'billing', name: 'case/report', baseline: 'production' }), { promotions: [receipt] });
  const root = '/v1/apps/billing/automations/case%2Freport/live-baselines/production';
  assert.deepEqual(calls, [`PUT ${root}/promotions/${promotionId}`, `GET ${root}`, `GET ${root}/history`]);
});

test('baseline discovery is one scoped read and preserves evidence health', async t => {
  const result = { baselines: [{ baseline: { baseline_name: 'production', revision: 2 }, evidence_status: 'usable', renewal_status: 'inconclusive', outdated: true, reasons: ['candidate_outdated'], cases: [{ name: 'happy', run_id: 'retained-run', status: 'passed' }], age_seconds: 3601, max_age_seconds: 3600 }], observed_at: '2026-10-16T12:00:00Z' };
  let calls = 0;
  const server = createServer((req, res) => {
    calls++;
    assert.equal(req.method, 'GET');
    assert.equal(req.url, '/v1/apps/billing/automations/case%2Freport/live-baselines');
    assert.equal(req.headers.authorization, 'Bearer token');
    assert.equal(req.headers['content-length'], undefined);
    res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(result));
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  assert.deepEqual(await WorkflowsService.listAutomationLiveBaselines({ slug: 'billing', name: 'case/report' }), result);
  assert.equal(calls, 1);
});

test('baseline retirement preserves explicit revision, stable identity and audit history', async t => {
  const retirementId = '00000000-0000-4000-8000-000000000014';
  const receipt = { retirement_id: retirementId, app_id: '00000000-0000-4000-8000-000000000001', name: 'case/report', baseline_name: 'production', revision: 4, previous_promotion_id: '00000000-0000-4000-8000-000000000013', actor_account_id: '00000000-0000-4000-8000-000000000002', retired_at: '2026-10-16T12:00:00Z' };
  const calls: string[] = [];
  const server = createServer((req, res) => {
    void (async () => {
      calls.push(`${req.method} ${req.url}`);
      assert.equal(req.headers.authorization, 'Bearer token');
      if (req.method === 'PUT') {
        const chunks: Buffer[] = []; for await (const chunk of req) chunks.push(Buffer.from(chunk));
        assert.deepEqual(JSON.parse(Buffer.concat(chunks).toString()), { expected_revision: 3 });
      }
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify(req.method === 'PUT' ? receipt : { promotions: [], retirement: receipt }));
    })().catch(error => { res.destroy(error); });
  });
  server.listen(0, '127.0.0.1'); await once(server, 'listening');
  t.after(() => new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())));
  const address = server.address(); assert.ok(address && typeof address !== 'string');
  new FaaSClient(`http://127.0.0.1:${address.port}`, { token: 'token', retry: { maxAttempts: 1, backoffMs: 0 } });
  for (let attempt = 0; attempt < 2; attempt++) assert.deepEqual(await WorkflowsService.retireAutomationLiveBaseline({ slug: 'billing', name: 'case/report', baseline: 'production', retirementId, requestBody: { expected_revision: 3 } }), receipt);
  assert.deepEqual(await WorkflowsService.getAutomationLiveBaselineHistory({ slug: 'billing', name: 'case/report', baseline: 'production' }), { promotions: [], retirement: receipt });
  const root = '/v1/apps/billing/automations/case%2Freport/live-baselines/production';
  assert.deepEqual(calls, [`PUT ${root}/retirements/${retirementId}`, `PUT ${root}/retirements/${retirementId}`, `GET ${root}/history`]);
});
