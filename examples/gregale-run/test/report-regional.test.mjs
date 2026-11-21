import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { createRequire } from 'node:module';
import test from 'node:test';
import { createReportIntake } from '../agent/report-intake.ts';

const dsn = process.env.REPORT_INTAKE_TEST_DATABASE_URL;

test('regional reports resume private progress and rerun only approved branches', { skip: !dsn }, async t => {
  const require = createRequire(new URL('../../../cmd/gregale/templates/agent-stateless-node/package.json', import.meta.url));
  const { Client, Pool } = require('pg');
  const schema = `report_regional_${randomUUID().replaceAll('-', '')}`;
  const admin = new Client({ connectionString: dsn });
  await admin.connect();
  await admin.query(`CREATE SCHEMA ${schema}`);
  const privateDSN = new URL(dsn);
  privateDSN.searchParams.set('options', `-c search_path=${schema}`);
  const db = new Pool({ connectionString: privateDSN.toString() });
  t.after(async () => {
    await db.end();
    try { await admin.query(`DROP SCHEMA ${schema} CASCADE`); }
    finally { await admin.end(); }
  });
  await db.query(await readFile(new URL('../agent/hosted-schema.sql', import.meta.url), 'utf8'));

  const owner = { requestID: randomUUID(), customerID: 'company-one' };
  const other = { ...owner, customerID: 'company-two' };
  const childKey = { ...owner, requestID: randomUUID() };
  const orders = JSON.parse(await readFile(new URL('../sales-report/rows.json', import.meta.url), 'utf8')).rows;
  const [bid, cid, northRun, southRun, mergeRun, deliveryID, endpointID] = Array.from({ length: 7 }, () => randomUUID());
  const route = '/v1/apps/pilot/run-artifact-deliveries/reports';
  const signed = { url: 'https://storage.example.test/private', method: 'GET', headers: {}, expires_at: '2026-11-20T00:00:00Z' };
  const summary = { currency: 'USD', orders: 4, units: 8, revenue_cents: 6996 };
  const secret = 'PRIVATE TOOL LOG';
  const base = { id: bid, status: 'queued', pipeline: { progress: { status: 'running' } } };
  const child = { id: cid, status: 'queued', pipeline: { rerun: { parent_id: bid } } };
  let original, rerun, deliveryIntent;
  let requests = 0, parentPosts = 0, childPosts = 0, deliveryPosts = 0, downloads = 0;
  const fetch = async (url, { method, headers, body, redirect }) => {
    requests++;
    assert.equal(headers.get('authorization'), 'Bearer account-key');
    assert.equal(redirect, 'error');
    assert.equal(url.origin, 'https://api.example.test');
    if (url.pathname === '/v2/execution-workflows') {
      assert.equal(method, 'POST');
      const manifest = JSON.parse(body);
      assert.equal(manifest.failure_policy, 'continue_independent');
      assert.deepEqual(manifest.steps.map(step => [step.label, step.task.version]), [['north', 1], ['south', 1], ['merge', 1]]);
      assert.ok(!body.includes(owner.customerID));
      parentPosts++;
      if (original) assert.deepEqual(manifest, original);
      else {
        original = manifest;
        base.workflow_id = manifest.workflow_id;
        throw new Error('lost original acknowledgement');
      }
      return Response.json(base, { status: 202 });
    }
    if (url.pathname === `/v2/execution-pipelines/${bid}/reruns`) {
      assert.equal(method, 'POST');
      const intent = JSON.parse(body);
      childPosts++;
      assert.deepEqual(intent.original, original);
      assert.deepEqual(intent.repeat_steps, ['south']);
      if (childPosts === 1) return Response.json({ detail: secret }, { status: 409 });
      if (rerun) assert.deepEqual(intent, rerun);
      else {
        rerun = intent;
        child.workflow_id = intent.workflow_id;
        assert.notEqual(child.workflow_id, base.workflow_id);
        throw new Error('lost child acknowledgement');
      }
      return Response.json(child, { status: 202 });
    }
    if (url.pathname.startsWith('/v1/execution-workflows/')) {
      assert.equal(method, 'GET');
      const workflow = url.pathname.split('/').at(-1);
      const row = [base, child].find(item => item.workflow_id === workflow);
      assert.ok(row);
      return Response.json({ workflow_id: workflow, managed: [row] });
    }
    if (url.pathname === `/v1/execution-pipelines/${bid}/events`) {
      assert.equal(method, 'GET');
      const after = url.searchParams.get('after');
      assert.ok(['0', '5'].includes(after));
      const step = { label: 'south', state: 'running', attempt: 1, run_id: southRun, events_url: '/private', stdout: secret };
      return Response.json({ pipeline_id: bid, next_sequence: 5, terminal: false, gap: after === '0',
        events: after === '0' ? [{ sequence: 5, type: 'step', step, raw: secret }] : [],
        progress: { status: 'running', steps: [step] } });
    }
    if (url.pathname === `/v1/execution-pipelines/${cid}/events`) {
      const step = { label: 'north', state: 'reused', run_id: northRun, reason: secret };
      return Response.json({ pipeline_id: cid, next_sequence: 1, terminal: false,
        events: [{ sequence: 1, type: 'step', step }], progress: { status: 'queued', steps: [step] } });
    }
    if (url.pathname === `/v1/executions/${mergeRun}`) {
      assert.equal(method, 'GET');
      return Response.json({ id: mergeRun, status: 'succeeded', result: { ...summary, source_reports: 2, secret },
        artifacts: [{ name: 'combined.html', size_bytes: 391, sha256: `sha256:${'a'.repeat(64)}` }] });
    }
    if (url.pathname === route) {
      assert.equal(method, 'POST');
      const intent = JSON.parse(body);
      assert.equal(intent.idempotency_key, `${cid}:combined.html`);
      assert.equal(intent.execution_id, mergeRun);
      if (deliveryIntent) assert.deepEqual(intent, deliveryIntent);
      deliveryIntent = intent;
      deliveryPosts++;
      return Response.json({ id: deliveryID, status: deliveryPosts === 1 ? 'pending' : 'completed',
        receipt_url: `${route}/${deliveryID}`, ...(deliveryPosts > 1 ? { object: { key: 'private' } } : {}) });
    }
    if (url.pathname === `${route}/${deliveryID}/download-url`) {
      assert.equal(method, 'POST');
      downloads++;
      return Response.json(signed);
    }
    assert.fail(`unexpected ${method} ${url.pathname}`);
  };
  const start = options => createReportIntake({ db, apiURL: 'https://api.example.test', token: 'account-key',
    app: 'pilot', endpointID, uploadRoute: 'reports', fetch, ...options });

  await assert.rejects(start().submitRegional(owner, orders), /lost original acknowledgement/);
  assert.deepEqual(await start().status(owner), { requestID: owner.requestID, status: 'staging' });
  assert.equal((await start().submitRegional(owner, orders)).status, 'running');
  await assert.rejects(start().submitRegional(owner, orders.map(row => ({ ...row, quantity: 1 }))), /different work/);
  await assert.rejects(start().submit(owner, new Blob(['csv'])), /different CSV bytes/);
  const first = await start().progress(owner);
  assert.deepEqual(first.pipeline, { nextSequence: 5, gap: true, terminal: false,
    events: [{ sequence: 5, type: 'step', step: { label: 'south', state: 'running', attempt: 1 } }],
    snapshot: { status: 'running', steps: [{ label: 'south', state: 'running', attempt: 1 }] } });
  assert.deepEqual((await start().progress(owner, first.pipeline.nextSequence)).pipeline.events, []);
  await assert.rejects(start().progress(owner, -1), /progress cursor/);
  await assert.rejects(start().rerun(owner, { requestID: childKey.requestID, repeatSteps: [] }), /Only a failed/);

  base.status = 'failed';
  base.pipeline.completion = { completion_id: bid, workflow_id: base.workflow_id, outcome: 'failed', steps: [
    { label: 'north', outcome: 'succeeded', attempts: [{ run_id: northRun }] },
    { label: 'south', outcome: 'failed', attempts: [{ run_id: southRun }] },
    { label: 'merge', outcome: 'blocked', attempts: [] },
  ] };
  assert.deepEqual((await start().status(owner)).recovery, { runSteps: ['south', 'merge'], repeatSteps: ['south'], reuseSteps: ['north'] });
  for (const repeatSteps of [[], ['north', 'south'], ['south', 'south']]) {
    await assert.rejects(start().rerun(owner, { requestID: childKey.requestID, repeatSteps }), /approve|exact/i);
  }
  const beforeForeign = requests;
  const intent = { requestID: childKey.requestID, repeatSteps: ['south'] };
  assert.equal(await start().progress(other), null);
  assert.equal(await start().rerun(other, intent), null);
  assert.equal(await start().download(other), null);
  assert.equal(requests, beforeForeign);
  assert.equal(childPosts, 0);

  assert.deepEqual(await start().rerun(owner, intent), { requestID: childKey.requestID, status: 'rerun_unavailable' });
  assert.equal((await start().status(childKey)).status, 'rerun_unavailable');
  await assert.rejects(start().rerun(owner, intent), /lost child acknowledgement/);
  assert.equal((await start().status(owner)).recovery.childRequestID, childKey.requestID);
  await assert.rejects(start().rerun(owner, { ...intent, requestID: randomUUID() }), /already has a rerun/);
  await assert.rejects(start().rerun(owner, { ...intent, repeatSteps: ['merge'] }), /different work/);
  assert.equal((await start().rerun(owner, intent)).status, 'queued');
  assert.equal((await start().rerun(owner, intent)).requestID, childKey.requestID);
  assert.deepEqual((await start().progress(childKey)).pipeline.snapshot.steps, [{ label: 'north', state: 'reused' }]);

  child.status = 'succeeded';
  child.pipeline.completion = { completion_id: cid, workflow_id: child.workflow_id, outcome: 'succeeded', steps: [
    { label: 'north', outcome: 'succeeded', reused_run_id: northRun, attempts: [] },
    { label: 'south', outcome: 'succeeded', attempts: [{ run_id: randomUUID() }] },
    { label: 'merge', outcome: 'succeeded', attempts: [{ run_id: mergeRun }] },
  ] };
  assert.equal((await start().status(childKey)).status, 'delivering');
  assert.deepEqual(await start({ completionWebhookSecret: 'new-receiver' }).status(childKey),
    { requestID: childKey.requestID, status: 'succeeded', summary });
  const beforeSign = requests;
  assert.deepEqual(await start().download(childKey), signed);
  assert.equal(await start().download({ ...other, requestID: childKey.requestID }), null);
  assert.equal(requests, beforeSign + 1);
  assert.deepEqual(await start().status(owner), { requestID: owner.requestID, status: 'failed', outcome: 'failed',
    recovery: { runSteps: ['south', 'merge'], repeatSteps: ['south'], reuseSteps: ['north'], childRequestID: childKey.requestID } });
  assert.deepEqual([parentPosts, childPosts, deliveryPosts, downloads], [2, 3, 2, 1]);
  const { rows } = await db.query('SELECT count(*)::int AS count FROM report_requests');
  assert.equal(rows[0].count, 2);
});
