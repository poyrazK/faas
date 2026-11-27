import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import { reportModel, materializeCompletion } from '../agent/bridge.mjs';
import { createReportAgent } from '../agent/agent.mjs';
import { jsonFile } from '../scripts/manifests.mjs';
import { runSlice } from '../../../cmd/gregale/templates/agent-stateless-node/agent.js';
import { GregalePipelineTools } from '../../../cmd/gregale/templates/agent-stateless-node/tools.js';

// In-memory test doubles only. The deployed starter uses transactional Postgres;
// the app must also provide durable report storage before acknowledging results.
class MemoryStore {
  rows = [];
  async transaction(threadID, fn) {
    const staged = [], rows = () => [...this.rows, ...staged].filter(r => r.thread_id === threadID);
    const result = await fn({
      seen: async id => rows().some(r => r.message_id === id),
      undispatched: async id => rows().find(r => r.message_id === `assistant:${id}` && r.content.tool_call?.dispatched === false)?.content.tool_call,
      answered: async id => rows().some(r => r.message_id === `assistant:${id}` && r.content.final),
      history: async () => rows().map(({ role, content }) => ({ role, content })),
      append: async (message_id, role, content) => staged.push({ thread_id: threadID, message_id, role, content: structuredClone(content) }),
    });
    this.rows.push(...staged); return result;
  }
  async markDispatched(threadID, id) { this.rows.find(r => r.thread_id === threadID && r.message_id === id).content.tool_call.dispatched = true; }
}

async function fixture() {
  const id = '12345678-1234-1234-1234-123456789abc', bytes = Buffer.from('<!doctype html><h1>Sales report</h1>');
  const summary = await jsonFile('report-pipeline/expected.json'), saved = new Map(), calls = [];
  const platform = {
    issueCallback: async () => ({ url: 'https://example.invalid/signed-callback' }),
    revokeCallbacks: async () => calls.push('revoke'),
    call: async (_path, body) => calls.push(body),
    pipelineReceipt: async () => { calls.push('pipeline'); return { id, completion: { completion_id: id, workflow_id: 'report-one', outcome: 'succeeded', steps: [
      { label: 'merge', outcome: 'succeeded', attempts: [{ run_id: 'merge-run', status: 'succeeded' }] },
    ] } }; },
    taskReceipt: async () => ({ id: 'merge-run', status: 'succeeded', result: summary, result_validation: { status: 'valid' }, artifacts: [
      { name: 'combined.html', size_bytes: bytes.length, sha256: createHash('sha256').update(bytes).digest('hex') },
    ] }),
    taskArtifact: async () => new Response(bytes),
  };
  const reports = {
    get: async key => saved.get(key),
    putOnce: async (key, record) => {
      calls.push('save');
      if (!saved.has(key)) saved.set(key, { ...record, report_ref: 'app-report:one' });
      return saved.get(key);
    },
  };
  const message = { endpointId: 'endpoint-one', threadId: 'thread-one', messageId: 'completion-one', role: 'tool', content: {
    type: 'tool_result', id: 'assistant:request-one', text: JSON.stringify({ completion_id: id, workflow_id: 'report-one', outcome: 'succeeded' }),
  } };
  return { id, bytes, summary, saved, calls, platform, reports, message };
}

test('Agent persists call identity through lost acknowledgement, then saves output before final revocation', async () => {
  const f = await fixture(), store = new MemoryStore(), model = reportModel({ prefix: 'demo' });
  let first = true;
  f.platform.call = async (_path, body) => { f.calls.push(body); if (first) { first = false; throw new Error('lost acknowledgement'); } };
  const deps = { store, model, platform: f.platform, tools: new GregalePipelineTools({ platform: f.platform }) };
  const user = { ...f.message, messageId: 'request-one', role: 'user', content: await jsonFile('sales-report/rows.json') };
  await assert.rejects(runSlice(user, deps), /lost acknowledgement/);
  await runSlice(user, deps);
  assert.deepEqual(f.calls[0], f.calls[1]);
  assert.equal(f.calls[0].operation, 'gregale_pipeline'); assert.equal(f.calls[0].id, 'assistant:request-one');
  assert.equal(f.calls[0].arguments.steps.at(-1).task.version, 1);
  const enriched = await materializeCompletion(f.message, f);
  assert.equal(enriched.content.report.bytes, undefined);
  await runSlice(enriched, deps);
  assert.ok(f.calls.indexOf('save') < f.calls.indexOf('revoke'));
  assert.match(store.rows.at(-1).content.text, /6996 USD cents/);
  assert.match(store.rows.at(-1).content.text, /app-report:one/);
  // The cached app-owned report remains usable when Gregale's grant is gone.
  f.platform.pipelineReceipt = async () => { throw new Error('grant revoked'); };
  assert.equal((await runSlice(await materializeCompletion(f.message, f), deps)).skipped, true);
  assert.equal(f.calls.filter(c => c === 'save').length, 1);
  assert.equal(f.saved.size, 1);
  assert.equal(store.rows.length, 4);
});

