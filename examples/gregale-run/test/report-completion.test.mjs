import assert from 'node:assert/strict';
import { execFile, execFileSync } from 'node:child_process';
import { createHmac, randomUUID } from 'node:crypto';
import { once } from 'node:events';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import { createReportCompletionReceiver } from '../agent/report-completion.ts';
import { createReportIntake } from '../agent/report-intake.ts';

const dsn = process.env.REPORT_INTAKE_TEST_DATABASE_URL;
const secret = 'test-report-completion-secret';
const eventType = 'execution.pipeline.finished';
const pgPath = new URL('../../../cmd/gregale/templates/agent-stateless-node/package.json', import.meta.url);
const url = 'https://reports.example.test/completion';

function sign(data, { id = randomUUID(), cloud = true, timestamp = Math.floor(Date.now() / 1000) } = {}) {
  const body = JSON.stringify(cloud ? { id, specversion: '1.0', type: eventType, data }
    : { id, rule_name: eventType, payload: { event: eventType, delivery_id: id, data } });
  const mac = createHmac('sha256', secret).update(`${timestamp}.${id}.${body}`).digest('hex');
  return { method: 'POST', body, headers: { 'x-faas-webhook-signature': `sha256=${mac}`,
    'x-faas-webhook-timestamp': String(timestamp), 'x-faas-delivery-id': id } };
}

test('report completion authenticates the raw body and only retries valid incomplete work', async () => {
  const data = { event_id: randomUUID(), workflow_id: `report-${randomUUID()}`, outcome: 'succeeded' };
  let calls = 0;
  const receive = createReportCompletionReceiver(secret, async event => {
    assert.deepEqual(event, data);
    return ++calls > 1;
  });
  const sent = sign(data);
  assert.equal((await receive(new Request(url))).status, 405);
  assert.equal((await receive(new Request(url, { method: 'POST', body: 'x'.repeat(16 * 1024 + 1) }))).status, 413);
  assert.equal((await receive(new Request(url, { ...sent, body: `${sent.body} ` }))).status, 401);
  assert.equal((await receive(new Request(url, sign(data, { timestamp: 1 })))).status, 401);
  assert.equal((await receive(new Request(url, sign({ ...data, outcome: 'running' })))).status, 400);
  assert.equal((await receive(new Request(url, sign({ ...data, workflow_id: 'some-other-workflow' })))).status, 204);
  assert.equal(calls, 0);
  assert.equal((await receive(new Request(url, sent))).status, 503);
  assert.equal((await receive(new Request(url, sign(data, { cloud: false })))).status, 204);
  assert.equal(calls, 2);
});

function restartedReceiver(intakeURL, options, pgURL, request) {
  const source = `
    import { createRequire } from 'node:module';
    import { readFileSync } from 'node:fs';
    const { intakeURL, options, pgURL, pgPath, request, url } = JSON.parse(readFileSync(0, 'utf8'));
    const { createReportIntake } = await import(intakeURL);
    const { Pool } = createRequire(pgPath)('pg');
    const db = new Pool({ connectionString: pgURL });
    try {
      const response = await createReportIntake({ ...options, db }).receiveCompletion(new Request(url, request));
      console.log(response.status);
    } finally { await db.end(); }
  `;
  return new Promise((resolve, reject) => {
    const worker = execFile(process.execPath, ['--input-type=module', '-e', source], { timeout: 15_000 },
      (error, stdout) => error ? reject(error) : resolve(Number(stdout.trim())));
    worker.stdin.end(JSON.stringify({ intakeURL, options, pgURL, pgPath: fileURLToPath(pgPath), request, url }));
  });
}

