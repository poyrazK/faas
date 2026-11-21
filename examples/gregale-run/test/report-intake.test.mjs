import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { agentSchema } from '../../../cmd/gregale/templates/agent-stateless-node/schema.js';
import { createReportAgent } from '../agent/agent.mjs';
import { createHostedReportHandler } from '../agent/trigger.mjs';

const dsn = process.env.REPORT_INTAKE_TEST_DATABASE_URL;

test('report intake recovers lost publication and isolates customer downloads in PostgreSQL', { skip: !dsn }, async t => {
  const { createReportIntake } = await import('../agent/report-intake.ts');
  const require = createRequire(new URL('../../../cmd/gregale/templates/agent-stateless-node/package.json', import.meta.url));
  const { Client } = require('pg');
  const db = new Client({ connectionString: dsn });
  await db.connect();
  const schema = `report_intake_${randomUUID().replaceAll('-', '')}`;
  t.after(async () => {
    try { await db.query(`DROP SCHEMA ${schema} CASCADE`); }
    finally { await db.end(); }
  });
  await db.query(`CREATE SCHEMA ${schema}`);
  await db.query(`SET search_path=${schema}`);
  await db.query(agentSchema);
  await db.query(await readFile(new URL('../agent/hosted-schema.sql', import.meta.url), 'utf8'));

  const endpointID = randomUUID(), fileID = randomUUID(), deliveryID = randomUUID();
  const key = { requestID: randomUUID(), customerID: 'customer-one' };
  const deliveryPath = `/v1/apps/report-agent/run-artifact-deliveries/reports/${deliveryID}`;
  const csv = new Blob(['order_id,region,quantity,unit_price_cents\n1,north,2,900\n']);
  const grantToken = 'agf1.private-test-token';
  const grantExpiresAt = new Date(Date.now() + 300_000).toISOString();
  let prepareCount = 0, putCount = 0, grantCount = 0, publicationAttempts = 0, signCount = 0;
  let lostAcknowledgement = true;
  const published = new Map();
  const fetch = async (url, request) => {
    assert.equal(request.headers.get('authorization'), 'Bearer backend');
    if (url.pathname === '/v1/execution-files') {
      prepareCount++;
      assert.equal(JSON.parse(request.body).size_bytes, csv.size);
      return Response.json({ id: fileID, status: 'missing' });
    }
    if (url.pathname === `/v1/execution-files/${fileID}/content`) {
      putCount++;
      assert.equal(request.method, 'PUT');
      assert.match(request.headers.get('x-content-sha256'), /^sha256:[a-f0-9]{64}$/);
      assert.equal(await request.body.text(), await csv.text());
      return Response.json({ id: fileID, status: 'ready' });
    }
    if (url.pathname === `/v1/execution-files/${fileID}/agent-grants`) {
      grantCount++;
      const grant = JSON.parse(request.body);
      assert.equal(grant.endpoint_id, endpointID);
      assert.equal(grant.step_label, 'csv-report');
      assert.equal(grant.path, 'orders.csv');
      assert.equal(grant.task_name, 'gr-demo-sales');
      assert.equal(grant.tool_call_id, `assistant:${grant.thread_id}`);
      return Response.json({ grant_token: grantToken, expires_at: grantExpiresAt });
    }
    if (url.pathname.endsWith('/agent-inbox/retained-messages')) {
      publicationAttempts++;
      const message = JSON.parse(request.body);
      assert.equal(message.idempotency_key, message.metadata.message_id);
      assert.equal(message.metadata.thread_id, message.metadata.message_id);
      assert.deepEqual(JSON.parse(Buffer.from(message.data_base64, 'base64')), { upload_ref: key.requestID });
      const { rows } = await db.query('SELECT grant_token FROM report_upload_grants WHERE thread_id=$1', [message.metadata.thread_id]);
      assert.equal(rows[0].grant_token, grantToken);
      assert.ok(!request.body.includes(grantToken));
      const prior = published.get(message.idempotency_key);
      if (prior) assert.deepEqual(message, prior);
      else published.set(message.idempotency_key, message);
      if (lostAcknowledgement) {
        lostAcknowledgement = false;
        throw new Error('lost publication acknowledgement');
      }
      return Response.json({ sequence: 1 }, { status: 201 });
    }
    assert.equal(url.pathname, deliveryPath + '/download-url');
    signCount++;
    return Response.json({ url: 'https://private.test/object', method: 'GET', headers: {}, expires_at: new Date().toISOString() });
  };
  const start = () => createReportIntake({ db, apiURL: 'https://api.test', token: 'backend', app: 'report-agent', endpointID, uploadRoute: 'reports', fetch });
  await assert.rejects(start().submit(key, csv), /lost publication acknowledgement/);
  assert.deepEqual(await start().status(key), { requestID: key.requestID, status: 'staging' });
  assert.deepEqual(await start().submit(key, csv), { requestID: key.requestID, status: 'queued' });
  await start().submit(key, csv);
  assert.deepEqual([prepareCount, putCount, grantCount, publicationAttempts, published.size], [1, 1, 1, 2, 1]);
  await assert.rejects(start().submit(key, new Blob(['changed bytes'])), /different CSV bytes/);
  await db.query("UPDATE report_requests SET grant_expires_at = now() - interval '1 second' WHERE request_id=$1", [key.requestID]);
  assert.deepEqual(await start().status(key), { requestID: key.requestID, status: 'intake_expired' });
  assert.equal((await start().submit(key, csv)).status, 'intake_expired');
  assert.deepEqual([grantCount, publicationAttempts], [1, 2]);
  await db.query('UPDATE report_requests SET grant_expires_at=$2 WHERE request_id=$1', [key.requestID, grantExpiresAt]);

  const { PostgresStore } = await import('../../../cmd/gregale/templates/agent-stateless-node/store.js');
  const workerDSN = new URL(dsn);
  workerDSN.searchParams.set('options', `-c search_path=${schema}`);
  const [threadID, inbox] = published.entries().next().value;
  const summary = { currency: 'USD', orders: 1, units: 2, revenue_cents: 1800 };
  const completionID = randomUUID(), workflowID = randomUUID(), runID = randomUUID();
  let dispatches = 0, writes = 0;
  const platform = {
    async issueCallback() { return { url: 'https://api.test/callback', expires_at: new Date().toISOString() }; },
    async revokeCallbacks() {},
    async call(path, dispatch) {
      assert.equal(path, '/v1/agent-tools/invoke');
      assert.equal(dispatch.id, `assistant:${threadID}`);
      assert.equal(dispatch.arguments.steps[0].label, 'csv-report');
      assert.deepEqual(dispatch.arguments.steps[0].file_grants, [grantToken]);
      dispatches++;
    },
    async pipelineReceipt() {
      return { id: completionID, completion: {
        completion_id: completionID, workflow_id: workflowID, outcome: 'succeeded',
        steps: [{ label: 'csv-report', outcome: 'succeeded', attempts: [{ run_id: runID }] }],
      } };
    },
    async taskReceipt() {
      return { id: runID, status: 'succeeded', result: summary,
        artifacts: [{ name: 'report.html', size_bytes: 1, sha256: `sha256:${'e'.repeat(64)}` }] };
    },
    async deliverRunArtifact(app, route, request) {
      assert.equal(app, 'report-agent');
      assert.equal(route, 'reports');
      assert.equal(request.idempotency_key, `${completionID}:report.html`);
      writes++;
      return { id: deliveryID, status: 'completed', receipt_url: deliveryPath, object: { key: 'private' } };
    },
  };
  const worker = () => createHostedReportHandler({ endpointID, processRecord: createReportAgent({
    store: new PostgresStore(workerDSN.toString()), platform, destination: { app: 'report-agent', route: 'reports' },
    async loadUploadGrant({ threadId, callId, uploadRef }) {
      const { rows } = await db.query(
        'SELECT grant_token FROM report_upload_grants WHERE endpoint_id=$1 AND thread_id=$2 AND call_id=$3 AND upload_ref=$4',
        [endpointID, threadId, callId, uploadRef]);
      return rows[0]?.grant_token;
    },
  }) });
  const event = body => ({
    method: 'POST', path: '/_triggers/esm/pilot', headers: { 'X-Faas-Invocation-Source': 'esm' },
    body: JSON.stringify({ endpoint_id: endpointID, channel: 'agent-inbox', group: 'slices', ...body }),
  });
  assert.equal((await worker()(event(inbox))).statusCode, 200);
  assert.equal((await worker()(event(inbox))).statusCode, 200);
  assert.deepEqual(await start().status(key), { requestID: key.requestID, status: 'running' });
  assert.equal(await start().download(key), null);
  const completion = event({
    metadata: { kind: 'tool_result', thread_id: threadID, message_id: 'done' },
    data_base64: Buffer.from(JSON.stringify({ type: 'tool_result', text: JSON.stringify({
      completion_id: completionID, workflow_id: workflowID, outcome: 'succeeded',
    }) })).toString('base64'),
  });
  assert.equal((await worker()(completion)).statusCode, 200);
  assert.equal((await worker()(completion)).statusCode, 200);
  assert.deepEqual([dispatches, writes], [1, 1]);
  const transcript = await db.query('SELECT content FROM agent_messages WHERE thread_id=$1', [threadID]);
  assert.equal(transcript.rows.length, 4);
  assert.ok(!JSON.stringify(transcript.rows).includes(grantToken));
  assert.deepEqual(await start().status(key), { requestID: key.requestID, status: 'succeeded', summary });
  const other = { ...key, customerID: 'customer-two' };
  assert.equal(await start().status(other), null);
  assert.equal(await start().download(other), null);
  assert.equal(signCount, 0);
  assert.equal((await start().download(key)).url, 'https://private.test/object');
  assert.equal(signCount, 1);
});