test('corrupt exports or failed app persistence prevent a final reply', async () => {
  const f = await fixture();
  f.platform.taskArtifact = async () => new Response(Buffer.alloc(f.bytes.length, 0));
  await assert.rejects(materializeCompletion(f.message, f), /checksum mismatch/);
  assert.equal(f.saved.size, 0); assert.ok(!f.calls.includes('revoke'));
  f.platform.taskArtifact = async () => new Response(f.bytes);
  f.reports.putOnce = async () => { throw new Error('storage unavailable'); };
  await assert.rejects(materializeCompletion(f.message, f), /storage unavailable/);
  assert.ok(!f.calls.includes('revoke'));
});

test('invalid result contracts are never passed to the Agent as a successful report', async () => {
  const f = await fixture(), original = f.platform.taskReceipt;
  f.platform.taskReceipt = async () => ({ ...await original(), result_validation: { status: 'invalid' } });
  await assert.rejects(materializeCompletion(f.message, f), /Invalid report result/);
  assert.equal(f.saved.size, 0);
});

test('failed pipelines produce a factual final answer without downloading a report', async () => {
  const f = await fixture(), original = f.platform.pipelineReceipt;
  const payload = JSON.parse(f.message.content.text); payload.outcome = 'failed'; f.message.content.text = JSON.stringify(payload);
  f.platform.pipelineReceipt = async () => { const r = await original(); r.completion.outcome = 'failed'; return r; };
  f.platform.taskArtifact = async () => { throw new Error('must not download'); };
  const next = reportModel().next([], await materializeCompletion(f.message, f));
  assert.equal(next.final, true); assert.match(next.text, /did not complete: failed/);
});

test('Agent inputs cannot silently drop a region or smuggle extra task arguments', async () => {
  const model = reportModel(), rows = (await jsonFile('sales-report/rows.json')).rows;
  assert.throws(() => model.next([], { role: 'user', content: { rows: [{ ...rows[0], region: 'west' }, rows[1]] } }), /Invalid Agent order/);
  assert.throws(() => model.next([], { role: 'user', content: { rows: [{ ...rows[0], source: 'code' }, rows[1]] } }), /Invalid Agent order/);
  assert.throws(() => model.next([], { role: 'tool', content: {} }), /Materialize/);
});

test('record processor connects the actual starter parser, pipeline adapter and completion bridge', async () => {
  const f = await fixture();
  const processRecord = createReportAgent({ store: new MemoryStore(), platform: f.platform, reports: f.reports });
  const record = (kind, id, data) => ({ endpoint_id: f.message.endpointId, metadata: { thread_id: f.message.threadId, message_id: id, kind },
    data_base64: Buffer.from(JSON.stringify(data)).toString('base64') });
  assert.deepEqual(await processRecord({}), { ignored: true });
  const first = await processRecord(record('user', 'request-one', await jsonFile('sales-report/rows.json')));
  assert.equal(first.pending.tool, 'gregale_pipeline');
  const second = await processRecord(record('tool_result', 'completion-one', f.message.content));
  assert.equal(second.final, true);
  assert.deepEqual(f.calls.slice(-2), ['save', 'revoke']);
});

test('managed delivery finalizes once and redelivery needs no source grant or custom report store', async () => {
  const f = await fixture(), store = new MemoryStore(), intents = [];
  f.platform.deliverRunArtifact = async (app, route, body) => {
    intents.push({ app, route, body }); f.calls.push('deliver');
    return { id: 'delivery-one', status: 'completed', receipt_url: '/v1/apps/reports/run-artifact-deliveries/reports/delivery-one',
      object: { bucket_id: 'bucket-one', key: 'reports/output' } };
  };
  f.platform.taskArtifact = async () => { throw new Error('Agent must not download managed bytes'); };
  const processRecord = createReportAgent({ store, platform: f.platform, destination: { app: 'reports', route: 'reports' } });
  const record = { endpoint_id: f.message.endpointId, metadata: { thread_id: f.message.threadId, message_id: f.message.messageId, kind: 'tool_result' },
    data_base64: Buffer.from(JSON.stringify(f.message.content)).toString('base64') };
  assert.equal((await processRecord(record)).final, true);
  assert.deepEqual(f.calls.slice(-2), ['deliver', 'revoke']);
  assert.equal(intents[0].body.idempotency_key, `${f.id}:combined.html`);
  assert.match(intents[0].body.sha256, /^sha256:[a-f0-9]{64}$/);
  f.platform.pipelineReceipt = async () => { throw new Error('source grant revoked'); };
  assert.equal((await processRecord(record)).skipped, true);
  assert.equal(intents.length, 1);
  assert.equal(store.rows.length, 2);
  assert.equal(store.rows[0].content.report.delivery.id, 'delivery-one');
  assert.equal(store.rows[0].content.report.bytes, undefined);
});

