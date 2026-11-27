import test from 'node:test';
import assert from 'node:assert/strict';
import { FaaSClient, TaskDeliveryError, TaskExecutionError, TaskSubmissionError } from '../src/preview.js';

const runID = '11111111-1111-4111-8111-111111111111';
const taskRef = { id: '22222222-2222-4222-8222-222222222222', name: 'pdf-report', version: 1, digest: 'recipe' };
const receipt = (status: string) => ({ id: runID, task: taskRef, status, result: { words: 3 }, artifacts: [{ name: 'report.html', size_bytes: 10, sha256: `sha256:${'a'.repeat(64)}` }] });
const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
const decode = (value: unknown): { words: number } => {
  if (!value || typeof value !== 'object' || typeof (value as { words?: unknown }).words !== 'number') throw new Error('Invalid report');
  return value as { words: number };
};

test('typed task stages missing inputs, delivers once and replays before uploading', async () => {
  let uploads = 0, admitted = false, deliveryReads = 0, prepared = false;
  const invocationKeys: Array<string | null> = [], deliveryKeys: string[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    const path = new URL(input.toString()).pathname;
    if (path === '/v1/execution-files') return response({ id: 'file', sha256: `sha256:${'b'.repeat(64)}`, upload_required: true });
    if (path === '/v1/execution-files/file/content') { uploads++; assert.equal(await (init?.body as Blob).text(), 'PDF input'); return response({ id: 'file', status: 'ready' }); }
    if (path === '/v1/run-tasks/invoke') {
      assert.equal(prepared, true);
      invocationKeys.push(new Headers(init?.headers).get('Idempotency-Key'));
      if (!uploads) return response({ code: 'execution_files_not_ready' }, 409);
      const status = admitted ? 'succeeded' : 'queued'; admitted = true;
      return response(receipt(status), 202);
    }
    if (path === `/v1/executions/${runID}`) return response(receipt('succeeded'));
    if (path === '/v1/apps/reports/run-artifact-deliveries/reports') {
      deliveryKeys.push(JSON.parse(String(init?.body)).idempotency_key);
      return response({ id: 'delivery', status: deliveryReads ? 'completed' : 'pending' }, deliveryReads ? 200 : 202);
    }
    if (path === '/v1/apps/reports/run-artifact-deliveries/reports/delivery') { deliveryReads++; return response({ id: 'delivery', status: 'completed' }); }
    assert.fail(`Unexpected task request ${path}`);
  } });
  try {
    const task = client.task<{ title: string }, { words: number }>('pdf-report', 1, decode);
    const options = { idempotencyKey: 'customer-42-report', files: { 'input.pdf': new Blob(['PDF input']) }, pollIntervalMs: 1,
      onPrepared: () => { prepared = true; }, deliver: { app: 'reports', route: 'reports', artifacts: { 'report.html': 'text/html' } } };
    const first = await task.run({ title: 'Quarterly report' }, options);
    const replay = await task.run({ title: 'Quarterly report' }, options);
    assert.equal(first.result.words, 3); assert.equal(replay.receipt.id, first.receipt.id);
    assert.equal(first.deliveries['report.html']?.status, 'completed');
    assert.equal(uploads, 1); assert.equal(deliveryReads, 1);
    assert.deepEqual(invocationKeys, ['customer-42-report', 'customer-42-report', 'customer-42-report']);
    assert.equal(deliveryKeys[0], deliveryKeys[1]);
  } finally { client.uninstall(); }
});

test('failed task and invalid result contracts never decode or promote an artifact', async () => {
  for (const status of ['failed', 'succeeded']) {
    let decoded = false;
    const client = new FaaSClient('https://api.example.test', { fetch: async (input) => {
      assert.equal(new URL(input.toString()).pathname, '/v1/run-tasks/invoke');
      return response({ ...receipt(status), result_validation: { status: 'invalid' } }, 202);
    } });
    try {
      await assert.rejects(client.task('pdf-report', 1, () => { decoded = true; }).run({}, { idempotencyKey: 'call', deliver: { app: 'reports', route: 'reports', artifacts: { 'report.html': 'text/html' } } }), TaskExecutionError);
      assert.equal(decoded, false);
    } finally { client.uninstall(); }
  }
});

test('lost admission acknowledgement retains the caller intent without resubmitting', async () => {
  let calls = 0;
  const client = new FaaSClient('https://api.example.test', { fetch: async () => { calls++; return new Response('not-json', { status: 202, headers: { 'content-type': 'application/json' } }); } });
  try {
    await assert.rejects(client.task('pdf-report', 1, decode).start({ title: 'Original' }, { idempotencyKey: 'stable', onPrepared: intent => { intent.request.input = { title: 'Mutated observer' }; } }), error => {
      assert.ok(error instanceof TaskSubmissionError);
      assert.equal(error.intent.idempotencyKey, 'stable');
      assert.deepEqual(error.intent.request.input, { title: 'Original' });
      return true;
    });
    assert.equal(calls, 1);
  } finally { client.uninstall(); }
});

test('ending a task wait leaves execution running and keeps its original handle', async () => {
  const paths: string[] = [], methods: string[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    methods.push(String(init?.method ?? 'GET'));
    paths.push(new URL(input.toString()).pathname); return response(receipt('running'), 202);
  } });
  try {
    const run = await client.task('pdf-report', 1, decode).start({}, { idempotencyKey: 'stable' });
    await assert.rejects(run.result({ timeoutMs: 5, pollIntervalMs: 1 }));
    assert.equal(run.id, runID);
    assert.equal(paths.filter(p => p === '/v1/run-tasks/invoke').length, 1);
    assert.equal(methods.includes('DELETE'), false);
  } finally { client.uninstall(); }
});

test('invalid wait limits are rejected before task admission', async () => {
  const client = new FaaSClient('https://api.example.test', { fetch: async () => { assert.fail('Invalid options admitted a task'); } });
  try {
    for (const timeoutMs of [NaN, Infinity, 0, -1, 2 ** 32]) {
      await assert.rejects(client.task('pdf-report', 1, decode).run({}, { idempotencyKey: 'stable', timeoutMs }), /Invalid task wait limits/);
    }
  } finally { client.uninstall(); }
});

test('delivery observation timeout retains its pending receipt for recovery', async () => {
  const methods: string[] = [];
  const client = new FaaSClient('https://api.example.test', { fetch: async (input, init) => {
    methods.push(String(init?.method ?? 'GET'));
    const path = new URL(input.toString()).pathname;
    if (path === '/v1/run-tasks/invoke') return response(receipt('succeeded'), 202);
    return response({ id: 'delivery', status: 'pending' }, 202);
  } });
  try {
    const run = await client.task('pdf-report', 1, decode).start({}, { idempotencyKey: 'stable' });
    await assert.rejects(run.deliver({ app: 'reports', route: 'reports', artifacts: { 'report.html': 'text/html' } }, { timeoutMs: 10, pollIntervalMs: 1 }), error => {
      assert.ok(error instanceof TaskDeliveryError);
      assert.equal(error.receipt.id, runID);
      assert.equal(error.delivery.id, 'delivery');
      assert.equal(error.delivery.status, 'pending');
      return true;
    });
    assert.equal(methods.includes('DELETE'), false);
  } finally { client.uninstall(); }
});
