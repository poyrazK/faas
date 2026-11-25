import assert from 'node:assert/strict';
import test from 'node:test';
import { getMcpTask, waitForMcpTask, type McpTaskConnection } from '../src/preview.js';

const taskId = 'durable task fixture';
const timestamp = '2026-11-24T01:00:00.123456789Z';

function connection(request: McpTaskConnection['request']): McpTaskConnection {
  return {
    getNegotiatedProtocolVersion: () => '2026-07-28',
    getServerCapabilities: () => ({ extensions: { 'io.modelcontextprotocol/tasks': {} } }),
    request,
  };
}

function snapshot(status: string, fields: Record<string, unknown> = {}) {
  return { taskId, status, createdAt: timestamp, lastUpdatedAt: timestamp, ttlMs: null, ...fields };
}

test('getMcpTask forwards the exact ID and preserves the retained tool result', async () => {
  const result = { content: [{ type: 'text', text: 'original result' }],
    structuredContent: { items: [1, 2] }, isError: true, _meta: { custom: 'retained tool metadata' } };
  const client = connection(async (request, schema, options) => {
    assert.deepEqual(request, { method: 'tasks/get', params: { taskId } });
    assert.equal(options.allowInputRequired, true);
    assert.equal(options.timeout, 2_500);
    assert.equal(schema['~standard'].version, 1);
    return snapshot('completed', { result, statusMessage: 'private status message', arguments: { private: true } });
  });
  const task = await getMcpTask(client, taskId, { timeoutMs: 2_500 });
  assert.deepEqual(task, { taskId, status: 'completed', nextAction: 'read_result', ttlMs: null,
    createdAt: timestamp, lastUpdatedAt: timestamp, result });
});

test('Task reads reject invalid IDs, timeouts, protocol versions and capabilities locally', async () => {
  const client = connection(async () => assert.fail('invalid reads must not reach the connection'));
  await assert.rejects(getMcpTask(client, taskId + '\n'), TypeError);
  await assert.rejects(getMcpTask(client, 'é'.repeat(129)), TypeError);
  await assert.rejects(waitForMcpTask(client, taskId, { timeoutMs: 0 }), RangeError);
  await assert.rejects(getMcpTask({ ...client, getNegotiatedProtocolVersion: () => '2025-11-25' }, taskId),
    { code: 'unavailable' });
  await assert.rejects(waitForMcpTask({ ...client, getServerCapabilities: () => ({ extensions: {} }) }, taskId),
    { code: 'unavailable' });
});

test('Task reads reject mismatched IDs and malformed state-specific results', async () => {
  for (const response of [
    snapshot('completed', { taskId: 'other task', result: {} }),
    snapshot('working', { pollIntervalMs: -1 }),
    snapshot('working', { resultType: 'input_required' }),
    snapshot('unknown'),
    snapshot('completed', { result: false }),
    snapshot('completed', { result: [] }),
    snapshot('failed'),
  ]) {
    let calls = 0;
    const client = connection(async () => { calls++; return response; });
    await assert.rejects(waitForMcpTask(client, taskId), { name: 'McpTaskReadError', code: 'invalid_task' });
    assert.equal(calls, 1);
  }
});

test('Task wait stops on input required, failure and cancellation', async () => {
  const error = { code: -32603, message: 'retained tool failure', data: { attempt: 1 } };
  for (const response of [
    snapshot('input_required', { inputRequests: { private: { message: 'use the reviewed input API' } } }),
    snapshot('failed', { error }),
    snapshot('cancelled'),
  ]) {
    let calls = 0;
    const client = connection(async ({ method }) => {
      assert.equal(method, 'tasks/get');
      calls++;
      return { resultType: 'complete', ...response };
    });
    const task = await waitForMcpTask(client, taskId);
    assert.equal(task.status, response.status);
    assert.equal(Object.hasOwn(task, 'inputRequests'), false);
    if (task.status === 'failed') assert.deepEqual(task.error, error);
    else assert.equal(Object.hasOwn(task, 'error'), false);
    assert.equal(calls, 1);
  }
});

test('Task wait honors server poll delays and retries only transient reads', async () => {
  const times: number[] = [];
  const client = connection(async ({ method, params }, _, options) => {
    assert.equal(method, 'tasks/get');
    assert.deepEqual(params, { taskId });
    assert.equal(options.timeout, 2_500);
    times.push(performance.now());
    if (times.length === 1) return snapshot('working', { pollIntervalMs: 400 });
    if (times.length === 2) throw Object.assign(new Error('upstream private text'), { status: 503 });
    return snapshot('completed', { result: { content: [] } });
  });
  assert.equal((await waitForMcpTask(client, taskId, { timeoutMs: 2_500 })).status, 'completed');
  assert.equal(times.length, 3);
  const [first, second, third] = times;
  assert.ok(first !== undefined && second !== undefined && third !== undefined);
  assert.ok(second - first >= 370);
  assert.ok(third - second >= 370);
});

test('unavailable Tasks and authentication failures stop immediately without exposing server text', async () => {
  for (const [details, code] of [
    [{ status: 404 }, 'not_found'],
    [{ code: -32002 }, 'not_found'],
    [{ status: 403 }, 'request_failed'],
  ] as const) {
    let calls = 0;
    const client = connection(async () => {
      calls++;
      throw Object.assign(new Error('private response text'), details);
    });
    await assert.rejects(waitForMcpTask(client, taskId), {
      name: 'McpTaskReadError', code, message: code === 'not_found'
        ? 'MCP Task is unavailable to this caller' : 'MCP Task read request failed',
    });
    assert.equal(calls, 1);
  }
  let calls = 0;
  const unavailable = connection(async () => {
    calls++;
    throw Object.assign(new Error('private response text'), { status: 503 });
  });
  await assert.rejects(getMcpTask(unavailable, taskId), { code: 'request_failed', status: 503 });
  assert.equal(calls, 1);
});

test('the total wait deadline interrupts a server-directed delay', async () => {
  let calls = 0;
  const client = connection(async () => {
    calls++;
    return snapshot('working', { pollIntervalMs: 24 * 60 * 60 * 1000 });
  });
  await assert.rejects(waitForMcpTask(client, taskId, { timeoutMs: 60 }), { code: 'timeout' });
  assert.equal(calls, 1);
});

test('cancelling a wait aborts the in-flight read and sends no mutation', async () => {
  const controller = new AbortController();
  let start!: () => void;
  const started = new Promise<void>(resolve => { start = resolve; });
  const called: string[] = [];
  const client = connection(async ({ method }, _, { signal }) => {
    called.push(method);
    return new Promise((_, reject) => {
      signal.addEventListener('abort', () => reject(signal.reason), { once: true });
      start();
    });
  });
  const waiting = waitForMcpTask(client, taskId, { signal: controller.signal });
  await started;
  controller.abort();
  await assert.rejects(waiting, { name: 'AbortError' });
  assert.deepEqual(called, ['tasks/get']);
});