test('webhook-only delivery recovers a lost admission and storage acknowledgement after process restart', { skip: !dsn }, async t => {
  const { Client, Pool } = createRequire(pgPath)('pg');
  const schema = `report_completion_${randomUUID().replaceAll('-', '')}`;
  const admin = new Client({ connectionString: dsn });
  await admin.connect();
  await admin.query(`CREATE SCHEMA ${schema}`);
  const pgURL = new URL(dsn);
  pgURL.searchParams.set('options', `-c search_path=${schema}`);
  const db = new Pool({ connectionString: pgURL.href });
  t.after(async () => {
    await db.end();
    try { await admin.query(`DROP SCHEMA ${schema} CASCADE`); }
    finally { await admin.end(); }
  });
  const dir = await mkdtemp(path.join(tmpdir(), 'report-completion-'));
  t.after(() => rm(dir, { recursive: true, force: true }));
  const bundle = path.join(dir, 'bundle');
  execFileSync(process.execPath, [fileURLToPath(new URL('../scripts/hosted-pilot.mjs', import.meta.url)), bundle, 'pilot']);
  const schemaSQL = await readFile(path.join(bundle, 'report-db.sql'), 'utf8');
  await db.query(schemaSQL);
  await db.query(schemaSQL);

  const [endpointID, pipelineID, runID, artifactID] = Array.from({ length: 4 }, () => randomUUID());
  const orders = JSON.parse(await readFile(new URL('../sales-report/rows.json', import.meta.url), 'utf8')).rows;
  const owner = { requestID: randomUUID(), customerID: 'company-one' };
  const summary = { currency: 'USD', orders: 4, units: 8, revenue_cents: 6996 };
  const receipt = { completion_id: pipelineID, outcome: 'succeeded',
    steps: [{ label: 'merge', outcome: 'succeeded', attempts: [{ run_id: runID }] }] };
  const deliveryRoute = '/v1/apps/pilot/run-artifact-deliveries/reports';
  const delivery = { id: artifactID, status: 'completed', receipt_url: `${deliveryRoute}/${artifactID}`, object: { key: 'private' } };
  const signed = { url: 'https://storage.example.test/report', method: 'GET', headers: {}, expires_at: '2026-11-20T00:00:00Z' };
  const recorded = [];
  let workflow, storageIntent, providerWrites = 0, expireSource = false;
  const server = createServer(async (req, res) => {
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    recorded.push([req.method, req.url, req.headers.authorization]);
    let status = 200, result;
    if (req.url === '/v2/execution-workflows') {
      workflow = JSON.parse(Buffer.concat(chunks)).workflow_id;
      status = 503;
    } else if (req.url === `/v1/execution-workflows/${workflow}` && !expireSource) {
      result = { managed: [{ id: pipelineID, workflow_id: workflow, status: 'succeeded',
        pipeline: { completion: { ...receipt, workflow_id: workflow } } }] };
    } else if (req.url === `/v1/executions/${runID}` && !expireSource) {
      result = { id: runID, status: 'succeeded', result: { ...summary, source_reports: 2 },
        artifacts: [{ name: 'combined.html', size_bytes: 381, sha256: `sha256:${'a'.repeat(64)}` }] };
    } else if (req.url === deliveryRoute) {
      const attempt = JSON.parse(Buffer.concat(chunks));
      if (!storageIntent) {
        storageIntent = attempt;
        providerWrites++;
        status = 503; // The private write committed before the client saw an acknowledgement.
      } else if (isDeepStrictEqual(attempt, storageIntent)) result = delivery;
      else status = 409;
    } else if (req.url === `${delivery.receipt_url}/download-url`) result = signed;
    else status = 404;
    res.writeHead(status, { 'content-type': 'application/json', connection: 'close' }).end(JSON.stringify(result ?? {}));
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => new Promise(resolve => server.close(resolve)));
  const options = { apiURL: `http://127.0.0.1:${server.address().port}`, token: 'trusted-key', app: 'pilot',
    endpointID, uploadRoute: 'reports', completionWebhookSecret: secret };
  const reports = createReportIntake({ ...options, db });
  await assert.rejects(reports.submitRegional(owner, orders), /request failed \(503\)/);
  assert.equal((await db.query('SELECT pipeline_id FROM report_regional_pipelines')).rows[0].pipeline_id, null);
  const event = { event_id: pipelineID, workflow_id: workflow, outcome: 'succeeded',
    receipt_url: 'https://untrusted.example.test/unused' };
  const id = randomUUID();
  const foreign = createReportIntake({ ...options, endpointID: randomUUID(), db });
  assert.equal((await foreign.receiveCompletion(new Request(url, sign(event, { id })))).status, 204);
  assert.equal(recorded.length, 1);

  const failed = await reports.receiveCompletion(new Request(url, sign(event, { id })));
  assert.equal(failed.status, 503);
  assert.equal(providerWrites, 1);
  const staged = (await db.query('SELECT pipeline_id, report, delivery_intent FROM report_regional_pipelines')).rows[0];
  assert.equal(staged.pipeline_id, pipelineID);
  assert.equal(staged.report, null);
  assert.deepEqual(staged.delivery_intent.request, storageIntent);
  assert.equal(storageIntent.idempotency_key, `${pipelineID}:combined.html`);
  expireSource = true;
  assert.equal((await reports.status(owner)).status, 'delivering');

  const intakeURL = pathToFileURL(path.join(bundle, 'report-intake.ts')).href;
  assert.equal(await restartedReceiver(intakeURL, options, pgURL.href, sign(event, { id })), 204);
  assert.equal(await restartedReceiver(intakeURL, options, pgURL.href, sign(event, { cloud: false })), 204);
  assert.equal(providerWrites, 1);
  assert.deepEqual(recorded.map(row => row.slice(0, 2)), [
    ['POST', '/v2/execution-workflows'], ['GET', `/v1/execution-workflows/${workflow}`],
    ['GET', `/v1/executions/${runID}`], ['POST', deliveryRoute], ['POST', deliveryRoute],
  ]);
  assert.ok(recorded.every(row => row[2] === 'Bearer trusted-key'));
  assert.deepEqual(await reports.status(owner), { requestID: owner.requestID, status: 'succeeded', summary });
  assert.equal(await reports.download({ ...owner, customerID: 'other' }), null);
  assert.deepEqual(await reports.download(owner), signed);
});