test('pending delivery retries the same storage intent and keeps the thread open', async () => {
  const f = await fixture(), intents = [];
  f.platform.deliverRunArtifact = async (_app, _route, body) => { intents.push(body); return { id: 'pending-one', status: 'pending' }; };
  const options = { platform: f.platform, destination: { app: 'reports', route: 'reports' } };
  for (let i = 0; i < 2; i++) await assert.rejects(materializeCompletion(f.message, options), /pending-one is pending/);
  assert.deepEqual(intents[0], intents[1]); assert.ok(!f.calls.includes('revoke'));
});

test('report export accepts the prefixed SHA-256 used by real Run receipts', async () => {
  const f = await fixture(), read = f.platform.taskReceipt;
  f.platform.taskReceipt = async () => { const result = await read(); result.artifacts[0].sha256 = `sha256:${result.artifacts[0].sha256}`; return result; };
  assert.equal((await materializeCompletion(f.message, f)).content.report.outcome, 'succeeded');
});

test('hosted realtime trigger hands a private CSV from one replayed call to the report step', async () => {
  const { createHostedReportHandler } = await import('../agent/trigger.mjs');
  const f = await fixture(), store = new MemoryStore();
  const token = 'agf1.test-private-token';
  const uploadRef = 'c835bd9a-ffdc-4531-ac01-5e503a614e75';
  let lost = true, writes = 0;
  f.platform.call = async (_path, body) => {
    f.calls.push(body);
    if (lost) { lost = false; throw new Error('lost acknowledgement'); }
  };
  const readPipeline = f.platform.pipelineReceipt;
  f.platform.pipelineReceipt = async () => {
    const receipt = await readPipeline();
    receipt.completion.steps[0].label = 'csv-report';
    return receipt;
  };
  const readTask = f.platform.taskReceipt;
  f.platform.taskReceipt = async () => {
    const receipt = await readTask();
    receipt.artifacts[0].name = 'report.html';
    delete receipt.result.source_reports;
    return receipt;
  };
  f.platform.deliverRunArtifact = async (_app, _route, request) => {
    assert.equal(request.artifact_name, 'report.html');
    assert.equal(request.idempotency_key, `${f.id}:report.html`);
    writes++;
    return { status: 'completed', receipt_url: 'private-csv-report', object: { key: 'private' } };
  };
  const start = () => createHostedReportHandler({ endpointID: f.message.endpointId, processRecord: createReportAgent({
    store, platform: f.platform, destination: { app: 'reports', route: 'out' },
    loadUploadGrant: async binding => {
      assert.deepEqual(binding, { threadId: f.message.threadId, callId: 'assistant:upload', uploadRef });
      return token;
    },
  }) });
  const event = (messageID, kind, content) => ({ method: 'POST', path: '/_triggers/esm/example',
    headers: { 'X-Faas-Invocation-Source': 'esm' }, body: JSON.stringify({
      endpoint_id: f.message.endpointId, channel: 'agent-inbox', group: 'slices',
      metadata: { thread_id: f.message.threadId, message_id: messageID, kind },
      data_base64: Buffer.from(JSON.stringify(content)).toString('base64'),
    }),
  });
  const inbox = event('upload', 'user', { upload_ref: uploadRef });
  assert.equal((await start()({ ...inbox, headers: {} })).statusCode, 404);
  assert.equal((await start()({ ...inbox, body: inbox.body.replace('"slices"', '"other"') })).statusCode, 400);
  assert.equal(store.rows.length, 0);
  await assert.rejects(start()(inbox), /lost acknowledgement/);
  assert.equal((await start()(inbox)).statusCode, 200);
  assert.deepEqual(f.calls[0], f.calls[1]);
  assert.equal(f.calls[0].arguments.upload_ref, undefined);
  assert.deepEqual(f.calls[0].arguments.steps[0].file_grants, [token]);
  const completion = event(f.message.messageId, 'tool_result', f.message.content);
  assert.equal((await start()(completion)).statusCode, 200);
  assert.equal((await start()(completion)).statusCode, 200);
  assert.equal(writes, 1);
  assert.equal(store.rows.length, 4);
  assert.match(store.rows.at(-1).content.text, /private-csv-report/);
  assert.ok(!JSON.stringify(store.rows).includes(token));
});
