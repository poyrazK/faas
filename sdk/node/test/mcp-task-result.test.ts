import assert from 'node:assert/strict';
import test from 'node:test';
import {
  cancelMcpTask, getMcpTask, waitForMcpTask, type McpTaskCancellationConnection, type McpTaskConnection,
} from '../src/preview.js';

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

function cancellationConnection(request: McpTaskCancellationConnection['request']): McpTaskCancellationConnection {
  return { ...connection(request), request };
}

test('Task cancellation returns an observed terminal state without sending a mutation', async () => {
  for (const [status, nextAction] of [['completed', 'read_result'], ['failed', 'none'], ['cancelled', 'none']] as const) {
    const calls: string[] = [];
    const client = cancellationConnection(async ({ method, params }) => {
      calls.push(method);
      assert.deepEqual(params, { taskId });
      return snapshot(status, { result: { private: true }, error: { private: true }, statusMessage: 'private' });
    });
    assert.deepEqual(await cancelMcpTask(client, taskId), {
      outcome: 'terminal', task: { ...snapshot(status), nextAction },
    });
    assert.deepEqual(calls, ['tasks/get']);
  }
});

test('a cancellation acknowledgement reports requested using one shared deadline', async () => {
  const calls: string[] = [];
  const signals = new Set<AbortSignal>();
  const client = cancellationConnection(async ({ method, params }, _, options) => {
    calls.push(method);
    signals.add(options.signal);
    assert.deepEqual(params, { taskId });
    assert.equal(options.allowInputRequired, true);
    assert.equal(options.timeout, 2_500);
    if (method === 'tasks/get') return snapshot('input_required', { inputRequests: { private: true } });
    return { _meta: { acknowledgement: 'private transport metadata' } };
  });
  assert.deepEqual(await cancelMcpTask(client, taskId, { timeoutMs: 2_500 }), { outcome: 'requested', taskId });
  assert.deepEqual(calls, ['tasks/get', 'tasks/cancel']);
  assert.equal(signals.size, 1);
});

test('an ambiguous cancellation performs one read and reports the actual observed state', async () => {
  for (const [status, failure] of [
    ['cancelled', new TypeError('reply lost')],
    ['completed', Object.assign(new Error('internal error'), { code: -32603 })],
    ['input_required', Object.assign(new Error('server error'), { code: -32000 })],
  ] as const) {
    const calls: string[] = [];
    const client = cancellationConnection(async ({ method }) => {
      calls.push(method);
      if (method === 'tasks/cancel') throw failure;
      if (calls.length === 1) return snapshot('input_required');
      return snapshot(status, { result: { private: true }, inputRequests: { private: true } });
    });
    const result = await cancelMcpTask(client, taskId);
    assert.equal(result.outcome, status === 'input_required' ? 'unconfirmed' : 'terminal');
    assert.ok(result.outcome === 'unconfirmed' || result.outcome === 'terminal');
    assert.equal(result.task?.status, status);
    assert.equal(Object.hasOwn(result.task ?? {}, 'result'), false);
    assert.equal(Object.hasOwn(result.task ?? {}, 'inputRequests'), false);
    assert.deepEqual(calls, ['tasks/get', 'tasks/cancel', 'tasks/get']);
  }
});

test('an invalid acknowledgement and unverified reconciliation remain unconfirmed', async () => {
  const calls: string[] = [];
  const client = cancellationConnection(async ({ method }) => {
    calls.push(method);
    if (method === 'tasks/cancel') return { resultType: 'input_required', inputRequests: {} };
    if (calls.length === 1) return snapshot('working');
    return snapshot('cancelled', { taskId: 'other Task' });
  });
  assert.deepEqual(await cancelMcpTask(client, taskId), { outcome: 'unconfirmed', taskId });
  assert.deepEqual(calls, ['tasks/get', 'tasks/cancel', 'tasks/get']);
});

test('denied reads and rejected cancellation requests never trigger a retry', async () => {
  const denied = cancellationConnection(async ({ method }) => {
    assert.equal(method, 'tasks/get');
    throw Object.assign(new Error('private response'), { status: 403 });
  });
  await assert.rejects(cancelMcpTask(denied, taskId), { code: 'request_failed', status: 403 });

  const calls: string[] = [];
  const rejected = cancellationConnection(async ({ method }) => {
    calls.push(method);
    if (method === 'tasks/get') return snapshot('working');
    throw Object.assign(new Error('private response'), { code: -32602 });
  });
  await assert.rejects(cancelMcpTask(rejected, taskId), {
    name: 'McpTaskCancellationError', code: 'request_rejected', message: 'MCP Task cancellation request was rejected',
  });
  assert.deepEqual(calls, ['tasks/get', 'tasks/cancel']);
});

test('aborting an in-flight cancellation leaves it unconfirmed without a follow-up read', async () => {
  const controller = new AbortController();
  const calls: string[] = [];
  const client = cancellationConnection(async ({ method }, _, { signal }) => {
    calls.push(method);
    if (method === 'tasks/get') return snapshot('working');
    controller.abort();
    signal.throwIfAborted();
  });
  assert.deepEqual(await cancelMcpTask(client, taskId, { signal: controller.signal }), {
    outcome: 'unconfirmed', taskId,
  });
  assert.deepEqual(calls, ['tasks/get', 'tasks/cancel']);
});
